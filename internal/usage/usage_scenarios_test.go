package usage

import "testing"

func pending(t *testing.T, behaviour string) {
	t.Helper()
	t.Fatalf("PENDING: %s", behaviour)
}

func TestSummarise(t *testing.T) {
	t.Run("a turn's usage is counted once, although the transcript repeats it on every content block of that message", func(t *testing.T) {
		pending(t, "a turn's usage is counted once, although the transcript repeats it on every content block of that message")
	})
	t.Run("tokens are attributed to the local day and the model the turn ran on", func(t *testing.T) {
		pending(t, "tokens are attributed to the local day and the model the turn ran on")
	})
	t.Run("a tool result's size and error flag are attributed to the tool that produced it", func(t *testing.T) {
		pending(t, "a tool result's size and error flag are attributed to the tool that produced it")
	})
	t.Run("a skill invoked through the Skill tool and the same skill run as a slash command count as uses of one skill", func(t *testing.T) {
		pending(t, "a skill invoked through the Skill tool and the same skill run as a slash command count as uses of one skill")
	})
}

func TestCapture(t *testing.T) {
	t.Run("a subagent transcript's usage lands in its parent session, under its agent type", func(t *testing.T) {
		pending(t, "a subagent transcript's usage lands in its parent session, under its agent type")
	})
	t.Run("a transcript unchanged since its last summary is not parsed again", func(t *testing.T) {
		pending(t, "a transcript unchanged since its last summary is not parsed again")
	})
	t.Run("backfill summarises a transcript no hook ever saw, and a second run parses nothing", func(t *testing.T) {
		pending(t, "backfill summarises a transcript no hook ever saw, and a second run parses nothing")
	})
}

func TestReport(t *testing.T) {
	t.Run("reports a skill installed in a config root and unused in 90 days, with the listing tokens it costs", func(t *testing.T) {
		pending(t, "reports a skill installed in a config root and unused in 90 days, with the listing tokens it costs")
	})
	t.Run("reports failing hooks with the hook name and command", func(t *testing.T) {
		pending(t, "reports failing hooks with the hook name and command")
	})
	t.Run("the JSON carries every number the terminal report prints", func(t *testing.T) {
		pending(t, "the JSON carries every number the terminal report prints")
	})
	t.Run("an empty usage store says how to fill it, rather than printing a report of zeroes", func(t *testing.T) {
		pending(t, "an empty usage store says how to fill it, rather than printing a report of zeroes")
	})
}
