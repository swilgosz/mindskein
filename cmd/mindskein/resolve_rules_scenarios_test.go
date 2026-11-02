package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/swilgosz/mindskein/internal/config"
	insight "github.com/swilgosz/mindskein/internal/usage"
)

// claudeRoot makes a Claude config directory with the given skills and a
// CLAUDE.md of tokens tokens, and points CLAUDE_CONFIG_DIR at it.
func claudeRoot(t *testing.T, tokens int, skills ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range skills {
		dir := filepath.Join(root, "skills", name)
		os.MkdirAll(dir, 0o700)
		os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: A skill\n---\n"), 0o600)
	}
	instructions(t, root, tokens)
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	return root
}

func instructions(t *testing.T, root string, tokens int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(strings.Repeat("x", tokens*4)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// commandSession records, through the Stop hook, a session of root at at
// that ran the given slash commands.
func commandSession(t *testing.T, root, id string, at time.Time, commands ...string) {
	t.Helper()
	ts := at.UTC().Format(time.RFC3339)
	body := `{"type":"assistant","timestamp":"` + ts + `","sessionId":"` + id + `",` +
		`"message":{"id":"msg_1","model":"claude-sonnet-5","content":[],"usage":{"input_tokens":1,"output_tokens":2}}}` + "\n"
	for _, c := range commands {
		body += `{"type":"user","timestamp":"` + ts + `","sessionId":"` + id + `",` +
			`"message":{"role":"user","content":"<command-name>/` + c + `</command-name>"}}` + "\n"
	}
	dir := filepath.Join(root, "projects", "-tmp")
	os.MkdirAll(dir, 0o700)
	transcript := filepath.Join(dir, id+".jsonl")
	os.WriteFile(transcript, []byte(body), 0o600)
	if err := run([]string{"hook", "stop"}, strings.NewReader(stopPayload(t, id, transcript)), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func resolvedFile(t *testing.T, home string) []insight.Resolution {
	t.Helper()
	all, err := (&insight.Resolved{Path: filepath.Join(home, "resolved.json")}).Load()
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func itemKeysOf(f map[string]any) []string {
	items, _ := f["items"].([]any)
	var keys []string
	for _, it := range items {
		keys = append(keys, it.(map[string]any)["key"].(string))
	}
	return keys
}

func TestResolveRecordsWhatItCovers(t *testing.T) {
	t.Run("resolve records each item a state finding lists, with the size of each file, and resolving again replaces the entry", func(t *testing.T) {
		home := pruneHome(t)
		root := claudeRoot(t, 5000, "alpha", "beta")
		commandSession(t, root, "sess0001", time.Now().Add(-time.Hour))

		heavy := itemKeysOf(findingOf(usageJSON(t), "heavy-instructions"))
		if len(heavy) != 1 {
			t.Fatalf("heavy instructions = %q, want CLAUDE.md", heavy)
		}
		for _, args := range [][]string{{"resolve", "heavy-instructions:" + heavy[0]}, {"resolve", "unused-skills"}} {
			if _, err := mindskein(t, args...); err != nil {
				t.Fatal(err)
			}
		}
		all := resolvedFile(t, home)
		if len(all) != 2 || all[0].Size != 5000 || len(all[0].Items) != 0 {
			t.Fatalf("entries = %+v, want CLAUDE.md resolved at its 5000 tokens", all)
		}
		if items := all[1].Items; len(items) != 2 || items[0].Key != "alpha" || items[1].Key != "beta" || items[0].Size != 0 {
			t.Errorf("whole unused-skills entry items = %+v, want alpha and beta without sizes", items)
		}

		instructions(t, root, 6000)
		if f := findingOf(usageJSON(t), "heavy-instructions"); f == nil {
			t.Fatal("CLAUDE.md grown 20% is still hidden")
		}
		if _, err := mindskein(t, "resolve", "heavy-instructions:"+heavy[0], "--note", "slimmed again"); err != nil {
			t.Fatal(err)
		}
		all = resolvedFile(t, home)
		if len(all) != 2 || all[1].Key != heavy[0] || all[1].Size != 6000 || all[1].Note != "slimmed again" {
			t.Fatalf("entries = %+v, want the CLAUDE.md entry replaced with its new size and note", all)
		}
		instructions(t, root, 6600)
		if f := findingOf(usageJSON(t), "heavy-instructions"); f != nil {
			t.Errorf("CLAUDE.md within 10%% of its new size is reported: %v", f)
		}

		os.MkdirAll(filepath.Join(root, "skills", "gamma"), 0o700)
		os.WriteFile(filepath.Join(root, "skills", "gamma", "SKILL.md"), []byte("---\nname: gamma\ndescription: New\n---\n"), 0o600)
		if keys := itemKeysOf(findingOf(usageJSON(t), "unused-skills")); len(keys) != 1 || keys[0] != "gamma" {
			t.Errorf("unused skills = %q, want only the one installed after the resolve", keys)
		}
	})

	t.Run("usage drops entries cleared by a use from resolved.json; serve only reads them", func(t *testing.T) {
		home := pruneHome(t)
		root := claudeRoot(t, 100, "alpha", "beta")
		commandSession(t, root, "sess0001", time.Now().Add(-time.Hour))
		if _, err := mindskein(t, "resolve", "unused-skills:alpha"); err != nil {
			t.Fatal(err)
		}
		if f := findingOf(usageJSON(t), "unused-skills"); strings.Join(itemKeysOf(f), ",") != "beta" {
			t.Fatalf("unused skills = %v, want alpha hidden", f)
		}

		commandSession(t, root, "sess0002", time.Now(), "alpha")
		file := filepath.Join(home, "resolved.json")
		before, _ := os.ReadFile(file)
		var served map[string]any
		apiGet(t, serveHandler(config.Defaults()), "/api/usage", &served)
		entries, _ := served["resolved"].([]any)
		if len(entries) != 1 || entries[0].(map[string]any)["cleared"] != true || entries[0].(map[string]any)["hidden"] != float64(0) {
			t.Errorf("/api/usage resolved = %v, want the entry cleared and hiding nothing", served["resolved"])
		}
		if after, _ := os.ReadFile(file); !bytes.Equal(before, after) {
			t.Errorf("serve rewrote resolved.json:\n%s", after)
		}

		report := usageJSON(t)
		if all := resolvedFile(t, home); len(all) != 0 {
			t.Errorf("after usage the file holds %+v, want the cleared entry dropped", all)
		}
		data, _ := json.Marshal(report["resolved"])
		if !strings.Contains(string(data), `"cleared":true`) {
			t.Errorf("the report that dropped it does not say it was cleared: %s", data)
		}
	})
}
