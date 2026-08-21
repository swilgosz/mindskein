package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func vault(t *testing.T, body string) Vault {
	t.Helper()
	cfg, err := Load(configFile(t, body))
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	return cfg.Vault
}

// TestVaultConfiguration covers where the priorities come from: the vault path
// and the plan.md path inside it, both hand-written in ~/.mindskein/config.toml.
//
// Neither key has a default. A guessed vault layout is a guess about somebody
// else's filesystem, and the way a wrong guess shows up — an empty brief — reads
// exactly like a quiet morning.
func TestVaultConfiguration(t *testing.T) {
	t.Run("reads the vault path and the plan path", func(t *testing.T) {
		v := vault(t, "[vault]\npath = \"/vault\"\nplan = \"620. Agents/plan.md\"\n")
		if v.Path != "/vault" || v.Plan != "620. Agents/plan.md" {
			t.Errorf("Vault = %+v", v)
		}
	})

	t.Run("expands a leading ~ in the vault path", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		v := vault(t, "[vault]\npath = \"~/SecondBrain\"\nplan = \"plan.md\"\n")
		want := filepath.Join(home, "SecondBrain", "plan.md")
		if got := v.PlanPath(); got != want {
			t.Errorf("PlanPath() = %q, want %q", got, want)
		}
		if strings.Contains(v.PlanPath(), "~") {
			t.Errorf("PlanPath() still carries a ~: %q", v.PlanPath())
		}
	})

	t.Run("resolves a relative plan path against the vault", func(t *testing.T) {
		v := vault(t, "[vault]\npath = \"/vault\"\nplan = \"620. Agents/plan.md\"\n")
		if got, want := v.PlanPath(), "/vault/620. Agents/plan.md"; got != want {
			t.Errorf("PlanPath() = %q, want %q", got, want)
		}
	})

	t.Run("takes an absolute plan path as it stands", func(t *testing.T) {
		v := vault(t, "[vault]\npath = \"/vault\"\nplan = \"/elsewhere/plan.md\"\n")
		if got, want := v.PlanPath(), "/elsewhere/plan.md"; got != want {
			t.Errorf("PlanPath() = %q, want %q", got, want)
		}
	})

	t.Run("reports no plan at all rather than guessing one", func(t *testing.T) {
		if got := vault(t, "[status]\nhide_after = \"7d\"\n").PlanPath(); got != "" {
			t.Errorf("PlanPath() = %q, want empty when nothing is configured", got)
		}
	})

	t.Run("says so when a relative plan has no vault to resolve against", func(t *testing.T) {
		// The one half-configuration that would otherwise resolve against
		// whatever directory the brief happened to be run from.
		cfg, err := Load(configFile(t, "[vault]\nplan = \"620. Agents/plan.md\"\n"))
		if err == nil {
			t.Fatalf("Load() = nil, want an error naming vault.path")
		}
		if !strings.Contains(err.Error(), "vault.path") {
			t.Errorf("Load() = %v, want it to name the missing key", err)
		}
		if got := cfg.Vault.PlanPath(); got != "" {
			t.Errorf("PlanPath() = %q, want empty", got)
		}
	})
}
