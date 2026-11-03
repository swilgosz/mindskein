// Command mindskein answers the morning question: what are my priorities,
// what's running elsewhere, and where did we leave off?
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/swilgosz/mindskein/internal/brief"
	"github.com/swilgosz/mindskein/internal/config"
	"github.com/swilgosz/mindskein/internal/handoff"
	"github.com/swilgosz/mindskein/internal/hook"
	"github.com/swilgosz/mindskein/internal/install"
	"github.com/swilgosz/mindskein/internal/priorities"
	"github.com/swilgosz/mindskein/internal/reviews"
	"github.com/swilgosz/mindskein/internal/session"
	"github.com/swilgosz/mindskein/internal/text"
	insight "github.com/swilgosz/mindskein/internal/usage"
)

// version is overridden at build time with
// -ldflags "-X main.version=$(git describe --tags --always)".
var version = "dev"

// buildVersion is what the binary reports.
//
// Only the release pipeline passes the ldflag, and the first install line in
// the README is `go install …@latest`, which does not. Go records the module
// version for those, so read it back rather than telling most of the people
// who install this that they are running "dev".
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	return resolveVersion(version, info.Main.Version)
}

// resolveVersion prefers what was stamped in, then what the module system
// recorded. "(devel)" is what a plain `go build` leaves behind and says no
// more than "dev" does.
func resolveVersion(stamped, module string) string {
	if stamped != "dev" {
		return stamped
	}
	if module == "" || module == "(devel)" {
		return stamped
	}
	return module
}

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
		{"brief", "print priorities, live sessions, last handoffs and reviews", func(args []string, _ io.Reader) error {
			return cmdBrief(args, stdout, stderr)
		}},
		{"status", "print live sessions only (mid-day check)", func(args []string, _ io.Reader) error {
			return cmdStatus(args, stdout, stderr)
		}},
		{"priorities", "print the !1/!2 lines in your priorities file", func(args []string, _ io.Reader) error {
			return cmdPriorities(args, stdout, stderr)
		}},
		{"prs", "list the pull requests waiting on you, from every org; decline or done one", func(args []string, _ io.Reader) error {
			return cmdPrs(args, stdout, stderr)
		}},
		{"init", "register mindskein's hooks in Claude Code's settings.json", func(args []string, _ io.Reader) error {
			return cmdInit(args, stdout, stderr)
		}},
		{"prune", "delete session records, handoffs, usage records and PR marks past their retention horizons", func(args []string, _ io.Reader) error {
			return cmdPrune(args, stdout, stderr)
		}},
		{"usage", "report token spend and what to improve in your setup", func(args []string, _ io.Reader) error {
			return cmdUsage(args, stdout, stderr)
		}},
		{"resolve", "mark a usage finding fixed, hidden until it happens again", func(args []string, _ io.Reader) error {
			return cmdResolve(args, stdout, stderr)
		}},
		{"unresolve", "undo a resolve, so the finding is reported again", func(args []string, _ io.Reader) error {
			return cmdUnresolve(args, stdout, stderr)
		}},
		{"serve", "open the dashboard on 127.0.0.1: usage, overview, setup, health", func(args []string, _ io.Reader) error {
			return cmdServe(args, stdout, stderr)
		}},
		{"upgrade", "build the newest mindskein with Go and replace this binary", func(args []string, _ io.Reader) error {
			return cmdUpgrade(args, stdout, stderr)
		}},
		{"hook", "handle a Claude Code hook payload on stdin", func(args []string, stdin io.Reader) error {
			return cmdHook(args, stdin, stderr)
		}},
		{"version", "print the mindskein version", func([]string, io.Reader) error {
			fmt.Fprintln(stdout, buildVersion())
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
	fmt.Fprintf(w, "mindskein %s — one command for the morning question.\n\n", buildVersion())
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

// cmdBrief prints the whole morning question in one page: what matters, what
// is running, where each thread was left, and which pull requests wait on you.
//
// The four sections come from independent sources, so each is composed as a
// section rather than rendered inline: an unreadable priorities file, an
// unreachable store or GitHub out of reach costs one line of explanation, not
// the other blocks.
func cmdBrief(args []string, stdout, stderr io.Writer) error {
	cfg, cfgErr := loadConfig()
	hideAfter := cfg.Status.HideAfter

	fs := flag.NewFlagSet("brief", flag.ContinueOnError)
	fs.SetOutput(stderr)
	all := fs.Bool("all", false, "widen every section: the backlog, ended sessions, every workstream")
	asJSON := fs.Bool("json", false, "print the brief as JSON")
	fs.Var(&hideAfter, "hide-after", "hide sessions quiet for longer than this (0 keeps every one)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if cfgErr != nil {
		fmt.Fprintf(stderr, "mindskein: %v — using defaults\n", cfgErr)
	}

	b := buildBrief(cfg, *all, hideAfter.Duration(), time.Now().UTC(), stderr)
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(b)
	}
	return brief.Render(stdout,
		brief.Section{Heading: priorities.Heading, Render: func(w io.Writer) error {
			if b.Priorities.Error != "" {
				return errors.New(b.Priorities.Error)
			}
			return priorities.Print(w, b.Priorities.View, 0)
		}},
		brief.Section{Heading: session.Heading, Render: func(w io.Writer) error {
			if b.Sessions.Error != "" {
				return errors.New(b.Sessions.Error)
			}
			return session.Print(w, b.Sessions.View)
		}},
		brief.Section{Heading: handoff.Heading, Render: func(w io.Writer) error {
			if b.Handoffs.Error != "" {
				return errors.New(b.Handoffs.Error)
			}
			return handoff.Print(w, b.Handoffs.View)
		}},
		brief.Section{Heading: reviews.BriefHeading, Render: func(w io.Writer) error {
			return reviews.PrintSummary(w, b.Reviews.Summary)
		}},
	)
}

// briefReport is the brief decided but not printed. The terminal and the JSON
// both print it, so they cannot disagree about a row.
//
// The blocks come from independent sources, so each carries its own error:
// an unreadable priorities file, an unreachable store or GitHub out of reach
// costs one line of explanation, not the other blocks.
type briefReport struct {
	GeneratedAt time.Time       `json:"generated_at"`
	Priorities  prioritiesBlock `json:"priorities"`
	Sessions    sessionsBlock   `json:"sessions"`
	Handoffs    handoffsBlock   `json:"handoffs"`
	Reviews     reviewsBlock    `json:"reviews"`
}

type reviewsBlock struct {
	Heading string `json:"heading"`
	reviews.Summary
}

type prioritiesBlock struct {
	Heading string `json:"heading"`
	Error   string `json:"error,omitempty"`
	priorities.View
}

type sessionsBlock struct {
	Heading string `json:"heading"`
	Error   string `json:"error,omitempty"`
	session.View
}

type handoffsBlock struct {
	Heading string `json:"heading"`
	Error   string `json:"error,omitempty"`
	handoff.View
}

// buildBrief decides every block. Warnings that cost no block, such as a PR
// cache that could not be saved, go to warn.
func buildBrief(cfg config.Config, all bool, hideAfter time.Duration, now time.Time, warn io.Writer) briefReport {
	b := briefReport{
		GeneratedAt: now,
		Priorities:  prioritiesBlock{Heading: priorities.Heading},
		Sessions:    sessionsBlock{Heading: session.Heading},
		Handoffs:    handoffsBlock{Heading: handoff.Heading},
		Reviews:     reviewsBlock{Heading: reviews.BriefHeading},
	}
	b.Reviews.Summary = reviews.Summarize(readQueue(cfg, now, warn), now)
	if v, err := priorityView(cfg, all); err != nil {
		b.Priorities.Error = err.Error()
	} else {
		b.Priorities.View = v
	}

	// Read once and use twice: the same handoffs name the sessions and fill
	// the last block. A failure here is not fatal to either — the sessions
	// block falls back to folder names, and the last block explains itself.
	handoffs, handoffErr := loadHandoffs()
	if v, err := sessionView(labelsFrom(handoffs), all, hideAfter, now); err != nil {
		b.Sessions.Error = err.Error()
	} else {
		b.Sessions.View = v
	}
	if handoffErr != nil {
		b.Handoffs.Error = handoffErr.Error()
	} else {
		b.Handoffs.View = handoff.BuildView(handoffs, handoff.RenderOptions{ShowAll: all})
	}
	return b
}

// cmdPriorities prints the PRIORITIES block: what the configured note calls the
// current focus, and what is queued behind it.
//
// Every way of having nothing to show — no config, no priorities file, no
// priority lines in it — prints the section with one line saying which, because a
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

	return renderPriorities(stdout, cfg, *all)
}

