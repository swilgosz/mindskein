package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/swilgosz/mindskein/internal/handoff"
	"github.com/swilgosz/mindskein/internal/session"
)

// statusJSON is `mindskein status --json` as a caller reads it: the raw bytes
// and every key that is really there, so a missing key cannot pass for a zero
// value.
func statusJSON(t *testing.T, args ...string) (string, map[string]any) {
	t.Helper()
	out, _ := status(t, append([]string{"--json"}, args...)...)
	return out, decodeOne(t, out)
}

// decodeOne fails unless out is exactly one JSON object.
func decodeOne(t *testing.T, out string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(out))
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("stdout holds more than one JSON value: %v\n%s", err, out)
	}
	return v
}

func jsonRows(t *testing.T, v map[string]any) []map[string]any {
	t.Helper()
	raw, ok := v["rows"].([]any)
	if !ok {
		t.Fatalf("rows = %#v, want an array", v["rows"])
	}
	rows := make([]map[string]any, len(raw))
	for i, r := range raw {
		rows[i] = r.(map[string]any)
	}
	return rows
}

// stageSession puts a record straight into the registry, quiet for silent,
// with the folder and status the scenario is about.
func stageSession(t *testing.T, home string, s session.Session, silent time.Duration) {
	t.Helper()
	dir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s.Agent = session.AgentClaudeCode
	s.StartedAt = time.Now().UTC().Add(-silent)
	s.LastEventAt = s.StartedAt
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, s.ID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStatusJSON(t *testing.T) {
	t.Run("prints the sessions as one object in the agreed shape", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		const folder = "/Users/seb/.engineer-of-wealth/a-folder-longer-than-twenty-two"
		const title = "R&D <fast> on a title much longer than thirty characters"
		stageSession(t, home, session.Session{
			ID: "aaaa1111-2222-3333-4444-555566667777", ProjectPath: folder,
			Status: session.StatusWaiting, LastEvent: "Stop",
		}, 16*time.Minute)
		writeHandoff(t, &handoff.Handoff{SessionID: "aaaa1111-2222-3333-4444-555566667777", Title: title, CWD: folder})

		out, v := statusJSON(t)

		if keys := sortedKeys(v); !slices.Equal(keys, []string{"interrupted", "rows", "running"}) {
			t.Errorf("top-level keys = %v", keys)
		}
		rows := jsonRows(t, v)
		if len(rows) != 1 {
			t.Fatalf("rows = %d, want 1", len(rows))
		}
		row := rows[0]
		stamp, err := time.Parse(time.RFC3339, row["last_event_at"].(string))
		if err != nil {
			t.Errorf("last_event_at = %v: %v", row["last_event_at"], err)
		}
		if d := time.Since(stamp); d < 16*time.Minute || d > 17*time.Minute {
			t.Errorf("last_event_at is %s ago, want about 16m", d)
		}
		delete(row, "last_event_at")
		want := map[string]any{
			"id":           "aaaa1111-2222-3333-4444-555566667777",
			"short_id":     "aaaa1111",
			"label":        title,
			"project":      "a-folder-longer-than-twenty-two",
			"project_path": folder,
			"state":        "waiting",
			"status":       "waiting",
			"age":          "16m",
			"event":        "Stop",
		}
		if !reflect.DeepEqual(row, want) {
			t.Errorf("row = %#v\nwant  %#v", row, want)
		}
		if v["running"] != 0.0 || v["interrupted"] != 0.0 {
			t.Errorf("running = %v, interrupted = %v, want 0 and 0", v["running"], v["interrupted"])
		}
		if !strings.HasPrefix(out, "{\n  \"rows\": [\n    {\n      \"id\":") {
			t.Errorf("want two-space indentation:\n%s", out)
		}
		if !strings.Contains(out, "R&D <fast>") {
			t.Errorf("HTML must not be escaped:\n%s", out)
		}
	})

	t.Run("counts running and interrupted as the text summary does", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		stageSession(t, home, session.Session{ID: "aaaa1111", ProjectPath: "/p/a", Status: session.StatusRunning}, time.Minute)

		_, v := statusJSON(t)
		if v["running"] != 1.0 {
			t.Errorf("running = %v, want 1", v["running"])
		}
	})

	t.Run("--all and --hide-after select the same sessions as the text", func(t *testing.T) {
		live, quiet, ancient, done := "aaaa1111", "bbbb2222", "cccc3333", "dddd4444"
		everyone := []string{live, quiet, ancient, done}

		tests := []struct {
			name   string
			config string
			args   []string
			want   []string
		}{
			{"defaults", "", nil, []string{live, quiet}},
			{"--all", "", []string{"--all"}, everyone},
			{"--hide-after", "", []string{"--hide-after=2d"}, []string{live}},
			{"--hide-after=0 keeps the aged but not the ended", "", []string{"--hide-after=0"}, []string{live, quiet, ancient}},
			{"--all wins over --hide-after", "", []string{"--all", "--hide-after=2d"}, everyone},
			{"hide_after in the config", "[status]\nhide_after = \"2d\"\n", nil, []string{live}},
			{"--hide-after wins over the config", "[status]\nhide_after = \"2d\"\n", []string{"--hide-after=30d"}, []string{live, quiet, ancient}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("MINDSKEIN_HOME", home)
				const folder = "/Users/seb/Projects/mindskein"
				stageSession(t, home, session.Session{ID: live, ProjectPath: folder, Status: session.StatusWaiting}, time.Hour)
				stageSession(t, home, session.Session{ID: quiet, ProjectPath: folder, Status: session.StatusWaiting}, 3*24*time.Hour)
				stageSession(t, home, session.Session{ID: ancient, ProjectPath: folder, Status: session.StatusWaiting}, 8*24*time.Hour)
				stageSession(t, home, session.Session{ID: done, ProjectPath: folder, Status: session.StatusEnded, EndReason: "logout"}, time.Hour)
				if tt.config != "" {
					writeConfig(t, home, tt.config)
				}

				text, _ := status(t, tt.args...)
				_, v := statusJSON(t, tt.args...)

				var got []string
				for _, r := range jsonRows(t, v) {
					got = append(got, r["short_id"].(string))
				}
				slices.Sort(got)
				if !slices.Equal(got, tt.want) {
					t.Errorf("JSON rows = %v, want %v", got, tt.want)
				}
				for _, id := range everyone {
					if inText := strings.Contains(text, id); inText != slices.Contains(got, id) {
						t.Errorf("%s: in text = %v, in JSON rows %v\n%s", id, inText, got, text)
					}
				}
				hidden, _ := v["hidden"].(string)
				if hidden != "" && !strings.Contains(text, hidden+" hidden (--all)") {
					t.Errorf("JSON hidden = %q, text says otherwise:\n%s", hidden, text)
				}
				if hidden == "" && strings.Contains(text, "hidden (--all)") {
					t.Errorf("JSON hides nothing, text says otherwise:\n%s", text)
				}
			})
		}
	})

	t.Run("an empty registry is an empty array and the existing hint", func(t *testing.T) {
		t.Setenv("MINDSKEIN_HOME", t.TempDir())

		out, v := statusJSON(t)

		if !strings.Contains(out, `"rows": []`) {
			t.Errorf(`want "rows": []:`+"\n%s", out)
		}
		if hint, _ := v["hint"].(string); !strings.Contains(hint, "none recorded yet") {
			t.Errorf("hint = %q, want the text's hint", hint)
		}
	})

	t.Run("every session hidden is still an empty array", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		stageSession(t, home, session.Session{ID: "aaaa1111", ProjectPath: "/p/a", Status: session.StatusEnded}, time.Hour)

		out, v := statusJSON(t)

		if !strings.Contains(out, `"rows": []`) {
			t.Errorf(`want "rows": []:`+"\n%s", out)
		}
		if v["hidden"] != "1 ended" {
			t.Errorf("hidden = %v, want 1 ended", v["hidden"])
		}
		if hint, _ := v["hint"].(string); !strings.Contains(hint, "none open") {
			t.Errorf("hint = %q, want the none open hint", hint)
		}
	})

	t.Run("a folder name that became the label still has its project_path", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		stageSession(t, home, session.Session{ID: "aaaa1111", ProjectPath: "/Users/seb/Projects/mindskein", Status: session.StatusWaiting}, time.Minute)

		_, v := statusJSON(t)

		row := jsonRows(t, v)[0]
		if row["label"] != "mindskein" {
			t.Errorf("label = %v, want the folder name", row["label"])
		}
		if _, ok := row["project"]; ok {
			t.Errorf("project = %v, want it left out", row["project"])
		}
		if row["project_path"] != "/Users/seb/Projects/mindskein" {
			t.Errorf("project_path = %v, want the full folder", row["project_path"])
		}
	})

	t.Run("a record with no folder keeps the key, empty", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		stageSession(t, home, session.Session{ID: "aaaa1111", Status: session.StatusWaiting}, time.Minute)

		out, _ := statusJSON(t)

		if !strings.Contains(out, `"project_path": ""`) {
			t.Errorf(`want "project_path": "":`+"\n%s", out)
		}
	})

	t.Run("a broken config warns on stderr and leaves stdout parseable", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		writeConfig(t, home, "[status\nhide_after = \"2d\"\n")
		stageSession(t, home, session.Session{ID: "aaaa1111", ProjectPath: "/p/a", Status: session.StatusWaiting}, 3*24*time.Hour)

		out, errOut := status(t, "--json")

		if rows := jsonRows(t, decodeOne(t, out)); len(rows) != 1 {
			t.Errorf("rows = %d, want the session the default horizon keeps", len(rows))
		}
		if !strings.Contains(errOut, "config.toml") || !strings.Contains(errOut, "using defaults") {
			t.Errorf("want the broken config named on stderr, got %q", errOut)
		}
	})

	t.Run("an unreadable store fails with nothing on stdout", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		if err := os.WriteFile(filepath.Join(home, "sessions"), nil, 0o600); err != nil {
			t.Fatal(err)
		}

		var stdout bytes.Buffer
		if err := run([]string{"status", "--json"}, nil, &stdout, io.Discard); err == nil {
			t.Error("run(status --json) = nil, want the store's error")
		}
		if stdout.Len() != 0 {
			t.Errorf("stdout = %q, want nothing", stdout.String())
		}
	})

	t.Run("an unknown flag fails with nothing on stdout", func(t *testing.T) {
		t.Setenv("MINDSKEIN_HOME", t.TempDir())

		var stdout, stderr bytes.Buffer
		if err := run([]string{"status", "--json", "--nope"}, nil, &stdout, &stderr); err == nil {
			t.Error("run(status --json --nope) = nil, want an error")
		}
		if stdout.Len() != 0 {
			t.Errorf("stdout = %q, want nothing", stdout.String())
		}
		if !strings.Contains(stderr.String(), "-nope") {
			t.Errorf("stderr = %q, want the flag named", stderr.String())
		}
	})
}

