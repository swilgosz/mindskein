// Package proc answers two questions about a process id: when it started, and
// when the machine booted.
//
// Both exist to tell a pid apart from the process that once held it. A pid is
// not an identity — the kernel reuses it, and a record naming a dead session's
// pid will eventually name something unrelated. Comparing start times is what
// makes "is this still the same process" answerable instead of assumed.
package proc

import (
	"errors"
	"time"
)

// ErrUnsupported is returned where a start time cannot be read at all. A
// caller must treat this as "cannot tell", never as "not running": reporting a
// live session as dead is the worse mistake of the two.
var ErrUnsupported = errors.New("process start time is not available on this platform")

// ErrNotRunning means no process holds that pid.
var ErrNotRunning = errors.New("no such process")

// StartTime is when the process holding pid started.
func StartTime(pid int) (time.Time, error) { return startTime(pid) }

// BootTime is when the machine last booted.
//
// It is the cheap half of the check: a record whose last event predates the
// boot cannot belong to a running process, whatever its pid says now, and that
// is decidable without looking up any process at all.
func BootTime() (time.Time, error) { return bootTime() }

// SameProcess reports whether pid is still held by the process that started at
// started. A zero started time means the record predates this check and the
// answer is not knowable.
func SameProcess(pid int, started time.Time) (bool, error) {
	if pid <= 0 || started.IsZero() {
		return false, ErrUnsupported
	}
	actual, err := StartTime(pid)
	if err != nil {
		return false, err
	}
	// Whole seconds. The stored value is marshalled through JSON and the
	// kernel reports microseconds; comparing exactly would make every record
	// look like a different process.
	return actual.Unix() == started.Unix(), nil
}
