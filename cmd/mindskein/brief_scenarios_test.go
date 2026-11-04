package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/swilgosz/mindskein/internal/handoff"
	"github.com/swilgosz/mindskein/internal/priorities"
	"github.com/swilgosz/mindskein/internal/session"
)

// briefHome sets up an install with a priorities file, a recorded session and a handoff,
// so a brief has something to say in all three sections.
func briefHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("MINDSKEIN_HOME", home)
	noGitHub(t)

	notes := t.TempDir()
	body := "## Priorities\n- [ ] !1 [[Wren Deploy Tool]] — ship the installer\n" +
		"- [ ] !3 Dig Deeper feature — later\n"
	if err := os.WriteFile(filepath.Join(notes, "PRIORITIES.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	config := "[priorities]\nfile = " + strconv.Quote(filepath.Join(notes, "PRIORITIES.md")) + "\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	payload := `{"session_id":"aaaa1111","cwd":"/Users/seb/Projects/mindskein","tool_name":"Edit"}`
	if err := run([]string{"hook", "pre-tool-use"}, strings.NewReader(payload), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	return home
}

// noGitHub answers the brief's REVIEWS with an empty queue, so a test about
// the other sections never reaches the real gh.
func noGitHub(t *testing.T) {
	t.Helper()
	previous := ghRun
	ghRun = (&fakeGitHub{login: "me"}).run
	t.Cleanup(func() { ghRun = previous })
}

// ended parses a stored end time, which is always UTC on disk.
func ended(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("time.Parse(%q) = %v", s, err)
	}
	return parsed
}

func writeHandoff(t *testing.T, h *handoff.Handoff) {
	t.Helper()
	store, err := handoff.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(h); err != nil {
		t.Fatal(err)
	}
}

func runBrief(t *testing.T, args ...string) string {
	t.Helper()
	var stdout bytes.Buffer
	if err := run(append([]string{"brief"}, args...), nil, &stdout, io.Discard); err != nil {
		t.Fatalf("run(brief %v) = %v, want nil", args, err)
	}
	return stdout.String()
}

// section is one block of the brief, from its heading to the blank line that
// ends it.
func section(out, heading string) string {
	_, rest, ok := strings.Cut(out, heading)
	if !ok {
		return ""
	}
	body, _, _ := strings.Cut(strings.TrimLeft(rest, "\n"), "\n\n")
	return body
}

func TestBriefCommand(t *testing.T) {
	t.Run("prints all three sections", func(t *testing.T) {
		briefHome(t)
		writeHandoff(t, &handoff.Handoff{
			SessionID: "aaaa1111",
			Title:     "Brief renderer",
			CWD:       "/Users/seb/Projects/mindskein",
			Repo:      "/Users/seb/Projects/mindskein",
			Message:   "compose the three sections",
			EndedAt:   ended(t, "2026-08-21T18:00:00Z"),
		})

		out := runBrief(t)
		for _, want := range []string{
			priorities.Heading, "Wren Deploy Tool",
			session.Heading, "aaaa1111",
			handoff.Heading, "compose the three sections",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("brief missing %q:\n%s", want, out)
			}
		}
		if got := strings.Index(out, session.Heading); got < strings.Index(out, priorities.Heading) {
			t.Errorf("sections out of order:\n%s", out)
		}
		if got := strings.Index(out, handoff.Heading); got < strings.Index(out, session.Heading) {
			t.Errorf("sections out of order:\n%s", out)
		}
	})

	t.Run("an unconfigured priorities file still leaves the other two sections printed", func(t *testing.T) {
		t.Setenv("MINDSKEIN_HOME", t.TempDir())
		noGitHub(t)
		out := runBrief(t)
		if !strings.Contains(out, "config.toml") {
			t.Errorf("brief does not say where to configure a priorities file:\n%s", out)
		}
		for _, want := range []string{session.Heading, handoff.Heading} {
			if !strings.Contains(out, want) {
				t.Errorf("brief missing %q after an unconfigured priorities file:\n%s", want, out)
			}
		}
	})

	t.Run("an empty registry prints the sessions hint", func(t *testing.T) {
		t.Setenv("MINDSKEIN_HOME", t.TempDir())
		noGitHub(t)
		if out := runBrief(t); !strings.Contains(out, "none recorded yet") {
			t.Errorf("brief does not explain an empty registry:\n%s", out)
		}
	})

	t.Run("no handoffs prints the where-we-left-off hint", func(t *testing.T) {
		briefHome(t)
		out := runBrief(t)
		if !strings.Contains(out, "nothing recorded yet") {
			t.Errorf("brief does not explain an empty handoff store:\n%s", out)
		}
	})

	t.Run("a session titled by a raw first prompt cannot break the sessions block", func(t *testing.T) {
		briefHome(t)
		// The sessions block names sessions by their handoff title, and an
		// unrenamed session's title is the first thing the person typed.
		writeHandoff(t, &handoff.Handoff{
			SessionID: "aaaa1111",
			Title:     "fix \x1b[31mthe\x1b[0m bug\nin the parser",
			CWD:       "/Users/seb/Projects/mindskein",
			Repo:      "/Users/seb/Projects/mindskein",
			Message:   "carry on",
			EndedAt:   ended(t, "2026-08-21T18:00:00Z"),
		})

		out := runBrief(t)
		if strings.ContainsRune(out, '\x1b') {
			t.Errorf("an escape sequence reached the terminal:\n%q", out)
		}
		sessions := section(out, session.Heading)
		if strings.Count(sessions, "aaaa1111") != 1 {
			t.Errorf("the session is not on exactly one row:\n%q", sessions)
		}
		if strings.Contains(sessions, "in the parser\n") {
			t.Errorf("a newline in the title split the row:\n%q", sessions)
		}
	})

	t.Run("showing all widens every section at once", func(t *testing.T) {
		briefHome(t)
		if err := run([]string{"hook", "session-end"}, strings.NewReader(
			`{"session_id":"aaaa1111","reason":"logout"}`), io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
		for i := range handoff.DefaultLimit + 2 {
			writeHandoff(t, &handoff.Handoff{
				SessionID: "h" + strconv.Itoa(i),
				Repo:      "/code/p" + strconv.Itoa(i),
				Message:   "prompt " + strconv.Itoa(i),
				EndedAt:   ended(t, "2026-08-21T18:00:00Z"),
			})
		}

		narrow := runBrief(t)
		if strings.Contains(narrow, "Dig Deeper") {
			t.Errorf("the backlog showed without --all:\n%s", narrow)
		}
		if !strings.Contains(narrow, "2 more (--all)") {
			t.Errorf("the handoff count was not reported:\n%s", narrow)
		}
		if !strings.Contains(narrow, "1 ended") {
			t.Errorf("the ended session was not reported as hidden:\n%s", narrow)
		}

		wide := runBrief(t, "--all")
		if !strings.Contains(wide, "Dig Deeper") {
			t.Errorf("--all did not widen the priorities:\n%s", wide)
		}
		if strings.Contains(wide, "more (--all)") {
			t.Errorf("--all still hid handoffs:\n%s", wide)
		}
		if !strings.Contains(wide, "logout") {
			t.Errorf("--all did not show the ended session:\n%s", wide)
		}
	})
}

func TestBriefJSON(t *testing.T) {
	t.Run("brief --json carries every row the text brief prints and the counts of what it hid, from the same view", func(t *testing.T) {
		briefHome(t)
		base := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
		for i := range 7 {
			writeHandoff(t, &handoff.Handoff{SessionID: "hand000" + string(rune('1'+i)), Project: "stream-" + string(rune('a'+i)),
				CWD: "/tmp/x", Status: "done", StartedAt: base.Add(time.Duration(i) * time.Hour), EndedAt: base.Add(time.Duration(i)*time.Hour + time.Minute),
				Message: "step " + string(rune('a'+i))})
		}
		text := runBrief(t)
		var b struct {
			Priorities struct {
				Rows   []struct{ Level, Label string }
				Hidden int
			}
			Sessions struct {
				Rows []struct {
					ShortID string `json:"short_id"`
				}
			}
			Handoffs struct {
				Rows   []struct{ Label, Message string }
				Hidden int
			}
		}
		if err := json.Unmarshal([]byte(runBrief(t, "--json")), &b); err != nil {
			t.Fatal(err)
		}
		if len(b.Priorities.Rows) != 1 || b.Priorities.Rows[0].Level != "!1" || !strings.Contains(text, b.Priorities.Rows[0].Label) {
			t.Errorf("priorities = %+v, want the one !1 the text prints", b.Priorities.Rows)
		}
		if b.Priorities.Hidden != 1 || !strings.Contains(text, "1 more in the backlog") {
			t.Errorf("priorities hidden = %d, want the 1 the text counts", b.Priorities.Hidden)
		}
		if len(b.Sessions.Rows) != 1 || !strings.Contains(text, b.Sessions.Rows[0].ShortID) {
			t.Errorf("sessions = %+v, want the one the text prints", b.Sessions.Rows)
		}
		if len(b.Handoffs.Rows) != 5 || b.Handoffs.Hidden != 2 || !strings.Contains(text, "2 more (--all)") {
			t.Fatalf("handoffs = %d rows, %d hidden; want 5 and the 2 the text counts", len(b.Handoffs.Rows), b.Handoffs.Hidden)
		}
		for _, r := range b.Handoffs.Rows {
			if !strings.Contains(text, r.Label) || !strings.Contains(text, r.Message) {
				t.Errorf("handoff %q / %q is in the JSON but not in the text", r.Label, r.Message)
			}
		}
	})
}
