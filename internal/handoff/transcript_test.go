package handoff

import (
	"strings"
	"testing"
	"time"
)

// line helpers keep the fixtures readable; real transcripts are compact JSONL.
func userLine(ts, source, text string) string {
	return `{"type":"user","timestamp":"` + ts + `","promptSource":"` + source +
		`","message":{"role":"user","content":"` + text + `"}}`
}

func toolResultLine(ts string) string {
	return `{"type":"user","timestamp":"` + ts +
		`","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`
}

func assistantToolLine(ts, tool string) string {
	return `{"type":"assistant","timestamp":"` + ts +
		`","message":{"role":"assistant","content":[{"type":"tool_use","name":"` + tool + `"}]}}`
}

func parse(t *testing.T, lines ...string) *Transcript {
	t.Helper()
	tr, err := parseTranscript(strings.NewReader(strings.Join(lines, "\n") + "\n"))
	if err != nil {
		t.Fatalf("parseTranscript: %v", err)
	}
	return tr
}

// TestTitlePrecedenceRenameBeatsGeneratedTitle is the case that a naive
// "last title record wins" gets wrong. Both record types are re-emitted
// throughout a real transcript and interleave, so the final title record is
// usually the generated one even in a session that was explicitly renamed.
func TestTitlePrecedenceRenameBeatsGeneratedTitle(t *testing.T) {
	tr := parse(t,
		`{"type":"ai-title","aiTitle":"MindSkein v0.1 project brief"}`,
		userLine("2026-08-18T08:00:00Z", "typed", "start"),
		`{"type":"custom-title","customTitle":"U2"}`,
		`{"type":"ai-title","aiTitle":"MindSkein v0.1 project brief"}`,
	)
	if tr.Title != "U2" {
		t.Errorf("Title = %q, want %q — an explicit rename outranks a generated title", tr.Title, "U2")
	}
}

func TestTitleFallsBackToGeneratedThenFirstPrompt(t *testing.T) {
	tr := parse(t,
		userLine("2026-08-18T08:00:00Z", "typed", "hello"),
		`{"type":"ai-title","aiTitle":"Fix promo code redirect"}`,
	)
	if tr.Title != "Fix promo code redirect" {
		t.Errorf("Title = %q, want the generated title", tr.Title)
	}

	tr = parse(t, userLine("2026-08-18T08:00:00Z", "typed", "no titles yet"))
	if tr.Title != "no titles yet" {
		t.Errorf("Title = %q, want the first prompt", tr.Title)
	}
}

// TestToolResultsAreNotUserMessages: tool echoes are also recorded as user
// records. promptSource is the discriminator, not the content shape.
func TestToolResultsAreNotUserMessages(t *testing.T) {
	tr := parse(t,
		userLine("2026-08-18T08:00:00Z", "typed", "the real question"),
		assistantToolLine("2026-08-18T08:00:05Z", "Bash"),
		toolResultLine("2026-08-18T08:00:06Z"),
	)
	if tr.LastMessage != "the real question" {
		t.Errorf("LastMessage = %q, want the typed prompt", tr.LastMessage)
	}
	if tr.LastTool != "Bash" {
		t.Errorf("LastTool = %q, want %q", tr.LastTool, "Bash")
	}
}

func TestSidechainRecordsAreIgnored(t *testing.T) {
	tr := parse(t,
		userLine("2026-08-18T08:00:00Z", "typed", "what the human asked"),
		`{"type":"user","timestamp":"2026-08-18T08:00:10Z","promptSource":"typed",`+
			`"isSidechain":true,"message":{"role":"user","content":"subagent instructions"}}`,
	)
	if tr.LastMessage != "what the human asked" {
		t.Errorf("LastMessage = %q, subagent prompts must not win", tr.LastMessage)
	}
}

// TestDurationMeasuredFromSegment covers /rename mid-session: the handoff
// should report time on the current work, not the whole evening.
func TestDurationMeasuredFromSegment(t *testing.T) {
	tr := parse(t,
		`{"type":"custom-title","customTitle":"U1"}`,
		userLine("2026-08-18T08:00:00Z", "typed", "work on U1"),
		assistantToolLine("2026-08-18T09:00:00Z", "Edit"),
		`{"type":"custom-title","customTitle":"U2"}`,
		userLine("2026-08-18T12:00:00Z", "typed", "now work on U2"),
		assistantToolLine("2026-08-18T12:30:00Z", "Bash"),
	)
	if tr.Title != "U2" {
		t.Fatalf("Title = %q, want U2", tr.Title)
	}
	if want := 30 * time.Minute; tr.Duration() != want {
		t.Errorf("Duration() = %v, want %v — measured from the rename, not session start", tr.Duration(), want)
	}
	if want := "2026-08-18T08:00:00Z"; tr.StartedAt.UTC().Format(time.RFC3339) != want {
		t.Errorf("StartedAt = %v, want the original start %s", tr.StartedAt, want)
	}
}

