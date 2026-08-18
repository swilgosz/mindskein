// Package hook parses the Claude Code hook payload arriving on stdin and
// delegates the state change to the session store.
//
// Events handled: pre-tool-use (running), notification (waiting, on
// idle_prompt / permission_prompt), stop (done).
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/swilgosz/mindskein/internal/session"
)

// Event is a mindskein hook subcommand — the `pre-tool-use` in
// `mindskein hook pre-tool-use`. It maps onto a Claude Code hook event.
type Event string

const (
	EventPreToolUse   Event = "pre-tool-use"
	EventNotification Event = "notification"
	EventStop         Event = "stop"
	EventSessionEnd   Event = "session-end"
)

// Events is every event the CLI accepts, in registration order.
var Events = []Event{EventPreToolUse, EventNotification, EventStop, EventSessionEnd}

// maxPayload caps how much stdin we will read. Payloads carry tool_input,
// which for a Write can be a whole file, and we only need a handful of scalar
// fields off the top.
const maxPayload = 4 << 20 // 4 MiB

// Payload is the subset of the Claude Code hook stdin JSON that mindskein
// reads. Every hook event sends the common fields; the rest are event
// specific and empty when absent.
//
// Verified against https://code.claude.com/docs/en/hooks.md.
type Payload struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	TranscriptPath string `json:"transcript_path"`
	PermissionMode string `json:"permission_mode"`
	HookEventName  string `json:"hook_event_name"`

	// PreToolUse
	ToolName string `json:"tool_name"`

	// Notification
	NotificationType string `json:"notification_type"`

	// SessionEnd: clear, resume, logout, prompt_input_exit or other.
	SessionEndReason string `json:"session_end_reason"`

	// Present only when the event came from a subagent. The session id is
	// still the parent's, so subagent activity correctly keeps the session
	// marked running.
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
}

// ParseEvent turns a CLI argument into an Event.
func ParseEvent(s string) (Event, error) {
	for _, e := range Events {
		if string(e) == s {
			return e, nil
		}
	}
	return "", fmt.Errorf("unknown hook event %q, expected one of %v", s, Events)
}

// Parse decodes a hook payload from r.
func Parse(r io.Reader) (*Payload, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxPayload))
	if err != nil {
		return nil, fmt.Errorf("reading hook payload: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty hook payload on stdin")
	}
	var p Payload
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parsing hook payload: %w", err)
	}
	if p.SessionID == "" {
		return nil, fmt.Errorf("hook payload has no session_id")
	}
	return &p, nil
}

// waitingNotifications are the notification_type values that mean the session
// has stopped and is blocked on a human. Claude emits these explicitly, so
// there is nothing to infer. Every other notification type (auth_success, the
// elicitation_* family, agent_completed) says nothing about whether the
// session is waiting, and is ignored.
var waitingNotifications = map[string]bool{
	"idle_prompt":       true,
	"permission_prompt": true,
	"agent_needs_input": true,
}

// Handle applies one hook event to the store and returns the session it wrote.
// It returns (nil, nil) for events that carry no status meaning, which is the
// normal outcome for most notification types.
func Handle(store *session.Store, ev Event, p *Payload, now time.Time, pid int) (*session.Session, error) {
	var status session.Status
	var lastEvent string

	switch ev {
	case EventPreToolUse:
		status = session.StatusRunning
		lastEvent = p.ToolName
		if lastEvent == "" {
			lastEvent = "tool"
		}
	case EventNotification:
		if !waitingNotifications[p.NotificationType] {
			return nil, nil
		}
		status = session.StatusWaiting
		lastEvent = p.NotificationType
	case EventSessionEnd:
		// The one ending that is reported rather than inferred. It can still be
		// missed — a SIGKILLed process never gets to run a hook — which is why
		// silence is still tracked separately.
		status = session.StatusEnded
		lastEvent = p.SessionEndReason
		if lastEvent == "" {
			lastEvent = "other"
		}
	case EventStop:
		// Stop fires on every turn completion, not at session end — so this
		// means "idle between turns". The idle_prompt notification that
		// follows usually moves it straight on to waiting.
		status = session.StatusDone
		lastEvent = "Stop"
	default:
		return nil, fmt.Errorf("unhandled hook event %q", ev)
	}

	return store.Upsert(p.SessionID, now, func(s *session.Session) {
		// A session that has ended stays ended. Hooks run in parallel, so a
		// slower Stop can land after the end event and would otherwise
		// resurrect a finished session.
		if s.Status == session.StatusEnded && ev != EventSessionEnd {
			return
		}
		if ev == EventSessionEnd {
			s.EndReason = p.SessionEndReason
		}
		s.Status = status
		s.LastEvent = lastEvent
		s.PID = pid
		if p.CWD != "" {
			s.ProjectPath = p.CWD
		}
	})
}
