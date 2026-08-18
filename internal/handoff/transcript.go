package handoff

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Transcript is everything one pass over a session transcript yields. Reading
// it is the whole cost of a handoff, so it is deliberately one pass.
type Transcript struct {
	// Title is what the session is called: the last /rename if there was one,
	// else the title Claude generated, else the opening prompt.
	Title string

	// StartedAt is the first human prompt; SegmentAt is the first human prompt
	// of the current stretch of work, which differs from StartedAt only after a
	// /rename part-way through. LastAt is the most recent activity of any kind.
	StartedAt time.Time
	SegmentAt time.Time
	LastAt    time.Time

	LastTool    string
	LastMessage string
}

// Duration is how long the current stretch of work has been running. Measured
// from SegmentAt rather than StartedAt so that renaming a session part-way —
// /rename U1, work, /clear, /rename U2 — reports time spent on U2, not the
// whole evening. Without a rename the two are identical.
func (t *Transcript) Duration() time.Duration {
	if t.SegmentAt.IsZero() || t.LastAt.Before(t.SegmentAt) {
		return 0
	}
	return t.LastAt.Sub(t.SegmentAt)
}

// maxMessage caps the stored prompt. The handoff is rewritten on every turn and
// is meant to be glanced at, so a pasted stack trace is truncated rather than
// copied in full.
const maxMessage = 1500

// interesting are the only record types worth decoding. Transcripts reach 13 MB,
// so lines are matched as raw text first and only the survivors are
// unmarshalled. The JSON is compact, so these substrings are exact — but they
// can appear anywhere in the line, not at the front. A false positive is
// harmless: the decoded type field decides.
var interesting = []string{
	`"type":"user"`,
	`"type":"assistant"`,
	`"type":"ai-title"`,
	`"type":"custom-title"`,
}

// record is the subset of a transcript line that a handoff needs.
type record struct {
	Type         string    `json:"type"`
	Timestamp    time.Time `json:"timestamp"`
	PromptSource string    `json:"promptSource"`
	IsSidechain  bool      `json:"isSidechain"`
	AITitle      string    `json:"aiTitle"`
	CustomTitle  string    `json:"customTitle"`
	Message      struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// block is one element of a structured message content array.
type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"`
}

// ReadTranscript parses a Claude Code session transcript.
func ReadTranscript(path string) (*Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening transcript: %w", err)
	}
	defer f.Close()
	return parseTranscript(f)
}

func parseTranscript(r io.Reader) (*Transcript, error) {
	var (
		t              Transcript
		aiTitle        string
		customTitle    string
		firstMessage   string
		segmentPending bool
	)

	// bufio.Reader, not bufio.Scanner: a single tool_result line routinely
	// exceeds Scanner's 64 KB token limit, and Scanner would stop at the first
	// one — silently truncating the parse to the top of the file.
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			if rec, ok := decode(line); ok {
				switch rec.Type {
				case "custom-title":
					// Renaming starts a new stretch of work; the next human
					// prompt opens it.
					if rec.CustomTitle != "" {
						customTitle = rec.CustomTitle
						segmentPending = true
					}
				case "ai-title":
					if rec.AITitle != "" {
						aiTitle = rec.AITitle
					}
				case "user":
					stamp(&t, rec.Timestamp)
					// promptSource is what separates a human prompt from the
					// tool results that are also recorded as user records. The
					// content shape is not a reliable discriminator.
					if rec.PromptSource == "" {
						break
					}
					if text := textOf(rec.Message.Content); text != "" {
						if t.StartedAt.IsZero() {
							t.StartedAt = rec.Timestamp
						}
						if firstMessage == "" {
							firstMessage = text
						}
						if segmentPending || t.SegmentAt.IsZero() {
							t.SegmentAt = rec.Timestamp
							segmentPending = false
						}
						t.LastMessage = truncate(text, maxMessage)
					}
				case "assistant":
					stamp(&t, rec.Timestamp)
					if tool := toolOf(rec.Message.Content); tool != "" {
						t.LastTool = tool
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("reading transcript: %w", err)
		}
	}

	// A rename with no prompt after it leaves the segment where it was rather
	// than reporting a zero-length one.
	if t.SegmentAt.IsZero() {
		t.SegmentAt = t.StartedAt
	}

	// Precedence matters and is not "the last title record wins": both kinds
	// are re-emitted throughout the file and interleave, so the final title
	// record is usually the generated one even in a session that was renamed.
	// An explicit rename outranks a generated title whenever one exists.
	switch {
	case customTitle != "":
		t.Title = customTitle
	case aiTitle != "":
		t.Title = aiTitle
	default:
		t.Title = truncate(strings.TrimSpace(firstMessage), 60)
	}
	return &t, nil
}

// maxDecode caps the size of a line worth unmarshalling. In a real 13 MB
// transcript, six tool_result lines carry ten of those megabytes, and decoding
// them costs 140 ms of a 190 ms parse — on a hook that runs every turn. Nothing
// that large is ever a typed prompt, and a paste that big would be truncated to
// maxMessage anyway, so skipping them loses nothing a handoff would show.
const maxDecode = 256 << 10

// decode rejects uninteresting lines before paying for JSON parsing, and
// tolerates malformed ones: a transcript being appended to while it is read can
// end in a partial line, which is not a reason to lose the whole handoff.
func decode(line string) (record, bool) {
	if len(line) > maxDecode {
		return record{}, false
	}
	// The whole line is scanned, not a prefix of it: key order is not stable.
	// Assistant records lead with parentUuid and carry the entire message
	// object — thinking blocks included — before the top-level "type", which
	// lands 1800 bytes in. Bounding this to a prefix dropped 216 of 217
	// assistant records in a real transcript while looking like it worked.
	// Scanning in full is not the expensive part anyway; decoding is.
	match := false
	for _, m := range interesting {
		if strings.Contains(line, m) {
			match = true
			break
		}
	}
	if !match {
		return record{}, false
	}
	var rec record
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return record{}, false
	}
	// Subagent records carry the parent's session id but describe work the
	// human never typed, so they must not become the last message.
	if rec.IsSidechain {
		return record{}, false
	}
	return rec, true
}

func stamp(t *Transcript, ts time.Time) {
	if !ts.IsZero() && ts.After(t.LastAt) {
		t.LastAt = ts
	}
}

// textOf pulls readable text out of a message content field, which is a bare
// string for a typed prompt and an array of blocks once images or attachments
// are involved.
func textOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var blocks []block
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(parts, "\n")
}

// toolOf returns the last tool invoked in one assistant message.
func toolOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var blocks []block
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	name := ""
	for _, b := range blocks {
		if b.Type == "tool_use" && b.Name != "" {
			name = b.Name
		}
	}
	return name
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}
