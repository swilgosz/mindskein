package main

import (
	"os"
	"strings"
	"testing"
)

// TestMain runs every test against a throwaway home. Code under test resolves
// ~ to find settings.json and the stores, and a test that forgets to isolate
// itself — or a mutation that turns a dry run into a write — must reach a
// temporary directory, never the developer's own.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "mindskein-test-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	// A child the test binary re-runs is handed its parent's temporary
	// MINDSKEIN_HOME, and keeps it; any other value is a real store.
	if !strings.HasPrefix(os.Getenv("MINDSKEIN_HOME"), os.TempDir()) {
		os.Unsetenv("MINDSKEIN_HOME")
	}
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
