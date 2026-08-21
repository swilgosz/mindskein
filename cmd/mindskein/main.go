// Command mindskein answers the morning question: what are my priorities,
// what's running elsewhere, and where did we leave off?
//
// Scope and roadmap live in the vault, not in this repo.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/swilgosz/mindskein/internal/config"
	"github.com/swilgosz/mindskein/internal/handoff"
	"github.com/swilgosz/mindskein/internal/hook"
	"github.com/swilgosz/mindskein/internal/priorities"
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
			return cmdStatus(args, stdout, stderr)
		}},
		{"priorities", "print the !1/!2 lines parsed out of plan.md", func(args []string, _ io.Reader) error {
			return cmdPriorities(args, stdout, stderr)
		}},
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

// errNotImplemented keeps a command dispatchable before it does anything, so
// the usage text and the command set stay honest.
var errNotImplemented = errors.New("not implemented yet")

func cmdBrief([]string, io.Reader) error { return errNotImplemented }

// cmdPriorities prints the PRIORITIES block: what the vault's plan.md calls the
// current focus, and what is queued behind it.
//
// Every way of having nothing to show — no config, no plan file, no priority
// lines in it — prints the section with one line saying which, because a
// morning brief that stack-traces is worse than one that is briefly empty.
func cmdPriorities(args []string, stdout, stderr io.Writer) error {
	cfg, cfgErr := loadConfig()

	flags := flag.NewFlagSet("priorities", flag.ContinueOnError)
	flags.SetOutput(stderr)
	all := flags.Bool("all", false, "include the !3 backlog")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if cfgErr != nil {
		fmt.Fprintf(stderr, "mindskein: %v\n", cfgErr)
	}

	path := cfg.Vault.PlanPath()
	if path == "" {
		return priorities.Hint(stdout,
			"no plan configured — set vault.path and vault.plan in "+configFile())
	}
	plan, err := priorities.ParseFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return priorities.Hint(stdout, "no plan at "+path)
	}
	if err != nil {
		return err
	}

	levels := priorities.Shown
	if *all {
		levels = priorities.All
	}
	return priorities.Render(stdout, plan, priorities.RenderOptions{Levels: levels})
}

// cmdStatus prints the live sessions block on its own: the mid-day check, and
// the only way to read the registry without opening the JSON by hand.
func cmdStatus(args []string, stdout, stderr io.Writer) error {
	cfg, cfgErr := loadConfig()
	hideAfter := cfg.Status.HideAfter

	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	all := fs.Bool("all", false, "include sessions that have ended or aged out")
	fs.Var(&hideAfter, "hide-after", "hide sessions quiet for longer than this (0 keeps every one)")
	if err := fs.Parse(args); err != nil {
		// -h is a request, not a failure: flag has already printed the usage.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if cfgErr != nil {
		// Worth a line on stderr rather than a silent fallback: a setting that
		// appears to do nothing is harder to debug than one that complains.
		// Never fatal, though — a typo in the config must not cost the listing.
		fmt.Fprintf(stderr, "mindskein: %v — using defaults\n", cfgErr)
	}

	store, err := session.DefaultStore()
	if err != nil {
		return err
	}
	sessions, err := store.List()
	if err != nil {
		return err
	}

	return session.Render(stdout, sessions, time.Now().UTC(), session.RenderOptions{
		Labels:    sessionLabels(),
		ShowAll:   *all,
		HideAfter: hideAfter.Duration(),
	})
}

// loadConfig reads ~/.mindskein/config.toml, which sits beside the session
// registry. It always returns a usable configuration; the error is advisory.
func loadConfig() (config.Config, error) {
	return config.Load(configFile())
}

// configFile names the file to edit. It is printed in hints, so it falls back
// to the path as a reader would write it rather than to an error: a message
// about where to put a setting must not itself fail.
func configFile() string {
	home, err := session.Home()
	if err != nil {
		return "~/.mindskein/config.toml"
	}
	return filepath.Join(home, "config.toml")
}

// sessionLabels names sessions by their handoff title. The registry cannot
// supply this — a title comes from the transcript, which only Stop reads — so
// the two stores are joined here, on session id. A session that has not yet
// completed a turn simply has no entry.
//
// A failure to read handoffs is not worth reporting: the folder name is a
// usable fallback, and status must still print.
func sessionLabels() map[string]string {
	store, err := handoff.DefaultStore()
	if err != nil {
		return nil
	}
	handoffs, err := store.List()
	if err != nil {
		return nil
	}
	labels := make(map[string]string, len(handoffs))
	for _, h := range handoffs {
		// Named, not Label: Label falls back to the folder, which would fill
		// this map for every session and leave the renderer unable to tell a
		// real title from the folder it already prints in the next column.
		if label := h.Named(); label != "" {
			labels[h.SessionID] = label
		}
	}
	return labels
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
	now := time.Now().UTC()
	sess, err := hook.Handle(store, event, payload, now, os.Getppid())
	if err != nil || sess == nil {
		return err
	}
	if event != hook.EventStop {
		return nil
	}
	return recordHandoff(sess, payload, now)
}

// recordHandoff writes the session handoff once a turn completes.
//
// Only Stop does this. It is the one event that fires where "where did we leave
// off" has an answer, and the only one that can afford the transcript read:
// PreToolUse runs on every single tool call and must never parse a 13 MB file.
func recordHandoff(sess *session.Session, payload *hook.Payload, now time.Time) error {
	store, err := handoff.DefaultStore()
	if err != nil {
		return err
	}
	// MINDSKEIN_PROJECT lets a session opt into a named workstream spanning
	// folders and sessions — export it before launching Claude. Empty is the
	// normal case, and the reader falls back to the session title.
	_, err = handoff.Record(store, sess, payload.TranscriptPath, os.Getenv("MINDSKEIN_PROJECT"), now)
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
