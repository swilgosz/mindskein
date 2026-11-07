package session

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/swilgosz/mindskein/internal/config"
	"github.com/swilgosz/mindskein/internal/text"
)

// Heading names the block, and is what a caller prints when the block itself
// could not be produced.
const Heading = "LIVE SESSIONS"

// finished are the states that mean the session is over and can be folded away
// by default. Interrupted is deliberately absent: it ended without reporting
// it, which is the opposite of finished business.
var finished = map[State]bool{
	StateEnded:      true,
	StateSuperseded: true,
	StateClosed:     true,
}

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

	// Probe establishes whether the process behind a record is still there.
	// The zero value cannot tell, and a session is then reported from what it
	// last said rather than from a guess about what it is doing now.
	Probe Probe
}

// Row is one session as the block shows it, before any column width applies.
type Row struct {
	ID          string    `json:"id"`
	ShortID     string    `json:"short_id"`
	Label       string    `json:"label"`
	Project     string    `json:"project,omitempty"`
	ProjectPath string    `json:"project_path"`
	State       State     `json:"state"`
	Status      string    `json:"status"`
	LastEventAt time.Time `json:"last_event_at"`
	Age         string    `json:"age"`
	Event       string    `json:"event,omitempty"`
}

// View is the LIVE SESSIONS block decided but not yet printed. The terminal
// and the JSON both print it, so they cannot disagree about what is hidden.
type View struct {
	Rows        []Row `json:"rows"`
	Running     int   `json:"running"`
	Interrupted int   `json:"interrupted"`

	// Hidden names what was left out and why; empty when nothing was, or
	// when everything was asked for.
	Hidden string `json:"hidden,omitempty"`

	// Hint stands in for the rows when there are none.
	Hint string `json:"hint,omitempty"`
}

// BuildView decides which sessions show and how each one reads.
func BuildView(sessions []*Session, now time.Time, opts RenderOptions) View {
	if len(sessions) == 0 {
		return View{Rows: []Row{}, Hint: "none recorded yet — the hooks write here as sessions run"}
	}

	v := View{Rows: []Row{}}
	ended, old := 0, 0
	for _, s := range sessions {
		// A session past the horizon that also ended counts as ended: that is
		// the reported fact, and counting it twice would overstate the total.
		state := s.Reported(now, opts.Probe)

		// An interrupted session is never hidden for being finished, because
		// it is the one thing here that is not finished: it stopped without
		// saying so, and it is the reason this block is read at all.
		hide := true
		switch {
		case finished[state]:
			ended++
		case s.Older(now, opts.HideAfter):
			old++
		default:
			hide = false
		}
		if hide && !opts.ShowAll {
			continue
		}

		status := string(state)
		if state == StateEnded && s.EndReason != "" {
			status += " (" + s.EndReason + ")"
		}
		switch state {
		case StateRunning:
			v.Running++
		case StateInterrupted:
			v.Interrupted++
		}

		project := s.ProjectName()
		label := opts.Labels[s.ID]
		if label == "" {
			// Without a title the folder is all there is, and repeating it in
			// both columns would waste the width.
			label, project = project, ""
		}

		v.Rows = append(v.Rows, Row{
			ID:          s.ID,
			ShortID:     s.ShortID(),
			Label:       label,
			Project:     project,
			ProjectPath: s.ProjectPath,
			State:       state,
			Status:      status,
			LastEventAt: s.LastEventAt,
			Age:         age(now.Sub(s.LastEventAt)),
			Event:       s.LastEvent,
		})
	}

	if !opts.ShowAll {
		v.Hidden = hiddenSummary(ended, old, opts.HideAfter)
	}
	if len(v.Rows) == 0 {
		v.Hint = fmt.Sprintf("none open — %s, run with --all to see them", v.Hidden)
	}
	return v
}

// Render writes the LIVE SESSIONS block: the whole of `mindskein status`, and
// one of the three sections of the morning brief.
//
// It lives here rather than in internal/brief because the package that owns the
// record owns presenting it; the brief composes this block rather than
// reimplementing it.
func Render(w io.Writer, sessions []*Session, now time.Time, opts RenderOptions) error {
	return Print(w, BuildView(sessions, now, opts))
}

// Print writes a view as the terminal shows it.
func Print(w io.Writer, v View) error {
	if _, err := fmt.Fprintln(w, Heading); err != nil {
		return err
	}
	if v.Hint != "" {
		_, err := fmt.Fprintln(w, "  "+v.Hint)
		return err
	}

	type cells struct{ id, label, project, status, age string }
	rows := make([]cells, len(v.Rows))
	var idW, labelW, projW, statusW, ageW int
	for i, r := range v.Rows {
		c := cells{r.ShortID, text.Truncate(r.Label, maxLabelWidth), text.Truncate(r.Project, maxProjectWidth), r.Status, r.Age}
		rows[i] = c
		idW = max(idW, len(c.id))
		labelW = max(labelW, len([]rune(c.label)))
		projW = max(projW, len([]rune(c.project)))
		statusW = max(statusW, len(c.status))
		ageW = max(ageW, len(c.age))
	}

	for i, c := range rows {
		line := fmt.Sprintf("  %-*s  %-*s  %-*s  %-*s  %*s",
			idW, c.id, labelW, c.label, projW, c.project,
			statusW, c.status, ageW, c.age)
		if e := v.Rows[i].Event; e != "" {
			line += "  (" + e + ")"
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}

	summary := fmt.Sprintf("\n  %s · %d running", plural(len(v.Rows), "session"), v.Running)
	if v.Interrupted > 0 {
		// Named in the summary, not just in a row: a session that stopped
		// without saying so is the one line here worth acting on, and it
		// should not have to be spotted by scanning a column.
		summary += fmt.Sprintf(" · %d interrupted", v.Interrupted)
	}
	if v.Hidden != "" {
		summary += fmt.Sprintf(" · %s hidden (--all)", v.Hidden)
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

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
