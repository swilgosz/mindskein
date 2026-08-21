package priorities

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// plan is the fixture: a note shaped like the real one, down to the legend
// table and the fenced example that make plan.md prose rather than a data file.
func plan(t *testing.T) []Item {
	t.Helper()
	items, err := ParseFile(filepath.Join("testdata", "plan.md"))
	if err != nil {
		t.Fatalf("ParseFile() = %v, want nil", err)
	}
	return items
}

func labels(items []Item, level Level) []string {
	var out []string
	for _, item := range items {
		if item.Level == level {
			out = append(out, item.Label)
		}
	}
	return out
}

func find(t *testing.T, items []Item, label string) Item {
	t.Helper()
	for _, item := range items {
		if item.Label == label {
			return item
		}
	}
	t.Fatalf("no item labelled %q in %v", label, labels(items, Now))
	return Item{}
}

func parse(t *testing.T, line string) []Item {
	t.Helper()
	items, err := Parse(strings.NewReader(line))
	if err != nil {
		t.Fatalf("Parse() = %v, want nil", err)
	}
	return items
}

func one(t *testing.T, line string) Item {
	t.Helper()
	items := parse(t, line)
	if len(items) != 1 {
		t.Fatalf("parsing %q gave %d items, want 1", line, len(items))
	}
	return items[0]
}

func render(t *testing.T, items []Item, opts RenderOptions) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, items, opts); err != nil {
		t.Fatalf("Render() = %v, want nil", err)
	}
	return buf.String()
}

// TestParsingPlan covers reading the priority lines out of the vault's plan.md.
//
// The file is a hand-written note, not a data format: the same tokens appear in
// prose, in a legend table and in a fenced example of the convention itself, so
// what is *not* a priority matters as much as what is.
func TestParsingPlan(t *testing.T) {
	items := plan(t)

	t.Run("collects the !1 lines in the order the plan declares them", func(t *testing.T) {
		want := []string{"Wren Deploy Tool", "Publishing Calendar"}
		if got := labels(items, Now); !equal(got, want) {
			t.Errorf("!1 = %q, want %q", got, want)
		}
	})

	t.Run("collects the !2 lines", func(t *testing.T) {
		want := []string{
			"Newsletter reactivation sequence",
			"Wren Roadmap",
			"Sprint — Wave 2",
			"Standalone Note",
			"Płynność finansowa",
		}
		if got := labels(items, Next); !equal(got, want) {
			t.Errorf("!2 = %q, want %q", got, want)
		}
	})

	t.Run("collects the !3 lines, which the reader may then leave out", func(t *testing.T) {
		want := []string{"MCP Function Calling - Use Case Ideas", "Dig Deeper feature", "Ukończony Eksperyment"}
		if got := labels(items, Backlog); !equal(got, want) {
			t.Errorf("!3 = %q, want %q", got, want)
		}
	})

	t.Run("ignores a checkbox line carrying no priority token", func(t *testing.T) {
		// The same file tracks the week's chores; they are not priorities.
		for _, item := range items {
			if strings.Contains(item.Label, "paywall") || strings.Contains(item.Label, "Re-upload") {
				t.Errorf("a plain checkbox was read as a priority: %+v", item)
			}
		}
		if got := parse(t, "- [ ] Ustawić paywall archiwum (okno 3 mies.)\n"); len(got) != 0 {
			t.Errorf("parsed %d items from a plain checkbox, want 0", len(got))
		}
	})

	t.Run("ignores a priority token that is not on a checkbox line", func(t *testing.T) {
		// The legend table explaining the convention mentions every token.
		legend := "| `!1` | Current focus — actively working on | 3 |\n"
		if got := parse(t, legend); len(got) != 0 {
			t.Errorf("parsed %d items from the legend row, want 0", len(got))
		}
	})

	t.Run("ignores the convention's own example inside a fenced code block", func(t *testing.T) {
		for _, item := range items {
			if strings.Contains(item.Label, "Example") {
				t.Errorf("the fenced example was read as work: %+v", item)
			}
		}
	})

	t.Run("keeps a line that names nothing, rather than dropping the work", func(t *testing.T) {
		item := one(t, "- [ ] !1 — fix the redemption path")
		if item.Label != "fix the redemption path" || item.Note != "" {
			t.Errorf("item = %+v, want the prose as the label", item)
		}
	})

	t.Run("marks a ticked checkbox done rather than dropping it", func(t *testing.T) {
		done := find(t, items, "Płynność finansowa")
		if !done.Done {
			t.Errorf("%+v should be done", done)
		}
		if open := find(t, items, "Publishing Calendar"); open.Done {
			t.Errorf("%+v should not be done", open)
		}
	})
}

