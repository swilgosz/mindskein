//go:build unix

package main

import "testing"

func TestUpgrade(t *testing.T) {
	t.Run("upgrade stops a running background server, installs, and starts it again with the same arguments; with --no-restart it stays stopped", func(t *testing.T) {
		pending(t, "upgrade stops a running background server, installs, and starts it again with the same arguments; with --no-restart it stays stopped")
	})

	t.Run("upgrade replaces the binary by rename, so the target path is never a partial file", func(t *testing.T) {
		pending(t, "upgrade replaces the binary by rename, so the target path is never a partial file")
	})

	t.Run("upgrade with a failing build leaves the old binary byte-for-byte and restarts a server that was running", func(t *testing.T) {
		pending(t, "upgrade with a failing build leaves the old binary byte-for-byte and restarts a server that was running")
	})

	t.Run("upgrade with an unchanged version says already up to date", func(t *testing.T) {
		pending(t, "upgrade with an unchanged version says already up to date")
	})

	t.Run("upgrade with no go on PATH is an error saying Go is needed, and touches nothing", func(t *testing.T) {
		pending(t, "upgrade with no go on PATH is an error saying Go is needed, and touches nothing")
	})

	t.Run("tests never touch the real home or the real installed binary: the install step and target path are injectable", func(t *testing.T) {
		pending(t, "tests never touch the real home or the real installed binary: the install step and target path are injectable")
	})
}