// renderPriorities is the PRIORITIES block printed alone.
func renderPriorities(w io.Writer, cfg config.Config, all bool) error {
	v, err := priorityView(cfg, all)
	if err != nil {
		return err
	}
	return priorities.Print(w, v, 0)
}

// priorityView is the PRIORITIES block decided, shared by the brief and by the
// command that prints it alone.
//
// Every way of having nothing to show — no config, no priorities file — is a
// hint in place of the rows, not an error.
func priorityView(cfg config.Config, all bool) (priorities.View, error) {
	path := cfg.Priorities.Path()
	if path == "" {
		return priorities.View{Hint: "no priorities file configured — set priorities.file in " + configFile()}, nil
	}
	list, err := priorities.ParseFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return priorities.View{Hint: "no priorities file at " + path}, nil
	}
	if err != nil {
		return priorities.View{}, err
	}

	levels := priorities.Shown
	if all {
		levels = priorities.All
	}
	return priorities.BuildView(list, priorities.RenderOptions{Levels: levels}), nil
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

	return renderSessions(stdout, sessionLabels(), *all, hideAfter.Duration())
}

// renderSessions is the LIVE SESSIONS block printed alone.
func renderSessions(w io.Writer, labels map[string]string, all bool, hideAfter time.Duration) error {
	v, err := sessionView(labels, all, hideAfter, time.Now().UTC())
	if err != nil {
		return err
	}
	return session.Print(w, v)
}

