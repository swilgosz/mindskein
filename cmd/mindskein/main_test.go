package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
	for _, args := range [][]string{{"brief"}} {
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
		`"reason":"logout"}`
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

// TestSessionEndWithoutAReason pins the shape every real SessionEnd payload
// had while the field was misspelled: no reason at all. The record must not
// then disagree with itself.
func TestSessionEndWithoutAReason(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDSKEIN_HOME", home)

	payload := `{"session_id":"aaaa1111","cwd":"/tmp/x","hook_event_name":"SessionEnd"}`
	if err := run([]string{"hook", "session-end"}, strings.NewReader(payload), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "sessions", "aaaa1111.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got session.Session
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.EndReason != "other" || got.LastEvent != "other" {
		t.Errorf("end_reason = %q, last_event = %q — want both %q", got.EndReason, got.LastEvent, "other")
	}
}

// TestStatusShowsWhyASessionEnded is the end-to-end the misspelling defeated:
// a real payload must reach the rendered row.
func TestStatusShowsWhyASessionEnded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDSKEIN_HOME", home)

	payload := `{"session_id":"aaaa1111","cwd":"/tmp/x","reason":"prompt_input_exit"}`
	if err := run([]string{"hook", "session-end"}, strings.NewReader(payload), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	out, _ := status(t, "--all")
	if !strings.Contains(out, "prompt_input_exit") {
		t.Errorf("status --all must say why the session ended:\n%s", out)
	}
}

// TestStatusHelpIsNotAFailure: -h is advertised in the top-level usage.
func TestStatusHelpIsNotAFailure(t *testing.T) {
	t.Setenv("MINDSKEIN_HOME", t.TempDir())
	if err := run([]string{"status", "-h"}, nil, io.Discard, io.Discard); err != nil {
		t.Errorf("run(status -h) = %v, want nil", err)
	}
}

// TestStatusDoesNotPrintTheFolderTwice covers the join between the two stores:
// a handoff with no title must not fill the label column with the folder the
// next column already shows.
func TestStatusDoesNotPrintTheFolderTwice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDSKEIN_HOME", home)

	// No transcript, so the handoff gets no title — the path
	// TestStopHookSurvivesAMissingTranscript already exercises.
	payload := `{"session_id":"aaaa1111","cwd":"/tmp/somerepo","transcript_path":"/nope/missing.jsonl"}`
	if err := run([]string{"hook", "stop"}, strings.NewReader(payload), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}

	out, _ := status(t)
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "aaaa1111") {
			continue
		}
		if strings.Count(line, "somerepo") > 1 {
			t.Errorf("folder printed in both columns:\n%s", line)
		}
	}
}

// TestEndedSessionSurvivesALateStop: hooks run in parallel, so a slower Stop
// can land after the end event and must not resurrect a finished session.
func TestEndedSessionSurvivesALateStop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDSKEIN_HOME", home)

	end := `{"session_id":"ffff6666","cwd":"/tmp/x","reason":"logout"}`
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

