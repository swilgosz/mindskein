package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/swilgosz/mindskein/internal/reviews"
)

const marksFile = "prs-marks.json"

// ageMarks makes every decline and done mark d older than it is.
func ageMarks(t *testing.T, home string, d time.Duration) {
	t.Helper()
	path := filepath.Join(home, marksFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, m := range doc["marks"].([]any) {
		mark := m.(map[string]any)
		at, err := time.Parse(time.RFC3339Nano, mark["marked_at"].(string))
		if err != nil {
			t.Fatal(err)
		}
		mark["marked_at"] = at.Add(-d)
	}
	data, _ = json.Marshal(doc)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// markedRefs are the refs the marks file holds.
func markedRefs(t *testing.T, home string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, marksFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Marks []struct {
			Ref string `json:"ref"`
		} `json:"marks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, m := range doc.Marks {
		refs = append(refs, m.Ref)
	}
	return refs
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := mindskein(t, args...)
	if err != nil {
		t.Fatalf("mindskein %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func unreachable([]string) ([]byte, error) {
	return nil, &reviews.GHError{Err: errors.New("exit status 1"), Stderr: "error connecting to api.github.com"}
}

func TestPrsDecline(t *testing.T) {
	t.Run("a declined PR leaves its group and is listed under Declined, in the text and in --json", func(t *testing.T) {
		useGitHub(t, everyGroup())
		if out := mustRun(t, "prs", "decline", "acme/api#12"); !strings.Contains(out, "acme/api#12") {
			t.Errorf("decline printed %q, want it to name the PR", out)
		}
		r := prsJSON(t)
		if got := listed(r)["acme/api#12"]; got[0] != "declined" {
			t.Errorf("acme/api#12 is in %v, want declined", got)
		}
		var names []string
		for _, g := range r.Groups {
			names = append(names, g.Name)
		}
		if got := strings.Join(names, ", "); got != "Waiting on you, Yours, Watching, Declined, Done this week" {
			t.Errorf("groups = %s", got)
		}
		out := mustRun(t, "prs")
		inOrder(t, out, "Waiting on you (0)", "Watching (1)", "Declined (1)", "acme/api#12", "Done this week (1)")
	})

	t.Run("takes the PR's URL as well as owner/repo#N", func(t *testing.T) {
		useGitHub(t, everyGroup())
		mustRun(t, "prs", "decline", "https://github.com/acme/api/pull/12/files")
		mustRun(t, "prs", "decline", "https://github.com/globex/web/pull/40")
		got := listed(prsJSON(t))
		if got["acme/api#12"][0] != "declined" || got["globex/web#40"][0] != "declined" {
			t.Errorf("listed %v, want both declined", got)
		}
	})

	t.Run("a declined PR comes back after 7 days if still open", func(t *testing.T) {
		home := useGitHub(t, everyGroup())
		mustRun(t, "prs", "decline", "acme/api#12")
		ageMarks(t, home, 6*24*time.Hour)
		if got := listed(prsJSON(t))["acme/api#12"]; got[0] != "declined" {
			t.Errorf("after 6 days acme/api#12 is in %v, want still declined", got)
		}
		ageMarks(t, home, 2*24*time.Hour)
		if got := listed(prsJSON(t))["acme/api#12"]; got[0] != "waiting" {
			t.Errorf("after 8 days acme/api#12 is in %v, want back in waiting", got)
		}
	})

	t.Run("with spaces configured, Declined shows inside the PR's space", func(t *testing.T) {
		home := useGitHub(t, spread())
		reviewsConfig(t, home, twoSpaces)
		mustRun(t, "prs", "decline", "acme/api#1")
		if p := arranged(t).byRef()["acme/api#1"]; p.Group != "declined" || p.Space != "Work" {
			t.Errorf("acme/api#1 = %+v, want declined in Work", p)
		}
		inOrder(t, mustRun(t, "prs"), "Work", "Declined (1)", "acme/api#1", "Business")
	})

	t.Run("declining writes only local state and calls no gh at all", func(t *testing.T) {
		f := everyGroup()
		home := useGitHub(t, f)
		mustRun(t, "prs", "decline", "acme/api#12")
		if n := f.callCount(); n != 0 {
			t.Errorf("decline ran gh %d times: %v", n, f.calls)
		}
		if got := markedRefs(t, home); len(got) != 1 || got[0] != "acme/api#12" {
			t.Errorf("marks = %v, want acme/api#12", got)
		}
	})

	t.Run("a bad ref says in one line what a ref looks like, and marks nothing", func(t *testing.T) {
		home := useGitHub(t, everyGroup())
		for _, ref := range []string{"acme/api", "acme/api#x", "acme/api#0", "acme#3", "https://example.com/acme/api/pull/1", "https://github.com/acme/api/issues/1"} {
			for _, verb := range []string{"decline", "done"} {
				_, err := mindskein(t, "prs", verb, ref)
				if err == nil {
					t.Errorf("prs %s %q succeeded", verb, ref)
					continue
				}
				msg := err.Error()
				if strings.Contains(msg, "\n") || !strings.Contains(msg, "owner/repo#N") || !strings.Contains(msg, "https://github.com/") {
					t.Errorf("prs %s %q: %q, want one line saying what a ref looks like", verb, ref, msg)
				}
			}
		}
		if _, err := mindskein(t, "prs", "decline"); err == nil {
			t.Error("prs decline with no ref succeeded")
		}
		if got := markedRefs(t, home); len(got) != 0 {
			t.Errorf("marks = %v, want none", got)
		}
	})
}

func TestPrsDone(t *testing.T) {
	t.Run("a PR marked done leaves its group and is listed under Done this week as marked done", func(t *testing.T) {
		useGitHub(t, everyGroup())
		if out := mustRun(t, "prs", "done", "me/tool#3"); !strings.Contains(out, "me/tool#3") {
			t.Errorf("done printed %q, want it to name the PR", out)
		}
		if got := listed(prsJSON(t))["me/tool#3"]; got != [2]string{"done", "marked done"} {
			t.Errorf("me/tool#3 is %v, want done, marked done", got)
		}
		inOrder(t, mustRun(t, "prs"), "Yours (0)", "Done this week (2)", "me/tool#3")
	})

	t.Run("a done PR comes back when its head commit changes", func(t *testing.T) {
		f := everyGroup()
		f.mine[0].oid = "a1"
		home := useGitHub(t, f)
		mustRun(t, "prs", "done", "me/tool#3")
		ageCache(t, home, time.Hour)
		if got := listed(prsJSON(t))["me/tool#3"]; got[0] != "done" {
			t.Errorf("unchanged, me/tool#3 is in %v, want done", got)
		}
		f.mine[0].oid = "b2"
		ageCache(t, home, time.Hour)
		if got := listed(prsJSON(t))["me/tool#3"]; got[0] != "yours" {
			t.Errorf("after a new commit me/tool#3 is in %v, want yours", got)
		}
	})

	t.Run("a done PR comes back when a review is newly requested from you", func(t *testing.T) {
		f := everyGroup()
		home := useGitHub(t, f)
		mustRun(t, "prs", "done", "acme/api#12")
		mustRun(t, "prs", "done", "globex/web#40")
		f.waiting = append(f.waiting, f.pulls[0])
		ageCache(t, home, time.Hour)
		got := listed(prsJSON(t))
		if got["acme/api#12"][0] != "done" {
			t.Errorf("acme/api#12, requested when marked, is in %v, want done", got["acme/api#12"])
		}
		if got["globex/web#40"][0] != "waiting" {
			t.Errorf("globex/web#40, newly requested, is in %v, want waiting", got["globex/web#40"])
		}
	})

	t.Run("a done PR comes back when your review is requested again, with no new commits", func(t *testing.T) {
		f := everyGroup()
		home := useGitHub(t, f)
		mustRun(t, "prs", "done", "acme/api#12")
		ageMarks(t, home, time.Minute)
		f.waiting[0].requested = time.Now()
		ageCache(t, home, time.Hour)
		if got := listed(prsJSON(t))["acme/api#12"]; got[0] != "waiting" {
			t.Errorf("requested again, acme/api#12 is in %v, want waiting", got)
		}
	})

	t.Run("a done PR that has not changed stays out after a week, from Done this week too", func(t *testing.T) {
		home := useGitHub(t, everyGroup())
		mustRun(t, "prs", "done", "me/tool#3")
		ageMarks(t, home, 8*24*time.Hour)
		ageCache(t, home, time.Hour)
		if got, ok := listed(prsJSON(t))["me/tool#3"]; ok {
			t.Errorf("me/tool#3 is listed in %v, want it out", got)
		}
	})

	t.Run("a PR marked done that is not in the queue still shows in Done this week", func(t *testing.T) {
		f := everyGroup()
		f.pulls = append(f.pulls, fakePR{repo: "acme/web", number: 9, title: "Old fix", author: "ann", add: 3, updated: ago(48 * time.Hour)})
		useGitHub(t, f)
		mustRun(t, "prs", "done", "acme/web#9")
		r := prsJSON(t)
		var found *reviews.PR
		for _, g := range r.Groups {
			for i, p := range g.PRs {
				if p.Ref == "acme/web#9" {
					found = &g.PRs[i]
				}
			}
		}
		if found == nil || found.Group != "done" || found.Reason != "marked done" || found.Title != "Old fix" ||
			found.URL != "https://github.com/acme/web/pull/9" {
			t.Errorf("acme/web#9 = %+v, want it in Done this week with its title and URL", found)
		}
	})

	t.Run("marking done never writes to GitHub", func(t *testing.T) {
		f := everyGroup()
		f.pulls = append(f.pulls, fakePR{repo: "acme/web", number: 9, author: "ann", updated: ago(time.Hour)})
		useGitHub(t, f)
		mustRun(t, "prs", "done", "acme/web#9")
		mustRun(t, "prs", "done", "acme/api#12")
		for _, call := range f.calls {
			line := strings.Join(call, " ")
			read := line == "api user" || strings.HasPrefix(line, "api notifications?") ||
				(strings.HasPrefix(line, "api graphql -f query=query(") && !strings.Contains(line, "mutation"))
			if !read || strings.Contains(line, "-X") || strings.Contains(line, "--method") {
				t.Errorf("gh %s is not a read", line)
			}
		}
	})

	t.Run("a PR GitHub does not know is not marked", func(t *testing.T) {
		home := useGitHub(t, everyGroup())
		_, err := mindskein(t, "prs", "done", "acme/api#999")
		if err == nil || !strings.Contains(err.Error(), "acme/api#999") || strings.Contains(err.Error(), "\n") {
			t.Errorf("done acme/api#999: %v, want one line naming it", err)
		}
		if got := markedRefs(t, home); len(got) != 0 {
			t.Errorf("marks = %v, want none", got)
		}
	})

	t.Run("with GitHub unreachable and nothing cached, done still marks the PR, which comes back once it is updated on GitHub", func(t *testing.T) {
		f := everyGroup()
		home := useGitHub(t, f)
		f.fail = unreachable
		out, err := mindskein(t, "prs", "done", "acme/api#12")
		if err != nil {
			t.Fatalf("done offline: %v", err)
		}
		if !strings.Contains(out, "acme/api#12") || !strings.Contains(out, "cannot reach GitHub") {
			t.Errorf("done printed %q, want the PR and why GitHub was not asked", out)
		}
		f.fail = nil
		if got := listed(prsJSON(t))["acme/api#12"]; got != [2]string{"done", "marked done"} {
			t.Errorf("not updated since, acme/api#12 is %v, want done", got)
		}
		ageMarks(t, home, time.Minute)
		f.waiting[0].updated = time.Now()
		ageCache(t, home, time.Hour)
		if got := listed(prsJSON(t))["acme/api#12"]; got[0] != "waiting" {
			t.Errorf("updated since, acme/api#12 is in %v, want waiting", got)
		}
	})
}

func TestPrunePRMarks(t *testing.T) {
	t.Run("the marks live in prs-marks.json in mindskein's state dir", func(t *testing.T) {
		home := useGitHub(t, everyGroup())
		mustRun(t, "prs", "decline", "acme/api#12")
		mustRun(t, "prs", "done", "me/tool#3")
		if got := markedRefs(t, home); strings.Join(got, " ") != "acme/api#12 me/tool#3" {
			t.Errorf("marks = %v", got)
		}
	})

	t.Run("prune removes declines past their 7 days and keeps newer ones", func(t *testing.T) {
		home := useGitHub(t, everyGroup())
		mustRun(t, "prs", "decline", "acme/api#12")
		ageMarks(t, home, 8*24*time.Hour)
		mustRun(t, "prs", "decline", "globex/web#40")
		var stdout bytes.Buffer
		if err := cmdPrune(nil, &stdout, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if got := markedRefs(t, home); len(got) != 1 || got[0] != "globex/web#40" {
			t.Errorf("marks = %v, want only globex/web#40", got)
		}
		if !strings.Contains(stdout.String(), "removed 1 PR mark") {
			t.Errorf("prune printed\n%s\nwant it to account for the PR marks", stdout.String())
		}
	})

	t.Run("prune removes a done mark older than a week once the last fetch shows the PR changed, merged or closed, and keeps one it lists unchanged or does not list", func(t *testing.T) {
		f := everyGroup()
		f.waiting[0].oid = "a1"
		f.pulls = append(f.pulls, fakePR{repo: "acme/web", number: 9, author: "ann", updated: ago(time.Hour)})
		home := useGitHub(t, f)
		mustRun(t, "prs", "done", "me/tool#3")
		mustRun(t, "prs", "done", "acme/web#9")
		mustRun(t, "prs", "done", "acme/api#12")
		mustRun(t, "prs", "done", "initech/cli#7")
		ageMarks(t, home, 8*24*time.Hour)
		f.waiting[0].oid = "b2"
		ageCache(t, home, time.Hour)
		mustRun(t, "prs")
		if err := cmdPrune(nil, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if got := markedRefs(t, home); strings.Join(got, " ") != "me/tool#3 acme/web#9" {
			t.Errorf("marks = %v, want me/tool#3 and acme/web#9", got)
		}
	})

	t.Run("a done PR the last fetch does not list stays out once prune has run, until it changes", func(t *testing.T) {
		f := everyGroup()
		f.mine[0].ci = "FAILURE"
		home := useGitHub(t, f)
		mustRun(t, "prs", "done", "me/tool#3")
		ageMarks(t, home, 8*24*time.Hour)
		f.mine[0].ci = "SUCCESS"
		ageCache(t, home, time.Hour)
		mustRun(t, "prs")
		if err := cmdPrune(nil, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		f.mine[0].ci = "FAILURE"
		ageCache(t, home, time.Hour)
		if got, ok := listed(prsJSON(t))["me/tool#3"]; ok {
			t.Errorf("unchanged, me/tool#3 is listed in %v, want it out", got)
		}
	})

	t.Run("prune --dry-run says which marks would go and removes none", func(t *testing.T) {
		home := useGitHub(t, everyGroup())
		mustRun(t, "prs", "decline", "acme/api#12")
		ageMarks(t, home, 8*24*time.Hour)
		var stdout bytes.Buffer
		if err := cmdPrune([]string{"--dry-run"}, &stdout, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), "would remove 1 PR mark") {
			t.Errorf("prune printed\n%s", stdout.String())
		}
		if got := markedRefs(t, home); len(got) != 1 {
			t.Errorf("marks = %v, want the decline kept", got)
		}
	})
}
