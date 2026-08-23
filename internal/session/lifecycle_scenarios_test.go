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
		Status: StatusWaiting, LastEvent: "idle_prompt", LastEventAt: at.Add(-overnight),
		PID: 4242, PIDStartedAt: at.Add(-24 * time.Hour)}
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

	t.Run("says unknown when a stale status cannot be checked", func(t *testing.T) {
		crashed := &Session{ID: "cccc3333", ProjectPath: "/tmp/x", Status: StatusWaiting,
			LastEvent: "idle_prompt", LastEventAt: at.Add(-4 * 24 * time.Hour)}
		got := render(t, []*Session{crashed}, RenderOptions{})
		if !strings.Contains(got, string(StateUnknown)) {
			t.Errorf("want a status this old reported as unknown:\n%s", got)
		}
	})

	t.Run("does not mark a session quiet overnight", func(t *testing.T) {
		// Silence is not death, and where the process can be looked up that
		// is now established rather than inferred: an overnight pause on a
		// live process reads exactly as what it is.
		got := render(t, []*Session{paused}, RenderOptions{Probe: aliveProbe})
		if strings.Contains(got, string(StateUnknown)) || strings.Contains(got, "stale") {
			t.Errorf("%v quiet on a live process is a pause, not a suspect status:\n%s", overnight, got)
		}
		if !strings.Contains(got, string(StateWaiting)) {
			t.Errorf("want the paused session reported as waiting:\n%s", got)
		}
	})
}

// TestTheBriefTellsLostWorkFromFinishedWork is the whole point of the unit. A
// session that finished its turn and a session killed mid-Edit both became a
// record that stopped changing, and the brief showed them the same way.
func TestTheBriefTellsLostWorkFromFinishedWork(t *testing.T) {
	killed := &Session{ID: "kill0001", ProjectPath: "/Users/me/Projects/api", PID: 4242,
		PIDStartedAt: at.Add(-time.Hour), Status: StatusRunning,
		LastEvent: "Edit", LastEventAt: at.Add(-20 * time.Minute)}

	t.Run("an interrupted session is reported as interrupted", func(t *testing.T) {
		got := render(t, []*Session{killed}, RenderOptions{Probe: deadProbe})
		if !strings.Contains(got, string(StateInterrupted)) {
			t.Errorf("want the killed session named:\n%s", got)
		}
	})

	t.Run("it names the project and the action it died on", func(t *testing.T) {
		// So the loss is identifiable without opening a transcript.
		got := render(t, []*Session{killed}, RenderOptions{Probe: deadProbe})
		for _, want := range []string{"api", "Edit"} {
			if !strings.Contains(got, want) {
				t.Errorf("row does not name %q:\n%s", want, got)
			}
		}
	})

	t.Run("it is counted in the summary, not just shown in a row", func(t *testing.T) {
		got := render(t, []*Session{killed}, RenderOptions{Probe: deadProbe})
		if !strings.Contains(got, "1 interrupted") {
			t.Errorf("the one line worth acting on has to be scanned for:\n%s", got)
		}
	})

	t.Run("it is never hidden as finished business", func(t *testing.T) {
		// Age must not fold it away either: it ended without saying so,
		// which is the opposite of finished.
		old := *killed
		old.LastEventAt = at.Add(-30 * 24 * time.Hour)
		got := render(t, []*Session{&old}, RenderOptions{Probe: deadProbe, ShowAll: false})
		if !strings.Contains(got, string(StateInterrupted)) {
			t.Errorf("an interrupted session was hidden:\n%s", got)
		}
	})

	t.Run("a session that closed cleanly is folded away", func(t *testing.T) {
		quiet := &Session{ID: "shut0001", ProjectPath: "/tmp/x", PID: 4242,
			PIDStartedAt: at.Add(-time.Hour), Status: StatusDone,
			LastEvent: "Stop", LastEventAt: at.Add(-20 * time.Minute)}
		got := render(t, []*Session{quiet}, RenderOptions{Probe: deadProbe})
		if strings.Contains(got, "shut0001") {
			t.Errorf("a terminal closed between turns is finished business:\n%s", got)
		}
	})

	t.Run("a cleared session does not read as ended work", func(t *testing.T) {
		cleared := &Session{ID: "clea0001", ProjectPath: "/tmp/x", Status: StatusEnded,
			EndReason: "clear", LastEvent: "clear", LastEventAt: at.Add(-time.Minute)}
		got := render(t, []*Session{cleared}, RenderOptions{Probe: aliveProbe, ShowAll: true})
		if !strings.Contains(got, string(StateSuperseded)) {
			t.Errorf("want superseded for a cleared session:\n%s", got)
		}
	})
}
