package reviews

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// The groups of the queue, in the order they are shown. A pull request is in
// at most one: the first that claims it.
const (
	GroupWaiting  = "waiting"
	GroupYours    = "yours"
	GroupWatching = "watching"
	GroupDone     = "done"
)

var groupNames = map[string]string{
	GroupWaiting:  "Waiting on you",
	GroupYours:    "Yours",
	GroupWatching: "Watching",
	GroupDone:     "Done this week",
}

// Reasons a pull request is in the queue.
const (
	ReasonReviewRequested  = "review requested"
	ReasonApproved         = "approved: merge it"
	ReasonChangesRequested = "changes requested: reply"
	ReasonNewComments      = "new comments: reply"
	ReasonFailingCI        = "failing CI: fix"
	ReasonAgentDraft       = "agent draft: review it"
	ReasonMentioned        = "mentioned"
	ReasonCommented        = "new comments"
	ReasonMerged           = "merged"
	ReasonClosed           = "closed"
	ReasonReviewed         = "reviewed"
)

// PR is one pull request in the queue.
type PR struct {
	Ref     string    `json:"ref"`
	URL     string    `json:"url"`
	Owner   string    `json:"owner"`
	Repo    string    `json:"repo"`
	Number  int       `json:"number"`
	Title   string    `json:"title"`
	Author  string    `json:"author"`
	Changed int       `json:"changed_lines"`
	Updated time.Time `json:"updated_at"`
	Draft   bool      `json:"draft"`
	Group   string    `json:"group"`
	Reason  string    `json:"reason"`

	// RequestedFromYou says your review is requested from you by name, not
	// through a team you are in.
	RequestedFromYou bool `json:"requested_from_you"`

	// SecurityFix says the title names a CVE or GHSA advisory, or the body
	// of a dependency bump does.
	SecurityFix bool `json:"security_fix"`

	// Notified says a notification mentions you on it or carries a comment
	// you take part in.
	Notified bool `json:"notified"`

	// Space is the Life Space the pull request belongs to, empty when none
	// are configured; Rank is its tier, 1 first, and 0 in Done this week.
	Space string `json:"space,omitempty"`
	Rank  int    `json:"rank,omitempty"`
}

// Group is one section of the queue.
type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	PRs  []PR   `json:"prs"`
}

// Queue is every pull request waiting on the signed-in user, as fetched at
// one moment.
type Queue struct {
	Login     string    `json:"login"`
	FetchedAt time.Time `json:"fetched_at"`

	// Spaces are the Life Spaces in the order they are shown, Other last;
	// none when the user configured none.
	Spaces []string `json:"spaces,omitempty"`
	Groups []Group  `json:"groups"`
}

// Ref writes a pull request the way GitHub links it in text: owner/repo#N.
func Ref(owner, repo string, number int) string {
	return fmt.Sprintf("%s/%s#%d", owner, repo, number)
}

// Fetch asks GitHub for the queue: who is signed in, the searches, and the
// notifications naming pull requests to watch. Each call is cut off after
// timeout; the first that fails fails the fetch, since a queue missing a
// group would read as having nothing in it.
func Fetch(ctx context.Context, run Runner, timeout time.Duration, marker string, now time.Time) (Queue, error) {
	g := github{run: run, timeout: timeout}
	login, err := g.login(ctx)
	if err != nil {
		return Queue{}, err
	}
	since := now.Add(-DoneWindow)
	day := since.UTC().Format("2006-01-02")

	var (
		waiting, mine, closed, reviewed []pullRequest
		watches                         []watch
		wg                              sync.WaitGroup
	)
	errs := make([]error, 5)
	for i, job := range []func() error{
		func() (err error) { waiting, err = g.search(ctx, login, searchWaiting, waitingFields); return },
		func() (err error) { mine, err = g.search(ctx, login, searchMine, openFields); return },
		func() (err error) {
			closed, err = g.search(ctx, login, fmt.Sprintf(searchClosed, day), baseFields)
			return
		},
		func() (err error) {
			reviewed, err = g.search(ctx, login, fmt.Sprintf(searchReviewed, day), reviewedFields)
			return
		},
		func() (err error) { watches, err = g.watched(ctx); return },
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = job()
		}()
	}
	wg.Wait()
	if err := firstError(errs); err != nil {
		return Queue{}, err
	}

	b := builder{login: login, marker: marker, seen: map[string]bool{}, notified: map[string]bool{}, groups: map[string][]PR{}}
	for _, p := range waiting {
		b.add(GroupWaiting, p, ReasonReviewRequested)
	}
	for _, p := range mine {
		b.add(GroupYours, p, b.blockedOnYou(p))
	}

	var unseen []watch
	for _, w := range watches {
		b.notified[w.ref()] = true
		if !b.seen[w.ref()] {
			unseen = append(unseen, w)
		}
	}
	watched, err := g.details(ctx, unseen)
	if err != nil {
		return Queue{}, err
	}
	for _, w := range unseen {
		p, ok := watched[w.ref()]
		if !ok || p.State != "OPEN" || p.authoredBy(login) {
			continue
		}
		reason := ReasonCommented
		if w.reason == "mention" {
			reason = ReasonMentioned
		}
		b.add(GroupWatching, p, reason)
	}

	for _, p := range closed {
		if p.ClosedAt == nil || p.ClosedAt.Before(since) {
			continue
		}
		reason := ReasonClosed
		if p.MergedAt != nil {
			reason = ReasonMerged
		}
		b.add(GroupDone, p, reason)
	}
	for _, p := range reviewed {
		if last := p.lastReviewBy(); !last.IsZero() && !last.Before(since) {
			b.add(GroupDone, p, ReasonReviewed)
		}
	}

	q := Queue{Login: login, FetchedAt: now}
	for _, id := range []string{GroupWaiting, GroupYours, GroupWatching, GroupDone} {
		prs := b.groups[id]
		for i := range prs {
			prs[i].Notified = id != GroupDone && b.notified[prs[i].Ref]
		}
		slices.SortStableFunc(prs, func(a, b PR) int {
			if c := b.Updated.Compare(a.Updated); c != 0 {
				return c
			}
			return cmp.Compare(a.Ref, b.Ref)
		})
		if prs == nil {
			prs = []PR{}
		}
		q.Groups = append(q.Groups, Group{ID: id, Name: groupNames[id], PRs: prs})
	}
	return q, nil
}

