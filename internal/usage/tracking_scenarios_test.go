package usage

import "testing"

func pending(t *testing.T, behaviour string) {
	t.Helper()
	t.Fatalf("PENDING: %s", behaviour)
}

func TestTracking(t *testing.T) {
	for _, s := range []string{
		"counts each subagent run once under its agent type and model, however many turns it took",
		"a run with no turns in the range is not counted",
		"a session with a project is reported under that project, and one without it under its folder",
		"a fixed window counts only turns on its days, both ends included, and its daily rows run across it",
		"an end day alone ends the days window on that day",
		"a since after until, or a date that does not parse, is an error naming the flag",
	} {
		t.Run(s, func(t *testing.T) { pending(t, s) })
	}
}
