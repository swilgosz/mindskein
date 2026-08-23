//go:build !darwin && !linux

package proc

import "time"

// Everywhere else, liveness is not decidable. Callers report "unknown" rather
// than guessing, which is the whole reason that state exists.
func bootTime() (time.Time, error) { return time.Time{}, ErrUnsupported }

func startTime(int) (time.Time, error) { return time.Time{}, ErrUnsupported }
