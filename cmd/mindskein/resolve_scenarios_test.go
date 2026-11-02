package main

import "testing"

func TestResolve(t *testing.T) {
	t.Run("resolve with an unknown id, or an item not in the current report, is an error listing the valid ids, and writes nothing", func(t *testing.T) {
		pending(t, "resolve with an unknown id, or an item not in the current report, is an error listing the valid ids, and writes nothing")
	})

	t.Run("resolve --list prints each entry with its time and note; unresolve removes one and the finding returns on the next report", func(t *testing.T) {
		pending(t, "resolve --list prints each entry with its time and note; unresolve removes one and the finding returns on the next report")
	})

	t.Run("the footer counts what is hidden; --include-resolved prints the report as if nothing were resolved", func(t *testing.T) {
		pending(t, "the footer counts what is hidden; --include-resolved prints the report as if nothing were resolved")
	})

	t.Run("--json keeps evidence and adds resolved beside id and items", func(t *testing.T) {
		pending(t, "--json keeps evidence and adds resolved beside id and items")
	})

	t.Run("prune leaves resolved.json untouched, and a missing or empty resolved.json means nothing is resolved", func(t *testing.T) {
		pending(t, "prune leaves resolved.json untouched, and a missing or empty resolved.json means nothing is resolved")
	})
}
