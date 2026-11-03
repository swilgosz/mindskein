package config

import "testing"

func pending(t *testing.T, behaviour string) {
	t.Helper()
	t.Fatalf("PENDING: %s", behaviour)
}

// TestReviewsConfiguration covers the [reviews] section that configures
// mindskein prs.
func TestReviewsConfiguration(t *testing.T) {
	for _, s := range []string{
		"defaults the cache age to ten minutes and the agent marker to none",
		"reads cache_age and agent_marker from [reviews]",
		"reports an unknown key in [reviews] rather than ignoring it",
	} {
		t.Run(s, func(t *testing.T) { pending(t, s) })
	}
}
