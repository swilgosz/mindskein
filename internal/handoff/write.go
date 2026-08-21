// Package handoff records what a session was doing, so the brief can answer
// "where did we leave off" without anyone opening a transcript.
//
// One file per session, in a central store. Central because the hooks are
// global, so cwd is any directory Claude is launched in — writing there would
// put an untracked file in git repos, copy typed prose into employer
// repositories, and drop stray notes into vault folders. Per session because
// Stop fires every turn and two sessions in one folder is the normal working
// pattern; a shared file would have them overwriting each other.
package handoff

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/swilgosz/mindskein/internal/session"
	"github.com/swilgosz/mindskein/internal/text"
)

const (
	dirPerm  fs.FileMode = 0o700
	filePerm fs.FileMode = 0o600
)

// Handoff is one session's record. Every identity field is stored rather than
// collapsed into a single project, because which of them is the project is the
// reader's question: a worktree is a task, a folder can host several unrelated
// workstreams, a title is whatever the session was renamed to. Choosing here
// would mean rewriting history to change the grouping.
type Handoff struct {
	SessionID string
	Title     string
	Project   string
	CWD       string
	RepoRoot  string
	Repo      string
	Branch    string
	Status    string
	StartedAt time.Time
	SegmentAt time.Time
	EndedAt   time.Time
	LastTool  string
	Message   string
}

func (h *Handoff) Duration() time.Duration {
	if h.SegmentAt.IsZero() || h.EndedAt.Before(h.SegmentAt) {
		return 0
	}
	return h.EndedAt.Sub(h.SegmentAt)
}

func (h *Handoff) Name() string {
	return Location{CWD: h.CWD, Root: h.RepoRoot}.Name()
}

// Named is what this work was called, empty when nothing named it. A caller
// that must be able to tell a real title from a folder asks for this.
//
// Flattened, because a title is not curated text: with no rename and no
// generated title it is the first thing the person typed, newlines, tabs and
// any pasted terminal escapes included. The stored fields keep the original —
// this is the display form, and every renderer reaches the title through here.
func (h *Handoff) Named() string {
	if h.Project != "" {
		return text.OneLine(h.Project)
	}
	return text.OneLine(h.Title)
}

// Label prefers what a human chose over what was generated, falling back to
// the folder so the handoff document always has a heading.
func (h *Handoff) Label() string {
	if named := h.Named(); named != "" {
		return named
	}
	return h.Name()
}

type Store struct {
	Dir string
}

func DefaultStore() (*Store, error) {
	home, err := session.Home()
	if err != nil {
		return nil, err
	}
	return &Store{Dir: filepath.Join(home, "handoffs")}, nil
}

func (s *Store) Path(id string) (string, error) {
	safe, err := session.SafeID(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.Dir, safe+".md"), nil
}

// Record builds a handoff from a finished turn and writes it. A missing or
// unreadable transcript still produces one: the session record alone answers
// where you were, and refusing to write would lose that too.
func Record(store *Store, sess *session.Session, transcriptPath, project string, now time.Time) (*Handoff, error) {
	loc := Locate(sess.ProjectPath)
	h := &Handoff{
		SessionID: sess.ID,
		Project:   project,
		CWD:       sess.ProjectPath,
		RepoRoot:  loc.Root,
		Repo:      loc.Repo,
		Branch:    loc.Branch,
		Status:    string(sess.Status),
		StartedAt: sess.StartedAt,
		SegmentAt: sess.StartedAt,
		EndedAt:   now,
	}

	// LastTool deliberately does not fall back to the session record: this runs
	// on Stop, and the Stop handler has already overwritten the tool name with
	// its own event. The transcript is the only source that still knows.
	if transcriptPath != "" {
		if tr, err := ReadTranscript(transcriptPath); err == nil {
			h.Title = tr.Title
			h.Message = tr.LastMessage
			h.LastTool = tr.LastTool
			if !tr.StartedAt.IsZero() {
				h.StartedAt = tr.StartedAt
			}
			if !tr.SegmentAt.IsZero() {
				h.SegmentAt = tr.SegmentAt
			}
		}
	}

	return h, store.Write(h)
}

// Write saves atomically by temp-then-rename, so a concurrent read sees the
// previous file whole rather than half of the new one.
func (s *Store) Write(h *Handoff) error {
	path, err := s.Path(h.SessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, dirPerm); err != nil {
		return fmt.Errorf("creating %s: %w", s.Dir, err)
	}

	tmp, err := os.CreateTemp(s.Dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", s.Dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(h.Markdown()); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, filePerm); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmpName, path, err)
	}
	return nil
}

// Markdown renders frontmatter for the reader and prose for a human. Values are
// quoted because paths and titles contain spaces, colons and newlines, and a
// hand-rolled parser should not have to guess where one ends.
func (h *Handoff) Markdown() string {
	var b strings.Builder

	b.WriteString("---\n")
	for _, f := range []struct{ k, v string }{
		{"session_id", h.SessionID},
		{"title", h.Title},
		{"project", h.Project},
		{"cwd", h.CWD},
		{"repo_root", h.RepoRoot},
		{"repo", h.Repo},
		{"branch", h.Branch},
		{"status", h.Status},
		{"last_tool", h.LastTool},
		{"message", h.Message},
	} {
		fmt.Fprintf(&b, "%s: %s\n", f.k, strconv.Quote(f.v))
	}
	for _, f := range []struct {
		k string
		v time.Time
	}{
		{"started_at", h.StartedAt},
		{"segment_at", h.SegmentAt},
		{"ended_at", h.EndedAt},
	} {
		fmt.Fprintf(&b, "%s: %s\n", f.k, strconv.Quote(f.v.UTC().Format(time.RFC3339)))
	}
	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "# MindSkein Handoff — %s\n\n", h.Label())

	where := h.Name()
	if h.Branch != "" {
		where += " · " + h.Branch
	}
	fmt.Fprintf(&b, "- **Session:** %s · %s\n", shortID(h.SessionID), where)
	fmt.Fprintf(&b, "- **Duration:** %s\n", humanDuration(h.Duration()))
	fmt.Fprintf(&b, "- **Status at end:** %s\n", orDash(h.Status))
	fmt.Fprintf(&b, "- **Last tool:** %s\n", orDash(h.LastTool))
	fmt.Fprintf(&b, "- **Ended:** %s\n", h.EndedAt.UTC().Format("2006-01-02 15:04 MST"))

	b.WriteString("\n## Next Action\n\n")
	if h.Message == "" {
		b.WriteString("_No prompt recorded._\n")
	} else {
		for _, line := range strings.Split(h.Message, "\n") {
			b.WriteString("> " + line + "\n")
		}
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
