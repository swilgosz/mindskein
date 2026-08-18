package session

import (
	"fmt"
	"io"
	"time"
)

const (
	maxLabelWidth   = 30
	maxProjectWidth = 22
)

// RenderOptions carries what the renderer cannot work out from a session alone.
type RenderOptions struct {
	// Labels names a session by what it is, keyed by session id. The registry
	// itself cannot supply this: a title is only known once a transcript has
	// been read, which happens on Stop. Sessions with no entry fall back to
	// their folder.
	Labels map[string]string

	// ShowAll includes sessions that have ended. Off by default, so finished
	// work does not bury live work.
	//
	// Only ended sessions are hidden, never merely quiet ones: a tab left open
	// overnight is paused, not finished, and hiding it would empty the view at
	// exactly the moment it is most wanted.
	ShowAll bool
}

// Render writes the LIVE SESSIONS block: the whole of `mindskein status`, and
// one of the three sections of the morning brief.
//
// It lives here rather than in internal/brief because the package that owns the
// record owns presenting it; the brief composes this block rather than
// reimplementing it.
func Render(w io.Writer, sessions []*Session, now time.Time, opts RenderOptions) error {
	if _, err := fmt.Fprintln(w, "LIVE SESSIONS"); err != nil {
		return err
	}

	if len(sessions) == 0 {
		_, err := fmt.Fprintln(w, "  none recorded yet — the hooks write here as sessions run")
		return err
	}

	type row struct{ id, label, project, status, age, event string }
	rows := make([]row, 0, len(sessions))
	running, ended := 0, 0

	for _, s := range sessions {
		if s.Ended() {
			ended++
			if !opts.ShowAll {
				continue
			}
		}

		status := string(s.Status)
		if s.Ended() && s.EndReason != "" {
			status += " (" + s.EndReason + ")"
		} else if s.Stale(now) {
			// A hard-killed process never reports its ending, so this status
			// may simply be lying. Say so rather than imply the session is
			// still sitting there.
			status += " (stale)"
		}
		if s.Status == StatusRunning {
			running++
		}

		project := s.ProjectName()
		label := opts.Labels[s.ID]
		if label == "" {
			// Without a title the folder is all there is, and repeating it in
			// both columns would waste the width.
			label, project = project, ""
		}

		rows = append(rows, row{
			id:      s.ShortID(),
			label:   truncate(label, maxLabelWidth),
			project: truncate(project, maxProjectWidth),
			status:  status,
			age:     age(now.Sub(s.LastEventAt)),
			event:   s.LastEvent,
		})
	}

	if len(rows) == 0 {
		if _, err := fmt.Fprintf(w, "  none open — %s ended, run with --all to see them\n",
			plural(ended, "session")); err != nil {
			return err
		}
		return nil
	}

	var idW, labelW, projW, statusW, ageW int
	for _, r := range rows {
		idW = max(idW, len(r.id))
		labelW = max(labelW, len([]rune(r.label)))
		projW = max(projW, len([]rune(r.project)))
		statusW = max(statusW, len(r.status))
		ageW = max(ageW, len(r.age))
	}

	for _, r := range rows {
		line := fmt.Sprintf("  %-*s  %-*s  %-*s  %-*s  %*s",
			idW, r.id, labelW, pad(r.label, labelW), projW, pad(r.project, projW),
			statusW, r.status, ageW, r.age)
		if r.event != "" {
			line += "  (" + r.event + ")"
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}

	summary := fmt.Sprintf("\n  %s · %d running", plural(len(rows), "session"), running)
	if ended > 0 && !opts.ShowAll {
		summary += fmt.Sprintf(" · %d ended hidden (--all)", ended)
	}
	_, err := fmt.Fprintln(w, summary)
	return err
}

// pad widens by rune count, so a title containing non-ASCII still lines up.
func pad(s string, n int) string {
	for len([]rune(s)) < n {
		s += " "
	}
	return s
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