// TestResolvingLabels covers turning an Obsidian wikilink into something worth
// printing in a narrow column.
func TestResolvingLabels(t *testing.T) {
	label := func(t *testing.T, line string) string {
		t.Helper()
		return one(t, line).Label
	}

	t.Run("resolves a plain wikilink to its note name", func(t *testing.T) {
		if got := label(t, "- [ ] !1 [[Standalone Note]] — note"); got != "Standalone Note" {
			t.Errorf("label = %q, want %q", got, "Standalone Note")
		}
	})

	t.Run("prefers the alias when the wikilink carries one", func(t *testing.T) {
		got := label(t, "- [ ] !1 [[6. Spaces/62. Business/623. Projects/Wren/_index|Wren Deploy]] — note")
		if got != "Wren Deploy" {
			t.Errorf("label = %q, want %q", got, "Wren Deploy")
		}
	})

	t.Run("resolves a path wikilink to its last segment", func(t *testing.T) {
		if got := label(t, "- [ ] !1 [[6. Spaces/62. Business/Offer]] — note"); got != "Offer" {
			t.Errorf("label = %q, want %q", got, "Offer")
		}
	})

	t.Run("names an _index note by the folder that gives it its identity", func(t *testing.T) {
		// Every project note in the vault is called _index, so the file name
		// alone would label them all the same.
		got := label(t, "- [ ] !1 [[6. Spaces/62. Business/623. Projects/Wren Deploy Tool/_index]] — note")
		if got != "Wren Deploy Tool" {
			t.Errorf("label = %q, want %q", got, "Wren Deploy Tool")
		}
		got = label(t, "- [ ] !1 [[623. Projects/Wren Deploy Tool/_index.md]] — note")
		if got != "Wren Deploy Tool" {
			t.Errorf("label = %q, want %q", got, "Wren Deploy Tool")
		}
	})

	t.Run("drops a heading anchor from the label", func(t *testing.T) {
		if got := label(t, "- [ ] !1 [[Wren Roadmap#v0.2 adapters]] — note"); got != "Wren Roadmap" {
			t.Errorf("label = %q, want %q", got, "Wren Roadmap")
		}
	})

	t.Run("falls back to the leading prose when there is no wikilink", func(t *testing.T) {
		got := label(t, "- [ ] !2 **Newsletter** reactivation sequence — 3-email bridge")
		if got != "Newsletter reactivation sequence" {
			t.Errorf("label = %q, want %q", got, "Newsletter reactivation sequence")
		}
	})

	t.Run("splits at the em dash between label and note, not one inside a title", func(t *testing.T) {
		item := one(t, "- [ ] !2 [[Sprint — Wave 2]] — coś, co ma myślnik w tytule")
		if item.Label != "Sprint — Wave 2" {
			t.Errorf("label = %q, want %q", item.Label, "Sprint — Wave 2")
		}
		if item.Note != "coś, co ma myślnik w tytule" {
			t.Errorf("note = %q", item.Note)
		}
	})
}

// TestTrimmingProse covers the trailing status paragraph. Real entries run to
// several hundred characters of dated commentary; the brief has one line.
func TestTrimmingProse(t *testing.T) {
	items := plan(t)

	t.Run("splits the label from the note at the em dash", func(t *testing.T) {
		item := find(t, items, "Publishing Calendar")
		if item.Note != "finalize and launch" {
			t.Errorf("note = %q, want %q", item.Note, "finalize and launch")
		}
	})

	t.Run("keeps the whole line as the label when there is no note", func(t *testing.T) {
		item := find(t, items, "Standalone Note")
		if item.Note != "" {
			t.Errorf("note = %q, want empty", item.Note)
		}
	})

	t.Run("strips markdown emphasis and wikilinks out of the note", func(t *testing.T) {
		note := find(t, items, "Wren Deploy Tool").Note
		for _, marker := range []string{"**", "`", "[[", "]]"} {
			if strings.Contains(note, marker) {
				t.Errorf("note still carries %q: %s", marker, note)
			}
		}
		if !strings.HasPrefix(note, "Installer post LIVE 2026-08-12") {
			t.Errorf("note = %q", note)
		}
		// A wikilink inside the prose reads as its label, not as a path.
		if !strings.Contains(note, "the product note") || strings.Contains(note, "624.") {
			t.Errorf("wikilink in the note was not resolved: %s", note)
		}
	})

	t.Run("truncates long prose at a word boundary", func(t *testing.T) {
		const width = 40
		full := find(t, items, "Wren Deploy Tool").Note
		line := lineWith(t, render(t, items, RenderOptions{NoteWidth: width}), "Wren Deploy Tool")
		note := line[strings.Index(line, "Installer"):]

		if len([]rune(note)) > width {
			t.Errorf("note is %d runes, want at most %d: %q", len([]rune(note)), width, note)
		}
		if !strings.HasSuffix(note, "…") {
			t.Errorf("a truncated note should say so: %q", note)
		}
		kept := strings.TrimSuffix(note, "…")
		if !strings.HasPrefix(full, kept) {
			t.Fatalf("the note is not a prefix of the prose: %q", note)
		}
		// Whatever comes next in the full prose must not be the rest of a word
		// the cut ran through.
		if rest := []rune(full[len(kept):]); len(rest) > 0 && isWord(rest[0]) {
			t.Errorf("note was cut mid-word: %q + %q", kept, string(rest[:5]))
		}
		if len([]rune(kept)) < width/2 {
			t.Errorf("backing up to a word boundary threw away the line: %q", note)
		}
	})

	t.Run("truncates on a rune boundary, leaving Polish text intact", func(t *testing.T) {
		polish := find(t, items, "Newsletter reactivation sequence").Note
		for width := 8; width < len([]rune(polish)); width++ {
			cut := render(t, []Item{{Level: Now, Label: "x", Note: polish}},
				RenderOptions{NoteWidth: width})
			if !utf8Valid(cut) {
				t.Fatalf("width %d produced invalid UTF-8: %q", width, cut)
			}
		}
	})
}

