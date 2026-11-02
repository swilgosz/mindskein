//go:build unix

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/swilgosz/mindskein/internal/proc"
)

const newVersion = "v9.9.9-upgrade-test"

// upgradeRig stands in for an installed mindskein and the Go toolchain.
//
// The installed binary is a temporary script that runs the test binary as
// mindskein, so a server it starts is a real one; it writes every invocation
// to a log, which says whether the old or the new binary ran. go is a script
// on a PATH holding nothing else, recording how it was called.
type upgradeRig struct {
	home   string
	dir    string
	target string
	newBin string
	path   string
	ran    string
}

func newUpgradeRig(t *testing.T) *upgradeRig {
	t.Helper()
	r := &upgradeRig{home: backgroundHome(t), dir: t.TempDir()}
	r.target = filepath.Join(r.dir, "bin", "mindskein")
	r.newBin = filepath.Join(r.dir, "built")
	r.path = filepath.Join(r.dir, "path")
	r.ran = filepath.Join(r.dir, "ran.log")
	for _, d := range []string{filepath.Dir(r.target), r.path} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	r.writeMindskein(t, r.target, "old", buildVersion(), "")
	t.Setenv("PATH", r.path)

	previous := upgradeTarget
	upgradeTarget = func() (string, error) { return r.target, nil }
	t.Cleanup(func() { upgradeTarget = previous })
	return r
}

// writeMindskein writes a binary named name that reports version and runs the
// rest as mindskein; extra is shell run first, to make it misbehave.
func (r *upgradeRig) writeMindskein(t *testing.T, path, name, version, extra string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	writeScript(t, path, fmt.Sprintf(`echo "%s $*" >> %q
%s
if [ "$1" = version ]; then echo %q; exit 0; fi
exec %q "$@"
`, name, r.ran, extra, version, self))
}

// installs makes go install produce a mindskein that reports version.
func (r *upgradeRig) installs(t *testing.T, version string) {
	t.Helper()
	r.installsWith(t, version, "")
}

func (r *upgradeRig) installsWith(t *testing.T, version, extra string) {
	t.Helper()
	r.writeMindskein(t, r.newBin, "new", version, extra)
	r.fakeGo(t, fmt.Sprintf(`/bin/cp %q "$GOBIN/mindskein"`, r.newBin))
}

func (r *upgradeRig) fakeGo(t *testing.T, body string) {
	t.Helper()
	writeScript(t, filepath.Join(r.path, "go"), fmt.Sprintf(`printf '%%s\n' "$@" > %q
printf '%%s\n' "GOBIN=$GOBIN" "GOPROXY=$GOPROXY" "HOME=$HOME" > %q
%s
`, filepath.Join(r.dir, "go.args"), filepath.Join(r.dir, "go.env"), body))
}

func (r *upgradeRig) upgrade(args ...string) (string, error) {
	var out, errOut bytes.Buffer
	err := run(append([]string{"upgrade"}, args...), nil, &out, &errOut)
	return out.String() + errOut.String(), err
}

func (r *upgradeRig) ranLog(t *testing.T) string {
	t.Helper()
	data, _ := os.ReadFile(r.ran)
	return string(data)
}

func (r *upgradeRig) read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// besideTarget lists the directory holding the installed binary, where
// upgrade must leave nothing of its own behind.
func (r *upgradeRig) besideTarget(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(r.target))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func answers(t *testing.T, rec servedRecord) {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", rec.Port))
	if err != nil {
		t.Fatalf("the background server does not answer on port %d: %v", rec.Port, err)
	}
	resp.Body.Close()
}

func stillRunning(rec servedRecord) bool {
	same, err := proc.SameProcess(rec.PID, rec.PIDStartedAt)
	return err == nil && same
}

