//go:build !unix

package serve

import (
	"errors"
	"os/exec"
)

var errNoBackground = errors.New("running the server in the background is not supported on this platform")

func detach(*exec.Cmd) error { return errNoBackground }

func terminate(int) error { return errNoBackground }

func kill(int) error { return errNoBackground }
