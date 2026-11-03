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

const maxTitleWidth = 50

// Print writes the report as the terminal shows it: each group under its
// name, one pull request per line with its URL last, so a terminal makes it
// a link. A report with no queue is its one line.
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
	var b strings.Builder
	fmt.Fprintln(&b, heading)

	type cells struct{ ref, title, size, age, reason, url string }
	var refW, titleW, sizeW, ageW, reasonW int
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
			rows[i] = append(rows[i], c)
			refW = max(refW, len([]rune(c.ref)))
			titleW = max(titleW, len([]rune(c.title)))
			sizeW = max(sizeW, len(c.size))
			ageW = max(ageW, len(c.age))
			reasonW = max(reasonW, len(c.reason))
		}
	}

	for i, g := range r.Groups {
		fmt.Fprintf(&b, "\n%s (%d)\n", g.Name, len(g.PRs))
		if len(g.PRs) == 0 {
			fmt.Fprintln(&b, "  none")
			continue
		}
		for _, c := range rows[i] {
			fmt.Fprintf(&b, "  %-*s  %-*s  %*s  %*s  %-*s  %s\n",
				refW, c.ref, titleW, c.title, sizeW, c.size, ageW, c.age, reasonW, c.reason, c.url)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
