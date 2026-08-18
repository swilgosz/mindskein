package session

import (
	"fmt"
	"io"
	"time"
)

// maxProjectWidth caps the project column so one deeply nested vault path
// cannot push the status and age columns off the edge of a terminal.
const maxProjectWidth = 28

// Render writes the LIVE SESSIONS block — the whole of `mindskein status`, and
// one of the three sections of the morning brief.
//
// It lives here rather than in internal/brief because the package that owns the
// record owns presenting it; the brief composes this block rather than
// reimplementing it.
func Render(w io.Writer, sessions []*Session, now time.Time) error {
	if _, err := fmt.Fprintln(w, "LIVE SESSIONS"); err != nil {
		return err
	}

	if len(sessions) == 0 {
		_, err := fmt.Fprintln(w, "  none recorded yet — the hooks write here as sessions run")
		return err
	}

	type row struct{ id, project, status, age, event string }
	rows := make([]row, 0, len(sessions))
	running := 0

	for _, s := range sessions {
		status := string(s.Status)
		if s.Stale(now) {
			// Nothing reports a terminated session, so a killed terminal
			// leaves its last status behind. Say so rather than imply the
			// session is still sitting there.
			status += " (stale)"
		} else if s.Status == StatusRunning {
			running++
		}
		rows = append(rows, row{
			id:      s.ShortID(),
			project: truncate(s.ProjectName(), maxProjectWidth),
			status:  status,
			age:     age(now.Sub(s.LastEventAt)),
			event:   s.LastEvent,
		})
	}

	var idW, projW, statusW, ageW int
	for _, r := range rows {
		idW = max(idW, len(r.id))
		projW = max(projW, len(r.project))
		statusW = max(statusW, len(r.status))
		ageW = max(ageW, len(r.age))
	}

	for _, r := range rows {
		line := fmt.Sprintf("  %-*s  %-*s  %-*s  %*s",
			idW, r.id, projW, r.project, statusW, r.status, ageW, r.age)
		if r.event != "" {
			line += "  (" + r.event + ")"
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}

	_, err := fmt.Fprintf(w, "\n  %s · %d running\n", plural(len(rows), "session"), running)
	return err
}

// age renders an elapsed duration at the resolution the reader cares about:
// minutes while a session is live, hours once it has been idle a while.
func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
