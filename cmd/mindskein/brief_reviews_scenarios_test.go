package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/swilgosz/mindskein/internal/handoff"
	"github.com/swilgosz/mindskein/internal/reviews"
)

const reviewsHeading = "REVIEWS"

const briefSpaces = `
[[reviews.spaces]]
name = "Work"
repos = ["acme"]

[[reviews.spaces]]
name = "Business"
repos = ["me"]

[[reviews.spaces]]
name = "Life"
repos = ["family"]
`

// morningQueue is one review requested from you, two PRs of yours blocked
// on you, a watched PR and a team request, spread over Work, Business and
// Other.
func morningQueue() *fakeGitHub {
	return &fakeGitHub{
		waiting: []fakePR{
			{repo: "acme/api", number: 412, title: "Rate limiter", author: "ann", add: 30, del: 8, updated: ago(2*24*time.Hour + time.Hour)},
			{repo: "globex/api", number: 7, title: "Old cleanup", author: "bob", add: 400, updated: ago(10 * 24 * time.Hour), asked: "@globex/core"},
		},
		mine: []fakePR{
			{repo: "acme/web", number: 88, title: "New header", author: "me", add: 12, updated: ago(3 * 24 * time.Hour), decision: "APPROVED"},
			{repo: "me/tool", number: 13, title: "Faster startup", author: "me", add: 5, updated: ago(time.Hour), draft: true},
			{repo: "me/tool", number: 14, title: "Waiting on review", author: "me", add: 5, updated: ago(time.Hour)},
		},
		notified: []fakeNotification{{reason: "mention", pr: fakePR{repo: "initech/cli", number: 9}}},
		pulls:    []fakePR{{repo: "initech/cli", number: 9, title: "Docs", author: "cat", add: 3, updated: ago(5 * time.Hour)}},
		closed:   []fakePR{{repo: "acme/web", number: 80, title: "Release", author: "me", add: 1, updated: ago(time.Hour), state: "MERGED", closed: ago(time.Hour), merged: ago(time.Hour)}},
	}
}

// briefReviewsJSON is the REVIEWS block as brief --json and /api/reviews
// carry it.
type briefReviewsJSON struct {
	Heading      string `json:"heading"`
	Error        string `json:"error"`
	Cached       bool   `json:"cached"`
	CacheAge     string `json:"cache_age"`
	RefreshError string `json:"refresh_error"`
	Counts       []struct {
		Space  string `json:"space"`
		Groups []struct {
			ID       string `json:"id"`
			Label    string `json:"label"`
			Count    int    `json:"count"`
			Notified bool   `json:"notified"`
		} `json:"groups"`
	} `json:"counts"`
	Top []struct {
		Ref string `json:"ref"`
		URL string `json:"url"`
		Why string `json:"why"`
	} `json:"top"`
}

