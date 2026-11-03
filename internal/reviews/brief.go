package reviews

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// BriefHeading names the queue's block in the morning brief.
const BriefHeading = "REVIEWS"

// TopCount is how many pull requests the brief names.
const TopCount = 3

var countLabels = map[string]string{
	GroupWaiting:  "waiting on you",
	GroupYours:    "yours blocked on you",
	GroupWatching: "watching",
}

// Summary is the queue as the morning brief shows it: how many open pull
// requests each space holds, and the few to look at first.
type Summary struct {
	// Error is the one line saying why there is no queue to show.
	Error string `json:"error,omitempty"`

	Cached       bool   `json:"cached"`
	CacheAge     string `json:"cache_age,omitempty"`
	RefreshError string `json:"refresh_error,omitempty"`

	// Counts has one entry per space holding an open pull request, in the
	// order spaces are shown; with no spaces configured, one with no name.
	Counts []SpaceCount `json:"counts"`

	// Top are the best-ranked open pull requests across every space.
	Top []Pick `json:"top"`
}

// SpaceCount is the open groups of one space that hold a pull request.
type SpaceCount struct {
	Space  string       `json:"space,omitempty"`
	Groups []GroupCount `json:"groups"`
}

// GroupCount is how many pull requests one open group holds in a space;
// Notified says a notification is on at least one of them.
type GroupCount struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Count    int    `json:"count"`
	Notified bool   `json:"notified"`
}

// Pick is a pull request the brief names, with the one line saying why.
type Pick struct {
	PR
	Why string `json:"why"`
}

// Summarize condenses an arranged report into the brief's block. It counts
// the open groups only: Declined and Done this week wait on no one.
func Summarize(r Report, now time.Time) Summary {
	s := Summary{Error: r.Error, Cached: r.Cached, CacheAge: r.CacheAge, RefreshError: r.RefreshError,
		Counts: []SpaceCount{}, Top: []Pick{}}
	if r.Error != "" {
		return s
	}

	spaces := r.Spaces
	if len(spaces) == 0 {
		spaces = []string{""}
	}
	var open []PR
	for _, space := range spaces {
		c := SpaceCount{Space: space}
		for _, g := range r.Groups {
			if !openGroup(g.ID) {
				continue
			}
			n := GroupCount{ID: g.ID, Label: countLabels[g.ID]}
			for _, p := range g.PRs {
				if p.Space == space {
					n.Count++
					n.Notified = n.Notified || p.Notified
					open = append(open, p)
				}
			}
			if n.Count > 0 {
				c.Groups = append(c.Groups, n)
			}
		}
		if len(c.Groups) > 0 {
			s.Counts = append(s.Counts, c)
		}
	}

	slices.SortStableFunc(open, byRank)
	for _, p := range open[:min(TopCount, len(open))] {
		s.Top = append(s.Top, Pick{PR: p, Why: why(p, now)})
	}
	return s
}

// why is the one line a pick is shown with: a review asked of you says how
// big and how old it is, since that is what decides when to take it.
func why(p PR, now time.Time) string {
	var w string
	switch p.Group {
	case GroupWaiting:
		w = fmt.Sprintf("%s, %d lines, %s", p.Reason, p.Changed, Age(now.Sub(p.Updated)))
	case GroupYours:
		w = "yours, " + p.Reason
	default:
		w = "watching, " + p.Reason
	}
	if p.Notified {
		w += " " + Bell
	}
	return w
}

// PrintSummary writes the brief's REVIEWS block: a counts line per space,
// then the picks, each with its URL last so a terminal makes it a link. A
// summary with no queue is its one line.
func PrintSummary(w io.Writer, s Summary) error {
	var b strings.Builder
	heading := BriefHeading
	if s.Cached {
		heading += " · cached " + s.CacheAge + " ago"
		if s.RefreshError != "" {
			heading += ", refresh failed: " + s.RefreshError
		}
	}
	fmt.Fprintln(&b, heading)
	if s.Error != "" {
		fmt.Fprintf(&b, "  %s\n", s.Error)
		_, err := io.WriteString(w, b.String())
		return err
	}
	if len(s.Counts) == 0 {
		fmt.Fprintln(&b, "  nothing waiting on you")
	}

	spaceW := 0
	for _, c := range s.Counts {
		spaceW = max(spaceW, len([]rune(c.Space)))
	}
	for _, c := range s.Counts {
		parts := make([]string, len(c.Groups))
		for i, g := range c.Groups {
			parts[i] = fmt.Sprintf("%d %s", g.Count, g.Label)
			if g.Notified {
				parts[i] += " " + Bell
			}
		}
		if spaceW > 0 {
			fmt.Fprintf(&b, "  %-*s  %s\n", spaceW, c.Space, strings.Join(parts, " · "))
		} else {
			fmt.Fprintf(&b, "  %s\n", strings.Join(parts, " · "))
		}
	}

	refW, whyW := 0, 0
	for _, p := range s.Top {
		refW = max(refW, len([]rune(p.Ref)))
		whyW = max(whyW, columns(p.Why))
	}
	for _, p := range s.Top {
		fmt.Fprintf(&b, "  → %-*s  %s%s  %s\n", refW, p.Ref, p.Why, strings.Repeat(" ", whyW-columns(p.Why)), p.URL)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
