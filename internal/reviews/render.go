package reviews

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/swilgosz/mindskein/internal/text"
)

// Heading names the queue when it is printed.
const Heading = "PULL REQUESTS"

// Bell marks a pull request with a notification on it.
const Bell = "🔔"

const maxTitleWidth = 50

type cells struct{ ref, title, size, age, reason, url string }

type layout struct {
	b                                  strings.Builder
	refW, titleW, sizeW, ageW, reasonW int
}

// Print writes the report as the terminal shows it: each group under its
// name, inside its Life Space when spaces are configured, one pull request
// per line with its URL last, so a terminal makes it a link. A report with
// no queue is its one line.
func Print(w io.Writer, r Report, now time.Time) error {
	if r.Error != "" {
		_, err := fmt.Fprintln(w, r.Error)
		return err
	}

	heading := Heading
	if r.Cached {
		heading += " · cached " + r.CacheAge + " ago"
		if r.RefreshError != "" {
			heading += ", refresh failed: " + r.RefreshError
		}
	}
	var l layout
	fmt.Fprintln(&l.b, heading)

	rows := make([][]cells, len(r.Groups))
	for i, g := range r.Groups {
		for _, p := range g.PRs {
			c := cells{
				ref:    p.Ref,
				title:  text.Truncate(text.OneLine(p.Title), maxTitleWidth),
				size:   fmt.Sprintf("%d lines", p.Changed),
				age:    Age(now.Sub(p.Updated)),
				reason: p.Reason,
				url:    p.URL,
			}
			if p.Notified {
				c.reason += " " + Bell
			}
			rows[i] = append(rows[i], c)
			l.refW = max(l.refW, len([]rune(c.ref)))
			l.titleW = max(l.titleW, len([]rune(c.title)))
			l.sizeW = max(l.sizeW, len(c.size))
			l.ageW = max(l.ageW, len(c.age))
			l.reasonW = max(l.reasonW, columns(c.reason))
		}
	}

	if len(r.Spaces) == 0 {
		for i, g := range r.Groups {
			fmt.Fprintf(&l.b, "\n%s (%d)\n", g.Name, len(g.PRs))
			if len(g.PRs) == 0 {
				fmt.Fprintln(&l.b, "  none")
			}
			l.rows("  ", rows[i])
		}
	}
	for _, space := range r.Spaces {
		fmt.Fprintf(&l.b, "\n%s\n", space)
		empty := true
		for i, g := range r.Groups {
			var in []cells
			for j, p := range g.PRs {
				if p.Space == space {
					in = append(in, rows[i][j])
				}
			}
			if len(in) == 0 {
				continue
			}
			empty = false
			fmt.Fprintf(&l.b, "  %s (%d)\n", g.Name, len(in))
			l.rows("    ", in)
		}
		if empty {
			fmt.Fprintln(&l.b, "  none")
		}
	}
	_, err := io.WriteString(w, l.b.String())
	return err
}

func (l *layout) rows(indent string, rows []cells) {
	for _, c := range rows {
		fmt.Fprintf(&l.b, "%s%-*s  %-*s  %*s  %*s  %s%s  %s\n", indent,
			l.refW, c.ref, l.titleW, c.title, l.sizeW, c.size, l.ageW, c.age,
			c.reason, strings.Repeat(" ", l.reasonW-columns(c.reason)), c.url)
	}
}

// columns is how many terminal columns s takes: the bell is drawn two wide.
func columns(s string) int {
	return len([]rune(s)) + strings.Count(s, Bell)
}
