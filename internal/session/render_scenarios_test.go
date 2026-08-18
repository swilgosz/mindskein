package session

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

var at = time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)

func live(id, path, event string) *Session {
	return &Session{ID: id, ProjectPath: path, Status: StatusWaiting,
		LastEvent: event, LastEventAt: at.Add(-2 * time.Minute)}
}

func dead(id, path string) *Session {
	return &Session{ID: id, ProjectPath: path, Status: StatusWaiting,
		LastEvent: "idle_prompt", LastEventAt: at.Add(-StaleAfter - time.Hour)}
}

func render(t *testing.T, sessions []*Session, opts RenderOptions) string {
	t.Helper()
	var out bytes.Buffer
	if err := Render(&out, sessions, at, opts); err != nil {
		t.Fatalf("Render() = %v, want nil", err)
	}
	return out.String()
}

// TestStatusLabelling covers naming a session by what it is rather than by the
// folder it happened to be launched from. Several unrelated workstreams share
// one folder, so the folder alone cannot identify a session.
func TestStatusLabelling(t *testing.T) {
	vault := "/Users/seb/SecondBrain/6. Spaces/62. Business"

	t.Run("labels a session with its title when one is known", func(t *testing.T) {
		got := render(t, []*Session{live("aaaa1111", vault, "Bash")},
			RenderOptions{Labels: map[string]string{"aaaa1111": "Content"}})
		if !strings.Contains(got, "Content") {
			t.Errorf("want the title in the row:\n%s", got)
		}
	})

	t.Run("falls back to the folder name when no title is known", func(t *testing.T) {
		got := render(t, []*Session{live("aaaa1111", vault, "Bash")}, RenderOptions{})
		if !strings.Contains(got, "62. Business") {
			t.Errorf("want the folder as the fallback label:\n%s", got)
		}
	})

	t.Run("shows the folder alongside the title", func(t *testing.T) {
		got := render(t, []*Session{live("aaaa1111", vault, "Bash")},
			RenderOptions{Labels: map[string]string{"aaaa1111": "Content"}})
		if !strings.Contains(got, "Content") || !strings.Contains(got, "62. Business") {
			t.Errorf("want both the title and the folder:\n%s", got)
		}
	})

	t.Run("truncates a long title rather than wrapping the row", func(t *testing.T) {
		long := strings.Repeat("very long title ", 8)
		got := render(t, []*Session{live("aaaa1111", vault, "Bash")},
			RenderOptions{Labels: map[string]string{"aaaa1111": long}})
		for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
			if len([]rune(line)) > 120 {
				t.Errorf("line is %d runes, too wide:\n%s", len([]rune(line)), line)
			}
		}
		if !strings.Contains(got, "…") {
			t.Errorf("want an ellipsis marking the truncation:\n%s", got)
		}
	})

	t.Run("keeps two identically titled sessions distinguishable by folder", func(t *testing.T) {
		got := render(t, []*Session{
			live("aaaa1111", "/Users/seb/Projects/mindskein", "Bash"),
			live("bbbb2222", "/Users/seb/Projects/income-grid", "Edit"),
		}, RenderOptions{Labels: map[string]string{"aaaa1111": "planner", "bbbb2222": "planner"}})
		if !strings.Contains(got, "mindskein") || !strings.Contains(got, "income-grid") {
			t.Errorf("same title in two repos must stay tellable apart:\n%s", got)
		}
	})
}

// TestStatusStaleHandling covers keeping the signal visible: a day of dead
// sessions must not bury the two that are live.
func TestStatusStaleHandling(t *testing.T) {
	mixed := []*Session{
		live("aaaa1111", "/Users/seb/Projects/mindskein", "Bash"),
		dead("bbbb2222", "/Users/seb/Projects/old"),
		dead("cccc3333", "/Users/seb/Projects/older"),
	}

	t.Run("hides stale sessions by default", func(t *testing.T) {
		got := render(t, mixed, RenderOptions{})
		if strings.Contains(got, "bbbb2222") || strings.Contains(got, "cccc3333") {
			t.Errorf("stale sessions should be hidden by default:\n%s", got)
		}
		if !strings.Contains(got, "aaaa1111") {
			t.Errorf("the live session must still show:\n%s", got)
		}
	})

	t.Run("reports how many stale sessions were hidden", func(t *testing.T) {
		got := render(t, mixed, RenderOptions{})
		if !strings.Contains(got, "2 stale hidden") {
			t.Errorf("want the hidden count:\n%s", got)
		}
		if !strings.Contains(got, "--all") {
			t.Errorf("want the flag that reveals them:\n%s", got)
		}
	})

	t.Run("says nothing about stale sessions when there are none", func(t *testing.T) {
		got := render(t, mixed[:1], RenderOptions{})
		if strings.Contains(got, "stale") {
			t.Errorf("no stale sessions, so no stale line:\n%s", got)
		}
	})

	t.Run("shows stale sessions when asked for all of them", func(t *testing.T) {
		got := render(t, mixed, RenderOptions{ShowStale: true})
		for _, id := range []string{"aaaa1111", "bbbb2222", "cccc3333"} {
			if !strings.Contains(got, id) {
				t.Errorf("--all must show %s:\n%s", id, got)
			}
		}
		if strings.Contains(got, "hidden") {
			t.Errorf("nothing is hidden with --all:\n%s", got)
		}
	})

	t.Run("marks a shown stale session as stale", func(t *testing.T) {
		got := render(t, mixed, RenderOptions{ShowStale: true})
		if !strings.Contains(got, "(stale)") {
			t.Errorf("want stale rows marked:\n%s", got)
		}
		if !strings.Contains(got, "0 running") {
			t.Errorf("a stale session must not count as running:\n%s", got)
		}
	})

	t.Run("prints a hint when every session is stale", func(t *testing.T) {
		got := render(t, mixed[1:], RenderOptions{})
		if !strings.Contains(got, "none active") {
			t.Errorf("want a hint rather than an empty block:\n%s", got)
		}
		if !strings.Contains(got, "--all") {
			t.Errorf("want the flag that reveals them:\n%s", got)
		}
	})

	t.Run("still prints the empty-registry hint when nothing is recorded", func(t *testing.T) {
		got := render(t, nil, RenderOptions{})
		if !strings.Contains(got, "none recorded yet") {
			t.Errorf("a fresh install must not look broken:\n%s", got)
		}
	})
}
