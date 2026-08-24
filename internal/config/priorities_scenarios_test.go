package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func priorities(t *testing.T, body string) (Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

// TestPrioritiesConfiguration covers where the priorities come from: one key
// naming one file.
//
// It has no default. A guessed location is a guess about somebody else's
// filesystem, and a wrong guess prints an empty list, which reads exactly like
// a quiet day.
func TestPrioritiesConfiguration(t *testing.T) {
	t.Run("reads an absolute file path", func(t *testing.T) {
		cfg, err := priorities(t, "[priorities]\nfile = \"/notes/plan.md\"\n")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.Priorities.Path(); got != "/notes/plan.md" {
			t.Errorf("Path() = %q, want /notes/plan.md", got)
		}
	})

	t.Run("expands a leading tilde", func(t *testing.T) {
		cfg, err := priorities(t, "[priorities]\nfile = \"~/notes/plan.md\"\n")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		home, _ := os.UserHomeDir()
		want := filepath.Join(home, "notes/plan.md")
		if got := cfg.Priorities.Path(); got != want {
			t.Errorf("Path() = %q, want %q", got, want)
		}
	})

	t.Run("no file configured is empty rather than an error", func(t *testing.T) {
		cfg, err := priorities(t, "[status]\nhide_after = \"7d\"\n")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.Priorities.Path(); got != "" {
			t.Errorf("Path() = %q, want empty", got)
		}
	})

	// A relative path has no base to resolve against once the root key is
	// gone, and the app is launched from a GUI as often as from a shell, so
	// the working directory is not a defensible answer.
	t.Run("a relative path is rejected by name", func(t *testing.T) {
		_, err := priorities(t, "[priorities]\nfile = \"notes/plan.md\"\n")
		if err == nil {
			t.Fatal("Load: want an error for a relative path")
		}
		if !strings.Contains(err.Error(), "absolute") {
			t.Errorf("error = %q, want it to say the path must be absolute", err)
		}
	})

	// The rename has to be louder than the Undecoded check would make it on
	// its own: "unknown setting" is true but does not say what replaced it.
	t.Run("a config still using vault names its replacement", func(t *testing.T) {
		_, err := priorities(t, "[vault]\npath = \"/notes\"\nplan = \"plan.md\"\n")
		if err == nil {
			t.Fatal("Load: want an error for the old vault section")
		}
		if !strings.Contains(err.Error(), "priorities.file") {
			t.Errorf("error = %q, want it to name priorities.file", err)
		}
	})
}