func briefReviews(t *testing.T) briefReviewsJSON {
	t.Helper()
	var doc struct {
		Reviews briefReviewsJSON `json:"reviews"`
	}
	if err := json.Unmarshal([]byte(runBrief(t, "--json")), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Reviews
}

// countsLines is the counts the JSON carries, written as the text brief
// writes them: one space per line.
func (r briefReviewsJSON) countsLines() []string {
	var lines []string
	for _, c := range r.Counts {
		var parts []string
		for _, g := range c.Groups {
			part := strconv.Itoa(g.Count) + " " + g.Label
			if g.Notified {
				part += " " + reviews.Bell
			}
			parts = append(parts, part)
		}
		lines = append(lines, strings.TrimSpace(c.Space+" "+strings.Join(parts, " · ")))
	}
	return lines
}

// squash collapses the column padding of a printed line.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

// reviewsSection is the REVIEWS block's body. Its heading is matched at the
// start of a line: a test's temporary path can hold the word too.
func reviewsSection(out string) string {
	return strings.TrimRight(section(out, "\n"+reviewsHeading), "\n")
}

func TestBriefReviews(t *testing.T) {
	t.Run("REVIEWS is the fourth section, after WHERE WE LEFT OFF", func(t *testing.T) {
		home := useGitHub(t, morningQueue())
		reviewsConfig(t, home, briefSpaces)
		out := runBrief(t)
		left, rev := strings.Index(out, "\n"+handoff.Heading), strings.Index(out, "\n"+reviewsHeading)
		if left < 0 || rev < left {
			t.Fatalf("brief printed\n%s\nwant REVIEWS after WHERE WE LEFT OFF", out)
		}
	})

	t.Run("counts the open PRs per configured space, in config order with Other last, skipping empty spaces and marking a notification with a bell", func(t *testing.T) {
		home := useGitHub(t, morningQueue())
		reviewsConfig(t, home, briefSpaces)
		var lines []string
		for _, l := range strings.Split(reviewsSection(runBrief(t)), "\n") {
			lines = append(lines, squash(l))
		}
		want := []string{
			"Work 1 waiting on you · 1 yours blocked on you",
			"Business 1 yours blocked on you",
			"Other 1 waiting on you · 1 watching " + reviews.Bell,
		}
		if len(lines) < 3 || strings.Join(lines[:3], "\n") != strings.Join(want, "\n") {
			t.Errorf("REVIEWS counts =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
		}
	})

	t.Run("then the top 3 open PRs across every space in rank order, each owner/repo#N with a one-line reason and its URL", func(t *testing.T) {
		home := useGitHub(t, morningQueue())
		reviewsConfig(t, home, briefSpaces)
		var top []string
		for _, l := range strings.Split(reviewsSection(runBrief(t)), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "→") {
				top = append(top, squash(l))
			}
		}
		want := []string{
			"→ acme/api#412 review requested, 38 lines, 2d https://github.com/acme/api/pull/412",
			"→ acme/web#88 yours, approved: merge it https://github.com/acme/web/pull/88",
			"→ me/tool#13 yours, agent draft: review it https://github.com/me/tool/pull/13",
		}
		if strings.Join(top, "\n") != strings.Join(want, "\n") {
			t.Errorf("REVIEWS top =\n%s\nwant\n%s", strings.Join(top, "\n"), strings.Join(want, "\n"))
		}
	})

	t.Run("with no spaces configured, one counts line names no space", func(t *testing.T) {
		useGitHub(t, morningQueue())
		lines := strings.Split(reviewsSection(runBrief(t)), "\n")
		if got := squash(lines[0]); got != "2 waiting on you · 2 yours blocked on you · 1 watching "+reviews.Bell {
			t.Errorf("counts line = %q, want the open groups with no space name", got)
		}
		if len(lines) < 2 || !strings.HasPrefix(strings.TrimSpace(lines[1]), "→") {
			t.Errorf("REVIEWS =\n%s\nwant the top 3 straight after the one counts line", strings.Join(lines, "\n"))
		}
	})

	t.Run("counts and top 3 leave out declined and done PRs and what skip rules drop, as prs does", func(t *testing.T) {
		home := useGitHub(t, morningQueue())
		reviewsConfig(t, home, "[[reviews.skip]]\nrepo = \"initech\"\n")
		mustRun(t, "prs", "decline", "acme/api#412")
		mustRun(t, "prs", "done", "acme/web#88")
		r := briefReviews(t)
		if got := strings.Join(r.countsLines(), "\n"); got != "1 waiting on you · 1 yours blocked on you" {
			t.Errorf("counts = %q, want only globex/api#7 and me/tool#13", got)
		}
		var refs []string
		for _, p := range r.Top {
			refs = append(refs, p.Ref)
		}
		if got := strings.Join(refs, " "); got != "me/tool#13 globex/api#7" {
			t.Errorf("top = %s, want me/tool#13 globex/api#7", got)
		}
	})

	t.Run("with nothing open, says so in one line", func(t *testing.T) {
		useGitHub(t, &fakeGitHub{closed: morningQueue().closed})
		if got := reviewsSection(runBrief(t)); strings.Count(got, "\n") != 0 || !strings.Contains(got, "nothing waiting on you") {
			t.Errorf("REVIEWS = %q, want one line saying nothing waits on you", got)
		}
	})

	t.Run("brief --json carries the same counts and top 3, each with its ref, URL and reason", func(t *testing.T) {
		home := useGitHub(t, morningQueue())
		reviewsConfig(t, home, briefSpaces)
		text := reviewsSection(runBrief(t))
		r := briefReviews(t)
		if r.Heading != reviewsHeading || len(r.Counts) != 3 || len(r.Top) != 3 {
			t.Fatalf("reviews = %+v, want the heading, 3 spaces and 3 PRs", r)
		}
		for _, l := range r.countsLines() {
			if !strings.Contains(squash(text), l) {
				t.Errorf("counts %q are in the JSON but not in the text:\n%s", l, text)
			}
		}
		for _, p := range r.Top {
			if !strings.Contains(squash(text), p.Ref+" "+p.Why+" "+p.URL) {
				t.Errorf("%s %q %s is in the JSON but not in the text:\n%s", p.Ref, p.Why, p.URL, text)
			}
		}
	})

	t.Run("the dashboard's /api/reviews carries the same REVIEWS block as brief --json", func(t *testing.T) {
		home := useGitHub(t, morningQueue())
		reviewsConfig(t, home, briefSpaces)
		mustRun(t, "prs")
		want := briefReviews(t)
		cfg, err := loadConfig()
		if err != nil {
			t.Fatal(err)
		}
		var got briefReviewsJSON
		apiGet(t, serveHandler(cfg), "/api/reviews", &got)
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("/api/reviews =\n%s\nwant\n%s", gotJSON, wantJSON)
		}
	})

	t.Run("the dashboard's /api/brief answers without asking GitHub, so a slow gh cannot hold up the other sections", func(t *testing.T) {
		f := morningQueue()
		useGitHub(t, f)
		cfg, err := loadConfig()
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]json.RawMessage
		apiGet(t, serveHandler(cfg), "/api/brief", &got)
		if n := f.callCount(); n != 0 {
			t.Errorf("/api/brief called gh %d times, want none", n)
		}
		if _, ok := got["reviews"]; ok {
			t.Errorf("/api/brief carries reviews, want them left to /api/reviews")
		}
		for _, k := range []string{"priorities", "sessions", "handoffs"} {
			if _, ok := got[k]; !ok {
				t.Errorf("/api/brief has no %s", k)
			}
		}
	})
}

