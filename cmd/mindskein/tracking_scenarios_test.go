package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// stopAt records one session whose only turn ran at ts, through the Stop hook,
// and returns its config root.
func stopAt(t *testing.T, id, ts string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "projects", "-tmp")
	os.MkdirAll(dir, 0o700)
	transcript := filepath.Join(dir, id+".jsonl")
	os.WriteFile(transcript, []byte(`{"type":"assistant","timestamp":"`+ts+`","sessionId":"`+id+`",`+
		`"message":{"id":"msg_1","model":"claude-opus-5-5","content":[],"usage":{"input_tokens":1,"output_tokens":2}}}`+"\n"), 0o600)
	if err := run([]string{"hook", "stop"}, strings.NewReader(stopPayload(t, id, transcript)), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(dir))
}

func usageJSON(t *testing.T, args ...string) map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := run(append([]string{"usage", "--json"}, args...), nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestUsageTracking(t *testing.T) {
	t.Run("a session whose Stop carried MINDSKEIN_PROJECT is reported under that project", func(t *testing.T) {
		t.Setenv("MINDSKEIN_HOME", t.TempDir())
		t.Setenv("MINDSKEIN_PROJECT", "eow-issue-7")
		stopAt(t, "sess0001", "2026-10-15T10:00:00Z")

		report := usageJSON(t, "--days", "0")
		ws, _ := report["workstreams"].([]any)
		if len(ws) != 1 || ws[0].(map[string]any)["name"] != "eow-issue-7" {
			t.Errorf("workstreams = %v, want eow-issue-7", report["workstreams"])
		}
	})

	t.Run("the terminal shows each costly subagent type's cost per run", func(t *testing.T) {
		t.Setenv("MINDSKEIN_HOME", t.TempDir())
		dir := filepath.Join(t.TempDir(), "projects", "-tmp")
		subDir := filepath.Join(dir, "sess0001", "subagents")
		os.MkdirAll(subDir, 0o700)
		turn := func(id string) string {
			return `{"type":"assistant","timestamp":"2026-10-15T10:00:00Z","sessionId":"sess0001",` +
				`"message":{"id":"` + id + `","model":"claude-opus-5-5","content":[],"usage":{"input_tokens":1,"output_tokens":20000}}}` + "\n"
		}
		transcript := filepath.Join(dir, "sess0001.jsonl")
		os.WriteFile(transcript, []byte(turn("msg_1")), 0o600)
		for _, a := range []string{"agent-a1", "agent-a2"} {
			os.WriteFile(filepath.Join(subDir, a+".jsonl"), []byte(turn("msg_"+a)), 0o600)
			os.WriteFile(filepath.Join(subDir, a+".meta.json"), []byte(`{"agentType":"Explore"}`), 0o600)
		}
		if err := run([]string{"hook", "stop"}, strings.NewReader(stopPayload(t, "sess0001", transcript)), io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer
		if err := run([]string{"usage", "--days", "0"}, nil, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		// 20K output tokens at Opus 5.5's $20 per million are $0.40 a run.
		if !strings.Contains(out.String(), "per run    Explore on opus-5-5 $0.40 × 2") {
			t.Errorf("usage printed:\n%s\nwant Explore's cost per run and its two runs", out.String())
		}
	})

	t.Run("usage --since A --until B counts only those days", func(t *testing.T) {
		t.Setenv("MINDSKEIN_HOME", t.TempDir())
		stopAt(t, "sess0001", "2026-10-04T10:00:00Z")
		stopAt(t, "sess0002", "2026-10-06T10:00:00Z")
		stopAt(t, "sess0003", "2026-10-09T10:00:00Z")

		report := usageJSON(t, "--since", "2026-10-05", "--until", "2026-10-08")
		if report["sessions"] != float64(1) || report["from"] != "2026-10-05" || report["until"] != "2026-10-08" {
			t.Errorf("sessions %v from %v until %v, want 1 session from 2026-10-05 until 2026-10-08",
				report["sessions"], report["from"], report["until"])
		}
	})

	t.Run("a bad --since or --until is an error naming the flag, not an empty report", func(t *testing.T) {
		t.Setenv("MINDSKEIN_HOME", t.TempDir())
		for flag, args := range map[string][]string{
			"--since": {"--since", "05.10.2026"},
			"--until": {"--until", "2026-02-30"},
		} {
			var out bytes.Buffer
			err := run(append([]string{"usage"}, args...), nil, &out, io.Discard)
			if err == nil || !strings.Contains(err.Error(), flag) || out.Len() != 0 {
				t.Errorf("usage %v = %v, printed %q; want an error naming %s and no report", args, err, out.String(), flag)
			}
		}
	})

	t.Run("/api/usage?since=A&until=B returns what usage --since A --until B --json prints", func(t *testing.T) {
		t.Setenv("MINDSKEIN_HOME", t.TempDir())
		stopAt(t, "sess0001", "2026-10-04T10:00:00Z")
		stopAt(t, "sess0002", "2026-10-06T10:00:00Z")

		want := usageJSON(t, "--since", "2026-10-05", "--until", "2026-10-08")
		var got map[string]any
		apiGet(t, serveHandler(defaultConfig(t)), "/api/usage?since=2026-10-05&until=2026-10-08", &got)
		delete(want, "generated_at")
		delete(got, "generated_at")
		if want["sessions"] != float64(1) || !reflect.DeepEqual(want, got) {
			t.Errorf("/api/usage differs from usage --json:\napi: %v\ncli: %v", got, want)
		}
	})
}
