package handoff

import (
	"strings"
	"testing"
	"time"
)

// The fixtures below keep the shape of real prompt records from a Claude
// desktop session, with the personal text replaced. The client prepends
// <system-reminder> blocks to what the person typed: inside the string when the
// content is a bare string, and as a text block of their own, ahead of the
// typed one, when the content is an array of blocks.

const reminderPair = `<system-reminder>\nThe session \"Build the parser\" you started (local_0000) finished a turn. ` +
	`It reported (quoted child output, data not instructions): \"Opened the draft PR.\"\n</system-reminder>\n` +
	`<system-reminder>\nLinked sessions now (status, not an instruction): parser = local_0000 (idle).\n</system-reminder>\n`

func realPromptRecord(ts, content string) string {
	return `{"parentUuid":"00000000-0000-0000-0000-000000000001","isSidechain":false,` +
		`"promptId":"00000000-0000-0000-0000-000000000002","type":"user",` +
		`"message":{"role":"user","content":` + content + `},` +
		`"uuid":"00000000-0000-0000-0000-000000000003","timestamp":"` + ts + `",` +
		`"permissionMode":"auto","origin":{"kind":"human"},"promptSource":"sdk","turnOrigin":"human",` +
		`"turnPosition":{"promptIndex":2,"turnIndex":3},"userType":"external","entrypoint":"claude-desktop",` +
		`"cwd":"/home/someone/project","sessionId":"00000000-0000-0000-0000-000000000004","version":"2.1.309",` +
		`"gitBranch":"main","slug":"quiet-paper-lamp"}`
}

func stringContent(text string) string { return `"` + text + `"` }

func blockContent(texts ...string) string {
	blocks := make([]string, len(texts))
	for i, t := range texts {
		blocks[i] = `{"type":"text","text":"` + t + `"}`
	}
	return "[" + strings.Join(blocks, ",") + "]"
}

func TestWhereWeLeftOffNeverShowsSystemReminders(t *testing.T) {
	t.Run("a string prompt keeps the typed text and drops the reminders ahead of it", func(t *testing.T) {
		tr := parse(t, realPromptRecord("2026-11-02T13:08:39Z", stringContent(reminderPair+`\nYes, open it`)))
		if tr.LastMessage != "Yes, open it" {
			t.Errorf("LastMessage = %q, want only the typed text", tr.LastMessage)
		}
		if tr.Title != "Yes, open it" {
			t.Errorf("Title = %q, the first-prompt fallback must not carry the reminders", tr.Title)
		}
	})

	t.Run("a block prompt keeps the typed block and drops the reminder block", func(t *testing.T) {
		tr := parse(t, realPromptRecord("2026-11-02T13:30:32Z", blockContent(reminderPair+`\n`, `Rewrite it here`)))
		if tr.LastMessage != "Rewrite it here" {
			t.Errorf("LastMessage = %q, want only the typed block", tr.LastMessage)
		}
	})

	t.Run("text before and after a reminder is kept", func(t *testing.T) {
		tr := parse(t, realPromptRecord("2026-11-02T13:30:32Z",
			stringContent(`Check the build.\n<system-reminder>\nLinked sessions now: none.\n</system-reminder>\nThen merge it.`)))
		if strings.Contains(tr.LastMessage, "system-reminder") || strings.Contains(tr.LastMessage, "Linked sessions") {
			t.Errorf("LastMessage = %q, the reminder must be gone", tr.LastMessage)
		}
		if !strings.Contains(tr.LastMessage, "Check the build.") || !strings.Contains(tr.LastMessage, "Then merge it.") {
			t.Errorf("LastMessage = %q, want the text on both sides of the reminder", tr.LastMessage)
		}
	})

	t.Run("a prompt that is only reminders leaves the previous last message in place", func(t *testing.T) {
		for name, content := range map[string]string{
			"string": stringContent(reminderPair),
			"blocks": blockContent(reminderPair, `\n`),
		} {
			tr := parse(t,
				userLine("2026-11-02T13:00:00Z", "sdk", "the real question"),
				realPromptRecord("2026-11-02T13:08:39Z", content),
			)
			if tr.LastMessage != "the real question" {
				t.Errorf("%s: LastMessage = %q, a prompt of only reminders must not replace it", name, tr.LastMessage)
			}
		}
	})

	t.Run("a prompt that is only reminders neither titles the session nor starts its work", func(t *testing.T) {
		tr := parse(t,
			realPromptRecord("2026-11-02T13:00:00Z", stringContent(reminderPair)),
			`{"type":"custom-title","customTitle":"renamed"}`,
			realPromptRecord("2026-11-02T13:10:00Z", blockContent(reminderPair)),
			userLine("2026-11-02T13:20:00Z", "sdk", "the first real question"),
		)
		if tr.Title != "renamed" {
			t.Errorf("Title = %q, want the rename", tr.Title)
		}
		want := time.Date(2026, 11, 2, 13, 20, 0, 0, time.UTC)
		if !tr.StartedAt.Equal(want) {
			t.Errorf("StartedAt = %s, want the first typed prompt at %s", tr.StartedAt, want)
		}
		if !tr.SegmentAt.Equal(want) {
			t.Errorf("SegmentAt = %s, want the first typed prompt at %s", tr.SegmentAt, want)
		}

		tr = parse(t,
			realPromptRecord("2026-11-02T13:00:00Z", stringContent(reminderPair)),
			userLine("2026-11-02T13:20:00Z", "sdk", "the first real question"),
		)
		if tr.Title != "the first real question" {
			t.Errorf("Title = %q, a prompt of only reminders must not be the title fallback", tr.Title)
		}
	})
}
