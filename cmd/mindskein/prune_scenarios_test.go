package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/swilgosz/mindskein/internal/config"
	"github.com/swilgosz/mindskein/internal/session"
)

func pruneHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("MINDSKEIN_HOME", home)
	return home
}

func agedSession(t *testing.T, id string, age time.Duration, now time.Time) {
	t.Helper()
	store, err := session.DefaultStore()
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if _, err := store.Upsert(id, now.Add(-age), func(s *session.Session) {
		s.Status = session.StatusWaiting
		s.ProjectPath = "/tmp/x"
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}
}

// TestPruneDailySweepsAtMostOncePerDay covers the cost side of pruning. It
// runs from the Stop hook, which fires on every turn, so the guard is what
// keeps a per-turn hook from walking two directories every time.
func TestPruneDailySweepsAtMostOncePerDay(t *testing.T) {
	cfg := config.Defaults()

	t.Run("the first sweep runs and stamps", func(t *testing.T) {
		home := pruneHome(t)
		now := time.Now().UTC()
		agedSession(t, "old00001", 60*24*time.Hour, now)

		if err := pruneDaily(cfg, now); err != nil {
			t.Fatalf("pruneDaily: %v", err)
		}
		if _, err := os.Stat(filepath.Join(home, pruneStamp)); err != nil {
			t.Fatalf("no stamp written: %v", err)
		}
		store, _ := session.DefaultStore()
		path, _ := store.Path("old00001")
		if _, err := os.Stat(path); err == nil {
			t.Error("the sweep ran but collected nothing")
		}
	})

	t.Run("a second sweep the same day does nothing", func(t *testing.T) {
		pruneHome(t)
		now := time.Now().UTC()
		if err := pruneDaily(cfg, now); err != nil {
			t.Fatalf("first sweep: %v", err)
		}
		agedSession(t, "old00002", 60*24*time.Hour, now)
		if err := pruneDaily(cfg, now); err != nil {
			t.Fatalf("second sweep: %v", err)
		}
		store, _ := session.DefaultStore()
		path, _ := store.Path("old00002")
		if _, err := os.Stat(path); err != nil {
			t.Error("swept twice in one day; on a per-turn hook that is the whole cost")
		}
	})

	t.Run("a sweep the next day runs again", func(t *testing.T) {
		pruneHome(t)
		now := time.Now().UTC()
		if err := pruneDaily(cfg, now); err != nil {
			t.Fatalf("first sweep: %v", err)
		}
		agedSession(t, "old00003", 60*24*time.Hour, now)
		if err := pruneDaily(cfg, now.Add(25*time.Hour)); err != nil {
			t.Fatalf("next-day sweep: %v", err)
		}
		store, _ := session.DefaultStore()
		path, _ := store.Path("old00003")
		if _, err := os.Stat(path); err == nil {
			t.Error("the horizon passed but nothing was collected")
		}
	})

	t.Run("a sweep that fails still stamps, so it is not retried every turn", func(t *testing.T) {
		// Stamping before sweeping is deliberate and is the whole reason the
		// order is what it is. A directory that cannot be read would
		// otherwise be retried on every single Stop for the rest of the day.
		if os.Geteuid() == 0 {
			t.Skip("root can read a 0000 directory, so the failure cannot be staged")
		}
		home := pruneHome(t)
		now := time.Now().UTC()

		sessions := filepath.Join(home, "sessions")
		if err := os.MkdirAll(sessions, 0o700); err != nil {
			t.Fatalf("staging: %v", err)
		}
		if err := os.Chmod(sessions, 0o000); err != nil {
			t.Fatalf("staging: %v", err)
		}
		t.Cleanup(func() { os.Chmod(sessions, 0o700) })

		if err := pruneDaily(cfg, now); err == nil {
			t.Fatal("precondition: the sweep was supposed to fail")
		}
		if _, err := os.Stat(filepath.Join(home, pruneStamp)); err != nil {
			t.Errorf("a failed sweep left no stamp, so it retries on every turn today: %v", err)
		}
	})

	t.Run("retention off still stamps rather than sweeping every turn", func(t *testing.T) {
		home := pruneHome(t)
		now := time.Now().UTC()
		agedSession(t, "old00004", 10*365*24*time.Hour, now)

		off := config.Defaults()
		off.Retention.Sessions = 0
		off.Retention.Handoffs = 0
		if err := pruneDaily(off, now); err != nil {
			t.Fatalf("pruneDaily: %v", err)
		}
		store, _ := session.DefaultStore()
		path, _ := store.Path("old00004")
		if _, err := os.Stat(path); err != nil {
			t.Error("retention off deleted a record")
		}
		if _, err := os.Stat(filepath.Join(home, pruneStamp)); err != nil {
			t.Error("no stamp, so this walks both directories on every turn forever")
		}
	})
}

// TestPruneCommandDryRun covers the promise the flag makes: it answers the
// question without acting on it.
func TestPruneCommandDryRun(t *testing.T) {
	pruneHome(t)
	now := time.Now().UTC()
	agedSession(t, "old00005", 60*24*time.Hour, now)

	var stdout, stderr bytes.Buffer
	if err := cmdPrune([]string{"--dry-run"}, &stdout, &stderr); err != nil {
		t.Fatalf("cmdPrune: %v", err)
	}
	store, _ := session.DefaultStore()
	path, _ := store.Path("old00005")
	if _, err := os.Stat(path); err != nil {
		t.Error("--dry-run deleted the record it was only asked about")
	}
	if !strings.Contains(stdout.String(), "would remove") || !strings.Contains(stdout.String(), "30d") {
		t.Errorf("output must say what would go and under which rule:\n%s", stdout.String())
	}
}
