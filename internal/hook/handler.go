// Package hook parses the Claude Code hook payload arriving on stdin and
// delegates the state change to the session store.
//
// Events handled: pre-tool-use (running), notification (waiting, on
// idle_prompt / permission_prompt), stop (done).
//
// Implemented by U1 (hook capture + session registry).
package hook
