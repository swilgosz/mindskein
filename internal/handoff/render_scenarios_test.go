package handoff

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// ended is a readable end time: handoffs are only ever ordered relative to one
// another, so the day matters and the year does not.
func ended(day, hour int) time.Time {
	return time.Date(2026, 8, day, hour, 0, 0, 0, time.UTC)
}

func render(t *testing.T, handoffs []*Handoff, opts RenderOptions) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, handoffs, opts); err != nil {
		t.Fatalf("Render() = %v, want nil", err)
	}
	return buf.String()
}

// rows is the listing itself: the block up to the blank line that separates it
// from the count of what was left out.
func rows(out string) []string {
	listing, _, _ := strings.Cut(strings.TrimSpace(out), "\n\n")
	var got []string
	for _, line := range strings.Split(listing, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" && trimmed != Heading {
			got = append(got, trimmed)
		}
	}
	return got
}

// The grouping rule is the open question this section answers: the writer
// records every identity a session has and leaves the choice to the reader.
func TestGrouping(t *testing.T) {
	t.Run("an explicit project name groups sessions across folders and repositories", func(t *testing.T) {
		in := []*Handoff{
			{SessionID: "a", Project: "Content W35", CWD: "/vault/business", Title: "Notes", EndedAt: ended(21, 18)},
			{SessionID: "b", Project: "Content W35", CWD: "/code/site", Repo: "/code/site", Title: "Draft", EndedAt: ended(21, 9)},
		}
		if got := NewestPer(in, ByProject); len(got) != 1 || got[0].SessionID != "a" {
			t.Fatalf("grouped to %s, want the single newest of one group", ids(got))
		}
	})

	t.Run("worktrees of one repository collapse onto the repository", func(t *testing.T) {
		in := []*Handoff{
			{SessionID: "a", CWD: "/code/ms/wt/u3", RepoRoot: "/code/ms/wt/u3", Repo: "/code/ms", Branch: "u3", Title: "U3", EndedAt: ended(21, 18)},
			{SessionID: "b", CWD: "/code/ms/wt/u2", RepoRoot: "/code/ms/wt/u2", Repo: "/code/ms", Branch: "u2", Title: "U2", EndedAt: ended(20, 9)},
		}
		got := NewestPer(in, ByProject)
		if len(got) != 1 || got[0].SessionID != "a" {
			t.Fatalf("grouped to %s, want one line for the repository", ids(got))
		}
		if name := got[0].ProjectName(); name != "ms" {
			t.Errorf("ProjectName() = %q, want the repository %q", name, "ms")
		}
	})

	t.Run("unrelated sessions sharing one folder stay apart, keyed by what they were called", func(t *testing.T) {
		in := []*Handoff{
			{SessionID: "a", CWD: "/vault/business", Title: "Excalidraw", EndedAt: ended(21, 18)},
			{SessionID: "b", CWD: "/vault/business", Title: "U3 Priorities parser", EndedAt: ended(21, 9)},
			{SessionID: "c", CWD: "/vault/business", Title: "AI tools article", EndedAt: ended(20, 9)},
		}
		if got := NewestPer(in, ByProject); len(got) != 3 {
			t.Fatalf("grouped %d sessions to %s, want all three kept apart", len(in), ids(got))
		}
	})

	t.Run("the same title in two folders stays apart", func(t *testing.T) {
		in := []*Handoff{
			{SessionID: "a", CWD: "/vault/business", Title: "planner", EndedAt: ended(21, 18)},
			{SessionID: "b", CWD: "/vault/life", Title: "planner", EndedAt: ended(21, 9)},
		}
		if got := NewestPer(in, ByProject); len(got) != 2 {
			t.Fatalf("grouped to %s, want a line each — losing one loses the work", ids(got))
		}
	})

	t.Run("a handoff with neither repository nor title falls back to its folder", func(t *testing.T) {
		in := []*Handoff{
			{SessionID: "a", CWD: "/vault/business", EndedAt: ended(21, 18)},
			{SessionID: "b", CWD: "/vault/business", EndedAt: ended(21, 9)},
		}
		got := NewestPer(in, ByProject)
		if len(got) != 1 {
			t.Fatalf("grouped to %s, want one line for the folder", ids(got))
		}
		if name := got[0].ProjectName(); name != "business" {
			t.Errorf("ProjectName() = %q, want the folder %q", name, "business")
		}
	})

	t.Run("the newest handoff in a group is the one that represents it", func(t *testing.T) {
		store := &Store{Dir: t.TempDir()}
		for _, h := range []*Handoff{
			{SessionID: "older", Repo: "/code/ms", Title: "U2", Message: "stale", EndedAt: ended(20, 9)},
			{SessionID: "newest", Repo: "/code/ms", Title: "U3", Message: "current", EndedAt: ended(21, 18)},
		} {
			if err := store.Write(h); err != nil {
				t.Fatalf("Write(%s) = %v", h.SessionID, err)
			}
		}
		listed, err := store.List()
		if err != nil {
			t.Fatalf("List() = %v", err)
		}
		out := render(t, listed, RenderOptions{})
		if !strings.Contains(out, "current") || strings.Contains(out, "stale") {
			t.Errorf("rendered the wrong handoff of the group:\n%s", out)
		}
	})
}