// sessionView is the LIVE SESSIONS block decided, shared by the brief and by
// the command that prints it alone.
func sessionView(labels map[string]string, all bool, hideAfter time.Duration, now time.Time) (session.View, error) {
	store, err := session.DefaultStore()
	if err != nil {
		return session.View{}, err
	}
	sessions, err := store.List()
	if err != nil {
		return session.View{}, err
	}
	return session.BuildView(sessions, now, session.RenderOptions{
		Labels:    labels,
		ShowAll:   all,
		HideAfter: hideAfter,
		Probe:     session.SystemProbe(),
	}), nil
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
	handoffs, err := loadHandoffs()
	if err != nil {
		return nil
	}
	return labelsFrom(handoffs)
}

func loadHandoffs() ([]*handoff.Handoff, error) {
	store, err := handoff.DefaultStore()
	if err != nil {
		return nil, err
	}
	return store.List()
}

func labelsFrom(handoffs []*handoff.Handoff) map[string]string {
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
func cmdHook(args []string, stdin io.Reader, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("hook: expected one of %v", hook.Events)
	}
	event, err := hook.ParseEvent(args[0])
	if err != nil {
		return fmt.Errorf("hook: %w", err)
	}
	if err := safely(func() error { return hookRunner(event, stdin) }); err != nil {
		logHookFailure(event, err, stderr)
	}
	return nil
}

