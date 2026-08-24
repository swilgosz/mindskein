package proc

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

// These run against the live kernel on purpose. A fake would assert that the
// parsing matches the fake, which is the one thing that cannot be wrong here;
// what can be wrong is the struct offset, the field index and the units.

func TestStartTimeOfThisProcess(t *testing.T) {
	got, err := StartTime(os.Getpid())
	if errors.Is(err, ErrUnsupported) {
		t.Skip("no process start time on this platform")
	}
	if err != nil {
		t.Fatalf("StartTime: %v", err)
	}
	if time.Since(got) > time.Hour {
		t.Errorf("start = %s, but this test process was launched moments ago; units or offset are wrong", got)
	}
	if got.After(time.Now().Add(time.Minute)) {
		t.Errorf("start = %s is in the future", got)
	}
}

func TestStartTimeOfADeadPid(t *testing.T) {
	// A process that has exited and been reaped. Its pid is the clearest case
	// of a record naming something that is no longer there.
	cmd := exec.Command("sleep", "0")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("waiting: %v", err)
	}
	_, err := StartTime(pid)
	if errors.Is(err, ErrUnsupported) {
		t.Skip("no process start time on this platform")
	}
	if !errors.Is(err, ErrNotRunning) {
		t.Errorf("StartTime(dead pid) error = %v, want ErrNotRunning", err)
	}
}

func TestStartTimeOfALiveChild(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	got, err := StartTime(cmd.Process.Pid)
	if errors.Is(err, ErrUnsupported) {
		t.Skip("no process start time on this platform")
	}
	if err != nil {
		t.Fatalf("StartTime: %v", err)
	}
	if d := time.Since(got); d > time.Minute || d < -time.Minute {
		t.Errorf("a process started moments ago reports %s (%v ago)", got, d)
	}
}

func TestBootTimeIsInThePast(t *testing.T) {
	got, err := BootTime()
	if errors.Is(err, ErrUnsupported) {
		t.Skip("no boot time on this platform")
	}
	if err != nil {
		t.Fatalf("BootTime: %v", err)
	}
	if got.After(time.Now()) {
		t.Errorf("boot = %s is in the future", got)
	}
	if time.Since(got) > 365*24*time.Hour {
		t.Errorf("boot = %s is over a year ago; units are probably wrong", got)
	}
	// Every process started at or after the boot. This catches a unit error
	// that a "looks recent" assertion would not.
	self, err := StartTime(os.Getpid())
	if err == nil && self.Before(got.Add(-time.Minute)) {
		t.Errorf("this process (%s) predates the boot (%s)", self, got)
	}
}

func TestSameProcess(t *testing.T) {
	t.Run("true for a pid still held by the same process", func(t *testing.T) {
		started, err := StartTime(os.Getpid())
		if errors.Is(err, ErrUnsupported) {
			t.Skip("unsupported platform")
		}
		if err != nil {
			t.Fatalf("StartTime: %v", err)
		}
		same, err := SameProcess(os.Getpid(), started)
		if err != nil {
			t.Fatalf("SameProcess: %v", err)
		}
		if !same {
			t.Error("the running process is not recognised as itself")
		}
	})

	t.Run("false when the pid was reused", func(t *testing.T) {
		// The failure this whole package exists to prevent: a stored pid that
		// now belongs to something else, reported as a live session.
		started, err := StartTime(os.Getpid())
		if errors.Is(err, ErrUnsupported) {
			t.Skip("unsupported platform")
		}
		if err != nil {
			t.Fatalf("StartTime: %v", err)
		}
		same, err := SameProcess(os.Getpid(), started.Add(-2*time.Hour))
		if err != nil {
			t.Fatalf("SameProcess: %v", err)
		}
		if same {
			t.Error("a pid held by a process that started at a different time was called the same process")
		}
	})

	t.Run("cannot tell without a stored start time", func(t *testing.T) {
		// Records written before this field existed. Reporting them as dead
		// would be worse than admitting we do not know.
		_, err := SameProcess(os.Getpid(), time.Time{})
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("error = %v, want ErrUnsupported for a zero start time", err)
		}
	})

	t.Run("cannot tell for a pid that is not a pid", func(t *testing.T) {
		if _, err := SameProcess(0, time.Now()); !errors.Is(err, ErrUnsupported) {
			t.Errorf("error = %v, want ErrUnsupported for pid 0", err)
		}
	})
}

// TestStartTimeCostFitsTheHookBudget holds the measurement the unit was
// approved against. PreToolUse runs on every tool call at ~8.7 ms, and a guard
// that quietly grew to milliseconds would be paid on all of them.
func TestStartTimeCostFitsTheHookBudget(t *testing.T) {
	if _, err := StartTime(os.Getpid()); errors.Is(err, ErrUnsupported) {
		t.Skip("unsupported platform")
	}
	const runs = 500
	start := time.Now()
	for i := 0; i < runs; i++ {
		_, _ = StartTime(os.Getpid())
	}
	per := time.Since(start) / runs
	if per > time.Millisecond {
		t.Errorf("StartTime costs %v per call, which is no longer negligible against an 8.7ms hook", per)
	}
	t.Logf("StartTime: %v per call", per)
}
