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
