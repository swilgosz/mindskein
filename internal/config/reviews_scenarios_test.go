package config

import (
	"fmt"
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

	t.Run("reads team authors and repos, ordered spaces and skip rules from [reviews]", func(t *testing.T) {
		cfg, err := Load(configFile(t, `[reviews]
[reviews.team]
authors = ["ann", "bob"]
repos = ["acme/web"]

[[reviews.spaces]]
name = "Work"
repos = ["acme", "globex/web"]

[[reviews.spaces]]
name = "Business"
repos = ["me"]

[[reviews.skip]]
repo = "acme/legacy-*"

[[reviews.skip]]
title = "WIP*"
author = "zed"
unless_review_requested = true
`))
		if err != nil {
			t.Fatal(err)
		}
		r := cfg.Reviews
		if fmt.Sprint(r.Team.Authors, r.Team.Repos) != "[ann bob] [acme/web]" {
			t.Errorf("Team = %+v", r.Team)
		}
		want := []Space{{Name: "Work", Repos: []string{"acme", "globex/web"}}, {Name: "Business", Repos: []string{"me"}}}
		if fmt.Sprint(r.Spaces) != fmt.Sprint(want) {
			t.Errorf("Spaces = %+v, want %+v in config order", r.Spaces, want)
		}
		skip := []SkipRule{{Repo: "acme/legacy-*"}, {Title: "WIP*", Author: "zed", UnlessReviewRequested: true}}
		if fmt.Sprint(r.Skip) != fmt.Sprint(skip) {
			t.Errorf("Skip = %+v, want %+v", r.Skip, skip)
		}
	})

	t.Run("every [reviews] key is optional: no team, no spaces, no skip rules", func(t *testing.T) {
		cfg, err := Load(configFile(t, "[reviews]\ncache_age = \"5m\"\n"))
		if err != nil {
			t.Fatal(err)
		}
		r := cfg.Reviews
		if len(r.Team.Authors)+len(r.Team.Repos)+len(r.Spaces)+len(r.Skip) != 0 {
			t.Errorf("Reviews = %+v, want no team, spaces or skip rules", r)
		}
	})

	t.Run("reports a space with no name, no repos, a duplicate name or the name Other, and a skip rule with no pattern, keeping the valid ones", func(t *testing.T) {
		cases := map[string]string{
			"[[reviews.spaces]]\nrepos = [\"acme\"]\n":                 "reviews.spaces[2]: no name",
			"[[reviews.spaces]]\nname = \"Life\"\n":                    "reviews.spaces[2] Life: no repos",
			"[[reviews.spaces]]\nname = \"work\"\nrepos = [\"me\"]\n":  "reviews.spaces[2] work: named twice",
			"[[reviews.spaces]]\nname = \"Other\"\nrepos = [\"me\"]\n": "reviews.spaces[2] Other: reserved",
			"[[reviews.skip]]\nunless_review_requested = true\n":       "reviews.skip[1]: no repo, author or title",
		}
		for extra, want := range cases {
			body := "[[reviews.spaces]]\nname = \"Work\"\nrepos = [\"acme\"]\n" + extra
			cfg, err := Load(configFile(t, body))
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("Load(%q) = %v, want an error saying %q", extra, err, want)
			}
			if len(cfg.Reviews.Spaces) != 1 || cfg.Reviews.Spaces[0].Name != "Work" || len(cfg.Reviews.Skip) != 0 {
				t.Errorf("Load(%q) kept %+v and %+v, want only the valid Work space", extra, cfg.Reviews.Spaces, cfg.Reviews.Skip)
			}
		}
	})
}
