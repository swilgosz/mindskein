package usage

import "testing"

func pending(t *testing.T, behaviour string) {
	t.Helper()
	t.Fatalf("PENDING: %s", behaviour)
}

func TestFindingIdentity(t *testing.T) {
	t.Run("every finding in usage --json has an id from the list, and the same id on the next run", func(t *testing.T) {
		pending(t, "every finding in usage --json has an id from the list, and the same id on the next run")
	})

	t.Run("findings that list things carry items keyed by hook, name, subagent row or path, and evidence is unchanged", func(t *testing.T) {
		pending(t, "findings that list things carry items keyed by hook, name, subagent row or path, and evidence is unchanged")
	})

	t.Run("the terminal report shows each finding's id, and --all shows each item's key", func(t *testing.T) {
		pending(t, "the terminal report shows each finding's id, and --all shows each item's key")
	})

	t.Run("a captured transcript records the last failure time per hook and each big result's time", func(t *testing.T) {
		pending(t, "a captured transcript records the last failure time per hook and each big result's time")
	})

	t.Run("a record without per-hook or per-result times falls back to the session's end time", func(t *testing.T) {
		pending(t, "a record without per-hook or per-result times falls back to the session's end time")
	})
}
