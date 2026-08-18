package handoff

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// List returns every readable handoff, most recently ended first.
//
// Unreadable or malformed files are skipped rather than failing the listing:
// one bad file must not blank out a section of the brief.
func (s *Store) List() ([]*Handoff, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", s.Dir, err)
	}

	var handoffs []*Handoff
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || filepath.Ext(name) != ".md" {
			continue
		}
		h, err := Load(filepath.Join(s.Dir, name))
		if err != nil {
			continue
		}
		handoffs = append(handoffs, h)
	}

	sort.Slice(handoffs, func(i, j int) bool {
		if handoffs[i].EndedAt.Equal(handoffs[j].EndedAt) {
			return handoffs[i].SessionID < handoffs[j].SessionID
		}
		return handoffs[i].EndedAt.After(handoffs[j].EndedAt)
	})
	return handoffs, nil
}

// Load reads one handoff. Only the frontmatter is parsed — the prose below it
// is a rendering of the same fields, for whoever opens the file by hand.
func Load(path string) (*Handoff, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return nil, fmt.Errorf("%s: no frontmatter", path)
	}

	h := &Handoff{}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			return h, nil
		}
		key, raw, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		value, err := strconv.Unquote(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		switch strings.TrimSpace(key) {
		case "session_id":
			h.SessionID = value
		case "title":
			h.Title = value
		case "project":
			h.Project = value
		case "cwd":
			h.CWD = value
		case "repo_root":
			h.RepoRoot = value
		case "repo":
			h.Repo = value
		case "branch":
			h.Branch = value
		case "status":
			h.Status = value
		case "last_tool":
			h.LastTool = value
		case "started_at":
			h.StartedAt = parseTime(value)
		case "segment_at":
			h.SegmentAt = parseTime(value)
		case "ended_at":
			h.EndedAt = parseTime(value)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return nil, fmt.Errorf("%s: unterminated frontmatter", path)
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// NewestPer collapses a list to the most recent handoff per group, preserving
// the order of the input.
//
// The grouping function is the caller's, not this package's. What counts as a
// project depends on the question being asked — one line per repository, per
// worktree, or per named workstream — and the writer deliberately records all
// of those rather than picking one. U4 chooses when it composes the brief.
func NewestPer(handoffs []*Handoff, key func(*Handoff) string) []*Handoff {
	seen := make(map[string]bool, len(handoffs))
	var out []*Handoff
	for _, h := range handoffs {
		k := key(h)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, h)
	}
	return out
}

// ByLocation groups handoffs by where the session ran, collapsing every
// worktree of a repository onto the repository itself. A reasonable default,
// and the one the brief starts with.
func ByLocation(h *Handoff) string {
	if h.Repo != "" {
		return h.Repo
	}
	return h.CWD
}

// BySession keeps every session separate — the grouping that matches how the
// same folder hosts several unrelated workstreams at once.
func BySession(h *Handoff) string { return h.SessionID }
