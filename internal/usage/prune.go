package usage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/swilgosz/mindskein/internal/session"
)

// Prune deletes records whose session's last turn is older than horizon. A
// zero horizon prunes nothing.
//
// It takes no lock: a record only summarises a transcript, and one removed
// while a Stop is capturing it is rebuilt from the start on the next turn.
func (s *Store) Prune(now time.Time, horizon time.Duration, dryRun bool) (*session.PruneResult, error) {
	res := &session.PruneResult{Horizon: horizon, DryRun: dryRun, Kind: "usage"}
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
		if e.IsDir() || strings.HasPrefix(name, ".") || filepath.Ext(name) != ".json" {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
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

// expired dates a record by its session's last turn, not by the file: a
// backfill writes today's date on a session from months ago. A record that
// will not parse falls back to the file's age, so a half-written one is left
// alone and a corrupt one is still collected in time.
func expired(path string, now time.Time, horizon time.Duration) bool {
	if r, err := loadRecord(path); err == nil && r.File != nil && !r.File.End.IsZero() {
		return now.Sub(r.File.End) > horizon
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return now.Sub(info.ModTime()) > horizon
}