// hookRunner is the body of a hook, indirected so a test can substitute one
// that panics. The property being protected is the process exit status, and
// nothing short of a real panic in a real process demonstrates it.
var hookRunner = handleHook

// safely runs fn and turns a panic into an ordinary error.
//
// Without this the panic reaches the runtime, which exits 2 — the one status
// PreToolUse reads as "block this tool call". A crash in the observer would
// stop the work it was only supposed to watch, and the user would see a
// blocked tool with no explanation anywhere.
//
// It cannot catch a runtime fatal error: a concurrent map write or a stack
// overflow bypasses recover and still exits 2. Those are bugs to prevent
// rather than absorb.
func safely(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v | %s", r, oneLineStack())
		}
	}()
	return fn()
}

// maxStack caps the flattened stack. The log is read a line at a time, so the
// frames that matter are the first few; the rest is the test harness or the
// runtime.
const maxStack = 2000

func oneLineStack() string {
	stack := text.OneLine(string(debug.Stack()))
	if len(stack) > maxStack {
		stack = stack[:maxStack] + " ..."
	}
	return stack
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
	handoffErr := recordHandoff(sess, payload, now)
	usageErr := recordUsage(payload)
	cfg, err := loadConfig()
	if err != nil {
		// A broken config must not stop the sweep from running on defaults,
		// the same way it does not stop the brief from printing.
		cfg = config.Defaults()
	}
	return errors.Join(handoffErr, usageErr, pruneDaily(cfg, now))
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

// recordUsage brings the session's usage summary up to date. It reads only
// what was appended since the last turn, so it stays cheap however long the
// session runs.
func recordUsage(payload *hook.Payload) error {
	if payload.TranscriptPath == "" {
		return nil
	}
	store, err := insight.DefaultStore()
	if err != nil {
		return err
	}
	_, err = store.Capture(payload.TranscriptPath, os.Getenv("MINDSKEIN_PROJECT"))
	return err
}

// logHookFailure appends one line to ~/.mindskein/hooks.log.
//
// When even that fails — an unwritable state directory is the case that
// matters — it says so on stderr instead. A hook exits 0 either way, and a
// hook that both does nothing and reports nothing is indistinguishable from
// one that works.
func logHookFailure(event hook.Event, cause error, stderr io.Writer) {
	line := fmt.Sprintf("%s\t%s\t%v\n", time.Now().UTC().Format(time.RFC3339), event, cause)
	if err := appendHookLog(line); err != nil {
		fmt.Fprintf(stderr, "mindskein: %s hook failed: %v (and could not write the log: %v)\n", event, cause, err)
	}
}

func appendHookLog(line string) error {
	home, err := session.Home()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(home, "hooks.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.WriteString(f, line)
	return err
}

// cmdPrune deletes state past the retention horizons.
//
// It is the only command that removes anything, so it says what rule it
// applied and offers --dry-run to answer the question without acting on it.
func cmdPrune(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "report what would be removed without removing it")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(stderr, "mindskein: %v\n", err)
	}
	res, err := prune(cfg, time.Now().UTC(), *dryRun)
	for _, r := range res {
		fmt.Fprintln(stdout, r.Summary())
	}
	return err
}

