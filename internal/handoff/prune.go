package handoff

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/swilgosz/mindskein/internal/session"
)

// Prune deletes handoffs that ended longer ago than horizon. A zero horizon
// prunes nothing.
func (s *Store) Prune(now time.Time, horizon time.Duration, dryRun bool) (*session.PruneResult, error) {
	res := &session.PruneResult{Horizon: horizon, DryRun: dryRun, Kind: "handoff"}
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
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || filepath.Ext(name) != ".md" {
			continue
		}
		id := strings.TrimSuffix(name, ".md")
		if _, err := session.SafeID(id); err != nil {
			continue
		}
		path := filepath.Join(s.Dir, name)
		if !expired(path, now, horizon) {
			res.Kept++
			continue
		}
		res.Removed = append(res.Removed, id)
		if dryRun {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("removing %s: %w", path, err)
		}
	}
	sort.Strings(res.Removed)
	return res, nil
}

// expired reads the frontmatter for an end time and falls back to the file's
// modification time when it will not parse — a half-written handoff is still
// collectable once it is old enough, and still repairable before that.
func expired(path string, now time.Time, horizon time.Duration) bool {
	if h, err := Load(path); err == nil && !h.EndedAt.IsZero() {
		return now.Sub(h.EndedAt) > horizon
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return now.Sub(info.ModTime()) > horizon
}
