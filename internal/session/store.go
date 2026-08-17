package session

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
)

// Store reads and writes ~/.mindskein/sessions/{session_id}.json with atomic
// writes, so a half-written file is never observable by a concurrent brief.
//
// Implemented by U1 (hook capture + session registry).
type Store struct {
	Dir string
}

const (
	dirPerm  fs.FileMode = 0o700
	filePerm fs.FileMode = 0o600

	// maxIDLen bounds the session id so it can never exceed a filesystem's
	// name limit. Claude Code ids are UUIDs; this is pure defence.
	maxIDLen = 128
)

// ErrInvalidID is returned for a session id that cannot safely become a
// filename. The id arrives in a hook payload, so it is untrusted input.
var ErrInvalidID = errors.New("invalid session id")

// Home returns the mindskein state directory, ~/.mindskein by default.
// MINDSKEIN_HOME overrides it, which is what the tests use.
func Home() (string, error) {
	if dir := os.Getenv("MINDSKEIN_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, ".mindskein"), nil
}

// DefaultStore is the store both the hooks and the brief use.
func DefaultStore() (*Store, error) {
	home, err := Home()
	if err != nil {
		return nil, err
	}
	return &Store{Dir: filepath.Join(home, "sessions")}, nil
}

// safeID validates that id can be used as a filename: no separators, no
// traversal, no surprises.
func safeID(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("%w: empty", ErrInvalidID)
	}
	if len(id) > maxIDLen {
		return "", fmt.Errorf("%w: longer than %d characters", ErrInvalidID, maxIDLen)
	}
	if id == "." || id == ".." {
		return "", fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return "", fmt.Errorf("%w: %q contains %q", ErrInvalidID, id, string(r))
		}
	}
	return id, nil
}

// Path is the file backing a session id.
func (s *Store) Path(id string) (string, error) {
	safe, err := safeID(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.Dir, safe+".json"), nil
}

// Load reads one session. A missing file returns an error wrapping
// fs.ErrNotExist, which callers check with errors.Is.
func (s *Store) Load(id string) (*Session, error) {
	path, err := s.Path(id)
	if err != nil {
		return nil, err
	}
	return loadFile(path)
}

func loadFile(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &sess, nil
}

// Save writes the session atomically: a temp file in the same directory,
// fsynced, then renamed over the target. A reader sees either the previous
// complete file or the new one, never a truncated middle.
func (s *Store) Save(sess *Session) error {
	path, err := s.Path(sess.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, dirPerm); err != nil {
		return fmt.Errorf("creating %s: %w", s.Dir, err)
	}

	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding session %s: %w", sess.ID, err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(s.Dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", s.Dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	if err := writeAndSync(tmp, data); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, filePerm); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmpName, path, err)
	}
	return nil
}

func writeAndSync(f *os.File, data []byte) error {
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("writing %s: %w", f.Name(), err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("syncing %s: %w", f.Name(), err)
	}
	return nil
}

// Upsert loads the session, applies the mutation and writes it back, stamping
// LastEventAt. A missing file starts a new session; an unreadable or corrupt
// one is replaced rather than propagated — the registry is a live cache, and a
// hook that failed hard would degrade the very session it is observing.
func (s *Store) Upsert(id string, now time.Time, apply func(*Session)) (*Session, error) {
	if _, err := safeID(id); err != nil {
		return nil, err
	}

	sess, err := s.Load(id)
	if err != nil || sess == nil {
		sess = &Session{ID: id, Agent: AgentClaudeCode, StartedAt: now}
	}
	// A file written by an older build, or hand-edited, may be missing these.
	if sess.ID == "" {
		sess.ID = id
	}
	if sess.Agent == "" {
		sess.Agent = AgentClaudeCode
	}
	if sess.StartedAt.IsZero() {
		sess.StartedAt = now
	}

	apply(sess)
	sess.LastEventAt = now

	if err := s.Save(sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// List returns every readable session, most recently active first. Corrupt or
// unreadable files are skipped rather than failing the listing: one bad file
// must not blank out the brief.
func (s *Store) List() ([]*Session, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", s.Dir, err)
	}

	var sessions []*Session
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || filepath.Ext(name) != ".json" {
			continue
		}
		sess, err := loadFile(filepath.Join(s.Dir, name))
		if err != nil {
			continue
		}
		sessions = append(sessions, sess)
	}

	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].LastEventAt.Equal(sessions[j].LastEventAt) {
			return sessions[i].ID < sessions[j].ID
		}
		return sessions[i].LastEventAt.After(sessions[j].LastEventAt)
	})
	return sessions, nil
}
