package handoff

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/swilgosz/mindskein/internal/config"
)

var now = time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC)

func seedHandoff(t *testing.T, store *Store, id string, age time.Duration) string {
	t.Helper()
	h := &Handoff{SessionID: id, Title: "a session", CWD: "/tmp/x", Status: "done",
		StartedAt: now.Add(-age - time.Hour), EndedAt: now.Add(-age), Message: "hello"}
	if err := store.Write(h); err != nil {
		t.Fatalf("seeding %s: %v", id, err)
	}
	path, _ := store.Path(id)
	return path
}

// TestPruneKeepsHandoffsLongerThanSessions covers the asymmetry the two
// horizons exist for. A session record is a live status and stops meaning
// anything once the process is gone. A handoff is the answer to "where did we
// leave off" — the thing the tool exists to give back — so the same age that
// collects a session must not collect a handoff.
func TestPruneKeepsHandoffsLongerThanSessions(t *testing.T) {
	if config.DefaultPruneHandoffs <= config.DefaultPruneSessions {
		t.Fatalf("handoffs (%s) must outlive session records (%s)",
			config.DefaultPruneHandoffs, config.DefaultPruneSessions)
	}

	t.Run("survives the session horizon", func(t *testing.T) {
		store := &Store{Dir: t.TempDir()}
		path := seedHandoff(t, store, "aged0001", config.DefaultPruneSessions+24*time.Hour)
		if _, err := (&Store{Dir: store.Dir}).Prune(now, config.DefaultPruneHandoffs, false); err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Error("a handoff was collected at the session horizon; the memory outlives the status")
		}
	})

	t.Run("is collected past its own horizon", func(t *testing.T) {
		store := &Store{Dir: t.TempDir()}
		path := seedHandoff(t, store, "aged0001", config.DefaultPruneHandoffs+24*time.Hour)
		res, err := store.Prune(now, config.DefaultPruneHandoffs, false)
		if err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if len(res.Removed) != 1 {
			t.Fatalf("Removed = %v, want [aged0001]", res.Removed)
		}
		if _, err := os.Stat(path); err == nil {
			t.Error("reported as removed but still on disk")
		}
	})

	t.Run("a zero horizon collects nothing", func(t *testing.T) {
		store := &Store{Dir: t.TempDir()}
		path := seedHandoff(t, store, "aged0001", 10*365*24*time.Hour)
		res, err := store.Prune(now, 0, false)
		if err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if len(res.Removed) != 0 {
			t.Errorf("retention off must mean off: %v", res.Removed)
		}
		if _, err := os.Stat(path); err != nil {
			t.Error("deleted with retention off")
		}
	})

	t.Run("a dry run reports without deleting", func(t *testing.T) {
		store := &Store{Dir: t.TempDir()}
		path := seedHandoff(t, store, "aged0001", config.DefaultPruneHandoffs+24*time.Hour)
		res, _ := store.Prune(now, config.DefaultPruneHandoffs, true)
		if len(res.Removed) != 1 {
			t.Fatalf("a dry run must still say what would go: %v", res.Removed)
		}
		if _, err := os.Stat(path); err != nil {
			t.Error("the dry run deleted the file it was only asked about")
		}
	})

	t.Run("says handoff, not session, in its summary", func(t *testing.T) {
		store := &Store{Dir: t.TempDir()}
		seedHandoff(t, store, "aged0001", config.DefaultPruneHandoffs+24*time.Hour)
		res, _ := store.Prune(now, config.DefaultPruneHandoffs, false)
		if got := res.Summary(); !strings.Contains(got, "handoff") {
			t.Errorf("summary = %q, want it to name what it pruned", got)
		}
	})

	t.Run("leaves a file it does not own alone", func(t *testing.T) {
		store := &Store{Dir: t.TempDir()}
		seedHandoff(t, store, "aged0001", config.DefaultPruneHandoffs+24*time.Hour)
		stray := store.Dir + "/README.txt"
		if err := os.WriteFile(stray, []byte("mine"), 0o600); err != nil {
			t.Fatalf("seeding stray: %v", err)
		}
		old := now.Add(-10 * 365 * 24 * time.Hour)
		if err := os.Chtimes(stray, old, old); err != nil {
			t.Fatalf("aging: %v", err)
		}
		if _, err := store.Prune(now, config.DefaultPruneHandoffs, false); err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if _, err := os.Stat(stray); err != nil {
			t.Error("prune deleted a file that was never its to manage")
		}
	})
}

// TestPruneFallsBackToModTime covers the half-written handoff. Parsing is the
// normal path; a file with no readable end time still has to be collectable,
// or a crash mid-write leaves a record nothing can ever remove.
func TestPruneFallsBackToModTime(t *testing.T) {
	t.Run("collects an unreadable handoff once it is old", func(t *testing.T) {
		store := &Store{Dir: t.TempDir()}
		path := store.Dir + "/broke001.md"
		if err := os.WriteFile(path, []byte("---\ntruncated"), 0o600); err != nil {
			t.Fatalf("seeding: %v", err)
		}
		old := now.Add(-config.DefaultPruneHandoffs - 24*time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatalf("aging: %v", err)
		}
		res, err := store.Prune(now, config.DefaultPruneHandoffs, false)
		if err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if len(res.Removed) != 1 {
			t.Errorf("Removed = %v, want the stale unreadable handoff", res.Removed)
		}
	})

	t.Run("leaves an unreadable handoff that is still recent", func(t *testing.T) {
		// A file being written right now also fails to parse.
		store := &Store{Dir: t.TempDir()}
		path := store.Dir + "/broke001.md"
		if err := os.WriteFile(path, []byte("---\ntruncated"), 0o600); err != nil {
			t.Fatalf("seeding: %v", err)
		}
		if _, err := store.Prune(now, config.DefaultPruneHandoffs, false); err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Error("a handoff being written was collected mid-write")
		}
	})

	t.Run("a handoff with no end time is judged by its file", func(t *testing.T) {
		store := &Store{Dir: t.TempDir()}
		h := &Handoff{SessionID: "noend001", Title: "x", CWD: "/tmp/x", Status: "done"}
		if err := store.Write(h); err != nil {
			t.Fatalf("seeding: %v", err)
		}
		path, _ := store.Path("noend001")
		old := now.Add(-config.DefaultPruneHandoffs - 24*time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatalf("aging: %v", err)
		}
		res, err := store.Prune(now, config.DefaultPruneHandoffs, false)
		if err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if len(res.Removed) != 1 {
			t.Errorf("Removed = %v, want the record with no end time", res.Removed)
		}
	})
}
