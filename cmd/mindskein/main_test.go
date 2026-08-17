package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestRunNoArgsPrintsUsageToStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(nil, &stdout, &stderr); err != nil {
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
	if err := run([]string{"version"}, &stdout, io.Discard); err != nil {
		t.Fatalf("run(version) = %v, want nil", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != version {
		t.Errorf("stdout = %q, want %q", got, version)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	err := run([]string{"nope"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), `unknown command "nope"`) {
		t.Errorf("run(nope) = %v, want unknown command error", err)
	}
}

func TestRunDispatchesToUnimplementedCommands(t *testing.T) {
	for _, args := range [][]string{
		{"brief"},
		{"status"},
		{"priorities"},
		{"hook", "pre-tool-use"},
		{"hook", "notification"},
		{"hook", "stop"},
	} {
		err := run(args, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "not implemented yet") {
			t.Errorf("run(%v) = %v, want not-implemented error", args, err)
		}
	}
}

func TestRunHookRejectsBadEvent(t *testing.T) {
	for _, args := range [][]string{{"hook"}, {"hook", "post-tool-use"}} {
		err := run(args, io.Discard, io.Discard)
		if err == nil || strings.Contains(err.Error(), "not implemented yet") {
			t.Errorf("run(%v) = %v, want validation error", args, err)
		}
	}
}
