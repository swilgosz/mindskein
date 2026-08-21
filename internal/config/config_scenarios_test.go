package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func configFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadHorizon(t *testing.T, body string) time.Duration {
	t.Helper()
	cfg, err := Load(configFile(t, body))
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	return cfg.Status.HideAfter.Duration()
}

// TestHideAfterConfiguration covers reading the retention horizon from
// ~/.mindskein/config.toml — the file the roadmap already assigns the vault
// path, plan.md path and project roots to.
func TestHideAfterConfiguration(t *testing.T) {
	t.Run("defaults to seven days when there is no config file", func(t *testing.T) {
		// The normal state: nobody has written the file yet.
		cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
		if err != nil {
			t.Fatalf("a missing config is not an error, got %v", err)
		}
		if got := cfg.Status.HideAfter.Duration(); got != DefaultHideAfter {
			t.Errorf("HideAfter = %v, want %v", got, DefaultHideAfter)
		}
	})

	t.Run("reads the horizon from config.toml", func(t *testing.T) {
		if got := loadHorizon(t, "[status]\nhide_after = \"3d\"\n"); got != 72*time.Hour {
			t.Errorf("HideAfter = %v, want 72h", got)
		}
	})

	t.Run("accepts a horizon written in days", func(t *testing.T) {
		if got := loadHorizon(t, "[status]\nhide_after = \"7d\"\n"); got != 7*24*time.Hour {
			t.Errorf("HideAfter = %v, want 168h", got)
		}
	})

	t.Run("accepts a horizon written in hours", func(t *testing.T) {
		if got := loadHorizon(t, "[status]\nhide_after = \"36h\"\n"); got != 36*time.Hour {
			t.Errorf("HideAfter = %v, want 36h", got)
		}
	})

	t.Run("accepts a horizon combining days and hours", func(t *testing.T) {
		if got := loadHorizon(t, "[status]\nhide_after = \"1d12h\"\n"); got != 36*time.Hour {
			t.Errorf("HideAfter = %v, want 36h", got)
		}
	})

	t.Run("treats a zero horizon as never hiding", func(t *testing.T) {
		// Distinct from an absent key, which takes the default.
		if got := loadHorizon(t, "[status]\nhide_after = \"0\"\n"); got != 0 {
			t.Errorf("HideAfter = %v, want 0 — an explicit zero disables hiding", got)
		}
	})

	t.Run("rejects a negative horizon", func(t *testing.T) {
		if _, err := Load(configFile(t, "[status]\nhide_after = \"-1d\"\n")); err == nil {
			t.Error("a negative horizon would hide everything; want an error")
		}
	})

	t.Run("reports a malformed config rather than silently using the default", func(t *testing.T) {
		cfg, err := Load(configFile(t, "[status\nhide_after = \"7d\"\n"))
		if err == nil {
			t.Error("want an error for a config that does not parse")
		}
		// Still usable: a broken config must not take the listing down.
		if got := cfg.Status.HideAfter.Duration(); got != DefaultHideAfter {
			t.Errorf("HideAfter = %v, want the default %v alongside the error", got, DefaultHideAfter)
		}
	})

	t.Run("reports a misspelled key rather than ignoring it", func(t *testing.T) {
		cfg, err := Load(configFile(t, "[status]\nhide_afer = \"2d\"\n"))
		if err == nil {
			t.Error("a typo must complain; a setting that appears to do nothing is worse")
		}
		if got := cfg.Status.HideAfter.Duration(); got != DefaultHideAfter {
			t.Errorf("HideAfter = %v, want the default %v", got, DefaultHideAfter)
		}
	})

	t.Run("still applies the keys it understood alongside the complaint", func(t *testing.T) {
		cfg, err := Load(configFile(t, "[status]\nhide_after = \"2d\"\nnonsense = 1\n"))
		if err == nil {
			t.Error("want the unknown key reported")
		}
		if got := cfg.Status.HideAfter.Duration(); got != 48*time.Hour {
			t.Errorf("HideAfter = %v, want 48h — one typo must not discard a valid key", got)
		}
	})

	t.Run("spells a horizon back in configurable units", func(t *testing.T) {
		// This string reaches the user twice: the flag default in `status -h`,
		// and the summary naming what was hidden.
		for in, want := range map[time.Duration]string{
			7 * 24 * time.Hour: "7d",
			36 * time.Hour:     "1d12h",
			90 * time.Minute:   "1h30m",
			30 * time.Second:   "30s",
			0:                  "0",
		} {
			if got := Duration(in).String(); got != want {
				t.Errorf("Duration(%v).String() = %q, want %q", in, got, want)
			}
		}
	})

	t.Run("keeps the default when the key is absent", func(t *testing.T) {
		if got := loadHorizon(t, "[status]\n"); got != DefaultHideAfter {
			t.Errorf("HideAfter = %v, want %v", got, DefaultHideAfter)
		}
	})
}