// writeSession puts a record straight into the registry with a chosen age.
// Going through the hooks would stamp LastEventAt as now, which is exactly the
// field the retention horizon reads.
func writeSession(t *testing.T, home, id string, silent time.Duration) {
	t.Helper()
	dir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sess := session.Session{
		ID: id, Agent: session.AgentClaudeCode, ProjectPath: "/Users/seb/Projects/mindskein",
		Status: session.StatusWaiting, LastEvent: "idle_prompt",
		StartedAt: time.Now().UTC().Add(-silent), LastEventAt: time.Now().UTC().Add(-silent),
	}
	data, err := json.Marshal(sess)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeConfig(t *testing.T, home, body string) {
	t.Helper()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func status(t *testing.T, args ...string) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := run(append([]string{"status"}, args...), nil, &stdout, &stderr); err != nil {
		t.Fatalf("run(status) = %v, want nil", err)
	}
	return stdout.String(), stderr.String()
}

// TestStatusRetentionHorizon covers the CLI half: where the horizon comes from
// and what overrides it. The hiding rule itself is covered in internal/session.
func TestStatusRetentionHorizon(t *testing.T) {
	t.Run("hides a session past the default horizon", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		writeSession(t, home, "aaaa1111", 8*24*time.Hour)
		writeSession(t, home, "bbbb2222", time.Hour)

		out, _ := status(t)
		if strings.Contains(out, "aaaa1111") {
			t.Errorf("want the week-old session hidden by default:\n%s", out)
		}
		if !strings.Contains(out, "bbbb2222") {
			t.Errorf("want the fresh session shown:\n%s", out)
		}
	})

	t.Run("reads the horizon from config.toml", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		writeConfig(t, home, "[status]\nhide_after = \"2d\"\n")
		writeSession(t, home, "aaaa1111", 3*24*time.Hour)

		out, _ := status(t)
		if strings.Contains(out, "aaaa1111") {
			t.Errorf("a configured 2d horizon should hide a 3-day-old session:\n%s", out)
		}
		if !strings.Contains(out, "older than 2d") {
			t.Errorf("want the configured horizon named in the summary:\n%s", out)
		}
	})

	t.Run("lets the flag override the configured horizon", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		writeConfig(t, home, "[status]\nhide_after = \"2d\"\n")
		writeSession(t, home, "aaaa1111", 3*24*time.Hour)

		out, _ := status(t, "--hide-after=30d")
		if !strings.Contains(out, "aaaa1111") {
			t.Errorf("--hide-after must win over the config file:\n%s", out)
		}
	})

	t.Run("keeps every session when the horizon is zero", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		writeSession(t, home, "aaaa1111", 100*24*time.Hour)

		out, _ := status(t, "--hide-after=0")
		if !strings.Contains(out, "aaaa1111") {
			t.Errorf("a zero horizon must hide nothing:\n%s", out)
		}
	})

	t.Run("warns about a malformed config but still prints", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		writeConfig(t, home, "[status\nhide_after = \"2d\"\n")
		writeSession(t, home, "aaaa1111", time.Hour)

		out, errOut := status(t)
		if !strings.Contains(out, "aaaa1111") {
			t.Errorf("a broken config must not cost the listing:\n%s", out)
		}
		if !strings.Contains(errOut, "config.toml") {
			t.Errorf("want the broken config named on stderr, got %q", errOut)
		}
	})

	t.Run("rejects an unparseable horizon on the command line", func(t *testing.T) {
		t.Setenv("MINDSKEIN_HOME", t.TempDir())
		if err := run([]string{"status", "--hide-after=soon"}, nil, io.Discard, io.Discard); err == nil {
			t.Error("want a flag parse error rather than a silent fallback")
		}
	})
}

// TestRunPrioritiesReadsThePlan checks the command wiring: config to plan file
// to output. The parsing and rendering themselves are covered in
// internal/priorities.
func TestRunPrioritiesReadsThePlan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDSKEIN_HOME", home)
	vault := t.TempDir()

	plan := "## Priorities\n- [ ] !1 [[Wren Deploy Tool]] — ship the installer\n" +
		"- [ ] !3 Dig Deeper feature — later\n"
	if err := os.WriteFile(filepath.Join(vault, "plan.md"), []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("says where to configure a plan when none is set", func(t *testing.T) {
		var stdout bytes.Buffer
		if err := run([]string{"priorities"}, nil, &stdout, io.Discard); err != nil {
			t.Fatalf("run(priorities) = %v, want nil", err)
		}
		if !strings.Contains(stdout.String(), "config.toml") {
			t.Errorf("stdout = %q, want the file to edit", stdout.String())
		}
	})

	config := "[vault]\npath = " + strconv.Quote(vault) + "\nplan = \"plan.md\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("prints the priorities the configured plan declares", func(t *testing.T) {
		var stdout bytes.Buffer
		if err := run([]string{"priorities"}, nil, &stdout, io.Discard); err != nil {
			t.Fatalf("run(priorities) = %v, want nil", err)
		}
		if !strings.Contains(stdout.String(), "Wren Deploy Tool") {
			t.Errorf("stdout = %q, want the !1 item", stdout.String())
		}
		if strings.Contains(stdout.String(), "Dig Deeper") {
			t.Errorf("stdout = %q, want the backlog left out", stdout.String())
		}
	})

	t.Run("includes the backlog with --all", func(t *testing.T) {
		var stdout bytes.Buffer
		if err := run([]string{"priorities", "--all"}, nil, &stdout, io.Discard); err != nil {
			t.Fatalf("run(priorities --all) = %v, want nil", err)
		}
		if !strings.Contains(stdout.String(), "Dig Deeper") {
			t.Errorf("stdout = %q, want the backlog listed", stdout.String())
		}
	})

	t.Run("says so when the configured plan is not there", func(t *testing.T) {
		missing := "[vault]\npath = " + strconv.Quote(vault) + "\nplan = \"absent.md\"\n"
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(missing), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		if err := run([]string{"priorities"}, nil, &stdout, io.Discard); err != nil {
			t.Fatalf("run(priorities) = %v, want nil, not a stack trace", err)
		}
		if !strings.Contains(stdout.String(), "no plan at") {
			t.Errorf("stdout = %q, want the missing path named", stdout.String())
		}
	})
}
