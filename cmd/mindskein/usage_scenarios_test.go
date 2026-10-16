package main

import "testing"

func pendingUsage(t *testing.T, behaviour string) {
	t.Helper()
	t.Fatalf("PENDING: %s", behaviour)
}

func TestUsageCapture(t *testing.T) {
	t.Run("a Stop with no transcript_path, or an unreadable one, writes no usage record and still exits 0", func(t *testing.T) {
		pendingUsage(t, "a Stop with no transcript_path, or an unreadable one, writes no usage record and still exits 0")
	})
	t.Run("a Stop with a readable transcript writes the session's usage record", func(t *testing.T) {
		pendingUsage(t, "a Stop with a readable transcript writes the session's usage record")
	})
}
