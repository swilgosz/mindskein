package handoff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/swilgosz/mindskein/internal/session"
)

func at(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestWriteLoadRoundTrip(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	want := &Handoff{
		SessionID: "7d98bd67-c67e-42fa-8c74-1814bb5f0481",
		Title:     "handoff writer",
		Project:   "Content W35",
		CWD:       "/Users/sebastian/SecondBrain/6. Spaces/62. Business",
		RepoRoot:  "/Users/sebastian/Projects/mindskein",
		Repo:      "/Users/sebastian/Projects/mindskein",
		Branch:    "u2-handoff-writer",
		Status:    "done",
		StartedAt: at(t, "2026-08-18T08:00:00Z"),
		SegmentAt: at(t, "2026-08-18T12:00:00Z"),
		EndedAt:   at(t, "2026-08-18T12:30:00Z"),
		LastTool:  "Bash",
		Message:   "explain what HANDOFF is for",
	}
	if err := store.Write(want); err != nil {
		t.Fatal(err)
	}

	path, err := store.Path(want.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ name, got, want string }{
		{"SessionID", got.SessionID, want.SessionID},
		{"Title", got.Title, want.Title},
		{"Project", got.Project, want.Project},
		{"CWD", got.CWD, want.CWD},
		{"Repo", got.Repo, want.Repo},
		{"Branch", got.Branch, want.Branch},
		{"Status", got.Status, want.Status},
		{"LastTool", got.LastTool, want.LastTool},
		// Message must survive the round trip: the brief reads these files
		// programmatically, and "where did we leave off" is the whole point.
		{"Message", got.Message, want.Message},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if !got.EndedAt.Equal(want.EndedAt) {
		t.Errorf("EndedAt = %v, want %v", got.EndedAt, want.EndedAt)
	}
	if got.Duration() != 30*time.Minute {
		t.Errorf("Duration() = %v, want 30m", got.Duration())
	}
}

// TestWriteIsReadableByHand: the file is the deliverable, not just a record.
func TestWriteIsReadableByHand(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	h := &Handoff{
		SessionID: "abc12345-0000",
		Title:     "handoff writer",
		CWD:       "/Users/sebastian/Projects/mindskein",
		RepoRoot:  "/Users/sebastian/Projects/mindskein",
		Branch:    "u2-handoff-writer",
		Status:    "done",
		SegmentAt: at(t, "2026-08-18T12:00:00Z"),
		EndedAt:   at(t, "2026-08-18T13:05:00Z"),
		LastTool:  "Bash",
		Message:   "first line\nsecond line",
	}
	if err := store.Write(h); err != nil {
		t.Fatal(err)
	}
	path, _ := store.Path(h.SessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{
		"# MindSkein Handoff — handoff writer",
		"1h 5m",
		"**Last tool:** Bash",
		"u2-handoff-writer",
		"## Next Action",
		"> first line\n> second line",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered handoff missing %q\ngot:\n%s", want, body)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != filePerm {
		t.Errorf("mode = %v, want %v — handoffs quote typed prompts", info.Mode().Perm(), filePerm)
	}
}

// TestConcurrentSessionsInOneFolderBothSurvive is the reason
// handoffs are keyed by session rather than by project.
func TestConcurrentSessionsInOneFolderBothSurvive(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	folder := "/Users/sebastian/SecondBrain/6. Spaces/62. Business"

	for _, s := range []struct{ id, title, msg string }{
		{"aaaa1111", "Content", "plan W35 notes"},
		{"bbbb2222", "Branding", "positioning draft"},
	} {
		if err := store.Write(&Handoff{
			SessionID: s.id, Title: s.title, CWD: folder,
			Status: "done", EndedAt: at(t, "2026-08-18T12:00:00Z"), Message: s.msg,
		}); err != nil {
			t.Fatal(err)
		}
	}

	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d handoffs, want 2 — same folder must not collapse into one file", len(list))
	}

	got := map[string]string{}
	for _, h := range list {
		got[h.Title] = h.SessionID
	}
	if got["Content"] != "aaaa1111" || got["Branding"] != "bbbb2222" {
		t.Errorf("handoffs clobbered each other: %+v", got)
	}
}

func TestListSortsNewestFirstAndSkipsJunk(t *testing.T) {
	dir := t.TempDir()
	store := &Store{Dir: dir}
	for _, s := range []struct{ id, ended string }{
		{"old00000", "2026-08-18T09:00:00Z"},
		{"new00000", "2026-08-18T18:00:00Z"},
		{"mid00000", "2026-08-18T13:00:00Z"},
	} {
		if err := store.Write(&Handoff{SessionID: s.id, EndedAt: at(t, s.ended)}); err != nil {
			t.Fatal(err)
		}
	}
	// A truncated file and a stray non-handoff must not break the listing.
	if err := os.WriteFile(filepath.Join(dir, "broken.md"), []byte("not a handoff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, h := range list {
		ids = append(ids, h.SessionID)
	}
	if len(ids) != 3 || ids[0] != "new00000" || ids[2] != "old00000" {
		t.Errorf("List() = %v, want newest first and junk skipped", ids)
	}
}

// TestMultilineMessageSurvivesTheRoundTrip: the prompt is stored as a quoted
// frontmatter value, so newlines must not break the parser.
func TestMultilineMessageSurvivesTheRoundTrip(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	msg := "first line\nsecond: with a colon\n\tand a tab — plus zażółć gęślą"
	if err := store.Write(&Handoff{SessionID: "eeee5555", Message: msg}); err != nil {
		t.Fatal(err)
	}
	path, _ := store.Path("eeee5555")
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Message != msg {
		t.Errorf("Message = %q, want %q", got.Message, msg)
	}
}

func TestListOnMissingDirectory(t *testing.T) {
	store := &Store{Dir: filepath.Join(t.TempDir(), "never-created")}
	list, err := store.List()
	if err != nil || list != nil {
		t.Errorf("List() = %v, %v — a fresh install must not look broken", list, err)
	}
}

func TestNewestPerGroupsByChosenKey(t *testing.T) {
	repo := "/Users/sebastian/Projects/mindskein"
	list := []*Handoff{
		{SessionID: "s3", Title: "writer", Repo: repo, EndedAt: at(t, "2026-08-18T18:00:00Z")},
		{SessionID: "s2", Title: "capture", Repo: repo, EndedAt: at(t, "2026-08-18T13:00:00Z")},
		{SessionID: "s1", Title: "Content", CWD: "/vault/62. Business", EndedAt: at(t, "2026-08-18T09:00:00Z")},
	}

	byLoc := NewestPer(list, ByLocation)
	if len(byLoc) != 2 || byLoc[0].SessionID != "s3" {
		t.Errorf("ByLocation = %d entries starting %q, want 2 starting s3", len(byLoc), byLoc[0].SessionID)
	}
	if bySess := NewestPer(list, BySession); len(bySess) != 3 {
		t.Errorf("BySession = %d entries, want all 3 kept apart", len(bySess))
	}
}

func TestRecordWithoutTranscriptStillWrites(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	sess := &session.Session{
		ID:          "cccc3333",
		ProjectPath: "/Users/sebastian/Projects/mindskein",
		Status:      session.StatusDone,
		StartedAt:   at(t, "2026-08-18T12:00:00Z"),
		LastEvent:   "Edit",
	}
	h, err := Record(store, sess, "/nonexistent/transcript.jsonl", "", at(t, "2026-08-18T12:45:00Z"))
	if err != nil {
		t.Fatalf("Record with a missing transcript = %v, want a handoff anyway", err)
	}
	if h.Duration() != 45*time.Minute {
		t.Errorf("Duration = %v, want 45m from the session record", h.Duration())
	}
	// The session record cannot supply the tool: by the time this runs, the Stop
	// handler has already overwritten LastEvent with its own event name.
	if h.LastTool != "" {
		t.Errorf("LastTool = %q, want empty rather than a hook event name", h.LastTool)
	}
	if !strings.Contains(h.Markdown(), "**Last tool:** —") {
		t.Error("an unknown tool should render as a dash")
	}
	if h.Label() != "mindskein" {
		t.Errorf("Label() = %q, want the folder name when there is no title", h.Label())
	}
}

func TestRecordRejectsUnsafeSessionID(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	sess := &session.Session{ID: "../../escape", Status: session.StatusDone}
	if _, err := Record(store, sess, "", "", time.Now()); err == nil {
		t.Error("Record with a traversal id = nil, want an error")
	}
}