func TestDurationWithoutRenameSpansTheSession(t *testing.T) {
	tr := parse(t,
		userLine("2026-08-18T08:00:00Z", "typed", "start"),
		assistantToolLine("2026-08-18T10:15:00Z", "Read"),
	)
	if want := 2*time.Hour + 15*time.Minute; tr.Duration() != want {
		t.Errorf("Duration() = %v, want %v", tr.Duration(), want)
	}
}

// TestLongLinesDoNotTruncateTheParse guards the reason bufio.Scanner is not
// used. The fixture line sits deliberately between Scanner's 64 KB token limit
// and maxDecode: Scanner would stop dead here, silently losing everything after
// it — including the last prompt, which is the point of the file.
func TestLongLinesDoNotTruncateTheParse(t *testing.T) {
	long := `{"type":"user","timestamp":"2026-08-18T08:00:01Z","message":{"role":"user",` +
		`"content":[{"type":"tool_result","content":"` + strings.Repeat("x", 100_000) + `"}]}}`
	if len(long) <= 64*1024 || len(long) >= maxDecode {
		t.Fatalf("fixture is %d bytes; it must sit between the Scanner limit and maxDecode", len(long))
	}
	tr := parse(t,
		userLine("2026-08-18T08:00:00Z", "typed", "first"),
		long,
		userLine("2026-08-18T08:05:00Z", "typed", "after the big line"),
	)
	if tr.LastMessage != "after the big line" {
		t.Errorf("LastMessage = %q — parsing stopped at the oversized line", tr.LastMessage)
	}
}

// TestOversizeLinesAreSkippedNotDecoded covers the cap itself: a handful of
// multi-megabyte tool payloads dominate a real transcript, and decoding them
// tripled the cost of a per-turn hook. Skipping them must not disturb anything
// around them.
func TestOversizeLinesAreSkippedNotDecoded(t *testing.T) {
	huge := `{"type":"user","timestamp":"2026-08-18T08:00:01Z","promptSource":"typed",` +
		`"message":{"role":"user","content":"` + strings.Repeat("x", maxDecode) + `"}}`
	tr := parse(t,
		userLine("2026-08-18T08:00:00Z", "typed", "before"),
		huge,
		assistantToolLine("2026-08-18T08:02:00Z", "Bash"),
	)
	if tr.LastMessage != "before" {
		t.Errorf("LastMessage = %q, want the last prompt under the cap", tr.LastMessage)
	}
	if tr.LastTool != "Bash" {
		t.Errorf("LastTool = %q — records after an oversize line must still be read", tr.LastTool)
	}
}

func TestMalformedLinesAreSkipped(t *testing.T) {
	tr := parse(t,
		userLine("2026-08-18T08:00:00Z", "typed", "good"),
		`{"type":"user","timestamp":`, // a transcript being appended to mid-read
	)
	if tr.LastMessage != "good" {
		t.Errorf("LastMessage = %q, a partial trailing line must not lose the handoff", tr.LastMessage)
	}
}

func TestStructuredContentIsFlattened(t *testing.T) {
	tr := parse(t, `{"type":"user","timestamp":"2026-08-18T08:00:00Z","promptSource":"typed",`+
		`"message":{"role":"user","content":[{"type":"image"},{"type":"text","text":"look at this"}]}}`)
	if tr.LastMessage != "look at this" {
		t.Errorf("LastMessage = %q, want the text block", tr.LastMessage)
	}
}

func TestEmptyTranscript(t *testing.T) {
	tr, err := parseTranscript(strings.NewReader(""))
	if err != nil {
		t.Fatalf("parseTranscript on empty input = %v, want nil", err)
	}
	if tr.Title != "" || tr.LastMessage != "" || tr.Duration() != 0 {
		t.Errorf("empty transcript should yield a zero record, got %+v", tr)
	}
}

// TestTypeKeyIsNotAlwaysFirst is a regression test for a prefilter that only
// looked at the front of the line. Real assistant records lead with parentUuid
// and carry the whole message object — thinking blocks and all — before the
// top-level type, which can land thousands of bytes in.
func TestTypeKeyIsNotAlwaysFirst(t *testing.T) {
	padded := `{"parentUuid":"fed0c394","isSidechain":false,"message":{"model":"claude-opus-5",` +
		`"type":"message","role":"assistant","content":[{"type":"thinking","thinking":"",` +
		`"signature":"` + strings.Repeat("A", 4000) + `"},{"type":"tool_use","name":"Grep"}]},` +
		`"timestamp":"2026-08-18T08:30:00Z","type":"assistant"}`
	if strings.Index(padded, `"type":"assistant"`) < 1000 {
		t.Fatalf("fixture must place the top-level type well past the front, got offset %d",
			strings.Index(padded, `"type":"assistant"`))
	}

	tr := parse(t,
		userLine("2026-08-18T08:00:00Z", "typed", "find it"),
		padded,
	)
	if tr.LastTool != "Grep" {
		t.Errorf("LastTool = %q, want %q — the prefilter must scan the whole line", tr.LastTool, "Grep")
	}
}
