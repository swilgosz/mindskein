package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	insight "github.com/swilgosz/mindskein/internal/usage"
)

// cmdResolve marks a finding of usage, or one item of it, as fixed. The
// report hides it until it happens again. Only what the current report
// lists can be resolved, so a typo is an error rather than a resolution
// that hides nothing.
func cmdResolve(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: mindskein resolve <id>[:<key>] [--note \"…\"] | mindskein resolve --list")
		fs.PrintDefaults()
	}
	note := fs.String("note", "", "why it is resolved, shown by --list")
	list := fs.Bool("list", false, "print what is resolved, when and why")
	days := fs.Int("days", 30, "check the id against the report of the last N days; 0 is all time")
	since := fs.String("since", "", "check against the report from this day, YYYY-MM-DD")
	until := fs.String("until", "", "check against the report ending on this day, YYYY-MM-DD")
	root := fs.String("root", "", "check against the report of one Claude config directory only")
	refs, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	file, err := insight.DefaultResolved()
	if err != nil {
		return err
	}
	if *list {
		if len(refs) > 0 {
			return errors.New("resolve --list takes no id")
		}
		return printResolved(stdout, file)
	}
	if len(refs) != 1 {
		fs.Usage()
		return errors.New("resolve: want one <id>[:<key>]")
	}
	id, key, err := insight.ParseRef(refs[0])
	if err != nil {
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
	now := time.Now()
	report, err := buildUsage(store, now, window, expandRoot(*root), nil)
	if err != nil {
		return err
	}
	if err := insight.Validate(report, id, key); err != nil {
		return err
	}
	res := insight.Resolution{ID: id, Key: key, ResolvedAt: now.UTC(), Note: *note}
	if err := file.Resolve(res); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "resolved %s: usage hides it until it happens again\nundo with: mindskein unresolve %s\n",
		res.Ref(), shellQuote(res.Ref()))
	return nil
}

// cmdUnresolve removes a resolution, so the finding is reported again.
func cmdUnresolve(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("unresolve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, "Usage: mindskein unresolve <id>[:<key>]") }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("unresolve: want one <id>[:<key>]")
	}
	id, key, err := insight.ParseRef(fs.Arg(0))
	if err != nil {
		return err
	}
	file, err := insight.DefaultResolved()
	if err != nil {
		return err
	}
	gone, err := file.Unresolve(id, key)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "unresolved %s: usage reports it again\n", gone.Ref())
	return nil
}

func printResolved(w io.Writer, file *insight.Resolved) error {
	all, err := file.Load()
	if err != nil {
		return err
	}
	if len(all) == 0 {
		_, err := fmt.Fprintln(w, "nothing resolved")
		return err
	}
	var b strings.Builder
	b.WriteString("RESOLVED\n")
	for _, r := range all {
		fmt.Fprintf(&b, "  %s  resolved %s", r.Ref(), r.ResolvedAt.Local().Format("2006-01-02 15:04"))
		if r.Note != "" {
			b.WriteString("  · " + r.Note)
		}
		b.WriteString("\n")
	}
	_, err = io.WriteString(w, b.String())
	return err
}

func loadResolved() ([]insight.Resolution, error) {
	file, err := insight.DefaultResolved()
	if err != nil {
		return nil, err
	}
	return file.Load()
}

// parseInterspersed parses flags placed before, between or after the
// positional arguments, which the flag package stops at, and returns the
// positionals.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return rest, nil
		}
		if n := len(args) - fs.NArg(); n > 0 && args[n-1] == "--" {
			return append(rest, fs.Args()...), nil
		}
		rest = append(rest, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// shellQuote quotes s for a POSIX shell when it holds anything but plain
// characters; hook keys carry spaces, quotes and $.
func shellQuote(s string) string {
	plain := strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:/@+=,", r))
	}) < 0
	if plain && s != "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
