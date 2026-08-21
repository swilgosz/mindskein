package session

import (
	"strings"
	"testing"
	"time"
)

const week = 7 * 24 * time.Hour

func quiet(id, path string, status Status, silent time.Duration) *Session {
	return &Session{ID: id, ProjectPath: path, Status: status,
		LastEvent: "idle_prompt", LastEventAt: at.Add(-silent)}
}

// TestStatusHidesOldSessions covers the retention horizon. Nothing removes a
// record whose process died without reporting an ending, so the registry only
// ever grows; the horizon is what keeps the listing readable meanwhile.
//
// It is set far past any real pause on purpose. Measured across real
// transcripts, 35% of resumptions came more than 8 hours after the session
// went quiet and the longest came after 48 — so a horizon in hours would hide
// work that is merely paused, which is the bug this must not reintroduce.
func TestStatusHidesOldSessions(t *testing.T) {
	opts := RenderOptions{HideAfter: week}

	t.Run("hides a session quiet for longer than the horizon", func(t *testing.T) {
		got := render(t, []*Session{quiet("aaaa1111", "/tmp/x", StatusWaiting, 8*24*time.Hour)}, opts)
		if strings.Contains(got, "aaaa1111") {
			t.Errorf("a session quiet past the horizon should be hidden:\n%s", got)
		}
	})

	t.Run("shows a session quiet for less than the horizon", func(t *testing.T) {
		got := render(t, []*Session{quiet("aaaa1111", "/tmp/x", StatusWaiting, 6*24*time.Hour)}, opts)
		if !strings.Contains(got, "aaaa1111") {
			t.Errorf("a session inside the horizon must still show:\n%s", got)
		}
	})

	t.Run("hides an old session whatever status it claims", func(t *testing.T) {
		// At a week, a record still saying "running" is a process that died
		// without reporting it — the status is not evidence it is alive.
		old := 8 * 24 * time.Hour
		got := render(t, []*Session{
			quiet("aaaa1111", "/tmp/x", StatusRunning, old),
			quiet("bbbb2222", "/tmp/x", StatusWaiting, old),
			quiet("cccc3333", "/tmp/x", StatusDone, old),
		}, opts)
		for _, id := range []string{"aaaa1111", "bbbb2222", "cccc3333"} {
			if strings.Contains(got, id) {
				t.Errorf("%s survived the horizon:\n%s", id, got)
			}
		}
	})

	t.Run("still shows a session paused overnight", func(t *testing.T) {
		got := render(t, []*Session{quiet("aaaa1111", "/tmp/x", StatusWaiting, 14*time.Hour)}, opts)
		if !strings.Contains(got, "aaaa1111") {
			t.Errorf("an overnight pause is not an ending:\n%s", got)
		}
	})

	t.Run("names the horizon in the hidden count", func(t *testing.T) {
		got := render(t, []*Session{
			quiet("aaaa1111", "/tmp/x", StatusWaiting, time.Minute),
			quiet("bbbb2222", "/tmp/x", StatusWaiting, 8*24*time.Hour),
		}, opts)
		if !strings.Contains(got, "1 older than 7d hidden") || !strings.Contains(got, "--all") {
			t.Errorf("want the count, the horizon and the flag:\n%s", got)
		}
	})

	t.Run("spells a sub-day horizon the way it was configured", func(t *testing.T) {
		got := render(t, []*Session{
			quiet("aaaa1111", "/tmp/x", StatusWaiting, time.Minute),
			quiet("bbbb2222", "/tmp/x", StatusWaiting, 48*time.Hour),
		}, RenderOptions{HideAfter: 36 * time.Hour})
		if !strings.Contains(got, "older than 1d12h") || strings.Contains(got, "0m0s") {
			t.Errorf("want the horizon spelled as configured, not as a Go duration:\n%s", got)
		}
	})

	t.Run("counts a session hidden for both reasons only once", func(t *testing.T) {
		got := render(t, []*Session{
			quiet("aaaa1111", "/tmp/x", StatusWaiting, time.Minute),
			ended("bbbb2222", "/tmp/x", "logout", 8*24*time.Hour),
		}, opts)
		if !strings.Contains(got, "1 ended hidden") {
			t.Errorf("an ended session past the horizon is still one session:\n%s", got)
		}
		if strings.Contains(got, "older than") {
			t.Errorf("counted twice, once per reason:\n%s", got)
		}
	})

	t.Run("shows old sessions when asked for all of them", func(t *testing.T) {
		got := render(t, []*Session{quiet("aaaa1111", "/tmp/x", StatusWaiting, 30*24*time.Hour)},
			RenderOptions{HideAfter: week, ShowAll: true})
		if !strings.Contains(got, "aaaa1111") {
			t.Errorf("--all must show sessions past the horizon:\n%s", got)
		}
		if strings.Contains(got, "hidden") {
			t.Errorf("nothing is hidden with --all:\n%s", got)
		}
	})

	t.Run("hides nothing by age when the horizon is zero", func(t *testing.T) {
		got := render(t, []*Session{quiet("aaaa1111", "/tmp/x", StatusWaiting, 30*24*time.Hour)},
			RenderOptions{})
		if !strings.Contains(got, "aaaa1111") {
			t.Errorf("a zero horizon must keep every session:\n%s", got)
		}
	})

	t.Run("says why nothing is left when every session is hidden", func(t *testing.T) {
		got := render(t, []*Session{
			ended("aaaa1111", "/tmp/x", "logout", time.Hour),
			quiet("bbbb2222", "/tmp/x", StatusWaiting, 8*24*time.Hour),
		}, opts)
		for _, want := range []string{"none open", "1 ended", "1 older than 7d", "--all"} {
			if !strings.Contains(got, want) {
				t.Errorf("empty listing missing %q:\n%s", want, got)
			}
		}
	})
}
