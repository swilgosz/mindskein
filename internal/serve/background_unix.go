//go:build unix

package serve

import (
	"errors"
	"os/exec"
	"syscall"
)

// detach gives the server a session of its own, which is also its own process
// group: closing the terminal that started it or interrupting that shell's
// foreground job does not reach it.
func detach(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return nil
}

func terminate(pid int) error { return signal(pid, syscall.SIGTERM) }

func kill(pid int) error { return signal(pid, syscall.SIGKILL) }

// signal treats a process that is already gone as delivered: it exited between
// the identity check and the signal, which is what was asked for.
func signal(pid int, sig syscall.Signal) error {
	if err := syscall.Kill(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
