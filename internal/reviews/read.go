package reviews

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CacheFile is the name of the last good queue in the mindskein home.
const CacheFile = "prs.json"

// DefaultTimeout bounds each call to gh.
const DefaultTimeout = 20 * time.Second

// Options is everything a read of the queue depends on.
type Options struct {
	Run       Runner
	Timeout   time.Duration
	CacheAge  time.Duration
	Marker    string
	CachePath string
	Now       time.Time
}

// Report is the queue as a reader gets it: fresh, from the cache, or not at
// all, with the one line that says why.
type Report struct {
	// Error is the one line saying why there is no queue to show.
	Error string `json:"error,omitempty"`

	// Cached says the queue was read from the cache, CacheAge how old it is.
	Cached   bool   `json:"cached"`
	CacheAge string `json:"cache_age,omitempty"`

	// RefreshError says why a cached queue is shown in place of a fresh one.
	RefreshError string `json:"refresh_error,omitempty"`

	Queue
}

type cacheDoc struct {
	// Marker is the agent marker the reasons were judged with; a queue
	// judged with another one is not reused.
	Marker string `json:"agent_marker"`
	Queue  Queue  `json:"queue"`
}

// Read returns the queue, from the cache while it is fresher than
// opts.CacheAge and from GitHub otherwise. A refresh that fails falls back
// to the cache, however old. The error is only a cache that could not be
// written; the report is usable either way.
func Read(ctx context.Context, opts Options) (Report, error) {
	cached, haveCache := loadCache(opts.CachePath)
	if haveCache && cached.Marker == opts.Marker {
		if age := opts.Now.Sub(cached.Queue.FetchedAt); age >= 0 && age < opts.CacheAge {
			return fromCache(cached.Queue, opts.Now, ""), nil
		}
	}

	q, err := Fetch(ctx, opts.Run, opts.Timeout, opts.Marker, opts.Now)
	if err != nil {
		if haveCache {
			return fromCache(cached.Queue, opts.Now, Explain(err)), nil
		}
		return Report{Error: Explain(err)}, nil
	}
	return Report{Queue: q}, saveCache(opts.CachePath, cacheDoc{Marker: opts.Marker, Queue: q})
}

func fromCache(q Queue, now time.Time, refreshErr string) Report {
	return Report{Cached: true, CacheAge: Age(now.Sub(q.FetchedAt)), RefreshError: refreshErr, Queue: q}
}

// loadCache reads the last good queue. Anything unreadable is treated as no
// cache: it is only ever a fallback.
func loadCache(path string) (cacheDoc, bool) {
	if path == "" {
		return cacheDoc{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cacheDoc{}, false
	}
	var doc cacheDoc
	if err := json.Unmarshal(data, &doc); err != nil || doc.Queue.FetchedAt.IsZero() {
		return cacheDoc{}, false
	}
	return doc, true
}

func saveCache(path string, doc cacheDoc) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-prs-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Age spells an elapsed time at the resolution a queue is read at.
func Age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
}
