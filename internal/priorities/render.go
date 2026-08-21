package priorities

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/swilgosz/mindskein/internal/text"
)

// Column widths, set against the real plan rather than guessed: project notes
// in the vault are named for their folder, and those run to 34 characters.
const (
	maxLabelWidth = 32
	maxNoteWidth  = 64
)

// Shown is what `mindskein priorities` and the brief print by default: the
// current focus and what is queued behind it. The backlog is a list of ideas,
// not of work in flight, and would bury both.
var Shown = []Level{Now, Next}

// All includes the backlog.
var All = []Level{Now, Next, Backlog}

// RenderOptions carries what the renderer cannot work out from the items alone.
type RenderOptions struct {
	// Levels are the levels to print, in the order they are printed. Empty
	// means Shown.
	Levels []Level

	// NoteWidth caps the status prose. Zero uses the default.
	NoteWidth int
}

// Render writes the PRIORITIES block: the whole of `mindskein priorities`, and
// one of the three sections of the morning brief.
func Render(w io.Writer, plan Plan, opts RenderOptions) error {
	items := plan.Items
	levels := opts.Levels
	if len(levels) == 0 {
		levels = Shown
	}
	width := opts.NoteWidth
	if width == 0 {
		width = maxNoteWidth
	}

	type row struct{ level, label, note string }
	var rows []row
	labelW, hidden := 0, 0

	for _, level := range levels {
		first := true
		for _, item := range items {
			if item.Level != level || item.Done {
				continue
			}
			label := text.Truncate(item.Label, maxLabelWidth)
			labelW = max(labelW, len([]rune(label)))
			marker := ""
			if first {
				marker, first = level.String(), false
			}
			rows = append(rows, row{marker, label, text.Summarize(item.Note, width)})
		}
	}
	for _, item := range items {
		if !item.Done && !shows(levels, item.Level) {
			hidden++
		}
	}

	if len(rows) == 0 {
		return Hint(w, emptyReason(plan, levels))
	}
	if _, err := fmt.Fprintln(w, "PRIORITIES"); err != nil {
		return err
	}
	for _, r := range rows {
		line := strings.TrimRight(fmt.Sprintf("  %-2s  %-*s  %s",
			r.level, labelW, r.label, r.note), " ")
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	// A list that silently ends at !2 reads like the whole plan; say what was
	// left out, as the sessions block does.
	if hidden > 0 {
		if _, err := fmt.Fprintf(w, "\n  %d more in the backlog (--all)\n", hidden); err != nil {
			return err
		}
	}
	return nil
}

// Hint writes the block with one explanatory line in place of the list. Every
// way of having no priorities to show — no config, no plan file, nothing at
// !1 or !2 — is a state to report, not an error to fail on.
func Hint(w io.Writer, hint string) error {
	if _, err := fmt.Fprintln(w, "PRIORITIES"); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, "  "+hint)
	return err
}

// emptyReason says why the list is empty, because the four reasons want four
// different reactions. The one that matters is the third: a file written to
// another convention parses to nothing, and "nothing found" reads as either a
// broken tool or a free morning, when in fact it is neither.
func emptyReason(plan Plan, levels []Level) string {
	name := "the plan"
	if plan.Source != "" {
		name = filepath.Base(plan.Source)
	}

	if len(plan.Items) == 0 {
		if plan.Checkboxes == 0 {
			return "no checkboxes in " + name + " — is that the right file?"
		}
		return fmt.Sprintf("read %s in %s, none tagged !1/!2/!3 — a priority looks like:  - [ ] !1 Ship the thing - why it matters",
			count(plan.Checkboxes, "checkbox", "checkboxes"), name)
	}

	backlog, done := 0, 0
	for _, item := range plan.Items {
		switch {
		case item.Done && shows(levels, item.Level):
			done++
		case !item.Done && !shows(levels, item.Level):
			backlog++
		}
	}
	switch {
	case backlog > 0:
		return fmt.Sprintf("nothing at %s — %s further down (--all)", join(levels),
			count(backlog, "item", "items"))
	case done > 0:
		return fmt.Sprintf("nothing open at %s — %s done", join(levels),
			count(done, "item", "items"))
	}
	return "nothing at " + join(levels) + " in " + name
}

func count(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

func shows(levels []Level, level Level) bool {
	for _, l := range levels {
		if l == level {
			return true
		}
	}
	return false
}

func join(levels []Level) string {
	names := make([]string, 0, len(levels))
	for _, l := range levels {
		names = append(names, l.String())
	}
	return strings.Join(names, " or ")
}
