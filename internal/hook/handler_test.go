package hook

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/swilgosz/mindskein/internal/session"
)

func testStore(t *testing.T) *session.Store {
	t.Helper()
	return &session.Store{Dir: filepath.Join(t.TempDir(), "sessions")}
}

func TestParseEvent(t *testing.T) {
	for _, name := range []string{"pre-tool-use", "notification", "stop"} {
		got, err := ParseEvent(name)
		if err != nil {
			t.Errorf("ParseEvent(%q) = %v, want nil", name, err)
		}
		if string(got) != name {
			t.Errorf("ParseEvent(%q) = %q", name, got)
		}
	}
	if _, err := ParseEvent("post-tool-use"); err == nil {
		t.Error("ParseEvent(post-tool-use) = nil, want an error")
	}
}

func TestParseRealPayload(t *testing.T) {
	// Shaped after the documented PreToolUse payload, tool_input included, to
	// confirm the extra fields are ignored rather than fatal.
	raw := `{
	  "session_id": "abc123",
	  "prompt_id": "550e8400-e29b-41d4-a716-446655440000",
	  "transcript_path": "/Users/seb/.claude/projects/x/transcript.jsonl",
	  "cwd": "/Users/seb/Projects/mindskein",
	  "permission_mode": "default",
	  "hook_event_name": "PreToolUse",
	  "tool_name": "Bash",
	  "tool_input": {"command": "go test ./...", "timeout": 120000},
	  "tool_use_id": "toolu_01ABC123"
	}`
	p, err := Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Parse() = %v, want nil", err)
	}
	if p.SessionID != "abc123" || p.ToolName != "Bash" || p.CWD != "/Users/seb/Projects/mindskein" {
		t.Errorf("Parse() = %+v, want the payload's scalars", p)
	}
}

func TestParseRejectsUnusable(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":           ``,
		"malformed":       `{"session_id":`,
		"no session id":   `{"cwd":"/tmp"}`,
		"wrong json type": `[]`,
	} {
		if _, err := Parse(strings.NewReader(raw)); err == nil {
			t.Errorf("Parse(%s) = nil, want an error", name)
		}
	}
}

func TestHandlePreToolUseMarksRunning(t *testing.T) {
	store := testStore(t)
	now := time.Date(2026, 8, 17, 21, 0, 0, 0, time.UTC)

	got, err := Handle(store, EventPreToolUse, &Payload{
		SessionID: "abc123",
		CWD:       "/Users/seb/Projects/mindskein",
		ToolName:  "Edit",
	}, now, 4242)
	if err != nil {
		t.Fatalf("Handle() = %v, want nil", err)
	}
	if got.Status != session.StatusRunning {
		t.Errorf("status = %q, want %q", got.Status, session.StatusRunning)
	}
	if got.LastEvent != "Edit" {
		t.Errorf("last_event = %q, want %q", got.LastEvent, "Edit")
	}
	if got.PID != 4242 {
		t.Errorf("pid = %d, want 4242", got.PID)
	}
}

func TestHandlePreToolUseWithoutToolName(t *testing.T) {
	store := testStore(t)
	got, err := Handle(store, EventPreToolUse, &Payload{SessionID: "abc123"}, time.Now().UTC(), 1)
	if err != nil {
		t.Fatalf("Handle() = %v, want nil", err)
	}
	if got.LastEvent != "tool" {
		t.Errorf("last_event = %q, want the %q fallback", got.LastEvent, "tool")
	}
}

func TestHandleNotification(t *testing.T) {
	waiting := []string{"idle_prompt", "permission_prompt", "agent_needs_input"}
	ignored := []string{"auth_success", "agent_completed", "elicitation_dialog", "elicitation_complete", ""}

	for _, kind := range waiting {
		store := testStore(t)
		got, err := Handle(store, EventNotification,
			&Payload{SessionID: "abc123", NotificationType: kind}, time.Now().UTC(), 1)
		if err != nil {
			t.Fatalf("Handle(%s) = %v, want nil", kind, err)
		}
		if got == nil {
			t.Fatalf("Handle(%s) wrote nothing, want a waiting session", kind)
		}
		if got.Status != session.StatusWaiting {
			t.Errorf("status for %s = %q, want %q", kind, got.Status, session.StatusWaiting)
		}
	}

	for _, kind := range ignored {
		store := testStore(t)
		got, err := Handle(store, EventNotification,
			&Payload{SessionID: "abc123", NotificationType: kind}, time.Now().UTC(), 1)
		if err != nil {
			t.Fatalf("Handle(%s) = %v, want nil", kind, err)
		}
		if got != nil {
			t.Errorf("Handle(%s) = %+v, want nil — it says nothing about waiting", kind, got)
		}
		// And it must not have created a file either.
		if list, _ := store.List(); len(list) != 0 {
			t.Errorf("Handle(%s) wrote %d session(s), want 0", kind, len(list))
		}
	}
}

