package reviews

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/swilgosz/mindskein/internal/session"
)

// MarksFile is the name of the decline and done marks in the mindskein home.
const MarksFile = "prs-marks.json"

// DeclineWindow is how long a declined pull request stays out of the queue.
const DeclineWindow = 7 * 24 * time.Hour

// The kinds of mark.
const (
	MarkDeclined = "declined"
	MarkDone     = "done"
)

// ReasonMarkedDone is the reason of a pull request you marked done.
const ReasonMarkedDone = "marked done"

// Mark keeps a pull request out of the queue: a declined one for
// DeclineWindow, a done one until it changes. Marks are local only; GitHub
// never learns of them.
type Mark struct {
	Ref      string    `json:"ref"`
	Kind     string    `json:"mark"`
	MarkedAt time.Time `json:"marked_at"`

	// Head and Requested are what a done mark compares against: the head
	// commit, and whether your review was requested, when it was marked. A
	// done mark without a head was made with GitHub out of reach, and the
	// pull request comes back once it is updated after the mark.
	Head      string `json:"head,omitempty"`
	Requested bool   `json:"requested,omitempty"`

	// URL, Title, Author, Changed and Updated describe a done pull request
	// for Done this week when the queue no longer lists it.
	URL     string    `json:"url,omitempty"`
	Title   string    `json:"title,omitempty"`
	Author  string    `json:"author,omitempty"`
	Changed int       `json:"changed_lines,omitempty"`
	Updated time.Time `json:"updated_at"`
}

const errRef = "write a pull request as owner/repo#N, like acme/api#12, or as its URL, https://github.com/acme/api/pull/12"

// ParseRef reads a pull request written owner/repo#N or as its GitHub URL,
// and returns it written owner/repo#N.
func ParseRef(s string) (string, error) {
	in := strings.TrimSpace(s)
	var owner, repo, number string
	if rest, ok := cutURL(in); ok {
		parts := strings.SplitN(rest, "/", 5)
		if len(parts) < 4 || parts[2] != "pull" {
			return "", fmt.Errorf("%q is not a pull request: %s", s, errRef)
		}
		owner, repo, number = parts[0], parts[1], parts[3]
	} else {
		path, n, ok := strings.Cut(in, "#")
		if !ok {
			return "", fmt.Errorf("%q is not a pull request: %s", s, errRef)
		}
		owner, repo, _ = strings.Cut(path, "/")
		number = n
	}
	n, err := strconv.Atoi(number)
	if err != nil || n <= 0 || !validName(owner) || !validName(repo) {
		return "", fmt.Errorf("%q is not a pull request: %s", s, errRef)
	}
	return Ref(owner, repo, n), nil
}

// cutURL strips a GitHub URL down to its path, dropping any query or
// fragment.
func cutURL(s string) (string, bool) {
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "github.com/"} {
		if rest, ok := strings.CutPrefix(s, prefix); ok {
			rest, _, _ = strings.Cut(rest, "?")
			rest, _, _ = strings.Cut(rest, "#")
			return rest, true
		}
	}
	return "", false
}

func validName(s string) bool {
	return s != "" && !strings.ContainsAny(s, "/#?:@ \t\n")
}

// splitRef is the owner, repo and number of a ref ParseRef returned.
func splitRef(ref string) (owner, repo string, number int) {
	path, n, _ := strings.Cut(ref, "#")
	owner, repo, _ = strings.Cut(path, "/")
	number, _ = strconv.Atoi(n)
	return owner, repo, number
}

// NewDoneMark marks ref done now: its head commit and whether your review
// is requested, from the queue when it lists ref and from GitHub otherwise.
// With GitHub out of reach it returns a mark without a head and the one line
// saying why; the error is only a pull request GitHub does not know.
func NewDoneMark(ctx context.Context, opts Options, ref string) (Mark, string, error) {
	m := Mark{Ref: ref, Kind: MarkDone, MarkedAt: opts.Now}
	report, _ := Read(ctx, opts)
	if report.Error == "" {
		for _, g := range report.Groups {
			for _, p := range g.PRs {
				if strings.EqualFold(p.Ref, ref) {
					return snapshot(m, p, g.ID == GroupWaiting), "", nil
				}
			}
		}
	}

	owner, repo, number := splitRef(ref)
	g := github{run: opts.Run, timeout: opts.Timeout}
	found, err := g.details(ctx, []watch{{owner: owner, repo: repo, number: number}})
	if err != nil {
		return m, Explain(err), nil
	}
	p, ok := found[ref]
	if !ok {
		return Mark{}, "", fmt.Errorf("%s: GitHub has no such pull request", ref)
	}
	return snapshot(m, p.pr(GroupDone, ReasonMarkedDone), false), "", nil
}

func snapshot(m Mark, p PR, requested bool) Mark {
	m.Ref, m.Head, m.Requested = p.Ref, p.Head, requested
	m.URL, m.Title, m.Author, m.Changed, m.Updated = p.URL, p.Title, p.Author, p.Changed, p.Updated
	return m
}

// changed says p is no longer what m marked done: a new head commit, a
// review newly requested from you, or, for a mark made without a head, any
// update since.
func (m Mark) changed(p PR, group string) bool {
	if m.Head == "" {
		return p.Updated.After(m.MarkedAt)
	}
	if p.Head != "" && p.Head != m.Head {
		return true
	}
	return group == GroupWaiting && !m.Requested
}

func openGroup(id string) bool {
	return id == GroupWaiting || id == GroupYours || id == GroupWatching
}

