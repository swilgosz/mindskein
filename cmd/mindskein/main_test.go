package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swilgosz/mindskein/internal/session"
)

func TestRunNoArgsPrintsUsageToStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(nil, nil, &stdout, &stderr); err != nil {
		t.Fatalf("run() = %v, want nil", err)
	}
	for _, want := range []string{"Usage:", "brief", "status", "priorities", "hook"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("usage output missing %q\ngot:\n%s", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunVersion(t *testing.T) {
	var stdout bytes.Buffer
	if err := run([]string{"version"}, nil, &stdout, io.Discard); err != nil {
		t.Fatalf("run(version) = %v, want nil", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != version {
		t.Errorf("stdout = %q, want %q", got, version)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	err := run([]string{"nope"}, nil, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), `unknown command "nope"`) {
		t.Errorf("run(nope) = %v, want unknown command error", err)
	}
}

func TestRunDispatchesToUnimplementedCommands(t *testing.T) {
	for _, args := range [][]string{{"brief"}, {"priorities"}} {
		err := run(args, nil, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "not implemented yet") {
			t.Errorf("run(%v) = %v, want not-implemented error", args, err)
		}
	}
}

func TestRunHookRejectsBadEvent(t *testing.T) {
	for _, args := range [][]string{{"hook"}, {"hook", "post-tool-use"}} {
		err := run(args, nil, io.Discard, io.Discard)
		if err == nil || strings.Contains(err.Error(), "not implemented yet") {
			t.Errorf("run(%v) = %v, want validation error", args, err)
		}
	}
}

// TestRunStatusReadsTheRegistry checks the command wiring; the rendering
// itself is covered in internal/session.
func TestRunStatusReadsTheRegistry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDSKEIN_HOME", home)

	payload := `{"session_id":"aaaa1111","cwd":"/Users/seb/Projects/mindskein","tool_name":"Edit"}`
	if err := run([]string{"hook", "pre-tool-use"}, strings.NewReader(payload), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := run([]string{"status"}, nil, &stdout, io.Discard); err != nil {
		t.Fatalf("run(status) = %v, want nil", err)
	}
	for _, want := range []string{"LIVE SESSIONS", "aaaa1111", "mindskein", "running", "(Edit)"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("status output missing %q\ngot:\n%s", want, stdout.String())
		}
	}
}

// TestRunStatusOnEmptyRegistry: a fresh install must not look broken.
func TestRunStatusOnEmptyRegistry(t *testing.T) {
	t.Setenv("MINDSKEIN_HOME", t.TempDir())
	var stdout bytes.Buffer
	if err := run([]string{"status"}, nil, &stdout, io.Discard); err != nil {
		t.Fatalf("run(status) with no sessions = %v, want nil", err)
	}
	if !strings.Contains(stdout.String(), "none recorded yet") {
		t.Errorf("want a hint, got:\n%s", stdout.String())
	}
}

// TestRunHookWritesSession covers the CLI half of hook capture: a payload on
// stdin lands as a session file under MINDSKEIN_HOME.
func TestRunHookWritesSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDSKEIN_HOME", home)

	payload := `{"session_id":"e0cc146a","cwd":"/Users/seb/Projects/mindskein",` +
		`"hook_event_name":"PreToolUse","tool_name":"Edit"}`
	if err := run([]string{"hook", "pre-tool-use"}, strings.NewReader(payload), io.Discard, io.Discard); err != nil {
		t.Fatalf("run(hook pre-tool-use) = %v, want nil", err)
	}

	data, err := os.ReadFile(filepath.Join(home, "sessions", "e0cc146a.json"))
	if err != nil {
		t.Fatalf("reading session file: %v", err)
	}
	var got session.Session
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("session file is not valid JSON: %v", err)
	}
	if got.Status != session.StatusRunning {
		t.Errorf("status = %q, want %q", got.Status, session.StatusRunning)
	}
	if got.LastEvent != "Edit" {
		t.Errorf("last_event = %q, want %q", got.LastEvent, "Edit")
	}
	if got.ProjectPath != "/Users/seb/Projects/mindskein" {
		t.Errorf("project_path = %q, want the payload cwd", got.ProjectPath)
	}
}

// TestRunHookSwallowsBadPayload guards the property that matters most in
// production: a hook must never fail the session it observes. A PreToolUse
// hook exiting non-zero can block the tool call outright.
func TestRunHookSwallowsBadPayload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDSKEIN_HOME", home)

	for name, stdin := range map[string]string{
		"malformed JSON":  `{"session_id":`,
		"empty stdin":     ``,
		"missing id":      `{"cwd":"/tmp"}`,
		"traversal in id": `{"session_id":"../../escape"}`,
		"unknown notif":   `{"session_id":"abc","notification_type":"auth_success"}`,
	} {
		t.Run(name, func(t *testing.T) {
			err := run([]string{"hook", "pre-tool-use"}, strings.NewReader(stdin), io.Discard, io.Discard)
			if err != nil {
				t.Errorf("run(hook) = %v, want nil — hooks must never fail the session", err)
			}
		})
	}

	// The escape attempt must not have written anything outside the store.
	if _, err := os.Stat(filepath.Join(filepath.Dir(home), "escape.json")); err == nil {
		t.Error("path traversal in session_id escaped the sessions directory")
	}
}

