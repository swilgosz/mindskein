// Package handoff writes and reads the per-project HANDOFF.md that answers
// "where did we leave off" without opening the transcript.
//
// v0.1 is the raw version only — timestamp, duration, status at end, last tool,
// last user message. Decision extraction is v0.2.
//
// Implemented by U2 (handoff writer): the Stop hook reads transcript_path and
// writes HANDOFF.md into the project dir.
package handoff
