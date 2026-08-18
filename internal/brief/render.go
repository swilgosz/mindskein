// Package brief composes the three sections of the morning brief: priorities
// from plan.md, live sessions from the registry, and the last handoff per
// project.
//
// Missing config, no sessions and no handoffs each degrade to a one-line hint,
// never a stack trace.
package brief