func firstError(errs []error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

type builder struct {
	login    string
	marker   string
	seen     map[string]bool
	notified map[string]bool
	groups   map[string][]PR
}

// add puts p in group with reason, unless it is already in an earlier group
// or reason is empty, which is how a pull request not waiting on you is left
// out.
func (b *builder) add(group string, p pullRequest, reason string) {
	if p.Number == 0 || reason == "" {
		return
	}
	owner, repo := p.Repository.Owner.Login, p.Repository.Name
	ref := Ref(owner, repo, p.Number)
	if b.seen[ref] {
		return
	}
	b.seen[ref] = true
	author := ""
	if p.Author != nil {
		author = p.Author.Login
	}
	b.groups[group] = append(b.groups[group], PR{
		Ref:     ref,
		URL:     p.URL,
		Owner:   owner,
		Repo:    repo,
		Number:  p.Number,
		Title:   p.Title,
		Author:  author,
		Changed: p.Additions + p.Deletions,
		Updated: p.UpdatedAt,
		Draft:   p.IsDraft,
		Group:   group,
		Reason:  reason,

		RequestedFromYou: group == GroupWaiting && p.requestedFrom(b.login),
		SecurityFix:      p.securityFix(author),
	})
}

// blockedOnYou is why a pull request of yours waits on you, or "" when it
// waits on someone else. Failing CI comes first because nothing else can
// land until it is fixed; an approval outranks comments, since the approval
// is itself a review that arrived after your last commit.
func (b *builder) blockedOnYou(p pullRequest) string {
	switch {
	case p.ciFailing():
		return ReasonFailingCI
	case p.ReviewDecision == "CHANGES_REQUESTED":
		return ReasonChangesRequested
	case p.ReviewDecision == "APPROVED":
		return ReasonApproved
	case p.hasNewComments(b.login):
		return ReasonNewComments
	case p.IsDraft && (b.marker == "" || strings.Contains(p.Body, b.marker) || strings.Contains(p.HeadRefName, b.marker)):
		return ReasonAgentDraft
	}
	return ""
}

func (p pullRequest) requestedFrom(login string) bool {
	for _, r := range p.ReviewRequests.Nodes {
		if who := r.RequestedReviewer; who != nil && who.Kind == "User" && strings.EqualFold(who.Login, login) {
			return true
		}
	}
	return false
}

func (p pullRequest) securityFix(author string) bool {
	return namesAdvisory(p.Title) || (dependencyBump(author, p.Title) && namesAdvisory(p.Body))
}

func namesAdvisory(s string) bool {
	s = strings.ToUpper(s)
	return strings.Contains(s, "CVE-") || strings.Contains(s, "GHSA-")
}

// dependencyBump recognises the pull requests Dependabot and Renovate open,
// by their author or by the titles they use.
func dependencyBump(author, title string) bool {
	switch strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(author, "app/"), "[bot]")) {
	case "dependabot", "dependabot-preview", "renovate":
		return true
	}
	t := strings.ToLower(title)
	return strings.HasPrefix(t, "bump ") || strings.HasPrefix(t, "update dependency ") ||
		strings.Contains(t, "(deps)") || strings.Contains(t, "(deps-dev)")
}

func (p pullRequest) authoredBy(login string) bool {
	return p.Author != nil && strings.EqualFold(p.Author.Login, login)
}

func (p pullRequest) ciFailing() bool {
	for _, c := range p.Commits.Nodes {
		if r := c.Commit.StatusCheckRollup; r != nil && (r.State == "FAILURE" || r.State == "ERROR") {
			return true
		}
	}
	return false
}

// hasNewComments says whether the latest comment or review is someone
// else's and came after the last commit. When it is someone else's, every
// comment of yours is older by definition, so the commit is the only other
// moment to compare with. Bots wait on no one, so their comments are skipped;
// the last few are read so that one bot comment does not hide a person's.
func (p pullRequest) hasNewComments(login string) bool {
	var latest activity
	for _, a := range append(slices.Clone(p.Comments.Nodes), p.Reviews.Nodes...) {
		if a.Author != nil && a.Author.Kind == "Bot" {
			continue
		}
		if a.at().After(latest.at()) {
			latest = a
		}
	}
	if latest.at().IsZero() || strings.EqualFold(latest.by(), login) {
		return false
	}
	for _, c := range p.Commits.Nodes {
		if !latest.at().After(c.Commit.CommittedDate) {
			return false
		}
	}
	return true
}

// lastReviewBy is when the signed-in user last submitted a review; a pending
// review has not been submitted and does not count.
func (p pullRequest) lastReviewBy() time.Time {
	var last time.Time
	for _, a := range p.Mine.Nodes {
		if a.State != "PENDING" && a.SubmittedAt != nil && a.SubmittedAt.After(last) {
			last = *a.SubmittedAt
		}
	}
	return last
}
