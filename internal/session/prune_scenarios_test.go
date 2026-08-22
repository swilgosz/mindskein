package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const month = 30 * 24 * time.Hour

// seed writes a session that last spoke `silent` ago, through Upsert rather
// than Save — that is the path the hooks take, and it is what leaves the .lock
// file beside the record that prune has to collect too.
func seed(t *testing.T, store *Store, id string, status Status, silent time.Duration) {
	t.Helper()
	_, err := store.Upsert(id, at.Add(-silent), func(s *Session) {
		s.ProjectPath = "/tmp/x"
		s.Status = status
		s.LastEvent = "Stop"
	})
	if err != nil {
		t.Fatalf("seeding %s: %v", id, err)
	}
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

// TestPruneRemovesRecordsPastTheHorizon covers the leak the retention horizon
// only ever papered over: hiding a record from the listing does not remove it,
// so the registry grows for as long as the tool is installed. Every session
// leaves a .json and a .lock behind and nothing ever collected either.
func TestPruneRemovesRecordsPastTheHorizon(t *testing.T) {
	t.Run("removes a record quiet past the horizon", func(t *testing.T) {
		store := testStore(t)
		seed(t, store, "old00001", StatusWaiting, 40*24*time.Hour)
		res, err := store.Prune(at, month, false)
		if err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if len(res.Removed) != 1 || res.Removed[0] != "old00001" {
			t.Fatalf("Removed = %v, want [old00001]", res.Removed)
		}
		path, _ := store.Path("old00001")
		if exists(t, path) {
			t.Error("the record survived the prune it was reported as removed by")
		}
	})

	t.Run("removes the lock file with the record", func(t *testing.T) {
		store := testStore(t)
		seed(t, store, "old00001", StatusWaiting, 40*24*time.Hour)
		if !exists(t, store.lockPath("old00001")) {
			t.Fatal("precondition: no lock file was written")
		}
		if _, err := store.Prune(at, month, false); err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if exists(t, store.lockPath("old00001")) {
			t.Error("a lock outliving its session is the leak in its own right")
		}
	})

	t.Run("keeps a record inside the horizon", func(t *testing.T) {
		store := testStore(t)
		seed(t, store, "new00001", StatusWaiting, 20*24*time.Hour)
		res, err := store.Prune(at, month, false)
		if err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if len(res.Removed) != 0 {
			t.Errorf("Removed = %v, want none", res.Removed)
		}
		path, _ := store.Path("new00001")
		if !exists(t, path) {
			t.Error("a record inside the horizon was deleted")
		}
	})

	t.Run("a record still claiming to run is not evidence it is alive", func(t *testing.T) {
		// The PID on a record is the hook's parent shell, not the Claude
		// process, and it is recycled within days. Age is the only signal
		// here that means anything a month later.
		store := testStore(t)
		seed(t, store, "old00001", StatusRunning, 40*24*time.Hour)
		res, _ := store.Prune(at, month, false)
		if len(res.Removed) != 1 {
			t.Errorf("Removed = %v, want the ancient running record gone", res.Removed)
		}
	})

	t.Run("a zero horizon removes nothing", func(t *testing.T) {
		store := testStore(t)
		seed(t, store, "old00001", StatusWaiting, 10*365*24*time.Hour)
		res, err := store.Prune(at, 0, false)
		if err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if len(res.Removed) != 0 {
			t.Errorf("a zero horizon must be off, not instant: %v", res.Removed)
		}
	})

	t.Run("a zero horizon spares even a record that will not parse", func(t *testing.T) {
		// The unparseable path falls back to modification time, where every
		// file is "older than zero". Retention off has to mean off on that
		// path too, or turning the horizon off would delete the very records
		// most worth keeping for repair.
		store := testStore(t)
		if err := os.MkdirAll(store.Dir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		broken := filepath.Join(store.Dir, "broke001.json")
		if err := os.WriteFile(broken, []byte("{ truncated"), 0o600); err != nil {
			t.Fatalf("seeding: %v", err)
		}
		old := at.Add(-10 * 365 * 24 * time.Hour)
		if err := os.Chtimes(broken, old, old); err != nil {
			t.Fatalf("aging: %v", err)
		}
		res, err := store.Prune(at, 0, false)
		if err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if len(res.Removed) != 0 || !exists(t, broken) {
			t.Errorf("retention off deleted a record anyway: %v", res.Removed)
		}
	})

	t.Run("a dry run reports without deleting", func(t *testing.T) {
		store := testStore(t)
		seed(t, store, "old00001", StatusWaiting, 40*24*time.Hour)
		res, err := store.Prune(at, month, true)
		if err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if len(res.Removed) != 1 {
			t.Fatalf("a dry run must still say what would go: %v", res.Removed)
		}
		path, _ := store.Path("old00001")
		if !exists(t, path) {
			t.Error("the dry run deleted the record it was only asked about")
		}
	})

	t.Run("counts what it kept", func(t *testing.T) {
		store := testStore(t)
		seed(t, store, "old00001", StatusWaiting, 40*24*time.Hour)
		seed(t, store, "new00001", StatusWaiting, time.Hour)
		seed(t, store, "new00002", StatusWaiting, time.Hour)
		res, _ := store.Prune(at, month, false)
		if res.Kept != 2 {
			t.Errorf("Kept = %d, want 2", res.Kept)
		}
	})

	t.Run("leaves a file it does not recognise alone", func(t *testing.T) {
		// The state directory is a plain folder in a user's home. Deleting
		// what we cannot parse would make an unrelated stray file a data
		// loss bug.
		store := testStore(t)
		seed(t, store, "old00001", StatusWaiting, 40*24*time.Hour)
		stray := filepath.Join(store.Dir, "notes.txt")
		if err := os.WriteFile(stray, []byte("hello"), 0o600); err != nil {
			t.Fatalf("seeding stray: %v", err)
		}
		if _, err := store.Prune(at, month, false); err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if !exists(t, stray) {
			t.Error("prune deleted a file that was never its to manage")
		}
	})

	t.Run("an unparseable record is removed once past the horizon", func(t *testing.T) {
		// A truncated write from a killed process is exactly what should be
		// collected. It is named like a record and it is old; the parse
		// failure is the reason to remove it, not to keep it forever.
		store := testStore(t)
		if err := os.MkdirAll(store.Dir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		broken := filepath.Join(store.Dir, "broke001.json")
		if err := os.WriteFile(broken, []byte("{ truncated"), 0o600); err != nil {
			t.Fatalf("seeding: %v", err)
		}
		old := at.Add(-40 * 24 * time.Hour)
		if err := os.Chtimes(broken, old, old); err != nil {
			t.Fatalf("aging: %v", err)
		}
		res, err := store.Prune(at, month, false)
		if err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if len(res.Removed) != 1 {
			t.Errorf("an old corrupt record is the thing prune exists for: %v", res.Removed)
		}
	})

	t.Run("an unparseable record inside the horizon is left to be repaired", func(t *testing.T) {
		store := testStore(t)
		if err := os.MkdirAll(store.Dir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		broken := filepath.Join(store.Dir, "broke001.json")
		if err := os.WriteFile(broken, []byte("{ truncated"), 0o600); err != nil {
			t.Fatalf("seeding: %v", err)
		}
		if _, err := store.Prune(at, month, false); err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if !exists(t, broken) {
			t.Error("a record being written right now must not be collected mid-write")
		}
	})

	t.Run("an empty store is not an error", func(t *testing.T) {
		store := testStore(t)
		res, err := store.Prune(at, month, false)
		if err != nil {
			t.Fatalf("Prune on a store that was never written: %v", err)
		}
		if len(res.Removed) != 0 || res.Kept != 0 {
			t.Errorf("got %+v, want an empty result", res)
		}
	})

	t.Run("names the horizon it applied", func(t *testing.T) {
		store := testStore(t)
		seed(t, store, "old00001", StatusWaiting, 40*24*time.Hour)
		res, _ := store.Prune(at, month, false)
		if !strings.Contains(res.Summary(), "30d") {
			t.Errorf("a destructive command must say what rule it applied: %q", res.Summary())
		}
	})
}
