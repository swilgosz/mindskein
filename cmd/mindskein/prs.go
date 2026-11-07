package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/swilgosz/mindskein/internal/config"
	"github.com/swilgosz/mindskein/internal/reviews"
	"github.com/swilgosz/mindskein/internal/session"
)

// ghRun and ghTimeout are how prs reaches GitHub. Tests replace them, so no
// run of the suite calls gh.
var (
	ghRun     reviews.Runner = reviews.GH
	ghTimeout                = reviews.DefaultTimeout
)

// cmdPrs prints the pull requests waiting on you, from every org, or with
// decline or done marks one so it stops showing.
//
// GitHub being out of reach is never an error: the command prints the
// cached queue with its age, or one line saying what is wrong and how to fix
// it, and exits cleanly.
func cmdPrs(args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && (args[0] == "decline" || args[0] == "done") {
		return cmdPrsMark(args[0], args[1:], stdout, stderr)
	}
	cfg, cfgErr := loadConfig()

	fs := flag.NewFlagSet("prs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: mindskein prs [--json] [--space <name>] | mindskein prs decline|done <owner/repo#N | URL>")
		fs.PrintDefaults()
	}
	asJSON := fs.Bool("json", false, "print the queue as JSON")
	spaceName := fs.String("space", "", "list only the PRs of this Life Space")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("prs takes no arguments, got %q", fs.Args())
	}
	if cfgErr != nil {
		fmt.Fprintf(stderr, "mindskein: %v — using defaults\n", cfgErr)
	}
	var space string
	if *spaceName != "" {
		var err error
		if space, err = reviews.SpaceNamed(cfg.Reviews, *spaceName); err != nil {
			return err
		}
	}

	now := time.Now().UTC()
	report := readQueue(cfg, now, stderr)
	if report.Error == "" && space != "" {
		report.Queue = reviews.Only(report.Queue, space)
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(report)
	}
	return reviews.Print(stdout, report, now)
}

// queueMu lets one read of the queue run at a time. A refresh takes seconds,
// and a dashboard request arriving during one would otherwise start its own;
// waiting for it finds the cache fresh instead.
var queueMu sync.Mutex

// readQueue is the queue as every reader shows it: read cache-first, with
// the decline and done marks taken in and the user's rules applied. What
// goes wrong on the way is warned about on warn, never returned: GitHub
// being out of reach is the report's one line.
func readQueue(cfg config.Config, now time.Time, warn io.Writer) reviews.Report {
	queueMu.Lock()
	// Freshness is judged on a clock read under the lock: now may predate the
	// fetch this request waited on, and a cache stamped after its reader's clock
	// is not fresh, so the wait would end in a second fetch.
	report, err := reviews.Read(context.Background(), readOptions(cfg, time.Now().UTC()))
	queueMu.Unlock()
	if err != nil {
		fmt.Fprintf(warn, "mindskein: saving the PR cache: %v\n", err)
	}
	return arrangeQueue(report, cfg, now, warn)
}

// readCachedQueue is the queue from the cache whatever its age, so it waits
// neither on GitHub nor on a fetch in flight. With no cache to show, it reads
// as readQueue does.
func readCachedQueue(cfg config.Config, now time.Time, warn io.Writer) reviews.Report {
	report, ok := reviews.ReadCached(readOptions(cfg, now))
	if !ok {
		return readQueue(cfg, now, warn)
	}
	return arrangeQueue(report, cfg, now, warn)
}

func arrangeQueue(report reviews.Report, cfg config.Config, now time.Time, warn io.Writer) reviews.Report {
	if report.Error != "" {
		return report
	}
	var marks []reviews.Mark
	if store, err := reviews.DefaultMarks(); err == nil {
		if marks, err = store.Load(); err != nil {
			fmt.Fprintf(warn, "mindskein: reading the PR marks: %v\n", err)
		}
	}
	report.Queue = reviews.Apply(report.Queue, marks, now)
	report.Queue = reviews.Arrange(report.Queue, cfg.Reviews, now)
	return report
}

func readOptions(cfg config.Config, now time.Time) reviews.Options {
	var cachePath string
	if home, err := session.Home(); err == nil {
		cachePath = filepath.Join(home, reviews.CacheFile)
	}
	return reviews.Options{
		Run:       ghRun,
		Timeout:   ghTimeout,
		CacheAge:  cfg.Reviews.CacheAge.Duration(),
		Marker:    cfg.Reviews.AgentMarker,
		CachePath: cachePath,
		Now:       now,
	}
}

// cmdPrsMark declines a pull request or marks it done. Either writes only
// the local marks file: declining asks GitHub nothing, and done only reads
// the head commit to tell later when the pull request changed.
func cmdPrsMark(verb string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("prs "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintf(stderr, "Usage: mindskein prs %s <owner/repo#N | URL>\n", verb) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("prs %s: want one pull request, owner/repo#N or its URL", verb)
	}
	ref, err := reviews.ParseRef(fs.Arg(0))
	if err != nil {
		return err
	}
	store, err := reviews.DefaultMarks()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	if verb == "decline" {
		if err := store.Put(reviews.Mark{Ref: ref, Kind: reviews.MarkDeclined, MarkedAt: now}); err != nil {
			return err
		}
		back := now.Add(reviews.DeclineWindow).Local().Format("Mon Jan 2")
		fmt.Fprintf(stdout, "declined %s: out of the queue until %s, back then if still open\n", ref, back)
		return nil
	}

	cfg, cfgErr := loadConfig()
	if cfgErr != nil {
		fmt.Fprintf(stderr, "mindskein: %v — using defaults\n", cfgErr)
	}
	mark, offline, err := reviews.NewDoneMark(context.Background(), readOptions(cfg, now), ref)
	if err != nil {
		return err
	}
	if err := store.Put(mark); err != nil {
		return err
	}
	if offline != "" {
		fmt.Fprintf(stdout, "marked %s done: out of the queue until it is next updated on GitHub (%s)\n", mark.Ref, offline)
		return nil
	}
	fmt.Fprintf(stdout, "marked %s done: out of the queue until it gets new commits or a new review request\n", mark.Ref)
	return nil
}
