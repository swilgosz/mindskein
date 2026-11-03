package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/swilgosz/mindskein/internal/reviews"
)

// act is one comment or review on a fake pull request.
type act struct {
	by  string
	at  time.Time
	bot bool
}

// fakePR is a pull request as the fake GitHub holds it.
type fakePR struct {
	repo      string
	number    int
	title     string
	author    string
	add, del  int
	updated   time.Time
	draft     bool
	state     string
	body      string
	head      string
	decision  string
	ci        string
	committed time.Time
	comment   *act
	botNote   *act
	review    *act
	closed    time.Time
	merged    time.Time
	reviewed  time.Time
}

func (p fakePR) ref() string { return fmt.Sprintf("%s#%d", p.repo, p.number) }

func (p fakePR) url() string { return fmt.Sprintf("https://github.com/%s/pull/%d", p.repo, p.number) }

func stamp(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func activities(field string, acts ...*act) map[string]any {
	nodes := []any{}
	for _, a := range acts {
		if a == nil {
			continue
		}
		kind := "User"
		if a.bot {
			kind = "Bot"
		}
		nodes = append(nodes, map[string]any{"author": map[string]any{"login": a.by, "__typename": kind}, field: stamp(a.at)})
	}
	return map[string]any{"nodes": nodes}
}

// node is the pull request as GitHub's GraphQL API returns it.
func (p fakePR) node() map[string]any {
	owner, name, _ := strings.Cut(p.repo, "/")
	state := p.state
	if state == "" {
		state = "OPEN"
	}
	var rollup any
	if p.ci != "" {
		rollup = map[string]any{"state": p.ci}
	}
	var decision any
	if p.decision != "" {
		decision = p.decision
	}
	committed := p.committed
	if committed.IsZero() {
		committed = p.updated
	}
	mine := []any{}
	if !p.reviewed.IsZero() {
		mine = append(mine, map[string]any{"state": "COMMENTED", "submittedAt": stamp(p.reviewed)})
	}
	return map[string]any{
		"number": p.number, "title": p.title, "url": p.url(), "isDraft": p.draft, "state": state,
		"body": p.body, "headRefName": p.head, "additions": p.add, "deletions": p.del,
		"updatedAt": stamp(p.updated), "mergedAt": stamp(p.merged), "closedAt": stamp(p.closed),
		"author":         map[string]any{"login": p.author},
		"repository":     map[string]any{"name": name, "owner": map[string]any{"login": owner}},
		"reviewDecision": decision,
		"commits": map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{
			"committedDate": stamp(committed), "statusCheckRollup": rollup}}}},
		"comments": activities("createdAt", p.comment, p.botNote),
		"reviews":  activities("submittedAt", p.review),
		"mine":     map[string]any{"nodes": mine},
	}
}

// fakeGitHub answers gh the way GitHub would for one signed-in user, and
// records every call.
type fakeGitHub struct {
	mu       sync.Mutex
	login    string
	waiting  []fakePR
	mine     []fakePR
	closed   []fakePR
	reviewed []fakePR
	notified []fakeNotification
	pulls    []fakePR
	pageSize int
	fail     func(args []string) ([]byte, error)
	calls    [][]string
}

type fakeNotification struct {
	reason string
	pr     fakePR
}

func vars(args []string) map[string]string {
	v := map[string]string{}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-f" || args[i] == "-F" {
			key, val, _ := strings.Cut(args[i+1], "=")
			v[key] = val
			i++
		}
	}
	return v
}

func (f *fakeGitHub) run(ctx context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, args)
	fail := f.fail
	f.mu.Unlock()
	if fail != nil {
		return fail(args)
	}
	if len(args) < 2 || args[0] != "api" {
		return nil, fmt.Errorf("unexpected gh %q", args)
	}
	switch {
	case args[1] == "user":
		return json.Marshal(map[string]any{"login": f.login})
	case strings.HasPrefix(args[1], "notifications"):
		list := []any{}
		for _, n := range f.notified {
			owner, name, _ := strings.Cut(n.pr.repo, "/")
			list = append(list, map[string]any{
				"reason": n.reason,
				"subject": map[string]any{"type": "PullRequest",
					"url": fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, name, n.pr.number)},
			})
		}
		return json.Marshal(list)
	case args[1] == "graphql":
		return f.graphql(vars(args))
	}
	return nil, fmt.Errorf("unexpected gh %q", args)
}