func TestUpgrade(t *testing.T) {
	t.Run("upgrade stops a running background server, installs, and starts it again with the same arguments; with --no-restart it stays stopped", func(t *testing.T) {
		r := newUpgradeRig(t)
		r.installs(t, newVersion)
		launchBackground(t, "--port", "0")
		before := readServed(t, r.home)

		out, err := r.upgrade()
		if err != nil {
			t.Fatalf("upgrade: %v\n%s", err, out)
		}
		after := readServed(t, r.home)
		if stillRunning(before) {
			t.Errorf("the server that was running (pid %d) was not stopped", before.PID)
		}
		if after.PID == before.PID || !stillRunning(after) {
			t.Fatalf("serve.json = %+v, want a server started again after the install", after)
		}
		if !reflect.DeepEqual(after.Args, before.Args) {
			t.Errorf("restarted with %q, want the arguments it ran with, %q", after.Args, before.Args)
		}
		answers(t, after)
		if !strings.Contains(r.ranLog(t), "new serve --port=0") {
			t.Errorf("runs = %q, want the new binary to serve again", r.ranLog(t))
		}

		r.installs(t, newVersion+".2")
		out, err = r.upgrade("--no-restart")
		if err != nil {
			t.Fatalf("upgrade --no-restart: %v\n%s", err, out)
		}
		if stillRunning(after) {
			t.Errorf("the server that was running (pid %d) was not stopped", after.PID)
		}
		if _, err := os.Stat(filepath.Join(r.home, "serve.json")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("upgrade --no-restart started a server (%v)\n%s", err, out)
		}
	})

	t.Run("upgrade replaces the binary by rename, so the target path is never a partial file", func(t *testing.T) {
		r := newUpgradeRig(t)
		r.installs(t, newVersion)
		old := r.read(t, r.target)
		oldInfo, err := os.Stat(r.target)
		if err != nil {
			t.Fatal(err)
		}
		// A hook that started before the swap holds the old file open, as a
		// running binary does; it must go on seeing the whole old file.
		held, err := os.Open(r.target)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()

		out, err := r.upgrade()
		if err != nil {
			t.Fatalf("upgrade: %v\n%s", err, out)
		}
		if got := r.read(t, r.target); !bytes.Equal(got, r.read(t, r.newBin)) {
			t.Fatalf("target = %q, want the binary go install built", got)
		}
		if seen, _ := io.ReadAll(held); !bytes.Equal(seen, old) {
			t.Errorf("the old binary, open during the upgrade, now reads %q: the target was written in place", seen)
		}
		newInfo, err := os.Stat(r.target)
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(oldInfo, newInfo) {
			t.Error("the target is the same file as before, rewritten rather than renamed over")
		}
		if newInfo.Mode()&0o111 == 0 {
			t.Errorf("target mode = %s, want it executable", newInfo.Mode())
		}
		if names := r.besideTarget(t); !reflect.DeepEqual(names, []string{"mindskein"}) {
			t.Errorf("beside the target: %q, want nothing left over", names)
		}
		if want := buildVersion() + " -> " + newVersion; !strings.Contains(out, want) {
			t.Errorf("output = %q, want %q", out, want)
		}
	})

	t.Run("upgrade with a failing build leaves the old binary byte-for-byte and restarts a server that was running", func(t *testing.T) {
		r := newUpgradeRig(t)
		r.fakeGo(t, `echo "cannot find module providing package" >&2; exit 1`)
		launchBackground(t, "--port", "0")
		before := readServed(t, r.home)
		old := r.read(t, r.target)

		out, err := r.upgrade()
		if err == nil {
			t.Fatalf("upgrade with a failing build succeeded\n%s", out)
		}
		if !strings.Contains(err.Error(), "cannot find module providing package") {
			t.Errorf("error = %q, want it to say what failed", err)
		}
		if got := r.read(t, r.target); !bytes.Equal(got, old) {
			t.Errorf("target changed after a failed build:\n%s", got)
		}
		after := readServed(t, r.home)
		if after.PID == before.PID || !stillRunning(after) {
			t.Fatalf("serve.json = %+v, want the server started again", after)
		}
		if !reflect.DeepEqual(after.Args, before.Args) {
			t.Errorf("restarted with %q, want %q", after.Args, before.Args)
		}
		answers(t, after)
		if !strings.Contains(r.ranLog(t), "old serve --port=0") {
			t.Errorf("runs = %q, want the old binary to serve again", r.ranLog(t))
		}
		if names := r.besideTarget(t); !reflect.DeepEqual(names, []string{"mindskein"}) {
			t.Errorf("beside the target: %q, want nothing left over", names)
		}
	})

	t.Run("upgrade with an unchanged version says already up to date", func(t *testing.T) {
		r := newUpgradeRig(t)
		r.installs(t, buildVersion())
		launchBackground(t, "--port", "0")
		before := readServed(t, r.home)
		old := r.read(t, r.target)

		out, err := r.upgrade()
		if err != nil {
			t.Fatalf("upgrade: %v\n%s", err, out)
		}
		if !strings.Contains(out, "already up to date") {
			t.Errorf("output = %q, want it to say already up to date", out)
		}
		if strings.Contains(out, "->") {
			t.Errorf("output = %q, reports a version change that did not happen", out)
		}
		if got := r.read(t, r.target); !bytes.Equal(got, old) {
			t.Errorf("target replaced although the version is unchanged:\n%s", got)
		}
		after := readServed(t, r.home)
		if after.PID == before.PID || !stillRunning(after) {
			t.Fatalf("serve.json = %+v, want the server started again", after)
		}
		answers(t, after)
	})

	t.Run("upgrade with no go on PATH is an error saying Go is needed, and touches nothing", func(t *testing.T) {
		r := newUpgradeRig(t)
		launchBackground(t, "--port", "0")
		record := r.read(t, filepath.Join(r.home, "serve.json"))
		before := readServed(t, r.home)
		old := r.read(t, r.target)

		out, err := r.upgrade()
		if err == nil {
			t.Fatalf("upgrade with no go on PATH succeeded\n%s", out)
		}
		if !strings.Contains(err.Error(), "Go") || !strings.Contains(err.Error(), "PATH") {
			t.Errorf("error = %q, want it to say Go is needed on PATH", err)
		}
		if !stillRunning(before) {
			t.Errorf("the background server (pid %d) was stopped", before.PID)
		}
		if got := r.read(t, filepath.Join(r.home, "serve.json")); !bytes.Equal(got, record) {
			t.Errorf("serve.json changed:\nbefore %s\nafter  %s", record, got)
		}
		if got := r.read(t, r.target); !bytes.Equal(got, old) {
			t.Errorf("target changed:\n%s", got)
		}
		if names := r.besideTarget(t); !reflect.DeepEqual(names, []string{"mindskein"}) {
			t.Errorf("beside the target: %q, want nothing written", names)
		}
	})

	t.Run("tests never touch the real home or the real installed binary: the install step and target path are injectable", func(t *testing.T) {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		selfBefore, err := os.Stat(self)
		if err != nil {
			t.Fatal(err)
		}
		r := newUpgradeRig(t)
		r.installs(t, newVersion)

		out, err := r.upgrade("--ref", "some-branch")
		if err != nil {
			t.Fatalf("upgrade: %v\n%s", err, out)
		}
		args := strings.Fields(string(r.read(t, filepath.Join(r.dir, "go.args"))))
		if want := []string{"install", "github.com/swilgosz/mindskein/cmd/mindskein@some-branch"}; !reflect.DeepEqual(args, want) {
			t.Errorf("go ran with %q, want %q", args, want)
		}
		env := string(r.read(t, filepath.Join(r.dir, "go.env")))
		if !strings.Contains(env, "GOPROXY=direct\n") {
			t.Errorf("go ran with %q, want GOPROXY=direct", env)
		}
		gobin := strings.TrimPrefix(strings.Split(env, "\n")[0], "GOBIN=")
		if filepath.Dir(gobin) != filepath.Dir(r.target) {
			t.Errorf("GOBIN = %q, want a directory beside the injected target %s", gobin, r.target)
		}
		if home, _ := os.UserHomeDir(); !strings.HasPrefix(home, os.TempDir()) || !strings.Contains(env, "HOME="+home+"\n") {
			t.Errorf("HOME = %q, go saw %q: want the throwaway home", home, env)
		}
		if got := r.read(t, r.target); !bytes.Equal(got, r.read(t, r.newBin)) {
			t.Errorf("the injected target was not replaced")
		}
		if selfAfter, err := os.Stat(self); err != nil || !os.SameFile(selfBefore, selfAfter) || !selfAfter.ModTime().Equal(selfBefore.ModTime()) {
			t.Errorf("the binary actually running, %s, was touched (%v)", self, err)
		}
	})
}

func TestUpgradeRestartFailure(t *testing.T) {
	r := newUpgradeRig(t)
	r.installsWith(t, newVersion, `if [ "$1" = serve ]; then echo "address already in use" >&2; exit 1; fi`)
	launchBackground(t, "--port", "0")

	out, err := r.upgrade()
	if err == nil {
		t.Fatalf("upgrade succeeded although the server did not start again\n%s", out)
	}
	if !strings.Contains(err.Error(), "address already in use") {
		t.Errorf("error = %q, want why the server did not start", err)
	}
	if got := r.read(t, r.target); !bytes.Equal(got, r.read(t, r.newBin)) {
		t.Errorf("target = %q, want the new binary to stay installed", got)
	}
}
