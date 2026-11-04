package reviews

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/swilgosz/mindskein/internal/config"
)

// The tiers a pull request is ranked in, first to last.
const (
	RankRequested = 1 + iota
	RankYours
	RankSecurity
	RankTeam
	RankRest
)

// FreshWindow is how recently a team's pull request must have been updated
// to rank above the rest.
const FreshWindow = 5 * 24 * time.Hour

// SecurityMaxLines is the size from which a security fix no longer ranks as
// a quick one.
const SecurityMaxLines = 200

// Arrange applies the user's rules to a fetched queue: it drops what a skip
// rule matches, puts each pull request in its Life Space and ranks the open
// ones. Inside a group, a lower rank comes first and equal ranks oldest
// first; Declined and Done this week stay newest first.
func Arrange(q Queue, rules config.Reviews, now time.Time) Queue {
	if len(rules.Spaces) > 0 {
		q.Spaces = make([]string, 0, len(rules.Spaces)+1)
		for _, s := range rules.Spaces {
			q.Spaces = append(q.Spaces, s.Name)
		}
		q.Spaces = append(q.Spaces, config.OtherSpace)
	}

	groups := make([]Group, len(q.Groups))
	for i, g := range q.Groups {
		prs := []PR{}
		for _, p := range g.PRs {
			if skipped(p, rules.Skip) {
				continue
			}
			p.Space = spaceOf(p, rules.Spaces)
			p.Rank = 0
			if openGroup(g.ID) {
				p.Rank = rank(p, rules.Team, now)
			}
			prs = append(prs, p)
		}
		if openGroup(g.ID) {
			slices.SortStableFunc(prs, byRank)
		}
		g.PRs = prs
		groups[i] = g
	}
	q.Groups = groups
	return q
}

// byRank orders open pull requests: a lower rank first, equal ranks oldest
// first.
func byRank(a, b PR) int {
	if c := cmp.Compare(a.Rank, b.Rank); c != 0 {
		return c
	}
	if c := a.Updated.Compare(b.Updated); c != 0 {
		return c
	}
	return cmp.Compare(a.Ref, b.Ref)
}

// SpaceNamed is the configured space called name, whatever its case, Other
// included. It fails naming the spaces there are.
func SpaceNamed(rules config.Reviews, name string) (string, error) {
	if len(rules.Spaces) == 0 {
		return "", fmt.Errorf("no spaces configured: add [[reviews.spaces]] to the config to use --space")
	}
	names := make([]string, 0, len(rules.Spaces)+1)
	for _, s := range rules.Spaces {
		names = append(names, s.Name)
	}
	names = append(names, config.OtherSpace)
	for _, n := range names {
		if strings.EqualFold(n, strings.TrimSpace(name)) {
			return n, nil
		}
	}
	return "", fmt.Errorf("no space named %q; the spaces are %s", name, strings.Join(names, ", "))
}

// Only narrows an arranged queue to the pull requests of one space.
func Only(q Queue, space string) Queue {
	q.Spaces = []string{space}
	groups := make([]Group, len(q.Groups))
	for i, g := range q.Groups {
		prs := []PR{}
		for _, p := range g.PRs {
			if p.Space == space {
				prs = append(prs, p)
			}
		}
		g.PRs = prs
		groups[i] = g
	}
	q.Groups = groups
	return q
}

// rank is the tier of an open pull request. A notification bumps it one
// tier, never above the first.
func rank(p PR, team config.Team, now time.Time) int {
	r := RankRest
	switch {
	case p.Group == GroupWaiting && p.RequestedFromYou:
		r = RankRequested
	case p.Group == GroupYours:
		r = RankYours
	case p.SecurityFix && p.Changed < SecurityMaxLines:
		r = RankSecurity
	case now.Sub(p.Updated) <= FreshWindow && fromTeam(p, team):
		r = RankTeam
	}
	if p.Notified && r > RankRequested {
		r--
	}
	return r
}

func fromTeam(p PR, team config.Team) bool {
	return slices.ContainsFunc(team.Authors, func(pattern string) bool { return match(pattern, p.Author) }) ||
		slices.ContainsFunc(team.Repos, func(pattern string) bool { return repoMatch(pattern, p) })
}

func spaceOf(p PR, spaces []config.Space) string {
	if len(spaces) == 0 {
		return ""
	}
	for _, s := range spaces {
		if slices.ContainsFunc(s.Repos, func(pattern string) bool { return repoMatch(pattern, p) }) {
			return s.Name
		}
	}
	return config.OtherSpace
}

// skipped says a rule drops p: every pattern the rule sets matches, and the
// rule does not spare a review requested from you.
func skipped(p PR, rules []config.SkipRule) bool {
	for _, r := range rules {
		if r.UnlessReviewRequested && p.RequestedFromYou {
			continue
		}
		if (r.Repo == "" || repoMatch(r.Repo, p)) &&
			(r.Author == "" || match(r.Author, p.Author)) &&
			(r.Title == "" || match(r.Title, p.Title)) {
			return true
		}
	}
	return false
}

// repoMatch matches an owner pattern against the owner, and an owner/repo
// pattern against both.
func repoMatch(pattern string, p PR) bool {
	if strings.Contains(pattern, "/") {
		return match(pattern, p.Owner+"/"+p.Repo)
	}
	return match(pattern, p.Owner)
}

// match says s matches pattern as a whole, ignoring case, where each * in
// pattern stands for any run of characters.
func match(pattern, s string) bool {
	pattern, s = strings.ToLower(strings.TrimSpace(pattern)), strings.ToLower(s)
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	head, tail := parts[0], parts[len(parts)-1]
	if !strings.HasPrefix(s, head) {
		return false
	}
	s = s[len(head):]
	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(s, part)
		if i < 0 {
			return false
		}
		s = s[i+len(part):]
	}
	return strings.HasSuffix(s, tail)
}
