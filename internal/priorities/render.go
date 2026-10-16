package priorities

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/swilgosz/mindskein/internal/text"
)

// Heading names the block, and is what a caller prints when the block itself
// could not be produced.
const Heading = "PRIORITIES"

// Column widths, measured rather than guessed: project notes named for their
// folder run to 34 characters.
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

// Row is one priority as the block shows it, before any width applies.
type Row struct {
	Level Level  `json:"level"`
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
}

// View is the PRIORITIES block decided but not yet printed.
type View struct {
	Rows []Row `json:"rows"`

	// Hidden counts open items at levels not shown.
	Hidden int `json:"hidden"`

	// Hint stands in for the rows when there are none.
	Hint string `json:"hint,omitempty"`
}

// BuildView picks the open items at the levels shown, in level order.
func BuildView(list List, opts RenderOptions) View {
	levels := opts.Levels
	if len(levels) == 0 {
		levels = Shown
	}
	var v View
	for _, level := range levels {
		for _, item := range list.Items {
			if item.Level == level && !item.Done {
				v.Rows = append(v.Rows, Row{Level: level, Label: item.Label, Note: item.Note})
			}
		}
	}
	for _, item := range list.Items {
		if !item.Done && !shows(levels, item.Level) {
			v.Hidden++
		}
	}
	if len(v.Rows) == 0 {
		v.Hint = emptyReason(list, levels)
	}
	return v
}

// Render writes the PRIORITIES block: the whole of `mindskein priorities`, and
// one of the three sections of the morning brief.
func Render(w io.Writer, list List, opts RenderOptions) error {
	return Print(w, BuildView(list, opts), opts.NoteWidth)
}

// Print writes a view as the terminal shows it. A zero noteWidth uses the
// default.
func Print(w io.Writer, v View, noteWidth int) error {
	if noteWidth == 0 {
		noteWidth = maxNoteWidth
	}
	if v.Hint != "" {
		return Hint(w, v.Hint)
	}

	type cells struct{ level, label, note string }
	rows := make([]cells, len(v.Rows))
	labelW := 0
	for i, r := range v.Rows {
		marker := ""
		if i == 0 || v.Rows[i-1].Level != r.Level {
			marker = r.Level.String()
		}
		label := text.Truncate(r.Label, maxLabelWidth)
		labelW = max(labelW, len([]rune(label)))
		rows[i] = cells{marker, label, text.Summarize(r.Note, noteWidth)}
	}

	if _, err := fmt.Fprintln(w, Heading); err != nil {
		return err
	}
	for _, r := range rows {
		line := strings.TrimRight(fmt.Sprintf("  %-2s  %-*s  %s",
			r.level, labelW, r.label, r.note), " ")
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	// A list that silently ends at !2 reads like the whole file; say what was
	// left out, as the sessions block does.
	if v.Hidden > 0 {
		if _, err := fmt.Fprintf(w, "\n  %d more in the backlog (--all)\n", v.Hidden); err != nil {
			return err
		}
	}
	return nil
}

// Hint writes the block with one explanatory line in place of the list. Every
// way of having no priorities to show — no config, no priorities file, nothing at
// !1 or !2 — is a state to report, not an error to fail on.
func Hint(w io.Writer, hint string) error {
	if _, err := fmt.Fprintln(w, Heading); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, "  "+hint)
	return err
}

// emptyReason says why the list is empty, because the four reasons want four
// different reactions. The one that matters is the third: a file written to
// another convention parses to nothing, and "nothing found" reads as either a
// broken tool or a free morning, when in fact it is neither.
func emptyReason(list List, levels []Level) string {
	name := "the priorities file"
	if list.Source != "" {
		name = filepath.Base(list.Source)
	}

	if len(list.Items) == 0 {
		if list.Checkboxes == 0 {
			return "no checkboxes in " + name + " — is that the right file?"
		}
		return fmt.Sprintf("read %s in %s, none tagged !1/!2/!3 — a priority looks like:  - [ ] !1 Ship the thing - why it matters",
			count(list.Checkboxes, "checkbox", "checkboxes"), name)
	}

	backlog, done := 0, 0
	for _, item := range list.Items {
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
