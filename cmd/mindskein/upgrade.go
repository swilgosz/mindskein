package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/swilgosz/mindskein/internal/serve"
	"github.com/swilgosz/mindskein/internal/session"
)

// upgradeModule is the package upgrade builds.
const upgradeModule = "github.com/swilgosz/mindskein/cmd/mindskein"

// upgradeTarget is the installed binary upgrade replaces. Tests point it at a
// temporary file so that no run of the suite can overwrite a real install.
var upgradeTarget = binaryPath

const versionTimeout = 10 * time.Second

// cmdUpgrade builds the newest mindskein from source and swaps it in for this
// binary, stopping a background server around the swap.
//
// Everything that can fail without side effects is checked before the server
// is stopped. Once it is, a server that was running is started again whatever
// happens to the build, so a failed upgrade leaves things as they were.
func cmdUpgrade(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ref := fs.String("ref", "main", "branch, tag or commit to build")
	noRestart := fs.Bool("no-restart", false, "leave a background server stopped after the upgrade")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("upgrade takes no arguments, got %q", fs.Args())
	}

	goTool, err := exec.LookPath("go")
	if err != nil {
		return errors.New("upgrade builds mindskein from source and needs Go on PATH; install it from https://go.dev/dl")
	}
	target, err := upgradeTarget()
	if err != nil {
		return err
	}
	home, err := session.Home()
	if err != nil {
		return err
	}
	// The new binary is built beside the old one so the swap is a rename
	// within one filesystem, which is atomic.
	work, err := os.MkdirTemp(filepath.Dir(target), ".mindskein-upgrade-")
	if err != nil {
		return fmt.Errorf("upgrade replaces %s and cannot write beside it: %w", target, err)
	}
	defer os.RemoveAll(work)

	was, err := serve.Stop(home)
	if err != nil {
		return fmt.Errorf("stopping the background server: %w", err)
	}
	if was != nil {
		fmt.Fprintf(stdout, "stopped the background server on %s (pid %d)\n", was.URL(), was.PID)
	}

	installErr := installNewest(stdout, goTool, *ref, work, target)
	if was == nil || (*noRestart && installErr == nil) {
		return installErr
	}
	return errors.Join(installErr, restartBackground(stdout, home, target, was.Args))
}

// installNewest builds ref into work and renames it over target when its version
// differs from this binary's. On any failure target is untouched.
func installNewest(stdout io.Writer, goTool, ref, work, target string) error {
	cmd := installCommand(goTool, work, ref)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building %s@%s failed (%v), %s is unchanged:\n%s",
			upgradeModule, ref, err, target, strings.TrimSpace(out.String()))
	}
	built := filepath.Join(work, "mindskein")
	if runtime.GOOS == "windows" {
		built += ".exe"
	}
	newVersion, err := versionOf(built)
	if err != nil {
		return fmt.Errorf("the freshly built mindskein does not run (%w), %s is unchanged", err, target)
	}
	oldVersion := buildVersion()
	if newVersion == oldVersion {
		fmt.Fprintf(stdout, "already up to date (%s)\n", oldVersion)
		return nil
	}
	if err := os.Rename(built, target); err != nil {
		return fmt.Errorf("replacing %s: %w", target, err)
	}
	fmt.Fprintf(stdout, "%s -> %s\n", oldVersion, newVersion)
	return nil
}

// installCommand is go install of ref into gobin.
//
// GOPROXY=direct makes go resolve ref against the repository itself: the
// module proxy rejects branch names containing a slash, and may serve a
// branch as it was when it last looked.
func installCommand(goTool, gobin, ref string) *exec.Cmd {
	cmd := exec.Command(goTool, "install", upgradeModule+"@"+ref)
	cmd.Dir = gobin
	cmd.Env = append(os.Environ(), "GOBIN="+gobin, "GOPROXY=direct")
	return cmd
}

func versionOf(binary string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "version").Output()
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return "", errors.New("it printed no version")
	}
	return v, nil
}

func restartBackground(stdout io.Writer, home, binary string, args []string) error {
	b, err := serve.Start(home, binary, args)
	if errors.Is(err, serve.ErrRunning) {
		fmt.Fprintf(stdout, "a background server is already running on %s (pid %d)\n", b.URL(), b.PID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("starting the background server again: %w", err)
	}
	fmt.Fprintf(stdout, "restarted the background server on %s (pid %d)\n", b.URL(), b.PID)
	return nil
}
