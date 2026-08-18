// Command mindskein answers the morning question: what are my priorities,
// what's running elsewhere, and where did we leave off?
//
// v0.1 scope and the unit breakdown live in the vault:
// "MindSkein v0.1 — mindskein brief". This file is the U0 dispatch skeleton;
// each subcommand is filled in by the unit named in its handler.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/swilgosz/mindskein/internal/hook"
	"github.com/swilgosz/mindskein/internal/session"
)

// version is overridden at build time with
// -ldflags "-X main.version=$(git describe --tags --always)".
var version = "dev"

// command is one top-level subcommand of the CLI. Every handler takes stdin
// because the hook subcommand reads its payload from it.
type command struct {
	name    string
	summary string
	run     func(args []string, stdin io.Reader) error
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "mindskein: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	commands := []command{
		{"brief", "print priorities, live sessions and last handoffs", cmdBrief},
		{"status", "print live sessions only (mid-day check)", func(args []string, _ io.Reader) error {
			return cmdStatus(args, stdout)
		}},
		{"priorities", "print the !1/!2 lines parsed out of plan.md", cmdPriorities},
		{"hook", "handle a Claude Code hook payload on stdin", cmdHook},
		{"version", "print the mindskein version", func([]string, io.Reader) error {
			fmt.Fprintln(stdout, version)
			return nil
		}},
	}

	fs := flag.NewFlagSet("mindskein", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { usage(stderr, commands) }
	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() == 0 {
		usage(stdout, commands)
		return nil
	}

	name := fs.Arg(0)
	for _, c := range commands {
		if c.name == name {
			return c.run(fs.Args()[1:], stdin)
		}
	}

	usage(stderr, commands)
	return fmt.Errorf("unknown command %q", name)
}

func usage(w io.Writer, commands []command) {
	fmt.Fprintf(w, "mindskein %s — one command for the morning question.\n\n", version)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  mindskein <command> [arguments]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-11s %s\n", c.name, c.summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run 'mindskein <command> -h' for details on a command.")
}

// notImplemented is what every handler returns until its unit lands. It keeps
// the skeleton honest: the command exists, is dispatchable, and says who owns it.
func notImplemented(unit string) error {
	return fmt.Errorf("not implemented yet — lands with %s", unit)
}

func cmdBrief([]string, io.Reader) error      { return notImplemented("U4 (brief renderer)") }
func cmdPriorities([]string, io.Reader) error { return notImplemented("U3 (priorities parser)") }

// cmdStatus prints the live sessions section on its own — the mid-day check,
// and the only way to read the registry without opening the JSON by hand.
//
// U4 still owns `brief`, which composes this block with priorities and
// handoffs. This lands early because U1 is not dogfoodable without it.
func cmdStatus(_ []string, stdout io.Writer) error {
	store, err := session.DefaultStore()
	if err != nil {
		return err
	}
	sessions, err := store.List()
	if err != nil {
		return err
	}
	return session.Render(stdout, sessions, time.Now().UTC())
}

// cmdHook dispatches the three hook events registered globally in
// ~/.claude/settings.json. The payload arrives on stdin as JSON.
//
// Runtime failures are logged to ~/.mindskein/hooks.log and swallowed. A hook
// that exits non-zero degrades the session it is meant to be quietly
// observing — for PreToolUse, exit code 2 blocks the tool call outright — and
// a missed status update is a far smaller problem than a broken editing
// session. Only a misconfigured invocation (missing or unknown event name) is
// reported as an error, because that one is a typo in settings.json and
// nothing will ever be recorded until it is fixed.
func cmdHook(args []string, stdin io.Reader) error {
	if len(args) == 0 {
		return fmt.Errorf("hook: expected one of %v", hook.Events)
	}
	event, err := hook.ParseEvent(args[0])
	if err != nil {
		return fmt.Errorf("hook: %w", err)
	}
	if err := handleHook(event, stdin); err != nil {
		logHookFailure(event, err)
	}
	return nil
}

func handleHook(event hook.Event, stdin io.Reader) error {
	payload, err := hook.Parse(stdin)
	if err != nil {
		return err
	}
	store, err := session.DefaultStore()
	if err != nil {
		return err
	}
	// os.Getppid is the process that spawned the hook. Command hooks run
	// through a shell, so this is best-effort provenance rather than a
	// reliable handle on the Claude process; nothing keys off it yet.
	_, err = hook.Handle(store, event, payload, time.Now().UTC(), os.Getppid())
	return err
}

// logHookFailure appends one line to ~/.mindskein/hooks.log. Every failure
// path here is itself ignored: if mindskein cannot even log, the hook still
// must not disturb the session.
func logHookFailure(event hook.Event, cause error) {
	home, err := session.Home()
	if err != nil {
		return
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(home, "hooks.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\t%s\t%v\n", time.Now().UTC().Format(time.RFC3339), event, cause)
}
