package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swilgosz/mindskein/internal/config"
	"github.com/swilgosz/mindskein/internal/hook"
)

// TestEveryHookEventIsRegistered is the guard against the two lists drifting.
// Adding an event to the handler and forgetting to register it produces a
// hook that is implemented, documented and never called.
func TestEveryHookEventIsRegistered(t *testing.T) {
	registered := map[hook.Event]bool{}
	for _, r := range registrations {
		registered[r.Arg] = true
	}
	for _, e := range hook.Events {
		if !registered[e] {
			t.Errorf("hook event %q is handled but never registered", e)
		}
	}
	if len(registrations) != len(hook.Events) {
		t.Errorf("%d registrations for %d events", len(registrations), len(hook.Events))
	}
}

func TestEnsureConfig(t *testing.T) {
	t.Run("writes a starter when there is none", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		created, err := EnsureConfig(path)
		if err != nil {
			t.Fatalf("EnsureConfig: %v", err)
		}
		if !created {
			t.Error("created = false for a file that did not exist")
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("no file written: %v", err)
		}
	})

	t.Run("never overwrites an existing config", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		const mine = "[vault]\npath = \"/Users/me/Notes\"\n"
		if err := os.WriteFile(path, []byte(mine), 0o600); err != nil {
			t.Fatal(err)
		}
		created, err := EnsureConfig(path)
		if err != nil {
			t.Fatalf("EnsureConfig: %v", err)
		}
		if created {
			t.Error("created = true for a file that already existed")
		}
		got, _ := os.ReadFile(path)
		if string(got) != mine {
			t.Error("an install overwrote the user's own configuration")
		}
	})

	t.Run("the starter parses and claims nothing", func(t *testing.T) {
		// A starter that fails to load would break the first run of every
		// command; one that sets a vault path would point at a note that is
		// not there.
		path := filepath.Join(t.TempDir(), "config.toml")
		if _, err := EnsureConfig(path); err != nil {
			t.Fatalf("EnsureConfig: %v", err)
		}
		cfg, err := config.Load(path)
		if err != nil {
			t.Fatalf("the starter config does not load: %v", err)
		}
		if cfg.Vault.Path != "" || cfg.Vault.Plan != "" {
			t.Errorf("the starter guessed a vault: %+v", cfg.Vault)
		}
	})

	t.Run("documents the keys it leaves unset", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		if _, err := EnsureConfig(path); err != nil {
			t.Fatalf("EnsureConfig: %v", err)
		}
		got, _ := os.ReadFile(path)
		for _, want := range []string{"vault", "plan", "hide_after", "retention", "handoffs"} {
			if !strings.Contains(string(got), want) {
				t.Errorf("the starter does not mention %q", want)
			}
		}
	})
}
