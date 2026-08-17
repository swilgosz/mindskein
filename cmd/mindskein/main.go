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
)

// version is overridden at build time with
// -ldflags "-X main.version=$(git describe --tags --always)".
var version = "dev"

// command is one top-level subcommand of the CLI.
type command struct {
	name    string
	summary string
	run     func(args []string) error
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "mindskein: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	commands := []command{
		{"brief", "print priorities, live sessions and last handoffs", cmdBrief},
		{"status", "print live sessions only (mid-day check)", cmdStatus},
		{"priorities", "print the !1/!2 lines parsed out of plan.md", cmdPriorities},
		{"hook", "handle a Claude Code hook payload on stdin", cmdHook},
		{"version", "print the mindskein version", func([]string) error {
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
			return c.run(fs.Args()[1:])
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

func cmdBrief([]string) error      { return notImplemented("U4 (brief renderer)") }
func cmdStatus([]string) error     { return notImplemented("U4 (brief renderer)") }
func cmdPriorities([]string) error { return notImplemented("U3 (priorities parser)") }

// cmdHook dispatches the three hook events registered globally in
// ~/.claude/settings.json. The payload arrives on stdin as JSON.
func cmdHook(args []string) error {
	events := []string{"pre-tool-use", "notification", "stop"}
	if len(args) == 0 {
		return fmt.Errorf("hook: expected one of %v", events)
	}
	for _, e := range events {
		if e == args[0] {
			return notImplemented("U1 (hook capture + session registry)")
		}
	}
	return fmt.Errorf("hook: unknown event %q, expected one of %v", args[0], events)
}
