package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/swilgosz/mindskein/internal/handoff"
	"github.com/swilgosz/mindskein/internal/session"
)

const (
	dirPerm  fs.FileMode = 0o700
	filePerm fs.FileMode = 0o600

	// recordVersion invalidates stored summaries when the parser changes what
	// it counts; a stale record is re-read from offset zero.
	recordVersion = 1
)

// Source is the transcript a summary was read from, and how far.
type Source struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Offset  int64     `json:"offset"`
}

// Subagent is one subagent transcript of a session.
type Subagent struct {
	Source
	AgentType string `json:"agent_type"`
	File      *File  `json:"file"`
}

// Record is everything stored for one session: the main transcript and each
// subagent transcript beneath it.
type Record struct {
	Version   int                  `json:"version"`
	SessionID string               `json:"session_id"`
	Root      string               `json:"root"`
	Repo      string               `json:"repo"`
	Main      Source               `json:"main"`
	File      *File                `json:"file"`
	Subagents map[string]*Subagent `json:"subagents,omitempty"`
}

// Store holds one usage record per session.
type Store struct {
	Dir string
	Loc *time.Location
}

// DefaultStore lives beside the session and handoff stores.
func DefaultStore() (*Store, error) {
	home, err := session.Home()
	if err != nil {
		return nil, err
	}
	return &Store{Dir: filepath.Join(home, "usage"), Loc: time.Local}, nil
}

func (s *Store) loc() *time.Location {
	if s.Loc == nil {
		return time.Local
	}
	return s.Loc
}

func (s *Store) path(id string) (string, error) {
	safe, err := session.SafeID(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.Dir, safe+".json"), nil
}

func (s *Store) Load(id string) (*Record, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	return loadRecord(path)
}

func loadRecord(path string) (*Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	return &r, nil
}

// List returns every record, oldest first. A corrupt record is skipped: one
// bad file must not blank the report.
func (s *Store) List() ([]*Record, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		r, err := loadRecord(filepath.Join(s.Dir, e.Name()))
		if err != nil || r.File == nil {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File.Start.Before(out[j].File.Start) })
	return out, nil
}

func (s *Store) save(r *Record) error {
	path, err := s.path(r.SessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, dirPerm); err != nil {
		return fmt.Errorf("creating %s: %w", s.Dir, err)
	}
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encoding usage %s: %w", r.SessionID, err)
	}
	tmp, err := os.CreateTemp(s.Dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, filePerm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Result says how much a capture actually read.
type Result struct {
	Parsed  int `json:"parsed"`
	Skipped int `json:"skipped"`
}

func (r *Result) add(o Result) {
	r.Parsed += o.Parsed
	r.Skipped += o.Skipped
}

// Capture brings the session's record up to date with its transcript and the
// subagent transcripts beside it. Only bytes appended since the last capture
// are read; a file that shrank is read again from the start.
func (s *Store) Capture(transcript string) (Result, error) {
	var res Result
	info, err := os.Stat(transcript)
	if err != nil {
		return res, fmt.Errorf("transcript: %w", err)
	}
	id := strings.TrimSuffix(filepath.Base(transcript), filepath.Ext(transcript))
	rec, err := s.Load(id)
	if err != nil || rec.Version != recordVersion {
		rec = &Record{Version: recordVersion, SessionID: id}
	}
	rec.Root = configRoot(transcript)

	changed := false
	if fresh(rec.Main, info) {
		res.Skipped++
	} else {
		if rec.File == nil || info.Size() < rec.Main.Offset {
			rec.File, rec.Main.Offset = newFile(), 0
		}
		off, err := summariseFile(transcript, rec.File, rec.Main.Offset, s.loc())
		if err != nil {
			return res, err
		}
		rec.Main = Source{Path: transcript, Size: info.Size(), ModTime: info.ModTime(), Offset: off}
		res.Parsed++
		changed = true
	}
	if rec.File.CWD != "" && rec.Repo == "" {
		rec.Repo = workstream(handoff.Locate(rec.File.CWD))
	}

	sub, subChanged, err := s.captureSubagents(rec, transcript)
	res.add(sub)
	if err != nil {
		return res, err
	}
	if changed || subChanged {
		return res, s.save(rec)
	}
	return res, nil
}

func (s *Store) captureSubagents(rec *Record, transcript string) (Result, bool, error) {
	var res Result
	dir := filepath.Join(strings.TrimSuffix(transcript, filepath.Ext(transcript)), "subagents")
	paths, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	changed := false
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		key := filepath.Base(p)
		if rec.Subagents == nil {
			rec.Subagents = map[string]*Subagent{}
		}
		sa := rec.Subagents[key]
		if sa != nil && fresh(sa.Source, info) {
			res.Skipped++
			continue
		}
		if sa == nil || sa.File == nil || info.Size() < sa.Offset {
			sa = &Subagent{File: newFile(), AgentType: agentType(p)}
		}
		off, err := summariseFile(p, sa.File, sa.Offset, s.loc())
		if err != nil {
			return res, changed, err
		}
		sa.Source = Source{Path: p, Size: info.Size(), ModTime: info.ModTime(), Offset: off}
		rec.Subagents[key] = sa
		res.Parsed++
		changed = true
	}
	return res, changed, nil
}

func fresh(src Source, info fs.FileInfo) bool {
	return src.Path != "" && src.Size == info.Size() && src.ModTime.Equal(info.ModTime())
}

func agentType(transcript string) string {
	data, err := os.ReadFile(strings.TrimSuffix(transcript, ".jsonl") + ".meta.json")
	if err == nil {
		var m struct {
			AgentType string `json:"agentType"`
		}
		if json.Unmarshal(data, &m) == nil && m.AgentType != "" {
			return m.AgentType
		}
	}
	return "subagent"
}

// configRoot is the Claude config directory a transcript belongs to:
// everything before /projects/.
func configRoot(transcript string) string {
	sep := string(filepath.Separator) + "projects" + string(filepath.Separator)
	if i := strings.LastIndex(transcript, sep); i > 0 {
		return transcript[:i]
	}
	return ""
}

// Backfill captures every session transcript under each root's projects
// directory.
func (s *Store) Backfill(roots []string) (Result, error) {
	var res Result
	var errs []error
	for _, root := range roots {
		paths, _ := filepath.Glob(filepath.Join(root, "projects", "*", "*.jsonl"))
		for _, p := range paths {
			r, err := s.Capture(p)
			res.add(r)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", p, err))
			}
		}
	}
	return res, errors.Join(errs...)
}

// Roots is the set of config roots worth scanning: the defaults plus every
// root a record has already been captured from.
func (s *Store) Roots(extra ...string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, p := range extra {
		add(p)
	}
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		add(dir)
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".claude"))
	}
	if recs, err := s.List(); err == nil {
		for _, r := range recs {
			add(r.Root)
		}
	}
	return out
}

// workstream names where a session ran: the repository behind a worktree, so
// sibling worktrees group together, or the folder outside a repository.
func workstream(loc handoff.Location) string {
	if loc.Repo != "" {
		return filepath.Base(strings.TrimSuffix(loc.Repo, ".git"))
	}
	return loc.Name()
}
