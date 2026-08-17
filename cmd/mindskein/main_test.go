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
	for _, args := range [][]string{{"brief"}, {"status"}, {"priorities"}} {
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

// TestRunHookWritesSession is the CLI-level half of the U1 DoD: a payload on
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