func (f *fakeGitHub) graphql(v map[string]string) ([]byte, error) {
	if !strings.HasPrefix(v["query"], "query(") {
		return nil, fmt.Errorf("not a query: %s", v["query"])
	}
	q, ok := v["q"]
	if !ok {
		data, missing := map[string]any{}, []any{}
		for i := 0; ; i++ {
			alias := "p" + strconv.Itoa(i)
			owner, ok := v["o"+strconv.Itoa(i)]
			if !ok {
				break
			}
			ref := owner + "/" + v["n"+strconv.Itoa(i)] + "#" + v["r"+strconv.Itoa(i)]
			data[alias] = nil
			for _, p := range f.pulls {
				if p.ref() == ref {
					data[alias] = map[string]any{"pullRequest": p.node()}
				}
			}
			if data[alias] == nil {
				missing = append(missing, map[string]any{"type": "NOT_FOUND", "path": []string{alias},
					"message": "Could not resolve to a Repository with the name '" + ref + "'."})
			}
		}
		out, err := json.Marshal(map[string]any{"data": data, "errors": missing})
		if len(missing) > 0 {
			return out, &reviews.GHError{Err: errors.New("exit status 1"), Stderr: "gh: Could not resolve to a Repository"}
		}
		return out, err
	}

	var found []fakePR
	switch {
	case q == "is:pr is:open review-requested:@me":
		found = f.waiting
	case q == "is:pr is:open author:@me":
		found = f.mine
	case strings.HasPrefix(q, "is:pr is:closed author:@me closed:>="):
		found = f.closed
	case strings.HasPrefix(q, "is:pr reviewed-by:@me -author:@me updated:>="):
		if v["me"] != f.login {
			return nil, fmt.Errorf("reviews asked for %q, signed in as %q", v["me"], f.login)
		}
		found = f.reviewed
	default:
		return nil, fmt.Errorf("unexpected search %q", q)
	}
	start, _ := strconv.Atoi(v["after"])
	end := len(found)
	if f.pageSize > 0 {
		end = min(start+f.pageSize, len(found))
	}
	nodes := []any{}
	for _, p := range found[start:end] {
		nodes = append(nodes, p.node())
	}
	return json.Marshal(map[string]any{"data": map[string]any{"search": map[string]any{
		"pageInfo": map[string]any{"hasNextPage": end < len(found), "endCursor": strconv.Itoa(end)},
		"nodes":    nodes,
	}}})
}

func (f *fakeGitHub) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// useGitHub points prs at f, with its own state directory, and returns that
// directory.
func useGitHub(t *testing.T, f *fakeGitHub) string {
	t.Helper()
	home := pruneHome(t)
	if f.login == "" {
		f.login = "me"
	}
	previous := ghRun
	ghRun = f.run
	t.Cleanup(func() { ghRun = previous })
	return home
}

func reviewsConfig(t *testing.T, home, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[reviews]\n"+body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func prsJSON(t *testing.T) reviews.Report {
	t.Helper()
	out, err := mindskein(t, "prs", "--json")
	if err != nil {
		t.Fatalf("prs --json: %v", err)
	}
	var r reviews.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("prs --json printed %q: %v", out, err)
	}
	return r
}

// listed maps each listed ref to the group it is in and its reason.
func listed(r reviews.Report) map[string][2]string {
	m := map[string][2]string{}
	for _, g := range r.Groups {
		for _, p := range g.PRs {
			m[p.Ref] = [2]string{g.ID, p.Reason}
		}
	}
	return m
}

func ago(d time.Duration) time.Time { return time.Now().Add(-d) }

// everyGroup is one pull request in each group, from four different orgs.
func everyGroup() *fakeGitHub {
	return &fakeGitHub{
		waiting:  []fakePR{{repo: "acme/api", number: 12, title: "Add a rate limiter", author: "ann", add: 100, del: 20, updated: ago(2 * 24 * time.Hour)}},
		mine:     []fakePR{{repo: "me/tool", number: 3, title: "Faster startup", author: "me", add: 5, del: 4, updated: ago(3 * time.Hour), ci: "FAILURE"}},
		notified: []fakeNotification{{reason: "mention", pr: fakePR{repo: "globex/web", number: 40}}},
		pulls:    []fakePR{{repo: "globex/web", number: 40, title: "New header", author: "bob", add: 30, updated: ago(time.Hour)}},
		closed:   []fakePR{{repo: "initech/cli", number: 7, title: "Release 1.2", author: "me", add: 1, updated: ago(24 * time.Hour), state: "MERGED", closed: ago(24 * time.Hour), merged: ago(24 * time.Hour)}},
	}
}

