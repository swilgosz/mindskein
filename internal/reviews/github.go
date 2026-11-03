// Package reviews builds the pull request queue: what on GitHub waits on you,
// read through the gh CLI the user is already signed in to.
//
// It only ever reads. Every call is a GET or a GraphQL query, so nothing here
// can review, approve, comment on or merge a pull request.
package reviews

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Runner runs gh with args and returns what it printed on stdout. Tests
// substitute one that answers from fixtures, so no test reaches GitHub.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// GH runs the gh CLI on PATH.
func GH(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1")
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return stdout.Bytes(), ctx.Err()
	}
	if err != nil {
		return stdout.Bytes(), &GHError{Err: err, Stderr: strings.TrimSpace(stderr.String())}
	}
	return stdout.Bytes(), nil
}

// GHError is gh exiting with an error, with what it said on stderr.
type GHError struct {
	Err    error
	Stderr string
}

func (e *GHError) Error() string {
	if e.Stderr != "" {
		return e.Stderr
	}
	return e.Err.Error()
}

func (e *GHError) Unwrap() error { return e.Err }

// The searches use the qualifiers of `gh search prs`; GraphQL search takes
// them as they are and returns sizes, reviews and CI in the same call.
const (
	searchWaiting  = "is:pr is:open review-requested:@me"
	searchMine     = "is:pr is:open author:@me"
	searchClosed   = "is:pr is:closed author:@me closed:>=%s"
	searchReviewed = "is:pr reviewed-by:@me -author:@me updated:>=%s"
)

// DoneWindow is how far back Done this week reaches.
const DoneWindow = 7 * 24 * time.Hour

// Asking for sizes is what makes a search slow: GitHub works out each diff,
// and a hundred of them take longer than it allows one query. Pages of 50
// stay well inside that.
const (
	pageSize = 50
	maxPages = 10
)

// The fields read for every pull request; open ones add what decides whether
// they wait on you.
const (
	baseFields = `number title url isDraft state additions deletions updatedAt mergedAt closedAt
    author { login }
    repository { name owner { login } }`
	openFields = baseFields + `
    reviewDecision headRefName headRefOid
    commits(last: 1) { nodes { commit { committedDate statusCheckRollup { state } } } }
    comments(last: 5) { nodes { author { login __typename } createdAt } }
    reviews(last: 5) { nodes { author { login __typename } submittedAt } }
    body`
	waitingFields  = openFields + "\n    reviewRequests(first: 20) { nodes { requestedReviewer { __typename ... on User { login } } } }"
	reviewedFields = baseFields + "\n    mine: reviews(author: $me, last: 5) { nodes { state submittedAt } }"
)

type actor struct {
	Login string `json:"login"`
	Kind  string `json:"__typename"`
}

type activity struct {
	Author      *actor     `json:"author"`
	State       string     `json:"state"`
	CreatedAt   *time.Time `json:"createdAt"`
	SubmittedAt *time.Time `json:"submittedAt"`
}

func (a activity) at() time.Time {
	switch {
	case a.SubmittedAt != nil:
		return *a.SubmittedAt
	case a.CreatedAt != nil:
		return *a.CreatedAt
	}
	return time.Time{}
}

func (a activity) by() string {
	if a.Author == nil {
		return ""
	}
	return a.Author.Login
}

type nodes[T any] struct {
	Nodes []T `json:"nodes"`
}

type pullRequest struct {
	Number      int        `json:"number"`
	Title       string     `json:"title"`
	URL         string     `json:"url"`
	IsDraft     bool       `json:"isDraft"`
	State       string     `json:"state"`
	Body        string     `json:"body"`
	HeadRefName string     `json:"headRefName"`
	HeadRefOid  string     `json:"headRefOid"`
	Additions   int        `json:"additions"`
	Deletions   int        `json:"deletions"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	MergedAt    *time.Time `json:"mergedAt"`
	ClosedAt    *time.Time `json:"closedAt"`
	Author      *actor     `json:"author"`
	Repository  struct {
		Name  string `json:"name"`
		Owner actor  `json:"owner"`
	} `json:"repository"`
	ReviewDecision string `json:"reviewDecision"`
	Commits        nodes[struct {
		Commit struct {
			CommittedDate     time.Time `json:"committedDate"`
			StatusCheckRollup *struct {
				State string `json:"state"`
			} `json:"statusCheckRollup"`
		} `json:"commit"`
	}] `json:"commits"`
	Comments nodes[activity] `json:"comments"`
	Reviews  nodes[activity] `json:"reviews"`
	Mine     nodes[activity] `json:"mine"`

	ReviewRequests nodes[struct {
		RequestedReviewer *actor `json:"requestedReviewer"`
	}] `json:"reviewRequests"`
}

type notification struct {
	Reason  string `json:"reason"`
	Subject struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"subject"`
}

// github is one round of calls, each cut off after timeout.
type github struct {
	run     Runner
	timeout time.Duration
}

func (g github) call(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	return g.run(ctx, args...)
}

func (g github) login(ctx context.Context) (string, error) {
	out, err := g.call(ctx, "api", "user")
	if err != nil {
		return "", err
	}
	var user actor
	if err := json.Unmarshal(out, &user); err != nil {
		return "", fmt.Errorf("reading gh api user: %w", err)
	}
	if user.Login == "" {
		return "", errors.New("gh api user returned no login")
	}
	return user.Login, nil
}