func TestBriefReviewsFailSoft(t *testing.T) {
	// others is the brief without its REVIEWS block.
	others := func(t *testing.T, out string) string {
		t.Helper()
		i := strings.Index(out, "\n"+reviewsHeading)
		if i < 0 {
			t.Fatalf("brief printed no REVIEWS:\n%s", out)
		}
		return out[:i]
	}
	failures := []struct {
		name string
		fail func([]string) ([]byte, error)
		want string
	}{
		{"signed out", func([]string) ([]byte, error) {
			return nil, &reviews.GHError{Err: errors.New("exit status 4"), Stderr: "To get started with GitHub CLI, please run:  gh auth login"}
		}, "not signed in to GitHub — run `gh auth login`"},
		{"offline", unreachable, "cannot reach GitHub — check the network"},
	}
	for _, c := range failures {
		t.Run(c.name+", REVIEWS is one line saying so with the fix, and the other three sections are unchanged", func(t *testing.T) {
			home := briefHome(t)
			healthy := others(t, runBrief(t))
			if err := os.Remove(filepath.Join(home, reviews.CacheFile)); err != nil {
				t.Fatal(err)
			}
			ghRun = (&fakeGitHub{fail: c.fail}).run
			out := runBrief(t)
			if got := reviewsSection(out); strings.TrimSpace(got) != c.want {
				t.Errorf("REVIEWS = %q, want the one line %q", got, c.want)
			}
			if others(t, out) != healthy {
				t.Errorf("the other sections changed:\n%s\nwant\n%s", others(t, out), healthy)
			}
			if r := briefReviews(t); r.Error != c.want {
				t.Errorf("JSON error = %q, want %q", r.Error, c.want)
			}
		})
	}

	t.Run("with gh missing, REVIEWS says to install it, and the brief still prints", func(t *testing.T) {
		briefHome(t)
		ghRun = reviews.GH
		t.Setenv("PATH", t.TempDir())
		got := reviewsSection(runBrief(t))
		if strings.Count(strings.TrimSpace(got), "\n") != 0 || !strings.Contains(got, "gh is not installed") || !strings.Contains(got, "https://cli.github.com") {
			t.Errorf("REVIEWS = %q, want one line saying to install gh", got)
		}
	})

	t.Run("a GitHub call that hangs is cut off by the same timeout prs uses", func(t *testing.T) {
		briefHome(t)
		previous := ghTimeout
		ghTimeout = 50 * time.Millisecond
		t.Cleanup(func() { ghTimeout = previous })
		ghRun = func(ctx context.Context, args ...string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		start := time.Now()
		if got := reviewsSection(runBrief(t)); !strings.Contains(got, "check the network") {
			t.Errorf("REVIEWS = %q, want one line saying to check the network", got)
		}
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("brief took %v, want it cut off by the timeout", took)
		}
	})

	t.Run("a refresh that fails shows the cached result with its age", func(t *testing.T) {
		f := morningQueue()
		home := useGitHub(t, f)
		mustRun(t, "prs")
		ageCache(t, home, 3*time.Hour)
		f.fail = unreachable
		got := reviewsSection(runBrief(t))
		if !strings.Contains(got, "cached 3h ago, refresh failed: cannot reach GitHub — check the network") || !strings.Contains(got, "acme/api#412") {
			t.Errorf("REVIEWS =\n%s\nwant the cached counts and top 3 with the cache's age and why", got)
		}
		if r := briefReviews(t); !r.Cached || r.CacheAge != "3h" || len(r.Top) != 3 {
			t.Errorf("JSON = %+v, want the cached queue with its age", r)
		}
	})
}

