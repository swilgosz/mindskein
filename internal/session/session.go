// Package session models a Claude Code session and its lifecycle status.
package session

import (
	"path/filepath"
	"strings"
	"time"
)

// Status is the coarse lifecycle state of a session.
//
// A caveat worth knowing before reading these: the Claude Code Stop hook fires
// on every turn completion, not at session end. StatusDone therefore means
// "not mid-turn", not "terminated". A session that finishes a turn and sits at
// the prompt lands on StatusWaiting, because the idle_prompt notification
// arrives just after Stop.
type Status string

const (
	StatusRunning Status = "running"
	StatusWaiting Status = "waiting"
	StatusDone    Status = "done"
)

// AgentClaudeCode is the only agent kind v0.1 records. Other vendors get their
// own value when an adapter exists.
const AgentClaudeCode = "claude-code"

// StaleAfter is how long a session may go without any hook event before its
// status stops being trustworthy. A terminal killed mid-session never sends a
// closing hook, so its file would otherwise sit at "waiting" forever.
const StaleAfter = 8 * time.Hour

// Session is the on-disk record: one JSON file per session id.
type Session struct {
	ID          string    `json:"id"`
	Agent       string    `json:"agent"`
	ProjectPath string    `json:"project_path"`
	PID         int       `json:"pid"`
	Status      Status    `json:"status"`
	StartedAt   time.Time `json:"started_at"`
	LastEventAt time.Time `json:"last_event_at"`
	LastEvent   string    `json:"last_event"`
}

// Stale reports whether the session has been silent long enough that its
// status is no longer meaningful.
func (s *Session) Stale(now time.Time) bool {
	return now.Sub(s.LastEventAt) > StaleAfter
}

// ProjectName is the last element of ProjectPath — what the brief shows
// instead of an absolute path.
func (s *Session) ProjectName() string {
	trimmed := strings.TrimRight(s.ProjectPath, string(filepath.Separator))
	if trimmed == "" {
		return "(unknown)"
	}
	return filepath.Base(trimmed)
}

// ShortID is the display form of a session id: enough to tell two concurrent
// sessions apart without wrapping the terminal.
func (s *Session) ShortID() string {
	if len(s.ID) <= 8 {
		return s.ID
	}
	return s.ID[:8]
}