// TestRenderingPriorities covers the PRIORITIES block: the whole of
// `mindskein priorities`, and the first section of the morning brief.
func TestRenderingPriorities(t *testing.T) {
	items := plan(t)

	t.Run("prints one line per item, each group marked with its level", func(t *testing.T) {
		out := render(t, items, RenderOptions{})
		if !strings.HasPrefix(out, "PRIORITIES\n") {
			t.Errorf("output does not open with the heading:\n%s", out)
		}
		for _, label := range []string{"Wren Deploy Tool", "Publishing Calendar", "Wren Roadmap"} {
			lineWith(t, out, label)
		}
		if got := strings.Count(out, "!1"); got != 1 {
			t.Errorf("the level is marked %d times, want once per group:\n%s", got, out)
		}
		if !strings.HasPrefix(lineWith(t, out, "Wren Deploy Tool"), "  !1  ") {
			t.Errorf("the first row of a group carries its level:\n%s", out)
		}
		if !strings.HasPrefix(lineWith(t, out, "Publishing Calendar"), "      ") {
			t.Errorf("a following row does not repeat the level:\n%s", out)
		}
	})

	t.Run("shows !1 and !2 but leaves the backlog out", func(t *testing.T) {
		out := render(t, items, RenderOptions{})
		if strings.Contains(out, "Dig Deeper") {
			t.Errorf("the backlog should not be listed by default:\n%s", out)
		}
		// A list that silently ends at !2 reads like the whole plan.
		if !strings.Contains(out, "2 more in the backlog (--all)") {
			t.Errorf("what was left out should be named:\n%s", out)
		}
		all := render(t, items, RenderOptions{Levels: All})
		if !strings.Contains(all, "Dig Deeper") {
			t.Errorf("--all should list the backlog:\n%s", all)
		}
		if strings.Contains(all, "more in the backlog") {
			t.Errorf("nothing is hidden with --all:\n%s", all)
		}
	})

	t.Run("leaves a done item out of the listing", func(t *testing.T) {
		out := render(t, items, RenderOptions{Levels: All})
		if strings.Contains(out, "Płynność") {
			t.Errorf("a ticked item is not work:\n%s", out)
		}
		if strings.Contains(out, "more in the backlog") {
			t.Errorf("a done item must not be counted as hidden:\n%s", out)
		}
	})

	t.Run("aligns the columns by rune count, so a Polish label does not skew them", func(t *testing.T) {
		out := render(t, []Item{
			{Level: Now, Label: "Płynność", Note: "aaa"},
			{Level: Now, Label: "a much longer label", Note: "bbb"},
		}, RenderOptions{})
		first := column(t, out, "aaa")
		if second := column(t, out, "bbb"); first != second {
			t.Errorf("notes start at runes %d and %d:\n%s", first, second, out)
		}
	})

	t.Run("hints instead of printing nothing when the plan has no priorities", func(t *testing.T) {
		out := render(t, nil, RenderOptions{})
		if !strings.HasPrefix(out, "PRIORITIES\n") {
			t.Errorf("the section is printed even when empty:\n%s", out)
		}
		if !strings.Contains(out, "!1 or !2") {
			t.Errorf("the hint should say what was looked for:\n%s", out)
		}
	})

	t.Run("hints instead of failing when there is no plan to read", func(t *testing.T) {
		var buf bytes.Buffer
		if err := Hint(&buf, "no plan configured"); err != nil {
			t.Fatalf("Hint() = %v, want nil", err)
		}
		if got := buf.String(); got != "PRIORITIES\n  no plan configured\n" {
			t.Errorf("Hint() wrote %q", got)
		}
	})
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func lineWith(t *testing.T, out, substr string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, substr) {
			return line
		}
	}
	t.Fatalf("no line containing %q in:\n%s", substr, out)
	return ""
}

// column reports where substr starts, counted in runes: a byte offset would
// report two identically aligned columns as different.
func column(t *testing.T, out, substr string) int {
	t.Helper()
	line := lineWith(t, out, substr)
	return len([]rune(line[:strings.Index(line, substr)]))
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func utf8Valid(s string) bool {
	return strings.ToValidUTF8(s, "\uFFFD") == s
}