func TestBriefReviewsCache(t *testing.T) {
	t.Run("brief reads the PR cache prs fills, and calls no gh while it is fresh", func(t *testing.T) {
		f := morningQueue()
		useGitHub(t, f)
		mustRun(t, "prs")
		calls := f.callCount()
		got := reviewsSection(runBrief(t))
		if f.callCount() != calls {
			t.Errorf("a fresh cache still called gh %d more times", f.callCount()-calls)
		}
		if !strings.Contains(got, "cached <1m ago") || !strings.Contains(got, "acme/api#412") {
			t.Errorf("REVIEWS =\n%s\nwant the cached queue with its age", got)
		}
	})

	t.Run("a brief on a stale cache fetches once, as prs does, and leaves the cache fresh for prs", func(t *testing.T) {
		f := morningQueue()
		useGitHub(t, f)
		mustRun(t, "prs")
		perFetch := f.callCount()

		home := useGitHub(t, f)
		if _, err := os.Stat(filepath.Join(home, reviews.CacheFile)); !os.IsNotExist(err) {
			t.Fatalf("a new home already holds a cache: %v", err)
		}
		before := f.callCount()
		runBrief(t)
		if got := f.callCount() - before; got != perFetch {
			t.Errorf("brief called gh %d times, want the %d of one fetch", got, perFetch)
		}
		before = f.callCount()
		if out := mustRun(t, "prs"); f.callCount() != before || !strings.Contains(out, "cached <1m ago") {
			t.Errorf("prs after brief called gh %d times, want it served from the cache brief wrote", f.callCount()-before)
		}
	})

	t.Run("dashboard requests arriving during a fetch share it rather than fetching again", func(t *testing.T) {
		f := morningQueue()
		useGitHub(t, f)
		mustRun(t, "prs")
		perFetch := f.callCount()

		useGitHub(t, f)
		ghRun = func(ctx context.Context, args ...string) ([]byte, error) {
			time.Sleep(20 * time.Millisecond)
			return f.run(ctx, args...)
		}
		cfg, err := loadConfig()
		if err != nil {
			t.Fatal(err)
		}
		h := serveHandler(cfg)
		before := f.callCount()
		done := make(chan int, 3)
		for range 3 {
			go func() {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/reviews", nil))
				done <- rec.Code
			}()
		}
		for range 3 {
			if code := <-done; code != http.StatusOK {
				t.Errorf("/api/reviews = %d", code)
			}
		}
		if got := f.callCount() - before; got != perFetch {
			t.Errorf("three requests called gh %d times, want the %d of one fetch", got, perFetch)
		}
	})
}
