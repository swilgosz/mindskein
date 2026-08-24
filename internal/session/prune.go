package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PruneResult is what a prune did, or would have done for a dry run.
type PruneResult struct {
	Removed []string
	Kept    int
	Horizon time.Duration
	DryRun  bool

	// Kind names what was pruned, so one summary line reads correctly for
	// sessions and handoffs alike. Empty means session records.
	Kind string
}

// Summary is the one line the CLI prints. A command that deletes files says
// which rule it applied, so an unexpected result is diagnosable from the
// output alone.
func (r *PruneResult) Summary() string {
	verb := "removed"
	if r.DryRun {
		verb = "would remove"
	}
	kind := r.Kind
	if kind == "" {
		kind = "session"
	}
	return fmt.Sprintf("%s %d %s record(s) older than %s, kept %d",
		verb, len(r.Removed), kind, ShortDuration(r.Horizon), r.Kept)
}

// ShortDuration spells a horizon the way it is written in config.toml rather
// than the way Go prints it: "30d", not "720h0m0s".
func ShortDuration(d time.Duration) string {
	if d <= 0 {
		return "off"
	}
	days := int(d / (24 * time.Hour))
	rest := d % (24 * time.Hour)
	switch {
	case days > 0 && rest == 0:
		return fmt.Sprintf("%dd", days)
	case days > 0:
		return fmt.Sprintf("%dd%s", days, strings.TrimSuffix(rest.String(), "0s"))
	default:
		return d.String()
	}
}

// Prune deletes session records that have been quiet for longer than horizon,
// together with the lock file each one leaves behind.
//
// A zero horizon prunes nothing: retention off must mean off, not immediate.
//
// A session whose process is still running is never deleted, whatever its age.
// An earlier version of this skipped the check on the belief that the stored
// pid belonged to the shell that ran the hook; it does not — it is the claude
// process itself, and the start time stored beside it tells that process from
// whatever later inherits the number. Age alone would eventually collect a
// session that had merely been open a long time.
func (s *Store) Prune(now time.Time, horizon time.Duration, dryRun bool) (*PruneResult, error) {
	return s.prune(now, horizon, dryRun, SystemProbe())
}

func (s *Store) prune(now time.Time, horizon time.Duration, dryRun bool, probe Probe) (*PruneResult, error) {
	res := &PruneResult{Horizon: horizon, DryRun: dryRun}
	if horizon <= 0 {
		return res, nil
	}
	entries, err := os.ReadDir(s.Dir)
	if os.IsNotExist(err) {
		return res, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.Dir, err)
	}

	for _, e := range entries {
		id, ok := recordID(e)
		if !ok {
			continue
		}
		path := filepath.Join(s.Dir, e.Name())
		if !s.expired(path, id, now, horizon) || s.alive(path, now, probe) {
			res.Kept++
			continue
		}
		res.Removed = append(res.Removed, id)
		if dryRun {
			continue
		}
		if err := s.remove(id, path); err != nil {
			return nil, err
		}
	}
	sort.Strings(res.Removed)
	return res, nil
}

// recordID returns the session id for a file this store owns, and false for
// anything else in the directory. The state directory sits in a user's home,
// and a stray file there is not ours to delete.
func recordID(e os.DirEntry) (string, bool) {
	if e.IsDir() || !strings.HasSuffix(e.Name(), fileExt) {
		return "", false
	}
	id := strings.TrimSuffix(e.Name(), fileExt)
	if _, err := safeID(id); err != nil {
		return "", false
	}
	return id, true
}

// expired decides whether one record is past the horizon.
//
// A record that will not parse falls back to its modification time. That is
// the truncated write left by a killed process — the parse failure is a reason
// to collect it once it is old, not a reason to keep it forever. Inside the
// horizon it is left alone, because a file being written right now also fails
// to parse.
func (s *Store) expired(path, id string, now time.Time, horizon time.Duration) bool {
	if sess, err := loadFile(path); err == nil {
		return sess.Older(now, horizon)
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return now.Sub(info.ModTime()) > horizon
}

// alive reports whether the record belongs to a process that is still running.
// A record that will not parse has no pid to check and is left to the horizon.
func (s *Store) alive(path string, now time.Time, probe Probe) bool {
	sess, err := loadFile(path)
	if err != nil {
		return false
	}
	switch sess.Reported(now, probe) {
	case StateRunning, StateWaiting:
		return true
	default:
		return false
	}
}

// remove deletes a record and its lock, holding the lock while it does so a
// hook cannot be mid-write on the file being deleted.
func (s *Store) remove(id, path string) error {
	unlock, err := s.lock(id)
	if err == nil {
		defer unlock()
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	if err := os.Remove(s.lockPath(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing lock for %s: %w", id, err)
	}
	return nil
}