// prune sweeps every store and returns one result per store. A failure on one
// does not stop the others: they are independent directories, and a permission
// problem on handoffs is no reason to leave session records uncollected.
func prune(cfg config.Config, now time.Time, dryRun bool) ([]*session.PruneResult, error) {
	var results []*session.PruneResult
	var firstErr error

	if store, err := session.DefaultStore(); err != nil {
		firstErr = err
	} else if res, err := store.Prune(now, time.Duration(cfg.Retention.Sessions), dryRun); err != nil {
		firstErr = err
	} else {
		results = append(results, res)
	}

	if store, err := handoff.DefaultStore(); err != nil {
		if firstErr == nil {
			firstErr = err
		}
	} else if res, err := store.Prune(now, time.Duration(cfg.Retention.Handoffs), dryRun); err != nil {
		if firstErr == nil {
			firstErr = err
		}
	} else {
		results = append(results, res)
	}

	if store, err := insight.DefaultStore(); err != nil {
		if firstErr == nil {
			firstErr = err
		}
	} else if res, err := store.Prune(now, time.Duration(cfg.Retention.Usage), dryRun); err != nil {
		if firstErr == nil {
			firstErr = err
		}
	} else {
		results = append(results, res)
	}

	if store, err := reviews.DefaultMarks(); err != nil {
		if firstErr == nil {
			firstErr = err
		}
	} else if res, err := store.Prune(now, dryRun); err != nil {
		if firstErr == nil {
			firstErr = err
		}
	} else {
		results = append(results, res)
	}

	// All are returned. Whatever was collected is worth reporting, and the
	// failure is still a failure: swallowing it because the other store
	// happened to succeed would let a directory quietly stop being pruned.
	return results, firstErr
}

// pruneStamp is the file recording the last sweep. Without it the hook would
// walk every store on every turn.
const pruneStamp = "last-prune"

// pruneDaily sweeps at most once a day, from the Stop hook.
//
// Stop is the right place: it already pays for a transcript read, so one extra
// stat is nothing, and unlike SessionEnd it cannot be missed by a process that
// dies hard. PreToolUse would be the wrong place — it runs on every single
// tool call and must stay out of the way.
//
// Errors are returned to the caller, which logs and swallows them. Collecting
// old files is never worth disturbing a live session over.
func pruneDaily(cfg config.Config, now time.Time) error {
	home, err := session.Home()
	if err != nil {
		return err
	}
	stamp := filepath.Join(home, pruneStamp)
	if info, err := os.Stat(stamp); err == nil && now.Sub(info.ModTime()) < 24*time.Hour {
		return nil
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	// Stamp first. If the sweep fails, the next attempt is tomorrow rather
	// than on every turn for the rest of the day.
	if err := os.WriteFile(stamp, []byte(now.Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return err
	}
	_, err = prune(cfg, now, false)
	return err
}

// defaultTimeout is the per-hook limit written into settings.json. Every hook
// here does a little file IO and, once a day, a directory sweep; five seconds
// is generous for that and short enough to bound a pathological case.
const defaultTimeout = 5

// cmdInit registers the hooks in Claude Code's settings.json, or removes them.
//
// It exists because the alternative is asking someone to hand-edit the file
// that controls their whole setup, and because deleting the binary without an
// uninstall leaves four hooks pointing at a command that is no longer there.
func cmdInit(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	uninstall := fs.Bool("uninstall", false, "remove mindskein's hooks instead of installing them")
	dryRun := fs.Bool("dry-run", false, "report what would change without changing it")
	settings := fs.String("settings", "", "path to settings.json (default ~/.claude/settings.json)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	path := *settings
	if path == "" {
		var err error
		if path, err = defaultSettingsPath(); err != nil {
			return err
		}
	}

	if *uninstall {
		rep, err := install.Unregister(path, *dryRun)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, rep)
		fmt.Fprintln(stdout, "state in ~/.mindskein was left in place; remove it by hand if you want it gone")
		return nil
	}

	binary, err := binaryPath()
	if err != nil {
		return err
	}
	rep, err := install.Register(path, install.Options{
		Binary:  binary,
		Timeout: defaultTimeout,
		Async:   true,
	}, *dryRun)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, rep)

	if !*dryRun {
		created, err := install.EnsureConfig(configFile())
		if err != nil {
			return err
		}
		if created {
			fmt.Fprintf(stdout, "wrote a starter config at %s\n", configFile())
		}
	}
	fmt.Fprintln(stdout, "restart Claude Code for the hooks to take effect")
	return nil
}

func defaultSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// binaryPath is the absolute path to this executable, with symlinks resolved.
//
// A hook runs with no PATH guarantee, so the command has to be absolute. The
// symlink is resolved because a Homebrew install puts the real binary in the
// Cellar and links it into the prefix; recording the link would break the
// hooks on the next upgrade.
func binaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating the mindskein binary: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe, nil
	}
	return resolved, nil
}

