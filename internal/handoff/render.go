package handoff

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

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

// Row is one workstream as the block shows it, before any column width
// applies.
type Row struct {
	SessionID string    `json:"session_id"`
	Label     string    `json:"label"`
	EndedAt   time.Time `json:"ended_at"`
	When      string    `json:"when"`
	Message   string    `json:"message"`
}

// View is the WHERE WE LEFT OFF block decided but not yet printed.
type View struct {
	Rows []Row `json:"rows"`

	// Hidden counts the workstreams beyond the limit.
	Hidden int `json:"hidden"`

	// Hint stands in for the rows when there are none.
	Hint string `json:"hint,omitempty"`
}

// BuildView collapses the store into one row per workstream, newest first.
//
// It takes the whole store and collapses it here, because which handoffs
// represent one piece of work is this block's question, not its caller's.
// Input order is the store's: newest first, which is what makes the newest
// handoff in a group the one that speaks for it.
func BuildView(handoffs []*Handoff, opts RenderOptions) View {
	group := opts.Group
	if group == nil {
		group = ByProject
	}
	groups := NewestPer(handoffs, group)
	if len(groups) == 0 {
		return View{Hint: "nothing recorded yet — a handoff is written when a session finishes a turn"}
	}

	var v View
	shown := groups
	if !opts.ShowAll && len(groups) > DefaultLimit {
		shown, v.Hidden = groups[:DefaultLimit], len(groups)-DefaultLimit
	}
	names := distinctNames(shown)
	for i, h := range shown {
		v.Rows = append(v.Rows, Row{
			SessionID: h.SessionID,
			Label:     labelOf(names[i], h),
			EndedAt:   h.EndedAt,
			When:      endedAt(h),
			Message:   message(h),
		})
	}
	return v
}

// Render writes the WHERE WE LEFT OFF block: one line per workstream, newest
// first, saying when it stopped and what was being asked at the time.
func Render(w io.Writer, handoffs []*Handoff, opts RenderOptions) error {
	return Print(w, BuildView(handoffs, opts))
}

// Print writes a view as the terminal shows it.
func Print(w io.Writer, v View) error {
	if v.Hint != "" {
		return Hint(w, v.Hint)
	}
	labels := make([]string, len(v.Rows))
	labelW, whenW := 0, 0
	for i, r := range v.Rows {
		labels[i] = text.Truncate(r.Label, maxLabelWidth)
		labelW = max(labelW, len([]rune(labels[i])))
		whenW = max(whenW, len([]rune(r.When)))
	}

	if _, err := fmt.Fprintln(w, Heading); err != nil {
		return err
	}
	for i, r := range v.Rows {
		line := strings.TrimRight(fmt.Sprintf("  %-*s  %-*s  %s",
			labelW, labels[i], whenW, r.When, r.Message), " ")
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	if v.Hidden > 0 {
		if _, err := fmt.Fprintf(w, "\n  %d more (--all)\n", v.Hidden); err != nil {
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

// labelOf names the line: the workstream, and the branch the representative
// handoff sat on where there is one. Like the time and the message beside it,
// the branch describes that handoff rather than the group — only the name
// identifies the group.
func labelOf(name string, h *Handoff) string {
	if h.Branch != "" {
		name += " · " + h.Branch
	}
	// Flattened once here rather than per part: every field on this row was
	// parsed out of a file a person can edit by hand.
	return text.OneLine(name)
}

// distinctNames names each group so that no two rows read alike. A name is a
// basename — of a repository, or of the folder a session ran in — and
// basenames repeat: an archived checkout beside its live one, or the same
// folder name under two parents. Two rows reading "mindskein" name nothing.
//
// The qualifier is the directory containing whatever the name was taken from,
// which is exactly the part of the key that made the groups different.
func distinctNames(groups []*Handoff) []string {
	names := make([]string, len(groups))
	repeats := make(map[string]int, len(groups))
	for i, h := range groups {
		names[i] = h.ProjectName()
		repeats[names[i]]++
	}
	for i, h := range groups {
		if repeats[names[i]] < 2 {
			continue
		}
		if qualifier := qualifierOf(h); qualifier != "" {
			names[i] = qualifier + "/" + names[i]
		}
	}
	return names
}

func qualifierOf(h *Handoff) string {
	switch {
	case h.Project != "":
		// An explicit project is the key itself, so two groups cannot share
		// one; nothing here would be telling them apart.
		return ""
	case h.Repo != "":
		return parentName(h.Repo)
	case h.Title != "":
		// The title is the name and the folder is the rest of the key.
		return baseName(h.CWD)
	default:
		return parentName(h.CWD)
	}
}

func parentName(path string) string {
	trimmed := strings.TrimRight(path, string(filepath.Separator))
	if trimmed == "" {
		return ""
	}
	parent := filepath.Dir(trimmed)
	if parent == "." || parent == string(filepath.Separator) {
		return ""
	}
	return baseName(parent)
}

func baseName(path string) string {
	trimmed := strings.TrimRight(path, string(filepath.Separator))
	if trimmed == "" {
		return ""
	}
	return text.OneLine(filepath.Base(trimmed))
}

// message is the last thing that was asked, which is the closest thing to
// "where we left off" that a raw handoff holds. A prompt can run to pages, so
// only its opening line survives the column.
func message(h *Handoff) string {
	first, _, _ := strings.Cut(h.Message, "\n")
	first = text.OneLine(first)
	if first == "" {
		return "(no prompt recorded)"
	}
	return text.Summarize(first, maxMessageWidth)
}

// ByProject groups by the workstream a session belonged to, which is a
// different question in and out of a repository. Inside one, the repository is
// the work and its worktrees are tasks within it, so they collapse. Outside,
// the folder is not the work — one notes folder can host content, branding and
// product sessions side by side — and the only thing naming a session is what
// it was called, so a title keys its own line.
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
	if name := baseName(repo); name != "" {
		return name
	}
	return "(unknown)"
}
