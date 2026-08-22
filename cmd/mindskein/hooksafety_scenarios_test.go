package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swilgosz/mindskein/internal/hook"
)

// A hook is an observer. It must never be able to degrade the session it is
// observing, and the exit status is the whole interface it has for saying so:
// Claude Code reads a PreToolUse exit of 2 as "block this tool call". An
// unrecovered Go panic exits 2. So a nil error is not the property under test
// here — the process exit status is.
const panicChild = "MINDSKEIN_TEST_PANIC_CHILD"

// runSelf re-execs the test binary as a child running exactly one scenario,
// and reports the exit status the shell running the hook would have seen.
func runSelf(t *testing.T, testName string, env ...string) (code int, output string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$", "-test.v")
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(`{"session_id":"safety00","cwd":"/tmp","hook_event_name":"Stop"}`)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, buf.String()
	case errors.As(err, &exit):
		return exit.ExitCode(), buf.String()
	default:
		t.Fatalf("running child: %v\n%s", err, buf.String())
		return 0, ""
	}
}

func TestHookSurvivesAPanic(t *testing.T) {
	if os.Getenv(panicChild) == "1" {
		hookRunner = func(hook.Event, io.Reader) error { panic("deliberate panic from a test") }
		if err := run([]string{"hook", "stop"}, os.Stdin, os.Stdout, os.Stderr); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	t.Run("a panicking hook exits 0", func(t *testing.T) {
		code, out := runSelf(t, "TestHookSurvivesAPanic", panicChild+"=1", "MINDSKEIN_HOME="+t.TempDir())
		if code == 2 {
			t.Fatalf("exit 2 blocks the tool call the hook was only meant to observe:\n%s", out)
		}
		if code != 0 {
			t.Fatalf("exit code = %d, want 0:\n%s", code, out)
		}
	})

	t.Run("the panic is written to the hook log", func(t *testing.T) {
		home := t.TempDir()
		code, out := runSelf(t, "TestHookSurvivesAPanic", panicChild+"=1", "MINDSKEIN_HOME="+home)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0:\n%s", code, out)
		}
		log, err := os.ReadFile(filepath.Join(home, "hooks.log"))
		if err != nil {
			t.Fatalf("reading hooks.log: %v", err)
		}
		for _, want := range []string{"panic", "deliberate panic from a test", "stop"} {
			if !strings.Contains(string(log), want) {
				t.Errorf("hooks.log missing %q, a silent crash is the one thing worse than a loud one:\n%s", want, log)
			}
		}
	})

	t.Run("the log names the line that panicked", func(t *testing.T) {
		home := t.TempDir()
		runSelf(t, "TestHookSurvivesAPanic", panicChild+"=1", "MINDSKEIN_HOME="+home)
		log, _ := os.ReadFile(filepath.Join(home, "hooks.log"))
		if !strings.Contains(string(log), "hooksafety_scenarios_test.go") {
			t.Errorf("without a stack the log says a crash happened but not where:\n%s", log)
		}
	})

	t.Run("the log line stays one line", func(t *testing.T) {
		home := t.TempDir()
		runSelf(t, "TestHookSurvivesAPanic", panicChild+"=1", "MINDSKEIN_HOME="+home)
		log, _ := os.ReadFile(filepath.Join(home, "hooks.log"))
		if n := strings.Count(strings.TrimRight(string(log), "\n"), "\n"); n != 0 {
			t.Errorf("a multi-line record makes the log unreadable by line; got %d extra lines:\n%s", n, log)
		}
	})
}

func TestHookSurvivesAnUnwritableStateDir(t *testing.T) {
	if os.Getenv(panicChild) == "2" {
		if err := run([]string{"hook", "stop"}, os.Stdin, os.Stdout, os.Stderr); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	blocked := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(blocked, 0o500); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	t.Run("exits 0 when it cannot write its own state", func(t *testing.T) {
		code, out := runSelf(t, "TestHookSurvivesAnUnwritableStateDir",
			panicChild+"=2", "MINDSKEIN_HOME="+filepath.Join(blocked, "state"))
		if code != 0 {
			t.Fatalf("exit code = %d, want 0:\n%s", code, out)
		}
	})

	t.Run("says why, since it cannot log to the dir it cannot write", func(t *testing.T) {
		_, out := runSelf(t, "TestHookSurvivesAnUnwritableStateDir",
			panicChild+"=2", "MINDSKEIN_HOME="+filepath.Join(blocked, "state"))
		if !strings.Contains(out, "mindskein") || !strings.Contains(strings.ToLower(out), "permission denied") {
			t.Errorf("a hook that fails silently forever is indistinguishable from one that works:\n%s", out)
		}
	})
}

// TestNoHookPathExitsTwo is the guarantee stated as a rule rather than as one
// example of it: whatever a hook is handed, the answer is never 2.
func TestNoHookPathExitsTwo(t *testing.T) {
	if os.Getenv(panicChild) == "3" {
		args := strings.Fields(os.Getenv("MINDSKEIN_TEST_ARGS"))
		if err := run(args, os.Stdin, os.Stdout, os.Stderr); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	for _, c := range []struct{ name, args string }{
		{"no event named", "hook"},
		{"an event that does not exist", "hook not-an-event"},
		{"a well formed event", "hook stop"},
		{"an event needing a transcript", "hook pre-tool-use"},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, out := runSelf(t, "TestNoHookPathExitsTwo",
				panicChild+"=3", "MINDSKEIN_TEST_ARGS="+c.args, "MINDSKEIN_HOME="+t.TempDir())
			if code == 2 {
				t.Errorf("%q exited 2, which blocks the tool call:\n%s", c.args, out)
			}
		})
	}
}