func TestPrs(t *testing.T) {
	t.Run("lists PRs from every org, your own included, under Waiting on you, Yours, Watching and Done this week", func(t *testing.T) {
		useGitHub(t, everyGroup())
		out, err := mindskein(t, "prs")
		if err != nil {
			t.Fatal(err)
		}
		order := []string{"Waiting on you (1)", "acme/api#12", "Yours (1)", "me/tool#3", "Watching (1)", "globex/web#40", "Done this week (1)", "initech/cli#7"}
		at := 0
		for _, want := range order {
			i := strings.Index(out[at:], want)
			if i < 0 {
				t.Fatalf("prs printed\n%s\nwant %q after the one before it", out, want)
			}
			at += i
		}
	})

	t.Run("prints each PR on one line as owner/repo#N with its title, changed lines, age, reason and URL", func(t *testing.T) {
		useGitHub(t, everyGroup())
		out, _ := mindskein(t, "prs")
		var line string
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "acme/api#12") {
				line = l
			}
		}
		for _, want := range []string{"Add a rate limiter", "120 lines", "2d", "review requested", "https://github.com/acme/api/pull/12"} {
			if !strings.Contains(line, want) {
				t.Errorf("line %q lacks %q", line, want)
			}
		}
	})

	t.Run("--json prints the same groups, each PR with its ref, URL, owner, repo, number, title, author, changed lines, updated time, draft flag, group and reason", func(t *testing.T) {
		f := everyGroup()
		f.mine[0].draft = true
		useGitHub(t, f)
		out, err := mindskein(t, "prs", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Login  string `json:"login"`
			Groups []struct {
				ID   string           `json:"id"`
				Name string           `json:"name"`
				PRs  []map[string]any `json:"prs"`
			} `json:"groups"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, g := range doc.Groups {
			names = append(names, g.Name)
		}
		if got := strings.Join(names, ", "); got != "Waiting on you, Yours, Watching, Done this week" {
			t.Errorf("groups = %s", got)
		}
		yours := doc.Groups[1].PRs[0]
		want := map[string]any{
			"ref": "me/tool#3", "url": "https://github.com/me/tool/pull/3", "owner": "me", "repo": "tool",
			"number": 3.0, "title": "Faster startup", "author": "me", "changed_lines": 9.0, "draft": true,
			"group": "yours", "reason": "failing CI: fix",
		}
		for k, v := range want {
			if yours[k] != v {
				t.Errorf("%s = %v, want %v", k, yours[k], v)
			}
		}
		if at, err := time.Parse(time.RFC3339, fmt.Sprint(yours["updated_at"])); err != nil || time.Since(at) < 2*time.Hour {
			t.Errorf("updated_at = %v, want three hours ago", yours["updated_at"])
		}
	})

	t.Run("an empty group says so in one line", func(t *testing.T) {
		useGitHub(t, &fakeGitHub{})
		out, _ := mindskein(t, "prs")
		if !strings.Contains(out, "Watching (0)\n  none\n") {
			t.Errorf("prs printed\n%s\nwant an empty group to say none", out)
		}
		r := prsJSON(t)
		if len(r.Groups) != 4 || r.Groups[2].PRs == nil || len(r.Groups[2].PRs) != 0 {
			t.Errorf("JSON groups = %+v, want all four, empty ones as []", r.Groups)
		}
	})

	t.Run("a PR of yours with green CI and nothing new is not listed", func(t *testing.T) {
		useGitHub(t, &fakeGitHub{mine: []fakePR{
			{repo: "me/tool", number: 1, author: "me", updated: ago(time.Hour), ci: "SUCCESS",
				comment: &act{by: "ann", at: ago(3 * time.Hour)}, committed: ago(2 * time.Hour)},
			{repo: "me/tool", number: 2, author: "me", updated: ago(time.Hour), ci: "PENDING", decision: "REVIEW_REQUIRED"},
		}})
		if got := listed(prsJSON(t)); len(got) != 0 {
			t.Errorf("listed %v, want nothing: both wait on someone else", got)
		}
	})

	t.Run("a PR of yours is listed when approved, changes requested, new comments or failing CI, each with its reason", func(t *testing.T) {
		useGitHub(t, &fakeGitHub{mine: []fakePR{
			{repo: "me/tool", number: 1, author: "me", updated: ago(time.Hour), ci: "SUCCESS", decision: "APPROVED",
				review: &act{by: "ann", at: ago(time.Hour)}, committed: ago(2 * time.Hour)},
			{repo: "me/tool", number: 2, author: "me", updated: ago(time.Hour), decision: "CHANGES_REQUESTED"},
			{repo: "me/tool", number: 3, author: "me", updated: ago(time.Hour), ci: "SUCCESS",
				comment: &act{by: "ann", at: ago(time.Hour)}, committed: ago(2 * time.Hour)},
			{repo: "me/tool", number: 4, author: "me", updated: ago(time.Hour),
				review: &act{by: "bob", at: ago(time.Hour)}, committed: ago(2 * time.Hour)},
			{repo: "me/tool", number: 5, author: "me", updated: ago(time.Hour), ci: "FAILURE"},
			{repo: "me/tool", number: 6, author: "me", updated: ago(time.Hour), ci: "ERROR"},
		}})
		got := listed(prsJSON(t))
		want := map[string]string{
			"me/tool#1": "approved: merge it",
			"me/tool#2": "changes requested: reply",
			"me/tool#3": "new comments: reply",
			"me/tool#4": "new comments: reply",
			"me/tool#5": "failing CI: fix",
			"me/tool#6": "failing CI: fix",
		}
		for ref, reason := range want {
			if got[ref] != [2]string{"yours", reason} {
				t.Errorf("%s = %v, want yours, %q", ref, got[ref], reason)
			}
		}
	})

	t.Run("a comment of yours after the last one from someone else is not new", func(t *testing.T) {
		useGitHub(t, &fakeGitHub{mine: []fakePR{
			{repo: "me/tool", number: 1, author: "me", updated: ago(time.Hour),
				comment: &act{by: "ann", at: ago(3 * time.Hour)}, review: &act{by: "me", at: ago(time.Hour)}, committed: ago(5 * time.Hour)},
			{repo: "me/tool", number: 2, author: "me", updated: ago(time.Hour),
				comment: &act{by: "me", at: ago(time.Hour)}, review: &act{by: "ann", at: ago(3 * time.Hour)}, committed: ago(5 * time.Hour)},
			{repo: "me/tool", number: 3, author: "me", updated: ago(time.Hour),
				comment: &act{by: "ann", at: ago(3 * time.Hour)}, committed: ago(time.Hour)},
		}})
		if got := listed(prsJSON(t)); len(got) != 0 {
			t.Errorf("listed %v, want nothing: you answered, or pushed, after the last comment", got)
		}
	})

	t.Run("a comment from a bot is not new; a person's comment before it still is", func(t *testing.T) {
		useGitHub(t, &fakeGitHub{mine: []fakePR{
			{repo: "me/tool", number: 1, author: "me", updated: ago(time.Hour), ci: "SUCCESS", decision: "REVIEW_REQUIRED",
				botNote: &act{by: "github-actions", at: ago(time.Hour), bot: true}, committed: ago(2 * time.Hour)},
			{repo: "me/tool", number: 2, author: "me", updated: ago(time.Hour), ci: "SUCCESS",
				comment: &act{by: "ann", at: ago(90 * time.Minute)}, botNote: &act{by: "github-actions", at: ago(time.Hour), bot: true}, committed: ago(2 * time.Hour)},
		}})
		want := map[string][2]string{"me/tool#2": {"yours", "new comments: reply"}}
		if got := listed(prsJSON(t)); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("listed %v, want %v: a bot's comment waits on no one", got, want)
		}
	})

	t.Run("a draft of yours carrying the configured marker in its body or head branch is an agent draft to review; one without it is not listed", func(t *testing.T) {
		home := useGitHub(t, &fakeGitHub{mine: []fakePR{
			{repo: "me/tool", number: 1, author: "me", updated: ago(time.Hour), draft: true, body: "Adds X.\n\n## Spec\n- one", head: "feature-x"},
			{repo: "me/tool", number: 2, author: "me", updated: ago(time.Hour), draft: true, body: "Adds Y.", head: "agent/## Spec-y"},
			{repo: "me/tool", number: 3, author: "me", updated: ago(time.Hour), draft: true, body: "Work in progress", head: "wip"},
			{repo: "me/tool", number: 4, author: "me", updated: ago(time.Hour), draft: false, body: "## Spec", head: "done"},
		}})
		reviewsConfig(t, home, "agent_marker = \"## Spec\"\n")
		got := listed(prsJSON(t))
		want := map[string][2]string{
			"me/tool#1": {"yours", "agent draft: review it"},
			"me/tool#2": {"yours", "agent draft: review it"},
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("listed %v, want %v", got, want)
		}
	})

	t.Run("without a configured marker every draft of yours is an agent draft to review", func(t *testing.T) {
		useGitHub(t, &fakeGitHub{mine: []fakePR{
			{repo: "me/tool", number: 3, author: "me", updated: ago(time.Hour), draft: true, body: "Work in progress", head: "wip"},
		}})
		if got := listed(prsJSON(t))["me/tool#3"]; got != [2]string{"yours", "agent draft: review it"} {
			t.Errorf("me/tool#3 = %v, want an agent draft", got)
		}
		out, _ := mindskein(t, "prs")
		if !strings.Contains(out, "agent draft: review it") {
			t.Errorf("prs printed\n%s\nwant the draft as an agent draft", out)
		}
	})

	t.Run("Watching lists open PRs you were mentioned in or commented on, and not ones that ask for your review", func(t *testing.T) {
		asked := fakePR{repo: "acme/api", number: 1, author: "ann", updated: ago(time.Hour)}
		mentioned := fakePR{repo: "acme/api", number: 2, author: "ann", updated: ago(time.Hour)}
		commented := fakePR{repo: "acme/web", number: 3, author: "bob", updated: ago(2 * time.Hour)}
		subscribed := fakePR{repo: "acme/web", number: 4, author: "bob", updated: ago(time.Hour)}
		merged := fakePR{repo: "acme/web", number: 5, author: "bob", updated: ago(time.Hour), state: "MERGED"}
		useGitHub(t, &fakeGitHub{
			waiting: []fakePR{asked},
			notified: []fakeNotification{
				{"mention", asked}, {"mention", mentioned}, {"comment", commented},
				{"subscribed", subscribed}, {"review_requested", asked}, {"comment", merged},
				{"mention", fakePR{repo: "acme/gone", number: 9}},
			},
			pulls: []fakePR{asked, mentioned, commented, subscribed, merged},
		})
		want := map[string][2]string{
			"acme/api#1": {"waiting", "review requested"},
			"acme/api#2": {"watching", "mentioned"},
			"acme/web#3": {"watching", "new comments"},
		}
		if got := listed(prsJSON(t)); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("listed %v, want %v", got, want)
		}
	})

	t.Run("Done this week holds your PRs merged or closed and PRs you reviewed in the last 7 days, and nothing older", func(t *testing.T) {
		week := 7 * 24 * time.Hour
		useGitHub(t, &fakeGitHub{
			pageSize: 1,
			closed: []fakePR{
				{repo: "acme/api", number: 1, author: "me", updated: ago(time.Hour), state: "MERGED", closed: ago(time.Hour), merged: ago(time.Hour)},
				{repo: "acme/api", number: 2, author: "me", updated: ago(time.Hour), state: "CLOSED", closed: ago(6 * 24 * time.Hour)},
				{repo: "acme/api", number: 3, author: "me", updated: ago(time.Hour), state: "CLOSED", closed: ago(week + time.Hour)},
			},
			reviewed: []fakePR{
				{repo: "acme/web", number: 4, author: "ann", updated: ago(time.Hour), reviewed: ago(2 * 24 * time.Hour)},
				{repo: "acme/web", number: 5, author: "ann", updated: ago(time.Hour), reviewed: ago(week + time.Hour)},
				{repo: "acme/web", number: 6, author: "ann", updated: ago(time.Hour)},
			},
		})
		want := map[string][2]string{
			"acme/api#1": {"done", "merged"},
			"acme/api#2": {"done", "closed"},
			"acme/web#4": {"done", "reviewed"},
		}
		if got := listed(prsJSON(t)); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("listed %v, want %v", got, want)
		}
	})

	t.Run("the login judging whose comment is whose comes from gh api user", func(t *testing.T) {
		f := &fakeGitHub{login: "octo-dev", mine: []fakePR{
			{repo: "me/tool", number: 1, author: "octo-dev", updated: ago(time.Hour),
				comment: &act{by: "octo-dev", at: ago(time.Hour)}, committed: ago(2 * time.Hour)},
			{repo: "me/tool", number: 2, author: "octo-dev", updated: ago(time.Hour),
				comment: &act{by: "me", at: ago(time.Hour)}, committed: ago(2 * time.Hour)},
		}}
		useGitHub(t, f)
		r := prsJSON(t)
		want := map[string][2]string{"me/tool#2": {"yours", "new comments: reply"}}
		if got := listed(r); fmt.Sprint(got) != fmt.Sprint(want) || r.Login != "octo-dev" {
			t.Errorf("login %q listed %v, want octo-dev and %v", r.Login, got, want)
		}
		if first := f.calls[0]; strings.Join(first, " ") != "api user" {
			t.Errorf("first call gh %v, want gh api user", first)
		}
	})

	failures := []struct {
		name string
		err  error
		out  string
		want string
	}{
		{
			name: "signed out of gh, prints one line saying to run gh auth login, and exits cleanly",
			err:  &reviews.GHError{Err: errors.New("exit status 4"), Stderr: "To get started with GitHub CLI, please run:  gh auth login\nAlternatively, populate the GH_TOKEN environment variable with a GitHub API authentication token."},
			want: "not signed in to GitHub — run `gh auth login`",
		},
		{
			name: "offline, prints one line saying to check the network, and exits cleanly",
			err:  &reviews.GHError{Err: errors.New("exit status 1"), Stderr: "error connecting to api.github.com\ncheck your internet connection or https://githubstatus.com"},
			want: "cannot reach GitHub — check the network",
		},
		{
			name: "rate-limited, prints one line saying to wait for the rate limit, and exits cleanly",
			err:  &reviews.GHError{Err: errors.New("exit status 1"), Stderr: "gh: API rate limit already exceeded for user ID 1."},
			out:  `{"data":null,"errors":[{"type":"RATE_LIMITED","message":"API rate limit already exceeded for user ID 1."}]}`,
			want: "GitHub rate limit reached — wait for it to reset, then try again",
		},
	}
	for _, c := range failures {
		t.Run(c.name, func(t *testing.T) {
			useGitHub(t, &fakeGitHub{fail: func([]string) ([]byte, error) { return []byte(c.out), c.err }})
			out, err := mindskein(t, "prs")
			if err != nil {
				t.Fatalf("prs = %v, want a clean exit", err)
			}
			if out != c.want+"\n" {
				t.Errorf("prs printed %q, want the one line %q", out, c.want)
			}
		})
	}

	t.Run("with gh missing, prints one line saying to install it, and exits cleanly", func(t *testing.T) {
		pruneHome(t)
		t.Setenv("PATH", t.TempDir())
		out, err := mindskein(t, "prs")
		if err != nil {
			t.Fatalf("prs = %v, want a clean exit", err)
		}
		if strings.Count(out, "\n") != 1 || !strings.Contains(out, "gh is not installed") || !strings.Contains(out, "https://cli.github.com") {
			t.Errorf("prs printed %q, want one line saying to install gh", out)
		}
	})

	t.Run("--json on a failure prints the one line as its error field", func(t *testing.T) {
		useGitHub(t, &fakeGitHub{fail: func([]string) ([]byte, error) {
			return nil, &reviews.GHError{Err: errors.New("exit status 1"), Stderr: "gh: Bad credentials (HTTP 401)"}
		}})
		if r := prsJSON(t); r.Error != "not signed in to GitHub — run `gh auth login`" {
			t.Errorf("error = %q", r.Error)
		}
	})

	t.Run("a GitHub call that hangs is cut off by the timeout and reported as a network failure", func(t *testing.T) {
		useGitHub(t, &fakeGitHub{})
		previous := ghTimeout
		ghTimeout = 50 * time.Millisecond
		t.Cleanup(func() { ghTimeout = previous })
		ghRun = func(ctx context.Context, args ...string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		start := time.Now()
		out, err := mindskein(t, "prs")
		if err != nil || !strings.Contains(out, "check the network") || strings.Count(out, "\n") != 1 {
			t.Errorf("prs = %q, %v; want one line saying to check the network", out, err)
		}
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("prs took %v, want it cut off by the timeout", took)
		}
	})

	t.Run("a cache fresher than the configured age is used without calling GitHub and says its age", func(t *testing.T) {
		f := everyGroup()
		useGitHub(t, f)
		if _, err := mindskein(t, "prs"); err != nil {
			t.Fatal(err)
		}
		calls := f.callCount()
		out, _ := mindskein(t, "prs")
		if f.callCount() != calls {
			t.Errorf("a fresh cache still called gh %d more times", f.callCount()-calls)
		}
		if !strings.Contains(out, "cached <1m ago") || !strings.Contains(out, "acme/api#12") {
			t.Errorf("prs printed\n%s\nwant the cached queue with its age", out)
		}
		if r := prsJSON(t); !r.Cached || r.CacheAge != "<1m" {
			t.Errorf("JSON cached = %v age %q, want true and <1m", r.Cached, r.CacheAge)
		}
	})

	t.Run("a cache older than the configured age is refreshed and rewritten", func(t *testing.T) {
		f := everyGroup()
		home := useGitHub(t, f)
		reviewsConfig(t, home, "cache_age = \"30m\"\n")
		mindskein(t, "prs")
		ageCache(t, home, time.Hour)
		f.waiting[0].title = "Add a rate limiter, retitled"
		calls := f.callCount()
		out, _ := mindskein(t, "prs")
		if f.callCount() == calls || strings.Contains(out, "cached") || !strings.Contains(out, "retitled") {
			t.Errorf("prs printed\n%s\nwant a fresh queue", out)
		}
		if out, _ := mindskein(t, "prs"); !strings.Contains(out, "cached <1m ago") || !strings.Contains(out, "retitled") {
			t.Errorf("prs printed\n%s\nwant the rewritten cache", out)
		}
	})

	t.Run("a refresh that fails falls back to the cache, marked with its age and the failure", func(t *testing.T) {
		f := everyGroup()
		home := useGitHub(t, f)
		mindskein(t, "prs")
		ageCache(t, home, 3*time.Hour)
		f.fail = func([]string) ([]byte, error) {
			return nil, &reviews.GHError{Err: errors.New("exit status 1"), Stderr: "error connecting to api.github.com"}
		}
		out, err := mindskein(t, "prs")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "cached 3h ago, refresh failed: cannot reach GitHub — check the network") || !strings.Contains(out, "acme/api#12") {
			t.Errorf("prs printed\n%s\nwant the cached queue with its age and why", out)
		}
		r := prsJSON(t)
		if !r.Cached || r.CacheAge != "3h" || r.RefreshError != "cannot reach GitHub — check the network" || len(listed(r)) != 4 {
			t.Errorf("JSON = %+v, want the cached queue with its age and why", r)
		}
	})

	t.Run("never runs a gh command that writes to GitHub", func(t *testing.T) {
		f := everyGroup()
		f.pageSize = 1
		useGitHub(t, f)
		mindskein(t, "prs")
		for _, call := range f.calls {
			line := strings.Join(call, " ")
			read := line == "api user" || strings.HasPrefix(line, "api notifications?") ||
				(strings.HasPrefix(line, "api graphql -f query=query(") && !strings.Contains(line, "mutation"))
			if !read || strings.Contains(line, "-X") || strings.Contains(line, "--method") {
				t.Errorf("gh %s is not a read", line)
			}
		}
	})
}

// ageCache makes the cached queue d older than it is.
func ageCache(t *testing.T, home string, d time.Duration) {
	t.Helper()
	path := filepath.Join(home, reviews.CacheFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	queue := doc["queue"].(map[string]any)
	at, err := time.Parse(time.RFC3339Nano, queue["fetched_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	queue["fetched_at"] = at.Add(-d)
	data, _ = json.Marshal(doc)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
