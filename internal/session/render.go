package session

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/swilgosz/mindskein/internal/config"
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

	// ShowAll includes sessions that have ended or aged out. Off by default, so
	// finished work does not bury live work.
	//
	// Being quiet is not on its own a reason to hide: a tab left open overnight
	// is paused, not finished, and hiding it would empty the view at exactly
	// the moment it is most wanted. Only HideAfter, set far past any real
	// pause, applies to silence.
	ShowAll bool

	// HideAfter drops sessions quiet for longer than this, whatever status
	// they claim: at that distance a record still saying "running" is a
	// process that died without reporting it. Zero hides nothing by age.
	//
	// The horizon has to clear the longest real pause rather than the typical
	// one, which is why it is measured in days and configured rather than
	// guessed here.
	HideAfter time.Duration
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
	running, ended, old := 0, 0, 0

	for _, s := range sessions {
		// A session past the horizon that also ended counts as ended: that is
		// the reported fact, and counting it twice would overstate the total.
		hide := true
		switch {
		case s.Ended():
			ended++
		case s.Older(now, opts.HideAfter):
			old++
		default:
			hide = false
		}
		if hide && !opts.ShowAll {
			continue
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
		// A stale status may simply be lying, so it cannot be counted as
		// running: the summary would then contradict the row above it.
		if s.Status == StatusRunning && !s.Stale(now) {
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

	hidden := hiddenSummary(ended, old, opts.HideAfter)

	if len(rows) == 0 {
		if _, err := fmt.Fprintf(w, "  none open — %s, run with --all to see them\n",
			hidden); err != nil {
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
	if hidden != "" && !opts.ShowAll {
		summary += fmt.Sprintf(" · %s hidden (--all)", hidden)
	}
	_, err := fmt.Fprintln(w, summary)
	return err
}

// hiddenSummary names what was left out and why, so a short list is never
// mistaken for the whole registry. The horizon is spelled by the same type
// that parses it, so what status reports back matches what was configured.
func hiddenSummary(ended, old int, horizon time.Duration) string {
	var parts []string
	if ended > 0 {
		parts = append(parts, fmt.Sprintf("%d ended", ended))
	}
	if old > 0 {
		parts = append(parts, fmt.Sprintf("%d older than %s", old, config.Duration(horizon)))
	}
	return strings.Join(parts, " and ")
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
