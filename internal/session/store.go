package session

// Store reads and writes ~/.mindskein/sessions/{session_id}.json with atomic
// writes, so a half-written file is never observable by a concurrent brief.
//
// Implemented by U1 (hook capture + session registry).
