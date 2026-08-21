package handoff

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/swilgosz/mindskein/internal/text"
)

// Heading names the block, and is what a caller prints when the block itself
// could not be produced.
const Heading = "WHERE WE LEFT OFF"

// DefaultLimit caps the list at a screenful. Stop writes a handoff every turn
// and nothing prunes them, so the store holds every workstream that has ever
// run; a morning brief wants the ones still warm, and a count for the rest.
const DefaultLimit = 5

const (
	maxLabelWidth   = 34
	maxMessageWidth = 60
	timeLayout      = "2006-01-02 15:04"
)

// RenderOptions carries what the renderer cannot work out from the handoffs.
type RenderOptions struct {
	// ShowAll lists every workstream rather than the newest few.
	ShowAll bool

	// Group decides what counts as one workstream. Nil means ByProject.
	Group func(*Handoff) string
}

// Render writes the WHERE WE LEFT OFF block: one line per workstream, newest
// first, saying when it stopped and what was being asked at the time.
//
// It takes the whole store and collapses it here, because which handoffs
// represent one piece of work is this block's question, not its caller's.
// Input order is the store's: newest first, which is what makes the newest
// handoff in a group the one that speaks for it.
func Render(w io.Writer, handoffs []*Handoff, opts RenderOptions) error {
	group := opts.Group
	if group == nil {
		group = ByProject
	}
	groups := NewestPer(handoffs, group)

	if len(groups) == 0 {
		return Hint(w, "nothing recorded yet — a handoff is written when a session finishes a turn")
	}

	shown, hidden := groups, 0
	if !opts.ShowAll && len(groups) > DefaultLimit {
		shown, hidden = groups[:DefaultLimit], len(groups)-DefaultLimit
	}

	type row struct{ label, when, message string }
	rows := make([]row, 0, len(shown))
	labelW, whenW := 0, 0
	for _, h := range shown {
		label := text.Truncate(labelOf(h), maxLabelWidth)
		when := endedAt(h)
		labelW = max(labelW, len([]rune(label)))
		whenW = max(whenW, len([]rune(when)))
		rows = append(rows, row{label: label, when: when, message: message(h)})
	}

	if _, err := fmt.Fprintln(w, Heading); err != nil {
		return err
	}
	for _, r := range rows {
		line := strings.TrimRight(fmt.Sprintf("  %-*s  %-*s  %s",
			labelW, r.label, whenW, r.when, r.message), " ")
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	if hidden > 0 {
		if _, err := fmt.Fprintf(w, "\n  %d more (--all)\n", hidden); err != nil {
			return err
		}
	}
	return nil
}

// Hint writes the block with one explanatory line in place of the list.
func Hint(w io.Writer, hint string) error {
	if _, err := fmt.Fprintln(w, Heading); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, "  "+hint)
	return err
}

// endedAt is when the work stopped, in local time: the record is stored in UTC
// so files written on any machine sort together, but "when did we leave off"
// is answered against the clock the work happened on.
//
// A record with no end time is one that was hand-edited or truncated, and
// saying so beats printing the year 1.
func endedAt(h *Handoff) string {
	if h.EndedAt.IsZero() {
		return "—"
	}
	return h.EndedAt.Local().Format(timeLayout)
}

// labelOf names the line: the workstream, and the branch it sat on where there
// is one. The branch belongs here because one worktree is one task, so it is
// the difference between two lines of the same repository.
func labelOf(h *Handoff) string {
	name := h.ProjectName()
	if h.Branch != "" {
		name += " · " + h.Branch
	}
	return name
}

// message is the last thing that was asked, which is the closest thing to
// "where we left off" that a raw handoff holds. A prompt can run to pages, so
// only its opening line survives the column.
func message(h *Handoff) string {
	first, _, _ := strings.Cut(h.Message, "\n")
	first = strings.Join(strings.Fields(text.Clean(first)), " ")
	if first == "" {
		return "(no prompt recorded)"
	}
	return text.Summarize(first, maxMessageWidth)
}

// ByProject groups by the workstream a session belonged to, which is a
// different question in and out of a repository. Inside one, the repository is
// the work and its worktrees are tasks within it, so they collapse. Outside,
// the folder is not the work — the vault hosts content, branding and product
// sessions in one directory — and the only thing naming a session is what it
// was called, so a title keys its own line.
//
// Sessions are keyed within their folder rather than by title alone, so two
// unrelated sessions that happen to share a generated title cannot swallow one
// another; the cost is that a session which moved folders leaves two lines,
// which is the more visible of the two failures.
//
// The consequence worth knowing: a stream continuing across days under
// different titles reads as several lines. Setting MINDSKEIN_PROJECT is what
// joins them, and it is the only key here that spans folders and repositories.
func ByProject(h *Handoff) string {
	switch {
	case h.Project != "":
		return "project\x00" + h.Project
	case h.Repo != "":
		return "repo\x00" + h.Repo
	case h.Title != "":
		return "title\x00" + h.CWD + "\x00" + h.Title
	default:
		return "cwd\x00" + h.CWD
	}
}

// ProjectName spells what ByProject keyed on. The two move together: a label
// naming something other than the group it heads would misreport which
// sessions had been collapsed into the line.
func (h *Handoff) ProjectName() string {
	switch {
	case h.Project != "":
		return h.Project
	case h.Repo != "":
		return repoName(h.Repo)
	default:
		return h.Label()
	}
}

func repoName(repo string) string {
	trimmed := strings.TrimRight(repo, string(filepath.Separator))
	if trimmed == "" {
		return "(unknown)"
	}
	return filepath.Base(trimmed)
}
