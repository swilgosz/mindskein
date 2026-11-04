//go:build unix

package reviews

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeGH puts a gh on an otherwise empty PATH that runs script.
func fakeGH(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/bin:/usr/bin")
}

func TestGH(t *testing.T) {
	t.Run("returns what gh prints and passes its arguments through", func(t *testing.T) {
		fakeGH(t, `echo "$@"`)
		out, err := GH(context.Background(), "api", "user")
		if err != nil || string(out) != "api user\n" {
			t.Errorf("GH = %q, %v", out, err)
		}
	})

	t.Run("a gh signed out of every account is explained as such", func(t *testing.T) {
		fakeGH(t, `echo "To get started with GitHub CLI, please run:  gh auth login" >&2; exit 4`)
		_, err := GH(context.Background(), "api", "user")
		if got := Explain(err); got != "not signed in to GitHub — run `gh auth login`" {
			t.Errorf("Explain = %q (from %v)", got, err)
		}
	})

	t.Run("a gh that fails without saying why is reported by its exit", func(t *testing.T) {
		fakeGH(t, `exit 3`)
		_, err := GH(context.Background(), "api", "user")
		if got := Explain(err); got != "gh failed: exit status 3" {
			t.Errorf("Explain = %q", got)
		}
	})

	t.Run("a gh that hangs is stopped at the deadline", func(t *testing.T) {
		fakeGH(t, `sleep 30`)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := GH(ctx, "api", "user")
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("GH took %v", took)
		}
		if got := Explain(err); got != "GitHub did not answer in time — check the network" {
			t.Errorf("Explain = %q (from %v)", got, err)
		}
	})
}
