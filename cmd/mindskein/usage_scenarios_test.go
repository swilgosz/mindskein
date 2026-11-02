package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stopPayload(t *testing.T, id, transcript string) string {
	t.Helper()
	data, _ := json.Marshal(map[string]string{
		"session_id": id, "cwd": t.TempDir(), "hook_event_name": "Stop", "transcript_path": transcript,
	})
	return string(data)
}

func usageRecords(t *testing.T, home string) []string {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(home, "usage", "*.json"))
	return paths
}

func TestUsageCapture(t *testing.T) {
	t.Run("a Stop with no transcript_path, or an unreadable one, writes no usage record and still exits 0", func(t *testing.T) {
		for name, transcript := range map[string]string{"absent": "", "unreadable": "/nonexistent/projects/x/sess0001.jsonl"} {
			t.Run(name, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("MINDSKEIN_HOME", home)
				var stderr bytes.Buffer
				err := run([]string{"hook", "stop"}, strings.NewReader(stopPayload(t, "sess0001", transcript)), &bytes.Buffer{}, &stderr)
				if err != nil {
					t.Fatalf("hook returned %v; a hook never fails the session it observes", err)
				}
				if recs := usageRecords(t, home); len(recs) != 0 {
					t.Errorf("usage records = %v, want none", recs)
				}
			})
		}
	})

	t.Run("a Stop with a readable transcript writes the session's usage record", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		dir := filepath.Join(t.TempDir(), "projects", "-tmp")
		os.MkdirAll(dir, 0o700)
		transcript := filepath.Join(dir, "sess0001.jsonl")
		os.WriteFile(transcript, []byte(`{"type":"assistant","timestamp":"2026-10-15T10:00:00Z","sessionId":"sess0001",`+
			`"message":{"id":"msg_1","model":"claude-opus-5-5","content":[],"usage":{"input_tokens":1,"output_tokens":2}}}`+"\n"), 0o600)

		if err := run([]string{"hook", "stop"}, strings.NewReader(stopPayload(t, "sess0001", transcript)), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		recs := usageRecords(t, home)
		if len(recs) != 1 || filepath.Base(recs[0]) != "sess0001.json" {
			t.Fatalf("usage records = %v, want sess0001.json", recs)
		}

		var out bytes.Buffer
		if err := run([]string{"usage", "--json", "--days", "0"}, nil, &out, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		var report struct {
			Sessions int `json:"sessions"`
		}
		if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Sessions != 1 {
			t.Errorf("usage --json = %s (%v), want 1 session", out.String(), err)
		}
	})
}