// cmdUsage reports spend and findings from the usage store. --backfill first
// summarises transcripts the hooks never saw, from the default config roots
// plus any named as arguments.
func cmdUsage(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("usage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	days := fs.Int("days", 30, "report the last N days; 0 is all time")
	since := fs.String("since", "", "report from this day, YYYY-MM-DD, instead of the last N days")
	until := fs.String("until", "", "end the report on this day, YYYY-MM-DD, instead of today")
	all := fs.Bool("all", false, "print every finding and all of its evidence")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	backfill := fs.Bool("backfill", false, "summarise transcripts already on disk before reporting")
	root := fs.String("root", "", "report one Claude config directory only, e.g. ~/.claude-personal")
	includeResolved := fs.Bool("include-resolved", false, "report findings as if nothing were resolved")
	if err := fs.Parse(args); err != nil {
		return err
	}
	window, err := insight.ParseWindow(*days, *since, *until)
	if err != nil {
		return err
	}
	store, err := insight.DefaultStore()
	if err != nil {
		return err
	}
	if *backfill {
		start := time.Now()
		roots := store.Roots(fs.Args()...)
		cfg, _ := loadConfig()
		var since time.Time
		if h := time.Duration(cfg.Retention.Usage); h > 0 {
			since = start.Add(-h)
		}
		res, err := store.Backfill(roots, since)
		fmt.Fprintf(stderr, "backfill: %d transcripts read, %d unchanged, %d older than retention, %s, from %s\n",
			res.Parsed, res.Skipped, res.Expired, time.Since(start).Round(time.Millisecond), strings.Join(roots, ", "))
		if err != nil {
			fmt.Fprintf(stderr, "mindskein: backfill: %v\n", err)
		}
	}
	file, err := insight.DefaultResolved()
	if err != nil {
		return err
	}
	var resolved []insight.Resolution
	if !*includeResolved {
		if resolved, err = file.Load(); err != nil {
			return err
		}
	}
	report, err := buildUsage(store, time.Now(), window, expandRoot(*root), resolved)
	if err != nil {
		return err
	}
	if err := file.Settle(report.Resolved); err != nil {
		fmt.Fprintf(stderr, "mindskein: dropping cleared resolutions: %v\n", err)
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	return insight.Render(stdout, report, *all)
}

func expandRoot(p string) string {
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.Clean(p)
}

// buildUsage builds the report every usage surface prints, its findings
// judged against resolved.
func buildUsage(store *insight.Store, now time.Time, window insight.Window, root string, resolved []insight.Resolution) (*insight.Report, error) {
	records, inv, err := loadUsage(store, root)
	if err != nil {
		return nil, err
	}
	return insight.BuildResolved(records, inv, now, window, store.Loc, resolved), nil
}

// loadUsage reads the records and scans the inventory a report is built
// from, of one config root when root is set.
func loadUsage(store *insight.Store, root string) ([]*insight.Record, []insight.Inventory, error) {
	records, err := store.List()
	if err != nil {
		return nil, nil, err
	}
	cwds := map[string][]string{}
	seen := map[string]bool{}
	for _, r := range records {
		if r.File.CWD != "" && !seen[r.Root+r.File.CWD] {
			seen[r.Root+r.File.CWD] = true
			cwds[r.Root] = append(cwds[r.Root], r.File.CWD)
		}
	}
	var inv []insight.Inventory
	for _, root := range store.Roots() {
		inv = append(inv, insight.Scan(root, cwds[root]))
	}
	if root != "" {
		records, inv = insight.ForRoot(records, inv, root)
	}
	return records, inv, nil
}
