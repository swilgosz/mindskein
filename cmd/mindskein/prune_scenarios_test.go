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

// TestPruneSweepsTheUsageStore covers the third store end to end: a usage
// record holds titles, first prompts and tool inputs, so prune collects it
// like a handoff.
func TestPruneSweepsTheUsageStore(t *testing.T) {
	home := pruneHome(t)
	// The Stop hook sweeps once a day itself; stamp today's sweep so the
	// record survives long enough for the command to be the one collecting it.
	os.WriteFile(filepath.Join(home, pruneStamp), []byte("today\n"), 0o600)
	dir := filepath.Join(t.TempDir(), "projects", "-tmp")
	os.MkdirAll(dir, 0o700)
	transcript := filepath.Join(dir, "old00006.jsonl")
	last := time.Now().UTC().Add(-config.DefaultPruneUsage - 24*time.Hour).Format(time.RFC3339)
	os.WriteFile(transcript, []byte(`{"type":"assistant","timestamp":"`+last+`","sessionId":"old00006",`+
		`"message":{"id":"msg_1","model":"claude-opus-5-5","content":[],"usage":{"input_tokens":1,"output_tokens":2}}}`+"\n"), 0o600)
	if err := run([]string{"hook", "stop"}, strings.NewReader(stopPayload(t, "old00006", transcript)), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(home, "usage", "old00006.json")
	if _, err := os.Stat(record); err != nil {
		t.Fatalf("precondition: no usage record captured: %v", err)
	}

	var stdout bytes.Buffer
	if err := cmdPrune(nil, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("cmdPrune: %v", err)
	}
	if _, err := os.Stat(record); err == nil {
		t.Error("a usage record past the horizon survived prune")
	}
	if !strings.Contains(stdout.String(), "usage record") {
		t.Errorf("output must account for the usage store:\n%s", stdout.String())
	}
}
