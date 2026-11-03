package main

import "testing"

func TestPrs(t *testing.T) {
	scenarios := []string{
		"lists PRs from every org, your own included, under Waiting on you, Yours, Watching and Done this week",
		"prints each PR on one line as owner/repo#N with its title, changed lines, age, reason and URL",
		"--json prints the same groups, each PR with its ref, URL, owner, repo, number, title, author, changed lines, updated time, draft flag, group and reason",
		"an empty group says so in one line",
		"a PR of yours with green CI and nothing new is not listed",
		"a PR of yours is listed when approved, changes requested, new comments or failing CI, each with its reason",
		"a comment of yours after the last one from someone else is not new",
		"a draft of yours carrying the configured marker in its body or head branch is an agent draft to review; one without it is not listed",
		"without a configured marker every draft of yours is an agent draft to review",
		"Watching lists open PRs you were mentioned in or commented on, and not ones that ask for your review",
		"Done this week holds your PRs merged or closed and PRs you reviewed in the last 7 days, and nothing older",
		"the login judging whose comment is whose comes from gh api user",
		"with gh missing, prints one line saying to install it, and exits cleanly",
		"signed out of gh, prints one line saying to run gh auth login, and exits cleanly",
		"offline, prints one line saying to check the network, and exits cleanly",
		"rate-limited, prints one line saying to wait for the rate limit, and exits cleanly",
		"--json on a failure prints the one line as its error field",
		"a GitHub call that hangs is cut off by the timeout and reported as a network failure",
		"a cache fresher than the configured age is used without calling GitHub and says its age",
		"a cache older than the configured age is refreshed and rewritten",
		"a refresh that fails falls back to the cache, marked with its age and the failure",
		"never runs a gh command that writes to GitHub",
	}
	for _, s := range scenarios {
		t.Run(s, func(t *testing.T) { pending(t, s) })
	}
}
