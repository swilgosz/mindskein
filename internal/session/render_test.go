package session

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRenderEmpty(t *testing.T) {
	var out bytes.Buffer
	if err := Render(&out, nil, time.Now(), RenderOptions{}); err != nil {
		t.Fatalf("Render() = %v, want nil", err)
	}
	got := out.String()
	if !strings.Contains(got, "LIVE SESSIONS") {
		t.Errorf("missing heading:\n%s", got)
	}
	// An empty registry is the normal state before any session runs, so it
	// gets a hint rather than a bare heading or an error.
	if !strings.Contains(got, "none recorded yet") {
		t.Errorf("empty registry should explain itself:\n%s", got)
	}
}

func TestRenderColumnsAlign(t *testing.T) {
	now := time.Date(2026, 8, 17, 23, 10, 0, 0, time.UTC)
	sessions := []*Session{
		{
			ID: "912d5686-9641", ProjectPath: "/Users/seb/SecondBrain/6. Spaces/62. Business",
			Status: StatusRunning, LastEvent: "Bash", LastEventAt: now.Add(-30 * time.Second),
		},
		{
			ID: "d776166a-8654", ProjectPath: "/Users/seb/Projects/mindskein",
			Status: StatusDone, LastEvent: "Stop", LastEventAt: now.Add(-15 * time.Minute),
		},
	}

	var out bytes.Buffer
	if err := Render(&out, sessions, now, RenderOptions{}); err != nil {
		t.Fatalf("Render() = %v, want nil", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want 5 (heading, 2 rows, blank, summary):\n%s", len(lines), out.String())
	}

	// The two rows must line up: same column offsets for status.
	if strings.Index(lines[1], "running") != strings.Index(lines[2], "done") {
		t.Errorf("status column not aligned:\n%s\n%s", lines[1], lines[2])
	}
	if !strings.Contains(lines[1], "912d5686") || !strings.Contains(lines[1], "62. Business") {
		t.Errorf("first row missing id or project:\n%s", lines[1])
	}
	if !strings.Contains(lines[1], "<1m") || !strings.Contains(lines[2], "15m") {
		t.Errorf("ages wrong:\n%s\n%s", lines[1], lines[2])
	}
	if !strings.Contains(lines[1], "(Bash)") {
		t.Errorf("last event missing:\n%s", lines[1])
	}
	if want := "2 sessions · 1 running"; !strings.Contains(lines[4], want) {
		t.Errorf("summary = %q, want it to contain %q", lines[4], want)
	}
}

// TestRenderMarksStale covers the gap left by there being no session-end hook:
// a killed terminal leaves its last status behind forever.
func TestRenderMarksStale(t *testing.T) {
	now := time.Date(2026, 8, 17, 23, 10, 0, 0, time.UTC)
	sessions := []*Session{{
		ID: "dead0001", ProjectPath: "/Users/seb/Projects/old",
		Status: StatusWaiting, LastEvent: "idle_prompt",
		LastEventAt: now.Add(-StaleAfter - time.Minute),
	}}

	var out bytes.Buffer
	if err := Render(&out, sessions, now, RenderOptions{ShowStale: true}); err != nil {
		t.Fatalf("Render() = %v, want nil", err)
	}
	got := out.String()
	if !strings.Contains(got, "stale") {
		t.Errorf("a session past StaleAfter should be marked stale:\n%s", got)
	}
	// Stale sessions must not be counted as running.
	if !strings.Contains(got, "0 running") {
		t.Errorf("stale session counted as running:\n%s", got)
	}
}

func TestRenderSingularSummary(t *testing.T) {
	now := time.Now()
	var out bytes.Buffer
	if err := Render(&out, []*Session{{ID: "a", Status: StatusRunning, LastEventAt: now}}, now, RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	if want := "1 session · 1 running"; !strings.Contains(out.String(), want) {
		t.Errorf("summary should read %q:\n%s", want, out.String())
	}
}

func TestRenderTruncatesLongProject(t *testing.T) {
	now := time.Now()
	long := "/Users/seb/" + strings.Repeat("verylongdirname", 4)
	var out bytes.Buffer
	if err := Render(&out, []*Session{{ID: "a", ProjectPath: long, Status: StatusRunning, LastEventAt: now}}, now, RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if len([]rune(line)) > 90 {
			t.Errorf("line %d runes wide, want it capped:\n%s", len([]rune(line)), line)
		}
	}
	if !strings.Contains(out.String(), "…") {
		t.Error("a truncated project name should be marked with an ellipsis")
	}
}

func TestAge(t *testing.T) {
	for _, c := range []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "<1m"},
		{time.Minute, "1m"},
		{47 * time.Minute, "47m"},
		{time.Hour + 7*time.Minute, "1h07m"},
		{25 * time.Hour, "1d1h"},
		{50 * time.Hour, "2d2h"},
	} {
		if got := age(c.in); got != c.want {
			t.Errorf("age(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
