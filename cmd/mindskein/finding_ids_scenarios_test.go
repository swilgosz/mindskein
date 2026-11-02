package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindingIDs(t *testing.T) {
	t.Run("usage --json gives a failing hook's finding the id hook-failures, the same on the next run", func(t *testing.T) {
		t.Setenv("MINDSKEIN_HOME", t.TempDir())
		dir := filepath.Join(t.TempDir(), "projects", "-tmp")
		os.MkdirAll(dir, 0o700)
		transcript := filepath.Join(dir, "sess0001.jsonl")
		os.WriteFile(transcript, []byte(`{"type":"assistant","timestamp":"2026-10-15T10:00:00Z","sessionId":"sess0001",`+
			`"message":{"id":"msg_1","model":"claude-opus-5-5","content":[],"usage":{"input_tokens":1,"output_tokens":2}}}`+"\n"+
			`{"type":"attachment","timestamp":"2026-10-15T10:00:01.250Z","sessionId":"sess0001","attachment":`+
			`{"type":"hook_non_blocking_error","hookName":"PostToolUse:Bash","hookEvent":"PostToolUse","command":"node hook.mjs"}}`+"\n"), 0o600)
		if err := run([]string{"hook", "stop"}, strings.NewReader(stopPayload(t, "sess0001", transcript)), io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}

		hookFinding := func() map[string]any {
			findings, _ := usageJSON(t, "--days", "0")["findings"].([]any)
			for _, f := range findings {
				if m := f.(map[string]any); strings.HasSuffix(m["title"].(string), "hook failures") {
					return m
				}
			}
			t.Fatalf("no hook finding in %v", findings)
			return nil
		}
		first, next := hookFinding(), hookFinding()
		if first["id"] != "hook-failures" || next["id"] != first["id"] {
			t.Errorf("ids = %v then %v, want hook-failures both times", first["id"], next["id"])
		}
		items, _ := first["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["key"] != "PostToolUse:Bash :: node hook.mjs" {
			t.Errorf("items = %v, want the failing hook keyed by its hook string", items)
		}

		var out bytes.Buffer
		if err := run([]string{"usage", "--days", "0", "--all"}, nil, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "1 hook failures  [hook-failures]") ||
			!strings.Contains(out.String(), "[hook-failures:PostToolUse:Bash :: node hook.mjs]") {
			t.Errorf("terminal report lacks the id or the item's address:\n%s", out.String())
		}
	})
}