// writeTranscript drops a minimal but realistic transcript on disk.
func writeTranscript(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "transcript.jsonl")
	lines := []string{
		`{"type":"ai-title","aiTitle":"Work on the handoff writer"}`,
		`{"type":"user","timestamp":"2026-08-18T12:00:00Z","promptSource":"typed","message":{"role":"user","content":"wire up the writer"}}`,
		`{"type":"assistant","timestamp":"2026-08-18T12:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Write"}]}}`,
		`{"type":"user","timestamp":"2026-08-18T12:01:01Z","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`,
		`{"type":"custom-title","customTitle":"handoff writer"}`,
		`{"type":"ai-title","aiTitle":"Work on the handoff writer"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestStopHookWritesHandoff covers the point of the feature: finishing a turn leaves
// a readable handoff, without anyone opening the transcript.
func TestStopHookWritesHandoff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDSKEIN_HOME", home)
	transcript := writeTranscript(t, t.TempDir())

	payload := `{"session_id":"e0cc146a","cwd":"/Users/seb/Projects/mindskein",` +
		`"transcript_path":"` + transcript + `","hook_event_name":"Stop"}`
	if err := run([]string{"hook", "stop"}, strings.NewReader(payload), io.Discard, io.Discard); err != nil {
		t.Fatalf("run(hook stop) = %v, want nil", err)
	}

	data, err := os.ReadFile(filepath.Join(home, "handoffs", "e0cc146a.md"))
	if err != nil {
		t.Fatalf("reading handoff: %v", err)
	}
	body := string(data)
	for _, want := range []string{
		`title: "handoff writer"`, // the rename, not the generated title
		`last_tool: "Write"`,      // from the transcript, not the session record
		"# MindSkein Handoff — handoff writer",
		"## Next Action",
		"> wire up the writer",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("handoff missing %q\ngot:\n%s", want, body)
		}
	}
}

// TestPreToolUseWritesNoHandoff protects the cost guarantee: PreToolUse fires
// on every tool call and must never parse a transcript.
func TestPreToolUseWritesNoHandoff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDSKEIN_HOME", home)
	transcript := writeTranscript(t, t.TempDir())

	payload := `{"session_id":"e0cc146a","cwd":"/tmp","transcript_path":"` + transcript + `","tool_name":"Edit"}`
	if err := run([]string{"hook", "pre-tool-use"}, strings.NewReader(payload), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "handoffs")); !os.IsNotExist(err) {
		t.Error("PreToolUse created a handoff directory — the transcript must not be read on every tool call")
	}
}

// TestStopHookSurvivesAMissingTranscript: the session record alone still
// answers where you were, and a hook must never fail the session.
func TestStopHookSurvivesAMissingTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDSKEIN_HOME", home)

	payload := `{"session_id":"dddd4444","cwd":"/tmp/x","transcript_path":"/nope/missing.jsonl"}`
	if err := run([]string{"hook", "stop"}, strings.NewReader(payload), io.Discard, io.Discard); err != nil {
		t.Fatalf("run(hook stop) = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(home, "handoffs", "dddd4444.md")); err != nil {
		t.Errorf("no handoff written despite a usable session record: %v", err)
	}
}

// TestSessionEndHookMarksEnded covers the one ending that is reported rather
// than inferred, and the reason that comes with it.
func TestSessionEndHookMarksEnded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDSKEIN_HOME", home)

	payload := `{"session_id":"ffff6666","cwd":"/tmp/x","hook_event_name":"SessionEnd",` +
		`"session_end_reason":"logout"}`
	if err := run([]string{"hook", "session-end"}, strings.NewReader(payload), io.Discard, io.Discard); err != nil {
		t.Fatalf("run(hook session-end) = %v, want nil", err)
	}

	data, err := os.ReadFile(filepath.Join(home, "sessions", "ffff6666.json"))
	if err != nil {
		t.Fatalf("reading session file: %v", err)
	}
	var got session.Session
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != session.StatusEnded {
		t.Errorf("status = %q, want %q", got.Status, session.StatusEnded)
	}
	if got.EndReason != "logout" {
		t.Errorf("end_reason = %q, want %q", got.EndReason, "logout")
	}
}

// TestEndedSessionSurvivesALateStop: hooks run in parallel, so a slower Stop
// can land after the end event and must not resurrect a finished session.
func TestEndedSessionSurvivesALateStop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDSKEIN_HOME", home)

	end := `{"session_id":"ffff6666","cwd":"/tmp/x","session_end_reason":"logout"}`
	stop := `{"session_id":"ffff6666","cwd":"/tmp/x"}`
	if err := run([]string{"hook", "session-end"}, strings.NewReader(end), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"hook", "stop"}, strings.NewReader(stop), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(home, "sessions", "ffff6666.json"))
	var got session.Session
	json.Unmarshal(data, &got)
	if got.Status != session.StatusEnded {
		t.Errorf("status = %q, want it to stay %q", got.Status, session.StatusEnded)
	}
}
