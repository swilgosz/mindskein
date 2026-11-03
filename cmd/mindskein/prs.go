package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/swilgosz/mindskein/internal/reviews"
	"github.com/swilgosz/mindskein/internal/session"
)

// ghRun and ghTimeout are how prs reaches GitHub. Tests replace them, so no
// run of the suite calls gh.
var (
	ghRun     reviews.Runner = reviews.GH
	ghTimeout                = reviews.DefaultTimeout
)

// cmdPrs prints the pull requests waiting on you, from every org.
//
// GitHub being out of reach is never an error: the command prints the
// cached queue with its age, or one line saying what is wrong and how to fix
// it, and exits cleanly.
func cmdPrs(args []string, stdout, stderr io.Writer) error {
	cfg, cfgErr := loadConfig()

	fs := flag.NewFlagSet("prs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the queue as JSON")
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

	var cachePath string
	if home, err := session.Home(); err == nil {
		cachePath = filepath.Join(home, reviews.CacheFile)
	}
	now := time.Now().UTC()
	report, err := reviews.Read(context.Background(), reviews.Options{
		Run:       ghRun,
		Timeout:   ghTimeout,
		CacheAge:  cfg.Reviews.CacheAge.Duration(),
		Marker:    cfg.Reviews.AgentMarker,
		CachePath: cachePath,
		Now:       now,
	})
	if err != nil {
		fmt.Fprintf(stderr, "mindskein: saving the PR cache: %v\n", err)
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(report)
	}
	return reviews.Print(stdout, report, now)
}