// TestBriefJSONSessions pins what the brief inherits from printing the same
// view, which the dashboard reads.
func TestBriefJSONSessions(t *testing.T) {
	type sessions struct {
		Rows  json.RawMessage `json:"rows"`
		Error string          `json:"error"`
	}
	brief := func(t *testing.T, args ...string) sessions {
		t.Helper()
		var b struct {
			Sessions sessions `json:"sessions"`
		}
		if err := json.Unmarshal([]byte(runBrief(t, append([]string{"--json"}, args...)...)), &b); err != nil {
			t.Fatal(err)
		}
		return b.Sessions
	}

	t.Run("rows carry project_path", func(t *testing.T) {
		briefHome(t)

		got := brief(t)

		if !strings.Contains(string(got.Rows), `"project_path": "/Users/seb/Projects/mindskein"`) {
			t.Errorf("rows = %s, want the full folder in project_path", got.Rows)
		}
	})

	t.Run("an empty registry is an empty array", func(t *testing.T) {
		t.Setenv("MINDSKEIN_HOME", t.TempDir())
		noGitHub(t)

		if got := brief(t); string(got.Rows) != "[]" {
			t.Errorf("rows = %s, want []", got.Rows)
		}
	})

	t.Run("an unreadable store is an error beside an empty array", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		noGitHub(t)
		if err := os.WriteFile(filepath.Join(home, "sessions"), nil, 0o600); err != nil {
			t.Fatal(err)
		}

		got := brief(t)

		if got.Error == "" {
			t.Error("want the store's error in the block")
		}
		if string(got.Rows) != "[]" {
			t.Errorf("rows = %s, want []", got.Rows)
		}
	})
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
