package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func settingsWith(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing settings: %v", err)
	}
	return path
}

func hookCommands(t *testing.T, path string) map[string][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading settings: %v", err)
	}
	var root struct {
		Hooks map[string][]struct {
			Hooks []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("settings is not valid JSON: %v", err)
	}
	out := map[string][]string{}
	for event, groups := range root.Hooks {
		for _, g := range groups {
			for _, e := range g.Hooks {
				cmd, _ := e["command"].(string)
				out[event] = append(out[event], cmd)
			}
		}
	}
	return out
}

// TestInitCommand covers the command a stranger runs first. Everything it
// touches is a copy: the settings path is passed explicitly so a test can
// never reach the real ~/.claude/settings.json.
func TestInitCommand(t *testing.T) {
	t.Run("installs every hook and seeds a config", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		path := settingsWith(t, `{}`)

		var stdout, stderr bytes.Buffer
		if err := cmdInit([]string{"--settings", path}, &stdout, &stderr); err != nil {
			t.Fatalf("cmdInit: %v", err)
		}
		got := hookCommands(t, path)
		for _, event := range []string{"PreToolUse", "Notification", "Stop", "SessionEnd"} {
			if len(got[event]) != 1 {
				t.Errorf("%s = %v, want one entry", event, got[event])
			}
		}
		if _, err := os.Stat(filepath.Join(home, "config.toml")); err != nil {
			t.Errorf("no starter config written: %v", err)
		}
		if !strings.Contains(stdout.String(), "restart Claude Code") {
			t.Errorf("nothing told the user the hooks are not live yet:\n%s", stdout.String())
		}
	})

	t.Run("registers an absolute path", func(t *testing.T) {
		// A hook runs with no PATH guarantee, so a bare name would never run.
		t.Setenv("MINDSKEIN_HOME", t.TempDir())
		path := settingsWith(t, `{}`)
		if err := cmdInit([]string{"--settings", path}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("cmdInit: %v", err)
		}
		for event, cmds := range hookCommands(t, path) {
			for _, cmd := range cmds {
				if !strings.HasPrefix(cmd, "/") {
					t.Errorf("%s command = %q, want an absolute path", event, cmd)
				}
			}
		}
	})

	t.Run("never overwrites an existing config", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		cfg := filepath.Join(home, "config.toml")
		const mine = "[vault]\npath = \"/Users/me/Notes\"\n"
		if err := os.WriteFile(cfg, []byte(mine), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := cmdInit([]string{"--settings", settingsWith(t, `{}`)}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("cmdInit: %v", err)
		}
		got, _ := os.ReadFile(cfg)
		if string(got) != mine {
			t.Error("init overwrote the user's own configuration")
		}
	})

	t.Run("a dry run changes nothing", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		path := settingsWith(t, `{}`)

		var stdout bytes.Buffer
		if err := cmdInit([]string{"--settings", path, "--dry-run"}, &stdout, &bytes.Buffer{}); err != nil {
			t.Fatalf("cmdInit: %v", err)
		}
		if got, _ := os.ReadFile(path); string(got) != `{}` {
			t.Errorf("--dry-run wrote to settings.json: %s", got)
		}
		if _, err := os.Stat(filepath.Join(home, "config.toml")); err == nil {
			t.Error("--dry-run wrote a config file")
		}
		if !strings.Contains(stdout.String(), "would update") {
			t.Errorf("--dry-run said nothing about what it would do:\n%s", stdout.String())
		}
	})

	t.Run("uninstall removes the hooks and says the state stays", func(t *testing.T) {
		t.Setenv("MINDSKEIN_HOME", t.TempDir())
		path := settingsWith(t, `{}`)
		if err := cmdInit([]string{"--settings", path}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("install: %v", err)
		}
		var stdout bytes.Buffer
		if err := cmdInit([]string{"--settings", path, "--uninstall"}, &stdout, &bytes.Buffer{}); err != nil {
			t.Fatalf("uninstall: %v", err)
		}
		if got := hookCommands(t, path); len(got) != 0 {
			t.Errorf("hooks left after uninstall: %v", got)
		}
		if !strings.Contains(stdout.String(), "~/.mindskein") {
			t.Errorf("uninstall did not say the captured state is still on disk:\n%s", stdout.String())
		}
	})

	t.Run("refuses a settings file it cannot parse", func(t *testing.T) {
		t.Setenv("MINDSKEIN_HOME", t.TempDir())
		const broken = `{ "hooks": oops }`
		path := settingsWith(t, broken)
		if err := cmdInit([]string{"--settings", path}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatal("cmdInit succeeded on unparseable settings")
		}
		if got, _ := os.ReadFile(path); string(got) != broken {
			t.Error("the unparseable file was overwritten")
		}
	})

	t.Run("rejects an unknown flag rather than guessing", func(t *testing.T) {
		t.Setenv("MINDSKEIN_HOME", t.TempDir())
		if err := cmdInit([]string{"--wat"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Error("an unknown flag was accepted")
		}
	})
}

func TestDefaultSettingsPath(t *testing.T) {
	got, err := defaultSettingsPath()
	if err != nil {
		t.Fatalf("defaultSettingsPath: %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join(".claude", "settings.json")) {
		t.Errorf("defaultSettingsPath = %q", got)
	}
}

func TestBinaryPathIsAbsolute(t *testing.T) {
	got, err := binaryPath()
	if err != nil {
		t.Fatalf("binaryPath: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("binaryPath = %q, want an absolute path", got)
	}
}