func TestHandleStopMarksDone(t *testing.T) {
	store := testStore(t)
	got, err := Handle(store, EventStop, &Payload{SessionID: "abc123"}, time.Now().UTC(), 1)
	if err != nil {
		t.Fatalf("Handle() = %v, want nil", err)
	}
	if got.Status != session.StatusDone || got.LastEvent != "Stop" {
		t.Errorf("got %+v, want status done and last_event Stop", got)
	}
}

// TestHandleFullLifecycle walks the sequence a real session produces, ending
// on the Stop-then-idle_prompt pair that leaves a session resting at waiting.
func TestHandleFullLifecycle(t *testing.T) {
	store := testStore(t)
	start := time.Date(2026, 8, 17, 21, 0, 0, 0, time.UTC)
	p := &Payload{SessionID: "abc123", CWD: "/Users/seb/Projects/mindskein"}

	steps := []struct {
		event  Event
		notif  string
		tool   string
		offset time.Duration
		want   session.Status
	}{
		{event: EventPreToolUse, tool: "Read", offset: 0, want: session.StatusRunning},
		{event: EventNotification, notif: "permission_prompt", offset: time.Minute, want: session.StatusWaiting},
		{event: EventPreToolUse, tool: "Edit", offset: 2 * time.Minute, want: session.StatusRunning},
		{event: EventStop, offset: 3 * time.Minute, want: session.StatusDone},
		{event: EventNotification, notif: "idle_prompt", offset: 4 * time.Minute, want: session.StatusWaiting},
	}

	for i, step := range steps {
		p.ToolName, p.NotificationType = step.tool, step.notif
		got, err := Handle(store, step.event, p, start.Add(step.offset), 1)
		if err != nil {
			t.Fatalf("step %d (%s) = %v, want nil", i, step.event, err)
		}
		if got.Status != step.want {
			t.Errorf("step %d (%s): status = %q, want %q", i, step.event, got.Status, step.want)
		}
		if got.StartedAt != start {
			t.Errorf("step %d: started_at = %v, want the first event's time %v", i, got.StartedAt, start)
		}
	}
}

// TestHandleConcurrentSessionsStaySeparate covers two repositories at once.
func TestHandleConcurrentSessionsStaySeparate(t *testing.T) {
	store := testStore(t)
	now := time.Date(2026, 8, 17, 21, 0, 0, 0, time.UTC)

	if _, err := Handle(store, EventPreToolUse,
		&Payload{SessionID: "aaaa1111", CWD: "/Users/seb/Projects/mindskein", ToolName: "Edit"}, now, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := Handle(store, EventNotification,
		&Payload{SessionID: "bbbb2222", CWD: "/Users/seb/Projects/automateideasai.com", NotificationType: "idle_prompt"},
		now.Add(time.Minute), 2); err != nil {
		t.Fatal(err)
	}

	got, err := store.List()
	if err != nil {
		t.Fatalf("List() = %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("List() returned %d sessions, want 2", len(got))
	}
	// Most recent first.
	if got[0].ID != "bbbb2222" || got[0].Status != session.StatusWaiting {
		t.Errorf("first = %+v, want bbbb2222 waiting", got[0])
	}
	if got[1].ID != "aaaa1111" || got[1].Status != session.StatusRunning {
		t.Errorf("second = %+v, want aaaa1111 running", got[1])
	}
	if got[1].ProjectName() != "mindskein" {
		t.Errorf("project name = %q, want %q", got[1].ProjectName(), "mindskein")
	}
}

func TestHandleRejectsUnknownEvent(t *testing.T) {
	store := testStore(t)
	if _, err := Handle(store, Event("post-tool-use"), &Payload{SessionID: "abc"}, time.Now(), 1); err == nil {
		t.Error("Handle(post-tool-use) = nil, want an error")
	}
}

func TestHandleRejectsUnsafeSessionID(t *testing.T) {
	store := testStore(t)
	_, err := Handle(store, EventPreToolUse, &Payload{SessionID: "../../escape"}, time.Now(), 1)
	if err == nil {
		t.Error("Handle() with a traversing session_id = nil, want an error")
	}
}
