package usage

import (
	"strings"
	"testing"
)

func agentRow(r *Report, name string) Spend {
	for _, a := range r.Agents {
		if a.Name == name {
			return a
		}
	}
	return Spend{}
}

func workstreamCost(r *Report, name string) float64 {
	for _, w := range r.Workstreams {
		if w.Name == name {
			return w.Cost
		}
	}
	return 0
}

func withSubagents(rec *Record, agents map[string]*File) *Record {
	rec.Subagents = map[string]*Subagent{}
	for key, f := range agents {
		rec.Subagents[key] = &Subagent{AgentType: strings.SplitN(key, "#", 2)[0], File: f}
	}
	return rec
}

func TestTracking(t *testing.T) {
	t.Run("counts each subagent run once under its agent type and model, however many turns it took", func(t *testing.T) {
		rec := withSubagents(sessionRecord("", summarise(t, assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5))),
			map[string]*File{
				"Explore#1": summarise(t,
					assistant(t, "2026-10-15T10:00:01Z", "msg_2", "claude-opus-5-5", 5),
					assistant(t, "2026-10-15T10:00:02Z", "msg_3", "claude-opus-5-5", 5),
					assistant(t, "2026-10-15T10:00:03Z", "msg_4", "claude-opus-5-5", 5)),
				"Explore#2":       summarise(t, assistant(t, "2026-10-15T10:00:04Z", "msg_5", "claude-opus-5-5", 5)),
				"cvo:validator#1": summarise(t, assistant(t, "2026-10-15T10:00:05Z", "msg_6", "claude-sonnet-5", 5)),
			})
		r := Build([]*Record{rec}, nil, now, 30, warsaw)
		explore := agentRow(r, "Explore on opus-5-5")
		if explore.Runs != 2 || explore.Turns != 4 {
			t.Errorf("Explore = %+v, want 2 runs of 4 turns between them", explore)
		}
		if v := agentRow(r, "cvo:validator on sonnet-5"); v.Runs != 1 {
			t.Errorf("cvo:validator = %+v, want 1 run", v)
		}
		if m := agentRow(r, "(main session) on opus-5-5"); m.Runs != 0 {
			t.Errorf("main session = %+v, want no runs: it is not a subagent", m)
		}
	})

	t.Run("a run with no turns in the range is not counted", func(t *testing.T) {
		rec := withSubagents(sessionRecord("", summarise(t, assistant(t, "2026-10-16T09:00:00Z", "msg_1", "claude-opus-5-5", 5))),
			map[string]*File{
				"Explore#old": summarise(t, assistant(t, "2026-10-01T10:00:00Z", "msg_2", "claude-opus-5-5", 5)),
				"Explore#new": summarise(t, assistant(t, "2026-10-16T09:00:01Z", "msg_3", "claude-opus-5-5", 5)),
			})
		r := Build([]*Record{rec}, nil, now, 7, warsaw)
		if got := agentRow(r, "Explore on opus-5-5").Runs; got != 1 {
			t.Errorf("runs = %d, want 1: the run from two weeks ago is outside the last 7 days", got)
		}
	})

	t.Run("a session with a project is reported under that project, and one without it under its folder", func(t *testing.T) {
		root := t.TempDir()
		store := &Store{Dir: t.TempDir(), Loc: warsaw}
		tagged := writeTranscript(t, root, "sess0001", assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5))
		plain := writeTranscript(t, root, "sess0002", assistant(t, "2026-10-15T11:00:00Z", "msg_2", "claude-opus-5-5", 5))
		if _, err := store.Capture(tagged, "eow-issue-7"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Capture(plain, ""); err != nil {
			t.Fatal(err)
		}
		// A later turn of the same session, seen without the variable, keeps it.
		if _, err := store.Capture(tagged, ""); err != nil {
			t.Fatal(err)
		}
		recs, err := store.List()
		if err != nil {
			t.Fatal(err)
		}
		r := Build(recs, nil, now, 30, warsaw)
		folder := recs[0].Repo
		if workstreamCost(r, "eow-issue-7") <= 0 || workstreamCost(r, folder) <= 0 || len(r.Workstreams) != 2 {
			t.Errorf("workstreams = %+v, want eow-issue-7 and %s apart", r.Workstreams, folder)
		}
		for _, s := range r.TopSessions {
			if (s.ID == "sess0001") != (s.Project == "eow-issue-7") {
				t.Errorf("session %s has project %q", s.ID, s.Project)
			}
		}
	})

	t.Run("a fixed window counts only turns on its days, both ends included, and its daily rows run across it", func(t *testing.T) {
		f := summarise(t,
			assistant(t, "2026-10-04T10:00:00Z", "msg_1", "claude-opus-5-5", 5),
			assistant(t, "2026-10-05T10:00:00Z", "msg_2", "claude-opus-5-5", 5),
			assistant(t, "2026-10-07T10:00:00Z", "msg_3", "claude-opus-5-5", 5),
			assistant(t, "2026-10-08T10:00:00Z", "msg_4", "claude-opus-5-5", 5))
		r := BuildWindow([]*Record{sessionRecord("", f)}, nil, now, Window{Since: "2026-10-05", Until: "2026-10-07"}, warsaw)
		if r.Tokens.Turns != 2 {
			t.Errorf("turns = %d, want the 5th and the 7th", r.Tokens.Turns)
		}
		if r.Days != 3 || r.From != "2026-10-05" || r.Until != "2026-10-07" {
			t.Errorf("window = %d days from %q until %q, want 3 days from 2026-10-05 until 2026-10-07", r.Days, r.From, r.Until)
		}
		if len(r.Daily) != 3 || r.Daily[0].Day != "2026-10-05" || r.Daily[2].Day != "2026-10-07" {
			t.Errorf("daily = %+v, want the 5th to the 7th", r.Daily)
		}
	})

	t.Run("an end day alone ends the days window on that day", func(t *testing.T) {
		f := summarise(t,
			assistant(t, "2026-10-06T10:00:00Z", "msg_1", "claude-opus-5-5", 5),
			assistant(t, "2026-10-07T10:00:00Z", "msg_2", "claude-opus-5-5", 5),
			assistant(t, "2026-10-08T10:00:00Z", "msg_3", "claude-opus-5-5", 5),
			assistant(t, "2026-10-09T10:00:00Z", "msg_4", "claude-opus-5-5", 5))
		r := BuildWindow([]*Record{sessionRecord("", f)}, nil, now, Window{Days: 2, Until: "2026-10-08"}, warsaw)
		if r.Tokens.Turns != 2 || r.From != "2026-10-07" || r.Until != "2026-10-08" {
			t.Errorf("turns = %d from %q until %q, want the 7th and the 8th", r.Tokens.Turns, r.From, r.Until)
		}
	})

	t.Run("a since after until, or a date that does not parse, is an error naming the flag", func(t *testing.T) {
		for _, c := range []struct {
			since, until, flag string
		}{
			{"2026-13-01", "", "--since"},
			{"", "yesterday", "--until"},
			{"2026-10-08", "2026-10-07", "--since"},
		} {
			_, err := ParseWindow(30, c.since, c.until)
			if err == nil || !strings.Contains(err.Error(), c.flag) {
				t.Errorf("ParseWindow(%q, %q) = %v, want an error naming %s", c.since, c.until, err, c.flag)
			}
		}
		if w, err := ParseWindow(30, "2026-10-05", "2026-10-05"); err != nil || w.Since != "2026-10-05" {
			t.Errorf("a one-day window = %+v, %v; want it accepted", w, err)
		}
	})
}
