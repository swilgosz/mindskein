package session

import (
	"testing"
	"time"

	"github.com/swilgosz/mindskein/internal/proc"
)

// The claim this unit corrects: a session that finished its turn and a session
// whose terminal was killed mid-Edit both become a record that simply stops
// changing. Only one of them lost work, and it was the one the brief hid.

// staged builds a probe with a fixed answer, so a dead process can be staged
// without killing one.
func staged(boot time.Time, alive bool, err error) Probe {
	return Probe{
		Boot:        func() (time.Time, error) { return boot, nil },
		SameProcess: func(int, time.Time) (bool, error) { return alive, err },
	}
}

var (
	bootedLongAgo = at.Add(-30 * 24 * time.Hour)
	aliveProbe    = staged(bootedLongAgo, true, nil)
	deadProbe     = staged(bootedLongAgo, false, proc.ErrNotRunning)
	reusedProbe   = staged(bootedLongAgo, false, nil)
	blindProbe    = staged(time.Time{}, false, proc.ErrUnsupported)
)

func record(status Status, lastEvent string, silent time.Duration) *Session {
	return &Session{
		ID: "aaaa1111", ProjectPath: "/tmp/x", PID: 4242,
		PIDStartedAt: at.Add(-time.Hour),
		Status:       status, LastEvent: lastEvent,
		StartedAt: at.Add(-silent - time.Hour), LastEventAt: at.Add(-silent),
	}
}

func TestReportedStateFromAReportedEnding(t *testing.T) {
	// A reported ending outranks everything derived: it is the one thing the
	// session told us about itself.
	for _, c := range []struct {
		reason string
		want   State
	}{
		{"logout", StateEnded},
		{"prompt_input_exit", StateEnded},
		{"other", StateEnded},
		{"clear", StateSuperseded},
		{"resume", StateSuperseded},
	} {
		t.Run(c.reason, func(t *testing.T) {
			s := record(StatusEnded, c.reason, time.Minute)
			s.EndReason = c.reason
			if got := s.Reported(at, aliveProbe); got != c.want {
				t.Errorf("Reported = %q, want %q", got, c.want)
			}
		})
	}

	t.Run("a cleared session is not an ended one", func(t *testing.T) {
		// /clear mints a new session id inside the same process. Reporting
		// that as "ended" reads as work finished; it was replaced.
		s := record(StatusEnded, "clear", time.Minute)
		s.EndReason = "clear"
		if got := s.Reported(at, aliveProbe); got == StateEnded {
			t.Error("a cleared session reads as ended")
		}
	})
}

func TestReportedStateWhenTheProcessIsGone(t *testing.T) {
	t.Run("killed mid-tool reads interrupted", func(t *testing.T) {
		// The case worth shouting about, and the one the old renderer hid
		// behind "running (stale)".
		s := record(StatusRunning, "Edit", 20*time.Minute)
		if got := s.Reported(at, deadProbe); got != StateInterrupted {
			t.Errorf("Reported = %q, want %q", got, StateInterrupted)
		}
	})

	t.Run("gone while idle reads closed, not interrupted", func(t *testing.T) {
		// A terminal closed between turns lost nothing. Calling that
		// interrupted would cry wolf on the ordinary case and make the real
		// one unfindable.
		for _, c := range []struct {
			name   string
			status Status
			event  string
		}{
			{"between turns", StatusDone, "Stop"},
			{"waiting on a human", StatusWaiting, "idle_prompt"},
		} {
			t.Run(c.name, func(t *testing.T) {
				s := record(c.status, c.event, 20*time.Minute)
				if got := s.Reported(at, deadProbe); got != StateClosed {
					t.Errorf("Reported = %q, want %q", got, StateClosed)
				}
			})
		}
	})

	t.Run("a reused pid is not a live session", func(t *testing.T) {
		// The guard the whole proc package exists for: the pid is held, but
		// by something else. Without the start-time comparison this reports
		// as running forever.
		s := record(StatusRunning, "Edit", 20*time.Minute)
		if got := s.Reported(at, reusedProbe); got != StateInterrupted {
			t.Errorf("Reported = %q, want %q", got, StateInterrupted)
		}
	})

	t.Run("a record older than the boot cannot be running", func(t *testing.T) {
		// Decidable without looking up any process: nothing survives a
		// reboot, whatever the pid says now.
		probe := Probe{
			Boot: func() (time.Time, error) { return at.Add(-time.Hour), nil },
			SameProcess: func(int, time.Time) (bool, error) {
				t.Error("the pid was looked up despite the record predating the boot")
				return true, nil
			},
		}
		s := record(StatusRunning, "Edit", 8*time.Hour)
		if got := s.Reported(at, probe); got != StateInterrupted {
			t.Errorf("Reported = %q, want %q", got, StateInterrupted)
		}
	})
}

