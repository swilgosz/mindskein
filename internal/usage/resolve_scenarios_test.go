package usage

import "testing"

func pending(t *testing.T, behaviour string) {
	t.Helper()
	t.Fatalf("PENDING: %s", behaviour)
}

func TestResolvedFindings(t *testing.T) {
	t.Run("resolve hook-failures hides the finding; a hook failure in a session after that shows it again, counting only the new failures", func(t *testing.T) {
		pending(t, "resolve hook-failures hides the finding; a hook failure in a session after that shows it again, counting only the new failures")
	})

	t.Run("resolve hook-failures:<hook> hides that hook only; the finding shows the other hooks and its title counts only them", func(t *testing.T) {
		pending(t, "resolve hook-failures:<hook> hides that hook only; the finding shows the other hooks and its title counts only them")
	})

	t.Run("a hook error in a record without per-hook times counts as happening at the session's end", func(t *testing.T) {
		pending(t, "a hook error in a record without per-hook times counts as happening at the session's end")
	})

	t.Run("big results and top-tier subagent rows resolved before a newer occurrence stay hidden, and come back counting only the newer ones", func(t *testing.T) {
		pending(t, "big results and top-tier subagent rows resolved before a newer occurrence stay hidden, and come back counting only the newer ones")
	})

	t.Run("a resolved rate or state finding, or a resolved item of one, is hidden and counted as hidden", func(t *testing.T) {
		pending(t, "a resolved rate or state finding, or a resolved item of one, is hidden and counted as hidden")
	})
}
