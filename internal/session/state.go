package session

import (
	"errors"
	"time"

	"github.com/swilgosz/mindskein/internal/proc"
)

// State is what a session is reported as, which is not the same as what its
// record stores.
//
// Activity — running, waiting, done, ended — is what the last hook event said,
// and is written to disk. Liveness is worked out at read time and never stored:
// the moment it is written down it is a claim about the past, and the whole
// failure this corrects is a record whose status stopped being true the instant
// the process died.
type State string

const (
	StateRunning     State = "running"
	StateWaiting     State = "waiting"
	StateEnded       State = "ended"
	StateSuperseded  State = "superseded"
	StateInterrupted State = "interrupted"
	StateClosed      State = "closed"
	StateUnknown     State = "unknown"
)

// Lost reports whether a state means work may have been lost. It is the one
// distinction the previous renderer could not draw, because a killed session
// and a finished one both looked like a record that stopped changing.
func (s State) Lost() bool { return s == StateInterrupted }

// supersedingReasons are the endings that replace a session rather than finish
// it. /clear mints a new session id inside the same process, and a resume
// continues the work elsewhere; calling either "ended" reads as work completed.
var supersedingReasons = map[string]bool{"clear": true, "resume": true}

// Probe answers liveness questions about a pid. It is a struct of functions so
// a scenario can stage a dead or recycled process without killing one.
type Probe struct {
	Boot        func() (time.Time, error)
	SameProcess func(pid int, started time.Time) (bool, error)
}

// SystemProbe asks the kernel.
func SystemProbe() Probe {
	return Probe{Boot: proc.BootTime, SameProcess: proc.SameProcess}
}

// liveness is the derived half, deliberately unexported: it exists only long
// enough to produce a State.
type liveness int

const (
	livenessUnknown liveness = iota
	livenessAlive
	livenessGone
)

// Reported derives what to show. The precedence is fixed, cheapest decisive
// answer first:
//
//  1. a reported ending — the one thing the session said about itself
//  2. the boot time — nothing survives a reboot, and this settles it without
//     looking up any process
//  3. pid liveness, guarded by start time
//  4. the staleness horizon, which only decides when nothing above could
func (s *Session) Reported(now time.Time, p Probe) State {
	if s.Status == StatusEnded {
		if supersedingReasons[s.EndReason] {
			return StateSuperseded
		}
		return StateEnded
	}

	switch s.liveness(now, p) {
	case livenessAlive:
		return s.active()
	case livenessGone:
		// Only a session that was mid-turn lost anything. A terminal closed
		// between turns is ordinary, and calling that interrupted would cry
		// wolf often enough to make the real case unfindable.
		if s.Status == StatusRunning {
			return StateInterrupted
		}
		return StateClosed
	}

	if now.Sub(s.LastEventAt) > StaleAfter {
		return StateUnknown
	}
	return s.active()
}

// active maps stored activity onto a reported state for a process known to be
// there. A finished turn in a live process is a session waiting for you, which
// is what "done" always meant and never said.
func (s *Session) active() State {
	if s.Status == StatusRunning {
		return StateRunning
	}
	return StateWaiting
}

func (s *Session) liveness(now time.Time, p Probe) liveness {
	// Every check below is about a process this record names. Without a pid it
	// names none, so there is nothing to confirm and nothing to refute —
	// calling that "gone" would be inventing a process in order to declare it
	// dead. The horizon decides instead.
	if s.PID <= 0 {
		return livenessUnknown
	}
	// Nothing that was running before the machine came up is running now, and
	// that settles it without looking up any process.
	if p.Boot != nil {
		if boot, err := p.Boot(); err == nil && !boot.IsZero() && s.LastEventAt.Before(boot) {
			return livenessGone
		}
	}
	if p.SameProcess == nil {
		return livenessUnknown
	}
	same, err := p.SameProcess(s.PID, s.PIDStartedAt)
	switch {
	case errors.Is(err, proc.ErrNotRunning):
		return livenessGone
	case err != nil:
		// Unsupported platform, or a record written before the start time was
		// captured. Reporting a live session as dead is the worse mistake.
		return livenessUnknown
	case same:
		return livenessAlive
	default:
		// The pid is held, but by something that started at a different time.
		return livenessGone
	}
}