// Apply takes the marks into a fetched queue. A declined pull request moves
// to Declined until its DeclineWindow is over. A done one leaves the queue
// until it changes, and shows in Done this week for DoneWindow after it was
// marked, whether the queue lists it or not. Declined is left out while it
// is empty.
func Apply(q Queue, marks []Mark, now time.Time) Queue {
	if len(marks) == 0 {
		return q
	}
	byRef := make(map[string]Mark, len(marks))
	for _, m := range marks {
		byRef[strings.ToLower(m.Ref)] = m
	}
	listed := map[string]bool{}
	var declined, done []PR
	var groups []Group
	for _, g := range q.Groups {
		prs := []PR{}
		for _, p := range g.PRs {
			listed[strings.ToLower(p.Ref)] = true
			m, ok := byRef[strings.ToLower(p.Ref)]
			switch {
			case !ok || !openGroup(g.ID):
			case m.Kind == MarkDeclined && now.Before(m.MarkedAt.Add(DeclineWindow)):
				p.Group = GroupDeclined
				p.Reason = "declined, back in " + Age(m.MarkedAt.Add(DeclineWindow).Sub(now))
				declined = append(declined, p)
				continue
			case m.Kind == MarkDone && !m.changed(p, g.ID):
				if now.Sub(m.MarkedAt) < DoneWindow {
					p.Group, p.Reason, p.Notified = GroupDone, ReasonMarkedDone, false
					done = append(done, p)
				}
				continue
			}
			prs = append(prs, p)
		}
		g.PRs = prs
		groups = append(groups, g)
	}
	for _, m := range marks {
		if m.Kind == MarkDone && !listed[strings.ToLower(m.Ref)] && now.Sub(m.MarkedAt) < DoneWindow {
			done = append(done, m.pr())
		}
	}

	q.Groups = nil
	for _, g := range groups {
		if g.ID == GroupDone {
			if len(declined) > 0 {
				q.Groups = append(q.Groups, Group{ID: GroupDeclined, Name: groupNames[GroupDeclined], PRs: newestFirst(declined)})
			}
			g.PRs = newestFirst(append(g.PRs, done...))
		}
		q.Groups = append(q.Groups, g)
	}
	return q
}

// pr is the pull request a done mark describes.
func (m Mark) pr() PR {
	owner, repo, number := splitRef(m.Ref)
	return PR{
		Ref: m.Ref, URL: m.URL, Owner: owner, Repo: repo, Number: number,
		Title: m.Title, Author: m.Author, Changed: m.Changed, Updated: m.Updated,
		Group: GroupDone, Reason: ReasonMarkedDone,
	}
}

func newestFirst(prs []PR) []PR {
	slices.SortStableFunc(prs, func(a, b PR) int {
		if c := b.Updated.Compare(a.Updated); c != 0 {
			return c
		}
		return cmp.Compare(a.Ref, b.Ref)
	})
	return prs
}

// Marks is the file of decline and done marks. The prs subcommands write it;
// prs reads it and prune sweeps it, judging done marks by the last queue
// fetched, cached at CachePath.
type Marks struct {
	Path      string
	CachePath string
}

// DefaultMarks lives in the mindskein home, beside the cached queue.
func DefaultMarks() (*Marks, error) {
	home, err := session.Home()
	if err != nil {
		return nil, err
	}
	return &Marks{Path: filepath.Join(home, MarksFile), CachePath: filepath.Join(home, CacheFile)}, nil
}

type marksDoc struct {
	Marks []Mark `json:"marks"`
}

// Load reads every mark, oldest first. A missing or empty file means none.
func (s *Marks) Load() ([]Mark, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var doc marksDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", s.Path, err)
	}
	return doc.Marks, nil
}

// Put records m, replacing an earlier mark of the same pull request.
func (s *Marks) Put(m Mark) error {
	all, err := s.Load()
	if err != nil {
		return err
	}
	all = slices.DeleteFunc(all, func(o Mark) bool { return strings.EqualFold(o.Ref, m.Ref) })
	return s.save(append(all, m))
}

// Prune removes the marks that keep nothing out any more, once they are
// DeclineWindow old: every decline, and a done mark when the last queue
// fetched after it was made no longer lists the pull request as open or
// shows it changed. A done mark the last fetch cannot judge is kept.
func (s *Marks) Prune(now time.Time, dryRun bool) (*session.PruneResult, error) {
	res := &session.PruneResult{Kind: "PR mark", Horizon: DeclineWindow, DryRun: dryRun}
	all, err := s.Load()
	if err != nil {
		return nil, err
	}
	cached, haveCache := loadCache(s.CachePath)
	open := map[string]Group{}
	for _, g := range cached.Queue.Groups {
		if openGroup(g.ID) {
			open[g.ID] = g
		}
	}
	lookUp := func(ref string) (PR, string, bool) {
		for id, g := range open {
			for _, p := range g.PRs {
				if strings.EqualFold(p.Ref, ref) {
					return p, id, true
				}
			}
		}
		return PR{}, "", false
	}

	kept := make([]Mark, 0, len(all))
	for _, m := range all {
		gone := false
		if now.Sub(m.MarkedAt) >= DeclineWindow {
			switch m.Kind {
			case MarkDone:
				if haveCache && cached.Queue.FetchedAt.After(m.MarkedAt) {
					p, group, ok := lookUp(m.Ref)
					gone = !ok || m.changed(p, group)
				}
			default:
				gone = true
			}
		}
		if gone {
			res.Removed = append(res.Removed, m.Ref)
		} else {
			kept = append(kept, m)
		}
	}
	res.Kept = len(kept)
	if dryRun || len(res.Removed) == 0 {
		return res, nil
	}
	return res, s.save(kept)
}

func (s *Marks) save(all []Mark) error {
	if all == nil {
		all = []Mark{}
	}
	data, err := json.MarshalIndent(marksDoc{Marks: all}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.Path, data, ".tmp-prs-marks-*")
}
