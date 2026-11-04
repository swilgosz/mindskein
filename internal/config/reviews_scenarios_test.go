package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestReviewsConfiguration covers the [reviews] section that configures
// mindskein prs.
func TestReviewsConfiguration(t *testing.T) {
	t.Run("defaults the cache age to ten minutes and the agent marker to none", func(t *testing.T) {
		for _, path := range []string{
			filepath.Join(t.TempDir(), "absent.toml"),
			configFile(t, "[reviews]\n"),
		} {
			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Reviews.CacheAge.Duration(); got != 10*time.Minute {
				t.Errorf("CacheAge = %v, want 10m", got)
			}
			if cfg.Reviews.AgentMarker != "" {
				t.Errorf("AgentMarker = %q, want none", cfg.Reviews.AgentMarker)
			}
		}
	})

	t.Run("reads cache_age and agent_marker from [reviews]", func(t *testing.T) {
		cfg, err := Load(configFile(t, "[reviews]\ncache_age = \"1h30m\"\nagent_marker = \"## Spec\"\n"))
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.Reviews.CacheAge.Duration(); got != 90*time.Minute {
			t.Errorf("CacheAge = %v, want 1h30m", got)
		}
		if cfg.Reviews.AgentMarker != "## Spec" {
			t.Errorf("AgentMarker = %q, want ## Spec", cfg.Reviews.AgentMarker)
		}

		cfg, err = Load(configFile(t, "[reviews]\ncache_age = \"0\"\n"))
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.Reviews.CacheAge.Duration(); got != 0 {
			t.Errorf("CacheAge = %v, want 0: an explicit zero refreshes every time", got)
		}
	})

	t.Run("reports an unknown key in [reviews] rather than ignoring it", func(t *testing.T) {
		cfg, err := Load(configFile(t, "[reviews]\nagent_marker = \"agent/\"\ncache_ages = \"1h\"\n"))
		if err == nil || !strings.Contains(err.Error(), "reviews.cache_ages") {
			t.Errorf("Load() = %v, want an error naming reviews.cache_ages", err)
		}
		if cfg.Reviews.AgentMarker != "agent/" {
			t.Errorf("AgentMarker = %q, want the key that was right still applied", cfg.Reviews.AgentMarker)
		}
	})
}
