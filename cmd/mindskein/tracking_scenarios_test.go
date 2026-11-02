package main

import "testing"

func pending(t *testing.T, behaviour string) {
	t.Helper()
	t.Fatalf("PENDING: %s", behaviour)
}

func TestUsageTracking(t *testing.T) {
	for _, s := range []string{
		"a session whose Stop carried MINDSKEIN_PROJECT is reported under that project",
		"the terminal shows each costly subagent type's cost per run",
		"usage --since A --until B counts only those days",
		"a bad --since or --until is an error naming the flag, not an empty report",
		"/api/usage?since=A&until=B returns what usage --since A --until B --json prints",
	} {
		t.Run(s, func(t *testing.T) { pending(t, s) })
	}
}
