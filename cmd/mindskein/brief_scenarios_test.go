package main

import (
	"bytes"
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

// briefHome sets up an install with a plan, a recorded session and a handoff,
// so a brief has something to say in all three sections.
func briefHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("MINDSKEIN_HOME", home)

	vault := t.TempDir()
	plan := "## Priorities\n- [ ] !1 [[Wren Deploy Tool]] — ship the installer\n" +
		"- [ ] !3 Dig Deeper feature — later\n"
	if err := os.WriteFile(filepath.Join(vault, "plan.md"), []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}
	config := "[vault]\npath = " + strconv.Quote(vault) + "\nplan = \"plan.md\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	payload := `{"session_id":"aaaa1111","cwd":"/Users/seb/Projects/mindskein","tool_name":"Edit"}`
	if err := run([]string{"hook", "pre-tool-use"}, strings.NewReader(payload), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	return home
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

	t.Run("an unconfigured plan still leaves the other two sections printed", func(t *testing.T) {
		t.Setenv("MINDSKEIN_HOME", t.TempDir())
		out := runBrief(t)
		if !strings.Contains(out, "config.toml") {
			t.Errorf("brief does not say where to configure a plan:\n%s", out)
		}
		for _, want := range []string{session.Heading, handoff.Heading} {
			if !strings.Contains(out, want) {
				t.Errorf("brief missing %q after an unconfigured plan:\n%s", want, out)
			}
		}
	})

	t.Run("an empty registry prints the sessions hint", func(t *testing.T) {
		t.Setenv("MINDSKEIN_HOME", t.TempDir())
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
