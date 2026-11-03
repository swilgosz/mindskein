package reviews

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// Explain turns a failed fetch into one line saying what went wrong and how
// to fix it.
//
// gh reports most failures only as text, so they are told apart by what it
// prints: the hint it gives when no account is signed in, GitHub's own
// "Bad credentials" and rate limit messages, and Go's network errors.
func Explain(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, exec.ErrNotFound) {
		return "gh is not installed — install it from https://cli.github.com, then run `gh auth login`"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "GitHub did not answer in time — check the network"
	}
	msg := strings.ToLower(err.Error())
	has := func(parts ...string) bool {
		for _, p := range parts {
			if strings.Contains(msg, p) {
				return true
			}
		}
		return false
	}
	switch {
	case has("gh auth login", "not logged in", "bad credentials", "http 401"):
		return "not signed in to GitHub — run `gh auth login`"
	case has("rate limit"):
		return "GitHub rate limit reached — wait for it to reset, then try again"
	case has("error connecting", "dial tcp", "no such host", "connection refused",
		"connection reset", "network is unreachable", "i/o timeout", "tls handshake timeout"):
		return "cannot reach GitHub — check the network"
	}
	line, _, _ := strings.Cut(strings.TrimSpace(err.Error()), "\n")
	return "gh failed: " + line
}
