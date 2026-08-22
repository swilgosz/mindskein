package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// settings.json is the file that controls the user's entire Claude Code
// setup. It is not ours. Every scenario here is ultimately about the same
// property: mindskein edits its own four entries and leaves the rest of the
// file exactly as it found it.

const withOtherHooks = `{
  "permissions": { "defaultMode": "auto" },
  "model": "opus",
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/Users/someone/.claude/hooks/reminder.sh",
            "timeout": 5,
            "statusMessage": "checking the roadmap"
          }
        ]
      }
    ]
  },
  "statusLine": { "type": "command", "command": "statusline.sh" }
}`

func write(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

func opts() Options { return Options{Binary: "/usr/local/bin/mindskein", Timeout: 5, Async: true} }

// commandsFor returns the command strings registered under one event.
func commandsFor(t *testing.T, path, event string) []string {
	t.Helper()
	var root struct {
		Hooks map[string][]struct {
			Hooks []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(read(t, path)), &root); err != nil {
		t.Fatalf("the file we wrote is not valid JSON: %v", err)
	}
	var out []string
	for _, g := range root.Hooks[event] {
		for _, e := range g.Hooks {
			cmd, _ := e["command"].(string)
			out = append(out, cmd)
		}
	}
	return out
}

func entryFor(t *testing.T, path, event, contains string) map[string]any {
	t.Helper()
	var root struct {
		Hooks map[string][]struct {
			Hooks []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(read(t, path)), &root); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for _, g := range root.Hooks[event] {
		for _, e := range g.Hooks {
			if cmd, _ := e["command"].(string); strings.Contains(cmd, contains) {
				return e
			}
		}
	}
	t.Fatalf("no %s entry containing %q", event, contains)
	return nil
}

func TestRegisterInstallsEveryHook(t *testing.T) {
	t.Run("registers all four events", func(t *testing.T) {
		path := write(t, "settings.json", `{}`)
		if _, err := Register(path, opts(), false); err != nil {
			t.Fatalf("Register: %v", err)
		}
		for _, event := range []string{"PreToolUse", "Notification", "Stop", "SessionEnd"} {
			got := commandsFor(t, path, event)
			if len(got) != 1 || !strings.Contains(got[0], "mindskein") {
				t.Errorf("%s = %v, want one mindskein entry", event, got)
			}
		}
	})

	t.Run("writes async and an explicit timeout", func(t *testing.T) {
		path := write(t, "settings.json", `{}`)
		if _, err := Register(path, opts(), false); err != nil {
			t.Fatalf("Register: %v", err)
		}
		e := entryFor(t, path, "PreToolUse", "mindskein")
		if e["async"] != true {
			// Without async the hook runs in the critical path of every
			// tool call. This is the state the author's own machine was in.
			t.Errorf("async = %v, want true", e["async"])
		}
		if e["timeout"] != float64(5) {
			t.Errorf("timeout = %v, want 5", e["timeout"])
		}
	})

	t.Run("registers the binary by absolute path", func(t *testing.T) {
		path := write(t, "settings.json", `{}`)
		if _, err := Register(path, opts(), false); err != nil {
			t.Fatalf("Register: %v", err)
		}
		e := entryFor(t, path, "Stop", "mindskein")
		cmd, _ := e["command"].(string)
		if !strings.HasPrefix(cmd, "/") {
			t.Errorf("command = %q; a hook runs with no PATH guarantee", cmd)
		}
		if !strings.HasSuffix(cmd, "hook stop") {
			t.Errorf("command = %q, want it to end with the event it handles", cmd)
		}
	})

	t.Run("gives the filtered events their matchers", func(t *testing.T) {
		path := write(t, "settings.json", `{}`)
		if _, err := Register(path, opts(), false); err != nil {
			t.Fatalf("Register: %v", err)
		}
		var root struct {
			Hooks map[string][]struct {
				Matcher string `json:"matcher"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal([]byte(read(t, path)), &root); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if m := root.Hooks["Notification"][0].Matcher; !strings.Contains(m, "idle_prompt") {
			t.Errorf("Notification matcher = %q; without it every notification type fires the hook", m)
		}
		if m := root.Hooks["PreToolUse"][0].Matcher; m != "" {
			t.Errorf("PreToolUse matcher = %q, want none", m)
		}
	})
}

func TestRegisterLeavesTheRestOfTheFileAlone(t *testing.T) {
	t.Run("keeps another tool's hook on the same event", func(t *testing.T) {
		path := write(t, "settings.json", withOtherHooks)
		if _, err := Register(path, opts(), false); err != nil {
			t.Fatalf("Register: %v", err)
		}
		got := commandsFor(t, path, "Stop")
		if len(got) != 2 {
			t.Fatalf("Stop = %v, want the existing hook and ours", got)
		}
		var kept bool
		for _, c := range got {
			if strings.Contains(c, "reminder.sh") {
				kept = true
			}
		}
		if !kept {
			t.Error("registering removed someone else's Stop hook")
		}
	})

	t.Run("keeps fields it does not understand on another tool's entry", func(t *testing.T) {
		path := write(t, "settings.json", withOtherHooks)
		if _, err := Register(path, opts(), false); err != nil {
			t.Fatalf("Register: %v", err)
		}
		if !strings.Contains(read(t, path), "statusMessage") {
			t.Error("a field we do not model was dropped from a hook that is not ours")
		}
	})

	t.Run("keeps unrelated top-level settings", func(t *testing.T) {
		path := write(t, "settings.json", withOtherHooks)
		if _, err := Register(path, opts(), false); err != nil {
			t.Fatalf("Register: %v", err)
		}
		for _, want := range []string{"permissions", "defaultMode", "statusLine", "statusline.sh", `"model"`} {
			if !strings.Contains(read(t, path), want) {
				t.Errorf("%s was lost", want)
			}
		}
	})

	t.Run("keeps the original key order", func(t *testing.T) {
		// Alphabetising a hand-maintained config turns a two-line edit into
		// a whole-file diff, and the user has to review it to trust us.
		path := write(t, "settings.json", withOtherHooks)
		if _, err := Register(path, opts(), false); err != nil {
			t.Fatalf("Register: %v", err)
		}
		out := read(t, path)
		perms, model := strings.Index(out, `"permissions"`), strings.Index(out, `"model"`)
		status := strings.Index(out, `"statusLine"`)
		if !(perms < model && model < status) {
			t.Errorf("keys were reordered:\n%s", out)
		}
	})
}

func TestRegisterIsIdempotent(t *testing.T) {
	t.Run("registering twice changes nothing the second time", func(t *testing.T) {
		path := write(t, "settings.json", withOtherHooks)
		if _, err := Register(path, opts(), false); err != nil {
			t.Fatalf("first: %v", err)
		}
		once := read(t, path)
		rep, err := Register(path, opts(), false)
		if err != nil {
			t.Fatalf("second: %v", err)
		}
		if got := read(t, path); got != once {
			t.Errorf("a second register rewrote the file:\n--- first ---\n%s\n--- second ---\n%s", once, got)
		}
		if len(rep.Changes) != 0 {
			t.Errorf("Changes = %v, want none on an already-registered setup", rep.Changes)
		}
	})

	t.Run("does not duplicate entries", func(t *testing.T) {
		path := write(t, "settings.json", `{}`)
		for i := 0; i < 3; i++ {
			if _, err := Register(path, opts(), false); err != nil {
				t.Fatalf("register %d: %v", i, err)
			}
		}
		if got := commandsFor(t, path, "Stop"); len(got) != 1 {
			t.Errorf("Stop = %v, want exactly one entry", got)
		}
	})
}

func TestRegisterRepairsAnIncompleteRegistration(t *testing.T) {
	// The state the author's own machine was in: registered, timeout set,
	// no async. A silent no-op here would leave it that way forever.
	const noAsync = `{
  "hooks": {
    "Stop": [
      { "hooks": [ { "type": "command", "command": "/old/path/mindskein hook stop", "timeout": 5 } ] }
    ]
  }
}`

	t.Run("adds the missing async", func(t *testing.T) {
		path := write(t, "settings.json", noAsync)
		if _, err := Register(path, opts(), false); err != nil {
			t.Fatalf("Register: %v", err)
		}
		if e := entryFor(t, path, "Stop", "mindskein"); e["async"] != true {
			t.Errorf("async = %v, want true", e["async"])
		}
	})

	t.Run("reports the repair rather than doing it silently", func(t *testing.T) {
		path := write(t, "settings.json", noAsync)
		rep, err := Register(path, opts(), false)
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if !strings.Contains(strings.ToLower(rep.String()), "async") {
			t.Errorf("the report does not mention what it fixed:\n%s", rep)
		}
	})

	t.Run("updates a stale binary path instead of adding a second entry", func(t *testing.T) {
		path := write(t, "settings.json", noAsync)
		if _, err := Register(path, opts(), false); err != nil {
			t.Fatalf("Register: %v", err)
		}
		got := commandsFor(t, path, "Stop")
		if len(got) != 1 {
			t.Fatalf("Stop = %v, want the old entry updated, not duplicated", got)
		}
		if strings.Contains(got[0], "/old/path") {
			t.Errorf("command = %q, want the current binary path", got[0])
		}
	})
}

func TestRegisterProtectsTheFile(t *testing.T) {
	t.Run("backs the file up before writing", func(t *testing.T) {
		path := write(t, "settings.json", withOtherHooks)
		rep, err := Register(path, opts(), false)
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if rep.Backup == "" {
			t.Fatal("no backup was taken of the file that controls the whole setup")
		}
		if got := read(t, rep.Backup); got != withOtherHooks {
			t.Error("the backup does not match what was there before")
		}
	})

	t.Run("refuses a file it cannot parse", func(t *testing.T) {
		// Overwriting here would destroy a config we could not read. The
		// only safe move is to stop and say so.
		const broken = `{ "hooks": { oops }`
		path := write(t, "settings.json", broken)
		if _, err := Register(path, opts(), false); err == nil {
			t.Fatal("Register succeeded on unparseable JSON")
		}
		if got := read(t, path); got != broken {
			t.Error("the unparseable file was overwritten anyway")
		}
	})

	t.Run("creates the file when there is none", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings.json")
		if _, err := Register(path, opts(), false); err != nil {
			t.Fatalf("Register: %v", err)
		}
		if got := commandsFor(t, path, "Stop"); len(got) != 1 {
			t.Errorf("Stop = %v, want one entry in a freshly created file", got)
		}
	})

	t.Run("a dry run writes nothing but reports everything", func(t *testing.T) {
		path := write(t, "settings.json", withOtherHooks)
		rep, err := Register(path, opts(), true)
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if got := read(t, path); got != withOtherHooks {
			t.Error("--dry-run modified the file")
		}
		if len(rep.Changes) == 0 {
			t.Error("--dry-run must still say what it would do")
		}
		if rep.Backup != "" {
			t.Error("--dry-run took a backup of a file it never touched")
		}
	})
}

func TestUnregisterRemovesOnlyOurEntries(t *testing.T) {
	t.Run("a full round trip restores the original", func(t *testing.T) {
		path := write(t, "settings.json", withOtherHooks)
		if _, err := Register(path, opts(), false); err != nil {
			t.Fatalf("Register: %v", err)
		}
		if _, err := Unregister(path, false); err != nil {
			t.Fatalf("Unregister: %v", err)
		}
		var before, after any
		if err := json.Unmarshal([]byte(withOtherHooks), &before); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(read(t, path)), &after); err != nil {
			t.Fatalf("invalid JSON after round trip: %v", err)
		}
		gotJSON, _ := json.Marshal(after)
		wantJSON, _ := json.Marshal(before)
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("round trip changed the file\n got: %s\nwant: %s", gotJSON, wantJSON)
		}
	})

	t.Run("leaves another tool's hook in place", func(t *testing.T) {
		path := write(t, "settings.json", withOtherHooks)
		if _, err := Register(path, opts(), false); err != nil {
			t.Fatalf("Register: %v", err)
		}
		if _, err := Unregister(path, false); err != nil {
			t.Fatalf("Unregister: %v", err)
		}
		got := commandsFor(t, path, "Stop")
		if len(got) != 1 || !strings.Contains(got[0], "reminder.sh") {
			t.Errorf("Stop = %v, want only the other tool's hook", got)
		}
	})

	t.Run("removes the event entirely when nothing else used it", func(t *testing.T) {
		// A dangling empty array is the kind of leftover that makes an
		// uninstall look like it half-worked.
		path := write(t, "settings.json", `{}`)
		if _, err := Register(path, opts(), false); err != nil {
			t.Fatalf("Register: %v", err)
		}
		if _, err := Unregister(path, false); err != nil {
			t.Fatalf("Unregister: %v", err)
		}
		if got := read(t, path); strings.Contains(got, "PreToolUse") {
			t.Errorf("an empty PreToolUse was left behind:\n%s", got)
		}
	})

	t.Run("uninstalling when nothing is registered is not an error", func(t *testing.T) {
		path := write(t, "settings.json", withOtherHooks)
		rep, err := Unregister(path, false)
		if err != nil {
			t.Fatalf("Unregister: %v", err)
		}
		if len(rep.Changes) != 0 {
			t.Errorf("Changes = %v, want none", rep.Changes)
		}
		if got := read(t, path); got != withOtherHooks {
			t.Error("an uninstall with nothing to do rewrote the file")
		}
	})

	t.Run("a dry run writes nothing", func(t *testing.T) {
		path := write(t, "settings.json", withOtherHooks)
		if _, err := Register(path, opts(), false); err != nil {
			t.Fatalf("Register: %v", err)
		}
		installed := read(t, path)
		rep, err := Unregister(path, true)
		if err != nil {
			t.Fatalf("Unregister: %v", err)
		}
		if got := read(t, path); got != installed {
			t.Error("--dry-run modified the file")
		}
		if len(rep.Changes) == 0 {
			t.Error("--dry-run must still say what it would remove")
		}
	})
}
