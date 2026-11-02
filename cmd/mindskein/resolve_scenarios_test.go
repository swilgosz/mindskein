package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/swilgosz/mindskein/internal/config"
)

const (
	failingHook = `PostToolUse:Bash :: node "${CLAUDE_PLUGIN_ROOT}/hooks/fix.mjs"`
	otherHook   = "SessionStart:startup :: run-hook.cmd"
)

// failingSession records, through the Stop hook, one session an hour ago in
// which each hook failed once; hooks are given as "<name> :: <command>".
func failingSession(t *testing.T, id string, hooks ...string) {
	t.Helper()
	at := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	dir := filepath.Join(t.TempDir(), "projects", "-tmp")
	os.MkdirAll(dir, 0o700)
	body := `{"type":"assistant","timestamp":"` + at + `","sessionId":"` + id + `",` +
		`"message":{"id":"msg_1","model":"claude-sonnet-5","content":[],"usage":{"input_tokens":1,"output_tokens":2}}}` + "\n"
	for _, h := range hooks {
		name, command, _ := strings.Cut(h, " :: ")
		body += `{"type":"attachment","timestamp":"` + at + `","sessionId":"` + id + `","attachment":` +
			`{"type":"hook_non_blocking_error","hookName":"` + name + `","command":` + quoteJSON(command) + `}}` + "\n"
	}
	transcript := filepath.Join(dir, id+".jsonl")
	os.WriteFile(transcript, []byte(body), 0o600)
	if err := run([]string{"hook", "stop"}, strings.NewReader(stopPayload(t, id, transcript)), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func quoteJSON(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func mindskein(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(args, nil, &out, io.Discard)
	return out.String(), err
}

func findingOf(report map[string]any, id string) map[string]any {
	findings, _ := report["findings"].([]any)
	for _, f := range findings {
		if m := f.(map[string]any); m["id"] == id {
			return m
		}
	}
	return nil
}

func TestResolve(t *testing.T) {
	t.Run("resolve with an unknown id, or an item not in the current report, is an error listing the valid ids, and writes nothing", func(t *testing.T) {
		home := pruneHome(t)
		failingSession(t, "sess0001", failingHook)
		file := filepath.Join(home, "resolved.json")

		for _, ref := range []string{"hook-failure", "hook-failures:PostToolUse:Bash", "unused-skills:worktree"} {
			_, err := mindskein(t, "resolve", ref)
			if err == nil || !strings.Contains(err.Error(), "valid ids: hook-failures") {
				t.Errorf("resolve %s = %v, want an error listing the valid ids", ref, err)
			}
			if _, err := os.Stat(file); !os.IsNotExist(err) {
				t.Fatalf("resolve %s wrote %s", ref, file)
			}
		}

		if _, err := mindskein(t, "resolve", "hook-failures:"+failingHook); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, ref := range []string{"nope", "big-results:Read"} {
			if _, err := mindskein(t, "resolve", ref); err == nil {
				t.Errorf("resolve %s succeeded", ref)
			}
		}
		if after, _ := os.ReadFile(file); !bytes.Equal(before, after) {
			t.Errorf("a refused resolve rewrote the file:\n%s\nthen\n%s", before, after)
		}
		if _, err := os.Stat(filepath.Join(home, "config.toml")); !os.IsNotExist(err) {
			t.Error("resolve wrote config.toml")
		}
	})

	t.Run("resolve --list prints each entry with its time and note; unresolve removes one and the finding returns on the next report", func(t *testing.T) {
		home := pruneHome(t)
		failingSession(t, "sess0001", failingHook)
		ref := "hook-failures:" + failingHook
		if _, err := mindskein(t, "resolve", ref, "--note", "removed the vercel plugin"); err != nil {
			t.Fatal(err)
		}
		list, err := mindskein(t, "resolve", "--list")
		if err != nil {
			t.Fatal(err)
		}
		today := time.Now().Format(time.DateOnly)
		if !strings.Contains(list, ref) || !strings.Contains(list, "resolved "+today) || !strings.Contains(list, "removed the vercel plugin") {
			t.Errorf("--list = %q, want the entry, its time and its note", list)
		}
		if f := findingOf(usageJSON(t), "hook-failures"); f != nil {
			t.Errorf("resolved finding still reported: %v", f)
		}

		if _, err := mindskein(t, "unresolve", ref); err != nil {
			t.Fatal(err)
		}
		if f := findingOf(usageJSON(t), "hook-failures"); f == nil || f["title"] != "1 hook failures" {
			t.Errorf("after unresolve the finding = %v, want it back", f)
		}
		if list, _ := mindskein(t, "resolve", "--list"); !strings.Contains(list, "nothing resolved") {
			t.Errorf("--list after unresolve = %q", list)
		}

		before, _ := os.ReadFile(filepath.Join(home, "resolved.json"))
		if _, err := mindskein(t, "unresolve", ref); err == nil || !strings.Contains(err.Error(), ref+" is not resolved") {
			t.Errorf("unresolve of what is not resolved = %v, want an error naming it", err)
		}
		if after, _ := os.ReadFile(filepath.Join(home, "resolved.json")); !bytes.Equal(before, after) {
			t.Error("a refused unresolve rewrote the file")
		}
	})

	t.Run("the footer counts what is hidden; --include-resolved prints the report as if nothing were resolved", func(t *testing.T) {
		pruneHome(t)
		failingSession(t, "sess0001", failingHook)
		if _, err := mindskein(t, "resolve", "hook-failures"); err != nil {
			t.Fatal(err)
		}
		out, err := mindskein(t, "usage")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "1 resolved · mindskein resolve --list") || strings.Contains(out, "[hook-failures]") {
			t.Errorf("usage = \n%s\nwant the finding hidden and the footer counting it", out)
		}
		all, err := mindskein(t, "usage", "--include-resolved")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(all, "1 hook failures  [hook-failures]") || strings.Contains(all, "resolved ·") {
			t.Errorf("usage --include-resolved = \n%s\nwant the finding and no footer", all)
		}
		if r := usageJSON(t, "--include-resolved"); findingOf(r, "hook-failures") == nil || len(r["resolved"].([]any)) != 0 {
			t.Errorf("usage --json --include-resolved = %v, want the finding and nothing resolved", r)
		}

		var served map[string]any
		apiGet(t, serveHandler(config.Defaults()), "/api/usage", &served)
		if entries, _ := served["resolved"].([]any); len(entries) != 1 || entries[0].(map[string]any)["hidden"] != float64(1) {
			t.Errorf("/api/usage resolved = %v, want the resolution and what it hides for the dashboard", served["resolved"])
		}
	})

	t.Run("--json keeps evidence and adds resolved beside id and items", func(t *testing.T) {
		pruneHome(t)
		failingSession(t, "sess0001", failingHook, otherHook)
		if _, err := mindskein(t, "resolve", "hook-failures:"+failingHook, "--note", "fixed"); err != nil {
			t.Fatal(err)
		}
		report := usageJSON(t)
		f := findingOf(report, "hook-failures")
		if f == nil {
			t.Fatalf("the unresolved hook's finding is gone: %v", report["findings"])
		}
		evidence, _ := f["evidence"].([]any)
		items, _ := f["items"].([]any)
		if len(evidence) != 1 || evidence[0] != "1× "+otherHook || len(items) != 1 || items[0].(map[string]any)["key"] != otherHook {
			t.Errorf("finding = %v, want evidence and items naming only the unresolved hook", f)
		}
		entries, _ := report["resolved"].([]any)
		if len(entries) != 1 {
			t.Fatalf("resolved = %v, want one entry", report["resolved"])
		}
		e := entries[0].(map[string]any)
		if e["id"] != "hook-failures" || e["key"] != failingHook || e["note"] != "fixed" || e["hidden"] != float64(1) {
			t.Errorf("resolved entry = %v, want id, key, note and the one failure it hides", e)
		}
		if at, err := time.Parse(time.RFC3339, e["resolved_at"].(string)); err != nil || time.Since(at) > time.Minute {
			t.Errorf("resolved_at = %v, want the time of the resolve", e["resolved_at"])
		}
	})

	t.Run("prune leaves resolved.json untouched, and a missing or empty resolved.json means nothing is resolved", func(t *testing.T) {
		home := pruneHome(t)
		failingSession(t, "sess0001", failingHook)
		file := filepath.Join(home, "resolved.json")
		if f := findingOf(usageJSON(t), "hook-failures"); f == nil {
			t.Fatal("with no resolved.json the finding is hidden")
		}
		for _, empty := range []string{"", "\n  \n"} {
			os.WriteFile(file, []byte(empty), 0o600)
			if f := findingOf(usageJSON(t), "hook-failures"); f == nil {
				t.Errorf("resolved.json %q hides the finding", empty)
			}
			if list, err := mindskein(t, "resolve", "--list"); err != nil || !strings.Contains(list, "nothing resolved") {
				t.Errorf("--list over %q = %q, %v", empty, list, err)
			}
		}

		if _, err := mindskein(t, "resolve", "hook-failures", "--note", "keep me"); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(file)
		old := time.Now().Add(-400 * 24 * time.Hour)
		os.Chtimes(file, old, old)
		if _, err := mindskein(t, "prune"); err != nil {
			t.Fatal(err)
		}
		if after, err := os.ReadFile(file); err != nil || !bytes.Equal(before, after) {
			t.Errorf("prune changed resolved.json: %v\n%s", err, after)
		}
	})

	t.Run("a corrupt resolved.json is an error for every command and is never overwritten; bad arguments say what is wanted", func(t *testing.T) {
		home := pruneHome(t)
		failingSession(t, "sess0001", failingHook)
		file := filepath.Join(home, "resolved.json")
		os.WriteFile(file, []byte("{not json"), 0o600)
		for _, args := range [][]string{{"usage"}, {"resolve", "hook-failures"}, {"resolve", "--list"}, {"unresolve", "hook-failures"}} {
			if _, err := mindskein(t, args...); err == nil || !strings.Contains(err.Error(), "resolved.json") {
				t.Errorf("%v over a corrupt file = %v, want an error naming it", args, err)
			}
		}
		if data, _ := os.ReadFile(file); string(data) != "{not json" {
			t.Errorf("the corrupt file was rewritten: %q", data)
		}
		os.Remove(file)

		for _, args := range [][]string{{"resolve"}, {"resolve", "a", "b"}, {"resolve", "--list", "a"}, {"resolve", ":x"},
			{"resolve", "--days", "-1", "hook-failures"}, {"unresolve"}, {"unresolve", ":x"}, {"resolve", "--bogus"}, {"unresolve", "--bogus"}} {
			if _, err := mindskein(t, args...); err == nil {
				t.Errorf("%v succeeded", args)
			}
		}
		if _, err := mindskein(t, "resolve", "--note", "after --", "--", "hook-failures"); err != nil {
			t.Errorf("an id after -- = %v", err)
		}
		for _, args := range [][]string{{"resolve", "-h"}, {"unresolve", "-h"}} {
			if _, err := mindskein(t, args...); err != nil {
				t.Errorf("%v = %v, want help without an error", args, err)
			}
		}
	})
}
