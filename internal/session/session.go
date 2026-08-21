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

	// StatusEnded is the only status reported as a fact rather than inferred.
	// It arrives from the session-end event, which also says why.
	StatusEnded Status = "ended"
)

// AgentClaudeCode is the only agent kind v0.1 records. Other vendors get their
// own value when an adapter exists.
const AgentClaudeCode = "claude-code"

// StaleAfter is how long a session may go without any hook event before its
// status stops being trustworthy — a SIGKILLed terminal never sends a closing
// event, so its file would otherwise sit at "waiting" forever.
//
// Three days, not the eight hours first guessed. Measured across real
// transcripts, 35% of resumptions came more than eight hours after the session
// went quiet and the longest came after 48; a shorter horizon marks ordinary
// overnight work as untrustworthy. This only ever marks a session now — what
// gets hidden is decided by StatusEnded, which is known rather than inferred.
const StaleAfter = 72 * time.Hour

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

	// EndReason is why the session finished: clear, resume, logout,
	// prompt_input_exit or other. Empty until it ends.
	EndReason string `json:"end_reason,omitempty"`
}

// Stale reports whether the session has been silent long enough that its
// status is no longer meaningful. An ended session is never stale: its status
// is a fact and does not decay.
func (s *Session) Stale(now time.Time) bool {
	if s.Status == StatusEnded {
		return false
	}
	return now.Sub(s.LastEventAt) > StaleAfter
}

// Older reports whether the session has been quiet for longer than the
// retention horizon, whatever status it claims — nothing removes a record that
// never reported its ending, so the registry only ever grows without this. A
// zero horizon never hides anything.
func (s *Session) Older(now time.Time, horizon time.Duration) bool {
	return horizon > 0 && now.Sub(s.LastEventAt) > horizon
}

// Ended reports whether the session is over. Unlike staleness this is reported,
// not guessed — though it can be missed entirely if the process dies hard.
func (s *Session) Ended() bool { return s.Status == StatusEnded }

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