func TestReportedStateWhenTheProcessIsAlive(t *testing.T) {
	t.Run("running stays running", func(t *testing.T) {
		s := record(StatusRunning, "Bash", time.Minute)
		if got := s.Reported(at, aliveProbe); got != StateRunning {
			t.Errorf("Reported = %q, want %q", got, StateRunning)
		}
	})

	t.Run("a finished turn in a live process is waiting for you", func(t *testing.T) {
		s := record(StatusDone, "Stop", time.Minute)
		if got := s.Reported(at, aliveProbe); got != StateWaiting {
			t.Errorf("Reported = %q, want %q", got, StateWaiting)
		}
	})

	t.Run("quiet for days is still running if the process is there", func(t *testing.T) {
		// Silence is not death. A tab left open over a long weekend is
		// paused, and the horizon only decides what to do when liveness
		// cannot be established at all.
		s := record(StatusRunning, "Bash", 5*24*time.Hour)
		if got := s.Reported(at, aliveProbe); got != StateRunning {
			t.Errorf("Reported = %q, want %q", got, StateRunning)
		}
	})
}

func TestReportedStateWhenLivenessCannotBeDetermined(t *testing.T) {
	t.Run("recent silence trusts what the record says", func(t *testing.T) {
		s := record(StatusRunning, "Bash", time.Minute)
		if got := s.Reported(at, blindProbe); got != StateRunning {
			t.Errorf("Reported = %q, want %q", got, StateRunning)
		}
	})

	t.Run("past the horizon it says so rather than guessing", func(t *testing.T) {
		s := record(StatusRunning, "Bash", StaleAfter+time.Hour)
		if got := s.Reported(at, blindProbe); got != StateUnknown {
			t.Errorf("Reported = %q, want %q", got, StateUnknown)
		}
	})

	t.Run("a record with no stored start time is not called dead", func(t *testing.T) {
		// Every record written before this field existed. Reporting them as
		// interrupted would invent lost work on upgrade.
		s := record(StatusRunning, "Bash", time.Minute)
		s.PIDStartedAt = time.Time{}
		if got := s.Reported(at, SystemProbe()); got != StateRunning {
			t.Errorf("Reported = %q, want %q for a record predating the guard", got, StateRunning)
		}
	})

	t.Run("an empty probe never claims to know", func(t *testing.T) {
		s := record(StatusRunning, "Edit", StaleAfter+time.Hour)
		if got := s.Reported(at, Probe{}); got != StateUnknown {
			t.Errorf("Reported = %q, want %q", got, StateUnknown)
		}
	})
}

// TestReportedStateIsNeverStored is the shape rule the unit was written
// around: activity is stored, liveness is derived, and derived truth must not
// leak back onto the record.
func TestReportedStateIsNeverStored(t *testing.T) {
	s := record(StatusRunning, "Edit", 20*time.Minute)
	before := *s
	if got := s.Reported(at, deadProbe); got != StateInterrupted {
		t.Fatalf("Reported = %q", got)
	}
	if s.Status != before.Status || s.LastEvent != before.LastEvent {
		t.Errorf("deriving a state mutated the record: %+v -> %+v", before, *s)
	}
}

func TestLostWorkIsIdentifiable(t *testing.T) {
	// What the unit buys: an interrupted session names its project and the
	// action it died on, so the loss is findable without opening a transcript.
	s := record(StatusRunning, "Edit", 20*time.Minute)
	s.ProjectPath = "/Users/me/Projects/api"
	if got := s.Reported(at, deadProbe); got != StateInterrupted {
		t.Fatalf("Reported = %q", got)
	}
	if s.ProjectName() != "api" {
		t.Errorf("ProjectName = %q", s.ProjectName())
	}
	if s.LastEvent != "Edit" {
		t.Errorf("LastEvent = %q, want the action it died on", s.LastEvent)
	}
}
