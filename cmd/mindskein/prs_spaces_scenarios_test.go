package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// arrangedPR is a pull request as prs --json places it: its space and rank.
type arrangedPR struct {
	Ref      string `json:"ref"`
	Group    string `json:"group"`
	Space    string `json:"space"`
	Rank     int    `json:"rank"`
	Notified bool   `json:"notified"`
}

type arrangedDoc struct {
	Spaces []string `json:"spaces"`
	Groups []struct {
		ID  string       `json:"id"`
		PRs []arrangedPR `json:"prs"`
	} `json:"groups"`
}

func arranged(t *testing.T, args ...string) arrangedDoc {
	t.Helper()
	out, err := mindskein(t, append([]string{"prs", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("prs --json %v: %v", args, err)
	}
	var doc arrangedDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("prs --json printed %q: %v", out, err)
	}
	return doc
}

func (d arrangedDoc) byRef() map[string]arrangedPR {
	m := map[string]arrangedPR{}
	for _, g := range d.Groups {
		for _, p := range g.PRs {
			m[p.Ref] = p
		}
	}
	return m
}

func (d arrangedDoc) group(id string) []string {
	var refs []string
	for _, g := range d.Groups {
		if g.ID == id {
			for _, p := range g.PRs {
				refs = append(refs, p.Ref)
			}
		}
	}
	return refs
}

func inOrder(t *testing.T, out string, want ...string) {
	t.Helper()
	at := 0
	for _, w := range want {
		i := strings.Index(out[at:], w)
		if i < 0 {
			t.Fatalf("printed\n%s\nwant %q after the one before it", out, w)
		}
		at += i + len(w)
	}
}

const twoSpaces = `
[[reviews.spaces]]
name = "Work"
repos = ["acme", "globex/web"]

[[reviews.spaces]]
name = "Business"
repos = ["me", "globex"]
`

// spread is one pull request for each space of twoSpaces and one for none.
func spread() *fakeGitHub {
	return &fakeGitHub{
		waiting: []fakePR{
			{repo: "acme/api", number: 1, author: "ann", updated: ago(time.Hour)},
			{repo: "globex/web", number: 2, author: "bob", updated: ago(time.Hour)},
			{repo: "globex/api", number: 3, author: "bob", updated: ago(time.Hour)},
			{repo: "initech/cli", number: 4, author: "cat", updated: ago(time.Hour)},
		},
		mine:   []fakePR{{repo: "me/tool", number: 5, author: "me", updated: ago(time.Hour), ci: "FAILURE"}},
		closed: []fakePR{{repo: "acme/web", number: 6, author: "me", updated: ago(time.Hour), state: "MERGED", closed: ago(time.Hour), merged: ago(time.Hour)}},
	}
}

func TestPrsSpaces(t *testing.T) {
	t.Run("each PR belongs to the first space whose owner or owner/repo pattern matches; one matching none is in Other", func(t *testing.T) {
		home := useGitHub(t, spread())
		reviewsConfig(t, home, twoSpaces)
		got := arranged(t).byRef()
		want := map[string]string{
			"acme/api#1": "Work", "globex/web#2": "Work", "globex/api#3": "Business",
			"initech/cli#4": "Other", "me/tool#5": "Business", "acme/web#6": "Work",
		}
		for ref, space := range want {
			if got[ref].Space != space {
				t.Errorf("%s in space %q, want %q", ref, got[ref].Space, space)
			}
		}
	})

	t.Run("matches owners and repos whatever their case, and * stands for any run of characters", func(t *testing.T) {
		home := useGitHub(t, &fakeGitHub{waiting: []fakePR{
			{repo: "Acme/API", number: 1, author: "ann", updated: ago(time.Hour)},
			{repo: "globex/web-legacy", number: 2, author: "bob", updated: ago(time.Hour)},
			{repo: "globex/api", number: 3, author: "bob", updated: ago(time.Hour)},
		}})
		reviewsConfig(t, home, "[[reviews.spaces]]\nname = \"Work\"\nrepos = [\"acme\", \"GLOBEX/*-legacy\"]\n")
		got := arranged(t).byRef()
		for ref, space := range map[string]string{"Acme/API#1": "Work", "globex/web-legacy#2": "Work", "globex/api#3": "Other"} {
			if got[ref].Space != space {
				t.Errorf("%s in space %q, want %q", ref, got[ref].Space, space)
			}
		}
	})

	t.Run("spaces print in config order, each with its groups, Other last", func(t *testing.T) {
		home := useGitHub(t, spread())
		reviewsConfig(t, home, twoSpaces)
		out, err := mindskein(t, "prs")
		if err != nil {
			t.Fatal(err)
		}
		inOrder(t, out,
			"Work", "Waiting on you (2)", "acme/api#1", "globex/web#2", "Done this week (1)", "acme/web#6",
			"Business", "Waiting on you (1)", "globex/api#3", "Yours (1)", "me/tool#5",
			"Other", "Waiting on you (1)", "initech/cli#4")
		if d := arranged(t); fmt.Sprint(d.Spaces) != "[Work Business Other]" {
			t.Errorf("JSON spaces = %v, want [Work Business Other]", d.Spaces)
		}
	})

	t.Run("with no spaces configured, prints one flat list with no space on any PR", func(t *testing.T) {
		useGitHub(t, spread())
		out, _ := mindskein(t, "prs")
		if strings.Contains(out, "Other") {
			t.Errorf("prs printed\n%s\nwant no space headings", out)
		}
		inOrder(t, out, "Waiting on you (4)", "Yours (1)", "Watching (0)", "Done this week (1)")
		d := arranged(t)
		if len(d.Spaces) != 0 {
			t.Errorf("JSON spaces = %v, want none", d.Spaces)
		}
		for ref, p := range d.byRef() {
			if p.Space != "" {
				t.Errorf("%s in space %q, want none", ref, p.Space)
			}
		}
	})

	t.Run("--space narrows the list and the JSON to one space, named in any case, Other included", func(t *testing.T) {
		home := useGitHub(t, spread())
		reviewsConfig(t, home, twoSpaces)
		out, err := mindskein(t, "prs", "--space", "business")
		if err != nil {
			t.Fatal(err)
		}
		inOrder(t, out, "Business", "globex/api#3", "me/tool#5")
		for _, other := range []string{"Work", "Other", "acme/api#1", "initech/cli#4", "acme/web#6"} {
			if strings.Contains(out, other) {
				t.Errorf("prs --space business printed\n%s\nwant nothing of %q", out, other)
			}
		}
		d := arranged(t, "--space", "Other")
		if got := d.byRef(); len(got) != 1 || got["initech/cli#4"].Space != "Other" || fmt.Sprint(d.Spaces) != "[Other]" {
			t.Errorf("--space Other --json = %+v, want initech/cli#4 alone", d)
		}
	})

	t.Run("--space naming no configured space fails, listing the spaces there are, without calling GitHub", func(t *testing.T) {
		f := spread()
		home := useGitHub(t, f)
		reviewsConfig(t, home, twoSpaces)
		_, err := mindskein(t, "prs", "--space", "Life")
		if err == nil || !strings.Contains(err.Error(), "Work, Business, Other") {
			t.Errorf("prs --space Life = %v, want an error listing Work, Business, Other", err)
		}
		if f.callCount() != 0 {
			t.Errorf("called gh %d times for a space that does not exist", f.callCount())
		}
	})

	t.Run("--space with no spaces configured fails, saying how to configure them", func(t *testing.T) {
		f := spread()
		useGitHub(t, f)
		_, err := mindskein(t, "prs", "--space", "Work")
		if err == nil || !strings.Contains(err.Error(), "[[reviews.spaces]]") || f.callCount() != 0 {
			t.Errorf("prs --space Work = %v after %d gh calls, want an error naming [[reviews.spaces]] and none", err, f.callCount())
		}
	})
}

// teamConfig names ann as a team author and acme/web as a team repo.
const teamConfig = "[reviews.team]\nauthors = [\"ann\"]\nrepos = [\"acme/web\"]\n"

func TestPrsRanking(t *testing.T) {
	t.Run("ranks a review requested from you 1, yours blocked on you 2, small security fixes 3, fresh team PRs 4 and everything else 5", func(t *testing.T) {
		day := 24 * time.Hour
		home := useGitHub(t, &fakeGitHub{
			waiting: []fakePR{
				{repo: "acme/api", number: 1, author: "zed", updated: ago(9 * day)},
				{repo: "acme/api", number: 3, author: "zed", asked: "@acme/core", title: "Fix CVE-2026-1234 in the parser", add: 150, del: 49, updated: ago(9 * day)},
				{repo: "acme/api", number: 4, author: "dependabot", asked: "@acme/core", title: "Bump lib from 1.0 to 1.1", body: "Fixes GHSA-abcd-efgh-ijkl.", add: 2, del: 2, updated: ago(9 * day)},
				{repo: "acme/api", number: 5, author: "zed", asked: "@acme/core", title: "GHSA-abcd-efgh-ijkl: rework auth", add: 150, del: 50, updated: ago(9 * day)},
				{repo: "acme/api", number: 6, author: "ann", asked: "@acme/core", title: "Tidy", updated: ago(day)},
				{repo: "acme/web", number: 7, author: "zed", asked: "@acme/core", title: "Tidy", updated: ago(4 * day)},
				{repo: "acme/api", number: 8, author: "ann", asked: "@acme/core", title: "Tidy", updated: ago(6 * day)},
				{repo: "acme/api", number: 9, author: "zed", asked: "@acme/core", title: "Refactor", body: "Also closes CVE-2026-9999.", updated: ago(day)},
				{repo: "acme/api", number: 10, author: "zed", asked: "someone-else", title: "Tidy", updated: ago(day)},
			},
			mine: []fakePR{{repo: "me/tool", number: 2, author: "me", updated: ago(time.Hour), decision: "APPROVED"}},
		})
		reviewsConfig(t, home, teamConfig)
		got := arranged(t).byRef()
		want := map[string]int{
			"acme/api#1": 1, "me/tool#2": 2, "acme/api#3": 3, "acme/api#4": 3, "acme/api#5": 5,
			"acme/web#7": 4, "acme/api#6": 4, "acme/api#8": 5, "acme/api#9": 5, "acme/api#10": 5,
		}
		for ref, rank := range want {
			if got[ref].Rank != rank {
				t.Errorf("%s ranked %d, want %d", ref, got[ref].Rank, rank)
			}
		}
	})

	t.Run("inside each group a lower rank comes first, and equal ranks oldest first", func(t *testing.T) {
		home := useGitHub(t, &fakeGitHub{waiting: []fakePR{
			{repo: "acme/api", number: 1, author: "zed", asked: "@acme/core", updated: ago(24 * time.Hour)},
			{repo: "acme/api", number: 2, author: "zed", asked: "@acme/core", updated: ago(72 * time.Hour)},
			{repo: "acme/api", number: 3, author: "zed", updated: ago(time.Hour)},
			{repo: "acme/api", number: 4, author: "zed", asked: "@acme/core", updated: ago(48 * time.Hour)},
			{repo: "acme/api", number: 5, author: "ann", asked: "@acme/core", updated: ago(2 * time.Hour)},
		}})
		reviewsConfig(t, home, teamConfig)
		want := "[acme/api#3 acme/api#5 acme/api#2 acme/api#4 acme/api#1]"
		if got := fmt.Sprint(arranged(t).group("waiting")); got != want {
			t.Errorf("waiting in order %s, want %s", got, want)
		}
		out, _ := mindskein(t, "prs")
		inOrder(t, out, "acme/api#3", "acme/api#5", "acme/api#2", "acme/api#4", "acme/api#1")
	})

	t.Run("a notification on a PR bumps it one tier and marks it with a bell, waiting on you and yours included", func(t *testing.T) {
		team := fakePR{repo: "acme/api", number: 1, author: "zed", asked: "@acme/core", updated: ago(time.Hour)}
		quiet := fakePR{repo: "acme/api", number: 2, author: "zed", asked: "@acme/core", updated: ago(time.Hour)}
		watched := fakePR{repo: "acme/web", number: 3, author: "zed", updated: ago(time.Hour)}
		mine := fakePR{repo: "me/tool", number: 4, author: "me", updated: ago(time.Hour), decision: "CHANGES_REQUESTED"}
		direct := fakePR{repo: "acme/api", number: 5, author: "zed", updated: ago(time.Hour)}
		useGitHub(t, &fakeGitHub{
			waiting:  []fakePR{team, quiet, direct},
			mine:     []fakePR{mine},
			notified: []fakeNotification{{"mention", team}, {"comment", watched}, {"comment", mine}, {"mention", direct}},
			pulls:    []fakePR{team, quiet, watched, mine, direct},
		})
		got := arranged(t).byRef()
		want := map[string]arrangedPR{
			"acme/api#1": {Group: "waiting", Rank: 4, Notified: true},
			"acme/api#2": {Group: "waiting", Rank: 5},
			"acme/web#3": {Group: "watching", Rank: 4, Notified: true},
			"me/tool#4":  {Group: "yours", Rank: 1, Notified: true},
			"acme/api#5": {Group: "waiting", Rank: 1, Notified: true},
		}
		for ref, w := range want {
			g := got[ref]
			if g.Group != w.Group || g.Rank != w.Rank || g.Notified != w.Notified {
				t.Errorf("%s = %s rank %d notified %v, want %s rank %d notified %v",
					ref, g.Group, g.Rank, g.Notified, w.Group, w.Rank, w.Notified)
			}
		}
		out, _ := mindskein(t, "prs")
		for _, line := range strings.Split(out, "\n") {
			bell := strings.Contains(line, "🔔")
			if strings.Contains(line, "acme/api#1 ") && !bell {
				t.Errorf("line %q lacks the bell", line)
			}
			if strings.Contains(line, "acme/api#2 ") && bell {
				t.Errorf("line %q has a bell without a notification", line)
			}
		}
	})

	t.Run("Done this week is not ranked", func(t *testing.T) {
		useGitHub(t, spread())
		if p := arranged(t).byRef()["acme/web#6"]; p.Group != "done" || p.Rank != 0 {
			t.Errorf("acme/web#6 = %+v, want done and unranked", p)
		}
	})
}

func TestPrsSkipRules(t *testing.T) {
	t.Run("skip rules drop PRs whose repo, author or title matches, from every group", func(t *testing.T) {
		legacy := fakePR{repo: "acme/legacy-app", number: 1, author: "zed", asked: "@acme/core", updated: ago(time.Hour)}
		bot := fakePR{repo: "acme/api", number: 2, author: "renovate-bot", updated: ago(time.Hour)}
		wip := fakePR{repo: "acme/api", number: 3, author: "zed", title: "WIP: new search", asked: "@acme/core", updated: ago(time.Hour)}
		kept := fakePR{repo: "acme/api", number: 4, author: "zed", title: "New search", asked: "@acme/core", updated: ago(time.Hour)}
		home := useGitHub(t, &fakeGitHub{
			waiting:  []fakePR{legacy, wip, kept},
			mine:     []fakePR{{repo: "acme/legacy-app", number: 5, author: "me", updated: ago(time.Hour), ci: "FAILURE"}},
			notified: []fakeNotification{{"mention", bot}},
			pulls:    []fakePR{bot},
			closed:   []fakePR{{repo: "acme/legacy-app", number: 6, author: "me", updated: ago(time.Hour), state: "CLOSED", closed: ago(time.Hour)}},
		})
		reviewsConfig(t, home, `
[[reviews.skip]]
repo = "acme/legacy-*"

[[reviews.skip]]
author = "*-bot"

[[reviews.skip]]
title = "wip*"
`)
		got := arranged(t).byRef()
		if _, ok := got["acme/api#4"]; !ok || len(got) != 1 {
			t.Errorf("listed %v, want acme/api#4 alone", got)
		}
	})

	t.Run("a skip rule marked unless_review_requested keeps a PR whose review is requested from you, and not one requested from your team", func(t *testing.T) {
		direct := fakePR{repo: "acme/api", number: 1, author: "zed", updated: ago(time.Hour)}
		team := fakePR{repo: "acme/api", number: 2, author: "zed", asked: "@acme/core", updated: ago(time.Hour)}
		watched := fakePR{repo: "acme/web", number: 3, author: "zed", updated: ago(time.Hour)}
		home := useGitHub(t, &fakeGitHub{
			waiting:  []fakePR{direct, team},
			notified: []fakeNotification{{"mention", watched}},
			pulls:    []fakePR{watched},
		})
		reviewsConfig(t, home, "[[reviews.skip]]\nrepo = \"acme\"\nunless_review_requested = true\n")
		got := arranged(t).byRef()
		if _, ok := got["acme/api#1"]; !ok || len(got) != 1 {
			t.Errorf("listed %v, want acme/api#1 alone", got)
		}
	})

	t.Run("a skipped PR is gone from the ranking: it counts in no space and takes no rank", func(t *testing.T) {
		home := useGitHub(t, &fakeGitHub{waiting: []fakePR{
			{repo: "acme/api", number: 1, author: "zed", updated: ago(time.Hour)},
		}})
		reviewsConfig(t, home, twoSpaces+"\n[[reviews.skip]]\nauthor = \"zed\"\n")
		out, _ := mindskein(t, "prs")
		if strings.Contains(out, "acme/api#1") || !strings.Contains(out, "Work") {
			t.Errorf("prs printed\n%s\nwant the Work space without acme/api#1", out)
		}
	})
}
