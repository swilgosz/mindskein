package main

import (
	"io"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestInstallCommand(t *testing.T) {
	gobin := filepath.Join(t.TempDir(), "gobin")
	cmd := installCommand("/opt/go/bin/go", gobin, "main")

	if cmd.Path != "/opt/go/bin/go" {
		t.Errorf("runs %q, want the go found on PATH", cmd.Path)
	}
	if want := []string{"/opt/go/bin/go", "install", "github.com/swilgosz/mindskein/cmd/mindskein@main"}; !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("args = %q, want %q", cmd.Args, want)
	}
	if cmd.Dir != gobin {
		t.Errorf("runs in %q, want the build directory, away from any go.mod in the caller's", cmd.Dir)
	}
	env := cmd.Environ()
	for _, want := range []string{"GOBIN=" + gobin, "GOPROXY=direct"} {
		if !slices.Contains(env, want) {
			t.Errorf("environment lacks %s", want)
		}
	}
}

func TestUpgradeUsage(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := run([]string{"upgrade", "extra"}, nil, io.Discard, io.Discard); err == nil {
		t.Error("upgrade with a stray argument succeeded, want a usage error")
	}
	if err := run([]string{"upgrade", "--bogus"}, nil, io.Discard, io.Discard); err == nil {
		t.Error("upgrade with an unknown flag succeeded, want a usage error")
	}
	if err := run([]string{"upgrade", "-h"}, nil, io.Discard, io.Discard); err != nil {
		t.Errorf("upgrade -h = %v, want help and exit 0", err)
	}
}
