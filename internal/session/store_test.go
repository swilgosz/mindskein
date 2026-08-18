package session

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	return &Store{Dir: filepath.Join(t.TempDir(), "sessions")}
}

func TestUpsertCreatesThenUpdates(t *testing.T) {
	s := testStore(t)
	start := time.Date(2026, 8, 17, 21, 0, 0, 0, time.UTC)

	created, err := s.Upsert("abc123", start, func(sess *Session) {
		sess.Status = StatusRunning
		sess.LastEvent = "Edit"
		sess.ProjectPath = "/Users/seb/Projects/mindskein"
	})
	if err != nil {
		t.Fatalf("Upsert() = %v, want nil", err)
	}
	if created.StartedAt != start || created.LastEventAt != start {
		t.Errorf("timestamps = %v/%v, want both %v", created.StartedAt, created.LastEventAt, start)
	}
	if created.Agent != AgentClaudeCode {
		t.Errorf("agent = %q, want %q", created.Agent, AgentClaudeCode)
	}

	later := start.Add(47 * time.Minute)
	updated, err := s.Upsert("abc123", later, func(sess *Session) {
		sess.Status = StatusWaiting
		sess.LastEvent = "idle_prompt"
	})
	if err != nil {
		t.Fatalf("Upsert() second call = %v, want nil", err)
	}
	// StartedAt is the whole point of the upsert: it must survive the update.
	if updated.StartedAt != start {
		t.Errorf("started_at = %v, want the original %v", updated.StartedAt, start)
	}
	if updated.LastEventAt != later {
		t.Errorf("last_event_at = %v, want %v", updated.LastEventAt, later)
	}
	if updated.Status != StatusWaiting {
		t.Errorf("status = %q, want %q", updated.Status, StatusWaiting)
	}
	if updated.ProjectPath != "/Users/seb/Projects/mindskein" {
		t.Errorf("project_path = %q, want it carried over", updated.ProjectPath)
	}
}

func TestUpsertRecoversFromCorruptFile(t *testing.T) {
	s := testStore(t)
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "abc123.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	got, err := s.Upsert("abc123", now, func(sess *Session) { sess.Status = StatusRunning })
	if err != nil {
		t.Fatalf("Upsert() over corrupt file = %v, want nil", err)
	}
	if got.Status != StatusRunning || got.ID != "abc123" {
		t.Errorf("got %+v, want a fresh running session", got)
	}
}

func TestSaveIsAtomicAndPrivate(t *testing.T) {
	s := testStore(t)
	if err := s.Save(&Session{ID: "abc123", LastEventAt: time.Now().UTC()}); err != nil {
		t.Fatalf("Save() = %v, want nil", err)
	}

	path := filepath.Join(s.Dir, "abc123.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %v, want 0600", perm)
	}

	// The temp file used for the atomic rename must not be left behind.
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("temp file %q left behind after Save", e.Name())
		}
	}
}

func TestSafeIDRejectsUnsafeInput(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{
		"",
		".",
		"..",
		"../escape",
		"a/b",
		`a\b`,
		"has space",
		"nul\x00byte",
		strings.Repeat("x", maxIDLen+1),
	} {
		if _, err := s.Path(id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Path(%q) error = %v, want ErrInvalidID", id, err)
		}
		if _, err := s.Upsert(id, time.Now(), func(*Session) {}); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Upsert(%q) error = %v, want ErrInvalidID", id, err)
		}
	}
}

func TestLoadMissingIsNotExist(t *testing.T) {
	s := testStore(t)
	if _, err := s.Load("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load(missing) = %v, want fs.ErrNotExist", err)
	}
}

func TestListSortsByRecencyAndSkipsJunk(t *testing.T) {
	s := testStore(t)
	base := time.Date(2026, 8, 17, 20, 0, 0, 0, time.UTC)

	for id, offset := range map[string]time.Duration{
		"oldest": 0,
		"middle": time.Hour,
		"newest": 2 * time.Hour,
	} {
		if err := s.Save(&Session{ID: id, LastEventAt: base.Add(offset)}); err != nil {
			t.Fatal(err)
		}
	}
	// Junk that List must ignore rather than choke on.
	if err := os.WriteFile(filepath.Join(s.Dir, "broken.json"), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "notes.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := s.List()
	if err != nil {
		t.Fatalf("List() = %v, want nil", err)
	}
	var ids []string
	for _, sess := range got {
		ids = append(ids, sess.ID)
	}
	want := []string{"newest", "middle", "oldest"}
	if len(ids) != len(want) {
		t.Fatalf("List() ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("List() ids = %v, want %v", ids, want)
		}
	}
}

func TestListMissingDirIsEmptyNotError(t *testing.T) {
	s := testStore(t)
	got, err := s.List()
	if err != nil {
		t.Fatalf("List() on missing dir = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("List() = %v, want empty", got)
	}
}

func TestHomeHonoursEnvOverride(t *testing.T) {
	t.Setenv("MINDSKEIN_HOME", "/tmp/mindskein-test")
	got, err := Home()
	if err != nil {
		t.Fatalf("Home() = %v, want nil", err)
	}
	if got != "/tmp/mindskein-test" {
		t.Errorf("Home() = %q, want the MINDSKEIN_HOME override", got)
	}
}

func TestSessionHelpers(t *testing.T) {
	now := time.Date(2026, 8, 17, 21, 0, 0, 0, time.UTC)
	fresh := &Session{ID: "e0cc146a-c9cb-4347", ProjectPath: "/Users/seb/Projects/mindskein/", LastEventAt: now}

	if got := fresh.ShortID(); got != "e0cc146a" {
		t.Errorf("ShortID() = %q, want %q", got, "e0cc146a")
	}
	if got := fresh.ProjectName(); got != "mindskein" {
		t.Errorf("ProjectName() = %q, want %q", got, "mindskein")
	}
	if fresh.Stale(now.Add(time.Hour)) {
		t.Error("Stale() = true one hour on, want false")
	}
	if !fresh.Stale(now.Add(StaleAfter + time.Minute)) {
		t.Error("Stale() = false past StaleAfter, want true")
	}

	empty := &Session{}
	if got := empty.ProjectName(); got != "(unknown)" {
		t.Errorf("ProjectName() with no path = %q, want %q", got, "(unknown)")
	}
	if got := empty.ShortID(); got != "" {
		t.Errorf("ShortID() with no id = %q, want empty", got)
	}
}
