package main

import "testing"

func pending(t *testing.T, behaviour string) {
	t.Helper()
	t.Fatalf("PENDING: %s", behaviour)
}

func TestServeInTheBackground(t *testing.T) {
	t.Run("serve --background returns at once, the server answers on its port, and serve.json records its pid, start time and arguments", func(t *testing.T) {
		pending(t, "serve --background returns at once, the server answers on its port, and serve.json records its pid, start time and arguments")
	})

	t.Run("serve --background while one is running says so with its URL and starts nothing", func(t *testing.T) {
		pending(t, "serve --background while one is running says so with its URL and starts nothing")
	})

	t.Run("serve --stop stops the recorded server and removes serve.json; with none running it says so and exits 0", func(t *testing.T) {
		pending(t, "serve --stop stops the recorded server and removes serve.json; with none running it says so and exits 0")
	})

	t.Run("serve --stop does not signal a process whose start time differs from the recorded one", func(t *testing.T) {
		pending(t, "serve --stop does not signal a process whose start time differs from the recorded one")
	})

	t.Run("serve --background together with --stop is a usage error", func(t *testing.T) {
		pending(t, "serve --background together with --stop is a usage error")
	})
}