// graphql runs query and decodes its data into into. A response that carries
// data alongside errors is used: one deleted repository must not cost the
// rest of the queue.
func (g github) graphql(ctx context.Context, query string, into any, vars ...string) error {
	args := append([]string{"api", "graphql", "-f", "query=" + query}, vars...)
	out, runErr := g.call(ctx, args...)
	var resp struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || len(resp.Data) == 0 || string(resp.Data) == "null" {
		if runErr != nil {
			return runErr
		}
		if len(resp.Errors) > 0 {
			return errors.New(resp.Errors[0].Message)
		}
		if err != nil {
			return fmt.Errorf("reading gh api graphql: %w", err)
		}
		return errors.New("gh api graphql returned no data")
	}
	if err := json.Unmarshal(resp.Data, into); err != nil {
		return fmt.Errorf("reading gh api graphql: %w", err)
	}
	return nil
}

// search returns every pull request matching q, page by page.
func (g github) search(ctx context.Context, login, q, fields string) ([]pullRequest, error) {
	params, vars := "$q: String!, $after: String", []string{"-f", "q=" + q}
	if strings.Contains(fields, "$me") {
		params += ", $me: String!"
		vars = append(vars, "-f", "me="+login)
	}
	query := fmt.Sprintf(`query(%s) {
  search(query: $q, type: ISSUE, first: %d, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes { ... on PullRequest { %s } }
  }
}`, params, pageSize, fields)

	var all []pullRequest
	after := []string{}
	for range maxPages {
		var page struct {
			Search struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []pullRequest `json:"nodes"`
			} `json:"search"`
		}
		if err := g.graphql(ctx, query, &page, append(vars, after...)...); err != nil {
			return nil, err
		}
		all = append(all, page.Search.Nodes...)
		if !page.Search.PageInfo.HasNextPage || page.Search.PageInfo.EndCursor == "" {
			break
		}
		after = []string{"-f", "after=" + page.Search.PageInfo.EndCursor}
	}
	return all, nil
}

// watched are the pull requests notifications say you were mentioned in or
// commented on, with the reason, newest first.
func (g github) watched(ctx context.Context) ([]watch, error) {
	out, err := g.call(ctx, "api", "notifications?participating=true&per_page=100")
	if err != nil {
		return nil, err
	}
	var list []notification
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("reading gh api notifications: %w", err)
	}
	var watches []watch
	seen := map[string]bool{}
	for _, n := range list {
		if n.Subject.Type != "PullRequest" || (n.Reason != "mention" && n.Reason != "comment") {
			continue
		}
		w, ok := parsePullURL(n.Subject.URL)
		if !ok || seen[w.ref()] {
			continue
		}
		seen[w.ref()] = true
		w.reason = n.Reason
		watches = append(watches, w)
	}
	return watches, nil
}

type watch struct {
	owner, repo string
	number      int
	reason      string
}

func (w watch) ref() string { return Ref(w.owner, w.repo, w.number) }

// parsePullURL reads owner, repo and number from a REST pull request URL,
// https://api.github.com/repos/<owner>/<repo>/pulls/<n>.
func parsePullURL(u string) (watch, bool) {
	_, path, ok := strings.Cut(u, "/repos/")
	if !ok {
		return watch{}, false
	}
	parts := strings.Split(path, "/")
	if len(parts) != 4 || parts[2] != "pulls" || parts[0] == "" || parts[1] == "" {
		return watch{}, false
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n <= 0 {
		return watch{}, false
	}
	return watch{owner: parts[0], repo: parts[1], number: n}, true
}

// details looks up each watched pull request, pageSize at a time: a query
// holding more of them runs into GitHub's time limit, as a search page does.
func (g github) details(ctx context.Context, watches []watch) (map[string]pullRequest, error) {
	found := map[string]pullRequest{}
	for batch := range slices.Chunk(watches, pageSize) {
		if err := g.lookUp(ctx, batch, found); err != nil {
			return nil, err
		}
	}
	return found, nil
}

// lookUp adds the pull requests of one batch of watches to found, in one query.
func (g github) lookUp(ctx context.Context, watches []watch, found map[string]pullRequest) error {
	var params, body strings.Builder
	var vars []string
	for i, w := range watches {
		if i > 0 {
			params.WriteString(", ")
		}
		fmt.Fprintf(&params, "$o%d: String!, $n%d: String!, $r%d: Int!", i, i, i)
		fmt.Fprintf(&body, "  p%d: repository(owner: $o%d, name: $n%d) { pullRequest(number: $r%d) { %s } }\n", i, i, i, i, openFields)
		vars = append(vars,
			"-f", fmt.Sprintf("o%d=%s", i, w.owner),
			"-f", fmt.Sprintf("n%d=%s", i, w.repo),
			"-F", fmt.Sprintf("r%d=%d", i, w.number))
	}
	query := "query(" + params.String() + ") {\n" + body.String() + "}"
	var data map[string]*struct {
		PullRequest *pullRequest `json:"pullRequest"`
	}
	if err := g.graphql(ctx, query, &data, vars...); err != nil {
		return err
	}
	for i, w := range watches {
		if r := data["p"+strconv.Itoa(i)]; r != nil && r.PullRequest != nil {
			found[w.ref()] = *r.PullRequest
		}
	}
	return nil
}
