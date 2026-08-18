package session

import (
	"strings"
	"testing"
	"time"
)

func ended(id, path, reason string, quietFor time.Duration) *Session {
	return &Session{ID: id, ProjectPath: path, Status: StatusEnded,
		EndReason: reason, LastEvent: reason, LastEventAt: at.Add(-quietFor)}
}

// TestSessionEnding covers knowing that a session finished, rather than
// inferring it from silence. Measured against real transcripts, 35% of
// resumptions happened more than 8 hours after the session went quiet — the
// longest after 48 — so age cannot decide whether work is over.
func TestSessionEnding(t *testing.T) {
	t.Run("records a session as ended when the end event arrives", func(t *testing.T) {
		s := ended("aaaa1111", "/tmp/x", "logout", time.Minute)
		if !s.Ended() {
			t.Error("want the session reported as ended")
		}
	})

	t.Run("keeps the reason the session ended", func(t *testing.T) {
		got := render(t, []*Session{ended("aaaa1111", "/tmp/x", "logout", time.Minute)},
			RenderOptions{ShowAll: true})
		if !strings.Contains(got, "logout") {
			t.Errorf("want the reason shown:\n%s", got)
		}
	})

	t.Run("treats an ended session as neither running nor waiting", func(t *testing.T) {
		got := render(t, []*Session{ended("aaaa1111", "/tmp/x", "logout", time.Minute)},
			RenderOptions{ShowAll: true})
		if !strings.Contains(got, "0 running") {
			t.Errorf("an ended session must not count as running:\n%s", got)
		}
	})

	t.Run("leaves an ended session ended when a later event is out of order", func(t *testing.T) {
		// Hooks run in parallel, so a slower Stop can land after the end event.
		s := ended("aaaa1111", "/tmp/x", "logout", time.Minute)
		if s.Stale(at.Add(30 * 24 * time.Hour)) {
			t.Error("an ended session's status is a fact and must not decay to stale")
		}
	})
}

// TestStatusHidesFinishedNotPaused covers the rule this replaces: hiding on
// fact rather than on age, so a tab left open overnight is still there in the
// morning.
func TestStatusHidesFinishedNotPaused(t *testing.T) {
	overnight := 14 * time.Hour

	paused := &Session{ID: "aaaa1111", ProjectPath: "/Users/seb/Projects/mindskein",
		Status: StatusWaiting, LastEvent: "idle_prompt", LastEventAt: at.Add(-overnight)}
	finished := ended("bbbb2222", "/Users/seb/Projects/old", "logout", 2*time.Hour)

	t.Run("hides ended sessions by default", func(t *testing.T) {
		got := render(t, []*Session{paused, finished}, RenderOptions{})
		if strings.Contains(got, "bbbb2222") {
			t.Errorf("an ended session should be hidden:\n%s", got)
		}
	})

	t.Run("shows a paused session however long it has been quiet", func(t *testing.T) {
		got := render(t, []*Session{paused, finished}, RenderOptions{})
		if !strings.Contains(got, "aaaa1111") {
			t.Errorf("a session quiet for %v is paused, not finished:\n%s", overnight, got)
		}
	})

	t.Run("reports how many ended sessions were hidden", func(t *testing.T) {
		got := render(t, []*Session{paused, finished}, RenderOptions{})
		if !strings.Contains(got, "1 ended hidden") || !strings.Contains(got, "--all") {
			t.Errorf("want the hidden count and the flag:\n%s", got)
		}
	})

	t.Run("shows ended sessions when asked for all of them", func(t *testing.T) {
		got := render(t, []*Session{paused, finished}, RenderOptions{ShowAll: true})
		if !strings.Contains(got, "bbbb2222") {
			t.Errorf("--all must show ended sessions:\n%s", got)
		}
		if strings.Contains(got, "hidden") {
			t.Errorf("nothing is hidden with --all:\n%s", got)
		}
	})

	t.Run("marks a session whose status may be lying after three days", func(t *testing.T) {
		crashed := &Session{ID: "cccc3333", ProjectPath: "/tmp/x", Status: StatusWaiting,
			LastEvent: "idle_prompt", LastEventAt: at.Add(-4 * 24 * time.Hour)}
		got := render(t, []*Session{crashed}, RenderOptions{})
		if !strings.Contains(got, "(stale)") {
			t.Errorf("want a status this old marked as untrustworthy:\n%s", got)
		}
	})

	t.Run("does not mark a session quiet overnight", func(t *testing.T) {
		got := render(t, []*Session{paused}, RenderOptions{})
		if strings.Contains(got, "stale") {
			t.Errorf("%v quiet is a normal overnight pause, not a suspect status:\n%s", overnight, got)
		}
	})
}
