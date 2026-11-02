package main

import "testing"

func TestFindingIDs(t *testing.T) {
	t.Run("usage --json gives a failing hook's finding the id hook-failures, the same on the next run", func(t *testing.T) {
		pending(t, "usage --json gives a failing hook's finding the id hook-failures, the same on the next run")
	})
}