func TestWhereWeLeftOff(t *testing.T) {
	t.Run("prints the project, when it ended, and the last message", func(t *testing.T) {
		out := render(t, []*Handoff{
			{SessionID: "a", Repo: "/code/ms", Message: "Ship the brief renderer", EndedAt: ended(21, 18)},
		}, RenderOptions{})
		if !strings.HasPrefix(out, Heading+"\n") {
			t.Errorf("block does not open with %q:\n%s", Heading, out)
		}
		for _, want := range []string{"ms", ended(21, 18).Local().Format(timeLayout), "Ship the brief renderer"} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("an ended-at recorded in UTC prints in the reader's local time", func(t *testing.T) {
		// The store is UTC so records from any machine sort together, and CI
		// runs in UTC — so the zone is forced here, or the assertion passes
		// against a renderer that never converts.
		local := time.FixedZone("test", 5*60*60)
		saved := time.Local
		time.Local = local
		t.Cleanup(func() { time.Local = saved })

		out := render(t, []*Handoff{
			{SessionID: "a", Repo: "/code/ms", Message: "x", EndedAt: ended(21, 18)},
		}, RenderOptions{})
		if !strings.Contains(out, "2026-08-21 23:00") {
			t.Errorf("output does not show 18:00 UTC as 23:00 local:\n%s", out)
		}
	})

	t.Run("a multi-line message renders on one line", func(t *testing.T) {
		out := render(t, []*Handoff{
			{SessionID: "a", Repo: "/code/ms", Message: "first line\nsecond line", EndedAt: ended(21, 18)},
		}, RenderOptions{})
		if got := rows(out); len(got) != 1 {
			t.Fatalf("rendered %d rows, want 1:\n%s", len(got), out)
		}
		if strings.Contains(out, "second line") {
			t.Errorf("the second line leaked into the block:\n%s", out)
		}
	})

	t.Run("a group with no recorded message says so rather than leaving the column blank", func(t *testing.T) {
		out := render(t, []*Handoff{
			{SessionID: "a", Repo: "/code/ms", EndedAt: ended(21, 18)},
		}, RenderOptions{})
		got := rows(out)
		if len(got) != 1 {
			t.Fatalf("rendered %d rows, want 1:\n%s", len(got), out)
		}
		if !strings.HasSuffix(got[0], ")") || !strings.Contains(got[0], "no prompt") {
			t.Errorf("row = %q, want it to end by saying no prompt was recorded", got[0])
		}
	})

	t.Run("the branch is named when the work sat on one", func(t *testing.T) {
		with := render(t, []*Handoff{
			{SessionID: "a", Repo: "/code/ms", Branch: "u4-brief", Message: "x", EndedAt: ended(21, 18)},
		}, RenderOptions{})
		if !strings.Contains(with, "u4-brief") {
			t.Errorf("output does not name the branch:\n%s", with)
		}
		without := render(t, []*Handoff{
			{SessionID: "a", Repo: "/code/ms", Message: "x", EndedAt: ended(21, 18)},
		}, RenderOptions{})
		if strings.Contains(without, "·") {
			t.Errorf("a branchless handoff still printed a separator:\n%s", without)
		}
	})

	t.Run("a hand-edited branch cannot break the page either", func(t *testing.T) {
		out := render(t, []*Handoff{
			{SessionID: "a", Repo: "/code/ms", Branch: "u4\x1b[31m\nbrief", Message: "x", EndedAt: ended(21, 18)},
		}, RenderOptions{})
		if strings.ContainsRune(out, '\x1b') {
			t.Errorf("an escape sequence reached the terminal:\n%q", out)
		}
		if got := rows(out); len(got) != 1 {
			t.Fatalf("rendered %d rows, want 1:\n%q", len(got), out)
		}
	})

	t.Run("beyond the default limit the count left out is printed", func(t *testing.T) {
		out := render(t, many(DefaultLimit+3), RenderOptions{})
		if got := rows(out); len(got) != DefaultLimit {
			t.Fatalf("rendered %d rows, want %d:\n%s", len(got), DefaultLimit, out)
		}
		if !strings.Contains(out, "3 more (--all)") {
			t.Errorf("output does not say 3 were left out:\n%s", out)
		}
	})

	t.Run("showing all prints every group", func(t *testing.T) {
		out := render(t, many(DefaultLimit+3), RenderOptions{ShowAll: true})
		if got := rows(out); len(got) != DefaultLimit+3 {
			t.Fatalf("rendered %d rows, want %d:\n%s", len(got), DefaultLimit+3, out)
		}
		if strings.Contains(out, "more (--all)") {
			t.Errorf("--all still reported hidden rows:\n%s", out)
		}
	})

	t.Run("a hand-edited record with no end time says so rather than printing the year 1", func(t *testing.T) {
		// Both kinds in one block, or the short column never has to be padded
		// against the long one and the alignment goes unchecked.
		out := render(t, []*Handoff{
			{SessionID: "a", Repo: "/code/dated", Message: "dated prompt", EndedAt: ended(21, 18)},
			{SessionID: "b", Repo: "/code/undated", Message: "undated prompt"},
		}, RenderOptions{})
		if strings.Contains(out, "0001") {
			t.Errorf("a zero end time printed as a date:\n%s", out)
		}
		if !strings.Contains(out, "—") {
			t.Errorf("output does not mark the missing end time:\n%s", out)
		}
		got := rows(out)
		if len(got) != 2 {
			t.Fatalf("rendered %d rows, want 2:\n%s", len(got), out)
		}
		// Rune offsets, not byte offsets: the dash standing in for a missing
		// time is three bytes wide and one column wide, which is the whole
		// reason the column is padded by rune count.
		dated := column(got[0], "dated prompt")
		undated := column(got[1], "undated prompt")
		if dated != undated {
			t.Errorf("the message column starts at %d and %d — the undated row is not padded:\n%s",
				dated, undated, out)
		}
	})

	t.Run("terminal escapes in a pasted prompt cannot redraw the page", func(t *testing.T) {
		out := render(t, []*Handoff{
			{SessionID: "a", Repo: "/code/ms", Message: "why is \x1b[31mthis\x1b[0m red", EndedAt: ended(21, 18)},
		}, RenderOptions{})
		if strings.ContainsRune(out, '\x1b') {
			t.Errorf("an escape sequence reached the terminal:\n%q", out)
		}
		if !strings.Contains(out, "why is [31mthis[0m red") {
			t.Errorf("the prompt text itself did not survive:\n%q", out)
		}
	})

	t.Run("a generated title carrying escapes and newlines cannot break the page", func(t *testing.T) {
		// With no rename and no generated title, the title is the first thing
		// the person typed — so it is exactly as raw as the message column.
		out := render(t, []*Handoff{
			{SessionID: "a", CWD: "/vault/business", Title: "fix \x1b[31mthe\x1b[0m bug\nin the parser",
				Message: "go on", EndedAt: ended(21, 18)},
			{SessionID: "b", CWD: "/vault/other", Title: "short", Message: "second", EndedAt: ended(20, 18)},
		}, RenderOptions{})
		if strings.ContainsRune(out, '\x1b') {
			t.Errorf("an escape sequence reached the terminal:\n%q", out)
		}
		got := rows(out)
		if len(got) != 2 {
			t.Fatalf("rendered %d rows, want 2 — a newline split one in half:\n%q", len(got), out)
		}
		if column(got[0], "go on") != column(got[1], "second") {
			t.Errorf("the columns do not line up:\n%s", out)
		}
	})

	t.Run("a tab in a prompt does not run the words either side together", func(t *testing.T) {
		out := render(t, []*Handoff{
			{SessionID: "a", Repo: "/code/ms", Message: "foo\tbar baz", EndedAt: ended(21, 18)},
		}, RenderOptions{})
		if !strings.Contains(out, "foo bar baz") {
			t.Errorf("the tab did not become a space:\n%q", out)
		}
	})

	t.Run("two repositories sharing a basename are told apart", func(t *testing.T) {
		out := render(t, []*Handoff{
			{SessionID: "a", Repo: "/Users/x/Projects/mindskein", Message: "live", EndedAt: ended(21, 18)},
			{SessionID: "b", Repo: "/Users/x/Projects/Archive/mindskein", Message: "archived", EndedAt: ended(20, 18)},
		}, RenderOptions{})
		got := rows(out)
		if len(got) != 2 {
			t.Fatalf("rendered %d rows, want 2:\n%s", len(got), out)
		}
		live := strings.Fields(got[0])[0]
		archived := strings.Fields(got[1])[0]
		if live == archived {
			t.Errorf("both rows are named %q — the reader cannot tell which is which:\n%s", live, out)
		}
		if !strings.Contains(archived, "Archive") {
			t.Errorf("the qualifier does not say where %q is:\n%s", archived, out)
		}
	})

	t.Run("the same title in two folders is told apart", func(t *testing.T) {
		out := render(t, []*Handoff{
			{SessionID: "a", CWD: "/vault/business", Title: "planner", Message: "one", EndedAt: ended(21, 18)},
			{SessionID: "b", CWD: "/vault/life", Title: "planner", Message: "two", EndedAt: ended(20, 18)},
		}, RenderOptions{})
		got := rows(out)
		if len(got) != 2 {
			t.Fatalf("rendered %d rows, want 2:\n%s", len(got), out)
		}
		if strings.Fields(got[0])[0] == strings.Fields(got[1])[0] {
			t.Errorf("two workstreams kept apart by the grouping render alike:\n%s", out)
		}
	})

	t.Run("a name that repeats nowhere is left unqualified", func(t *testing.T) {
		out := render(t, []*Handoff{
			{SessionID: "a", Repo: "/Users/x/Projects/mindskein", Message: "live", EndedAt: ended(21, 18)},
			{SessionID: "b", Repo: "/Users/x/Projects/other", Message: "elsewhere", EndedAt: ended(20, 18)},
		}, RenderOptions{})
		if strings.Contains(out, "Projects/mindskein") {
			t.Errorf("an unambiguous name was qualified anyway:\n%s", out)
		}
	})

	t.Run("no handoffs at all prints one hint line", func(t *testing.T) {
		out := render(t, nil, RenderOptions{})
		got := rows(out)
		if len(got) != 1 {
			t.Fatalf("rendered %d lines, want a single hint:\n%s", len(got), out)
		}
		if !strings.HasPrefix(out, Heading+"\n") {
			t.Errorf("hint is not under the heading:\n%s", out)
		}
	})
}

// many builds n distinct workstreams, newest first, as the store lists them.
func many(n int) []*Handoff {
	out := make([]*Handoff, 0, n)
	for i := range n {
		out = append(out, &Handoff{
			SessionID: strconv.Itoa(i),
			Repo:      "/code/p" + strconv.Itoa(i),
			Message:   "prompt " + strconv.Itoa(i),
			EndedAt:   ended(21, 23-i),
		})
	}
	return out
}

// column is where a substring starts, measured the way a terminal measures it.
func column(line, substr string) int {
	i := strings.Index(line, substr)
	if i < 0 {
		return -1
	}
	return utf8.RuneCountInString(line[:i])
}

func ids(handoffs []*Handoff) string {
	var out []string
	for _, h := range handoffs {
		out = append(out, h.SessionID)
	}
	return "[" + strings.Join(out, " ") + "]"
}
