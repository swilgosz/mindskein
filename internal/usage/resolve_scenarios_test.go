package usage

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// hookSession is one session whose hooks failed at the given times, keyed by
// hook name; each failure runs the command "run.sh".
func hookSession(t *testing.T, id, start string, failures map[string][]string) *Record {
	t.Helper()
	lines := []string{assistant(t, start, "msg_"+id, "claude-sonnet-5", 5)}
	for hook, times := range failures {
		for _, ts := range times {
			lines = append(lines, hookError(t, ts, hook, "run.sh"))
		}
	}
	f := summarise(t, lines...)
	f.SessionID = id
	return sessionRecord("", f)
}

func buildResolved(recs []*Record, inv []Inventory, res ...Resolution) *Report {
	return BuildResolved(recs, inv, now, Window{}, warsaw, res)
}

func findingByID(r *Report, id string) (Finding, bool) {
	for _, f := range r.Findings {
		if f.ID == id {
			return f, true
		}
	}
	return Finding{}, false
}

func itemKeys(f Finding) []string {
	var keys []string
	for _, it := range f.Items {
		keys = append(keys, it.Key)
	}
	slices.Sort(keys)
	return keys
}

func hiddenBy(t *testing.T, r *Report, id, key string) int {
	t.Helper()
	for _, e := range r.Resolved {
		if e.ID == id && e.Key == key {
			return e.Hidden
		}
	}
	t.Fatalf("no resolved entry %s:%s in %+v", id, key, r.Resolved)
	return 0
}

const hookA, hookB = "SessionStart:startup :: run.sh", "PostToolUse:Bash :: run.sh"

func TestResolvedFindings(t *testing.T) {
	t.Run("resolve hook-failures hides the finding; a hook failure in a session after that shows it again, counting only the new failures", func(t *testing.T) {
		before := hookSession(t, "sess0001", "2026-10-14T10:00:00Z", map[string][]string{
			"SessionStart:startup": {"2026-10-14T10:00:01Z", "2026-10-14T10:00:02Z", "2026-10-14T10:00:03Z"},
		})
		res := Resolution{ID: FindingHookFailures, ResolvedAt: mustTime(t, "2026-10-14T12:00:00Z")}

		r := buildResolved([]*Record{before}, nil, res)
		if f, ok := findingByID(r, FindingHookFailures); ok {
			t.Errorf("resolved hook failures still reported: %+v", f)
		}
		if n := hiddenBy(t, r, FindingHookFailures, ""); n != 3 {
			t.Errorf("hidden = %d, want the 3 failures before the resolve time", n)
		}

		after := hookSession(t, "sess0002", "2026-10-15T09:00:00Z", map[string][]string{
			"SessionStart:startup": {"2026-10-15T09:00:01Z", "2026-10-15T09:00:02Z"},
		})
		r = buildResolved([]*Record{before, after}, nil, res)
		f, ok := findingByID(r, FindingHookFailures)
		if !ok {
			t.Fatalf("a failure after the resolve time does not bring the finding back: %+v", r.Findings)
		}
		if f.Title != "2 hook failures" {
			t.Errorf("title = %q, want only the 2 new failures counted", f.Title)
		}
		if len(f.Items) != 1 || f.Items[0].Text != "2× "+hookA {
			t.Errorf("items = %+v, want the hook with its 2 new failures", f.Items)
		}
		if len(r.HookErrors) != 1 || r.HookErrors[0].Count != 5 {
			t.Errorf("hook error table = %+v, want every failure in the window still counted", r.HookErrors)
		}

		plain := buildResolved([]*Record{before, after}, nil)
		if f, _ := findingByID(plain, FindingHookFailures); f.Title != "5 hook failures" {
			t.Errorf("without resolutions the title = %q, want all 5", f.Title)
		}
	})

	t.Run("resolve hook-failures:<hook> hides that hook only; the finding shows the other hooks and its title counts only them", func(t *testing.T) {
		var a []string
		for i := range 6 {
			a = append(a, fmt.Sprintf("2026-10-15T10:00:0%dZ", i))
		}
		rec := hookSession(t, "sess0001", "2026-10-15T10:00:00Z", map[string][]string{
			"SessionStart:startup": a,
			"PostToolUse:Bash":     {"2026-10-15T10:01:00Z", "2026-10-15T10:02:00Z"},
		})
		r := buildResolved([]*Record{rec}, nil, Resolution{ID: FindingHookFailures, Key: hookA, ResolvedAt: mustTime(t, "2026-10-15T11:00:00Z")})
		f, ok := findingByID(r, FindingHookFailures)
		if !ok {
			t.Fatalf("resolving one hook hid the whole finding")
		}
		if f.Title != "2 hook failures" {
			t.Errorf("title = %q, want only the other hook's 2 failures", f.Title)
		}
		if keys := itemKeys(f); !slices.Equal(keys, []string{hookB}) {
			t.Errorf("items = %q, want only %q", keys, hookB)
		}
		if len(f.Evidence) != 1 || f.Evidence[0] != "2× "+hookB {
			t.Errorf("evidence = %q, want only the unresolved hook", f.Evidence)
		}
		if n := hiddenBy(t, r, FindingHookFailures, hookA); n != 6 {
			t.Errorf("hidden = %d, want the resolved hook's 6 failures", n)
		}
	})

	t.Run("a hook error in a record without per-hook times counts as happening at the session's end", func(t *testing.T) {
		rec := hookSession(t, "sess0001", "2026-10-15T10:00:00Z", map[string][]string{
			"SessionStart:startup": {"2026-10-15T10:00:01Z"},
		})
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		var old Record
		if err := json.Unmarshal(data, &old); err != nil {
			t.Fatal(err)
		}
		old.File.HookLast = nil
		old.File.End = mustTime(t, "2026-10-15T18:00:00Z")

		between := Resolution{ID: FindingHookFailures, ResolvedAt: mustTime(t, "2026-10-15T12:00:00Z")}
		if f, ok := findingByID(buildResolved([]*Record{&old}, nil, between), FindingHookFailures); !ok || f.Title != "1 hook failures" {
			t.Errorf("resolved before the session's end: finding = %+v, want it shown, dated at the end", f)
		}
		afterEnd := Resolution{ID: FindingHookFailures, ResolvedAt: mustTime(t, "2026-10-15T18:00:00Z")}
		if f, ok := findingByID(buildResolved([]*Record{&old}, nil, afterEnd), FindingHookFailures); ok {
			t.Errorf("resolved at the session's end: finding = %+v, want it hidden", f)
		}
	})

	t.Run("big results and top-tier subagent rows resolved before a newer occurrence stay hidden, and come back counting only the newer ones", func(t *testing.T) {
		session := func(id, day string, results ...int) *Record {
			lines := []string{}
			for i, size := range results {
				ts := fmt.Sprintf("%sT10:0%d:00Z", day, i)
				use := toolUse(fmt.Sprintf("toolu_%s_%d", id, i), "Read", map[string]any{"file_path": "/x"})
				lines = append(lines, assistant(t, ts, fmt.Sprintf("msg_%s_%d", id, i), "claude-sonnet-5", 5, use),
					toolResult(t, ts, fmt.Sprintf("toolu_%s_%d", id, i), strings.Repeat("r", size), false))
			}
			f := summarise(t, lines...)
			f.SessionID = id
			rec := sessionRecord("", f)
			rec.Subagents = map[string]*Subagent{"a.jsonl": {AgentType: "Explore",
				File: summarise(t, assistant(t, day+"T10:30:00Z", "msg_s_"+id, "claude-opus-5-5", 300_000))}}
			return rec
		}
		older := session("sess0001", "2026-10-14", 40_000)
		resolvedAt := mustTime(t, "2026-10-14T12:00:00Z")
		res := []Resolution{
			{ID: FindingBigResults, ResolvedAt: resolvedAt},
			{ID: FindingTopTierSubagents, Key: "Explore on opus-5-5", ResolvedAt: resolvedAt},
		}

		r := buildResolved([]*Record{older}, nil, res...)
		for _, id := range []string{FindingBigResults, FindingTopTierSubagents} {
			if f, ok := findingByID(r, id); ok {
				t.Errorf("%s resolved after its only occurrence still reported: %+v", id, f)
			}
		}
		if hiddenBy(t, r, FindingBigResults, "") != 1 || hiddenBy(t, r, FindingTopTierSubagents, "Explore on opus-5-5") != 1 {
			t.Errorf("resolved = %+v, want one big result and one subagent run hidden", r.Resolved)
		}

		newer := session("sess0002", "2026-10-15", 40_000, 50_000, 20_000)
		r = buildResolved([]*Record{older, newer}, nil, res...)
		big, ok := findingByID(r, FindingBigResults)
		if !ok || big.Title != "2 tool results over 30K characters" {
			t.Errorf("big results = %+v, want the 2 newer results over 30K only", big)
		}
		if len(r.BigResults) != 4 {
			t.Errorf("largest results table = %d rows, want all 4 in the window", len(r.BigResults))
		}
		sub, ok := findingByID(r, FindingTopTierSubagents)
		if !ok {
			t.Fatalf("a subagent run after the resolve time does not bring the finding back: %+v", r.Findings)
		}
		alone, _ := findingByID(Build([]*Record{newer}, nil, now, 0, warsaw), FindingTopTierSubagents)
		together, _ := findingByID(Build([]*Record{older, newer}, nil, now, 0, warsaw), FindingTopTierSubagents)
		if sub.Saving != alone.Saving || sub.Saving == together.Saving {
			t.Errorf("saving = %q, want only the newer run's %q, not both runs' %q", sub.Saving, alone.Saving, together.Saving)
		}
		if !slices.Equal(sub.Evidence, alone.Evidence) {
			t.Errorf("evidence = %q, want the newer run's %q", sub.Evidence, alone.Evidence)
		}
	})

	t.Run("a resolved rate or state finding, or a resolved item of one, is hidden and counted as hidden", func(t *testing.T) {
		root, recs, inv := everyFinding(t)
		plain := Build(recs, inv, now, 0, warsaw)
		r := buildResolved(recs, inv,
			Resolution{ID: FindingContextBloat, ResolvedAt: now},
			Resolution{ID: FindingUnusedAgents, ResolvedAt: now},
			Resolution{ID: FindingUnusedSkills, Key: "unused-skill", ResolvedAt: now},
			Resolution{ID: FindingLongMemoryIndex, Key: filepath.Join(root, "projects", "-p", "memory", "MEMORY.md"), ResolvedAt: now},
		)
		for _, id := range []string{FindingContextBloat, FindingUnusedAgents, FindingLongMemoryIndex} {
			if f, ok := findingByID(r, id); ok {
				t.Errorf("%s still reported once resolved: %+v", id, f)
			}
		}
		skills, _ := findingByID(r, FindingUnusedSkills)
		before, _ := findingByID(plain, FindingUnusedSkills)
		if slices.Contains(itemKeys(skills), "unused-skill") || len(skills.Items) != len(before.Items)-1 {
			t.Errorf("unused skills = %q, want every one but the resolved unused-skill", itemKeys(skills))
		}
		if !strings.HasPrefix(skills.Title, fmt.Sprintf("%d ", len(before.Items)-1)) || skills.Saving == before.Saving {
			t.Errorf("title %q, saving %q; want both recomputed without the resolved skill (was %q, %q)", skills.Title, skills.Saving, before.Title, before.Saving)
		}
		for _, want := range []struct {
			id, key string
			hidden  int
		}{
			{FindingContextBloat, "", 1},
			{FindingUnusedAgents, "", 2},
			{FindingUnusedSkills, "unused-skill", 1},
			{FindingLongMemoryIndex, filepath.Join(root, "projects", "-p", "memory", "MEMORY.md"), 1},
		} {
			if got := hiddenBy(t, r, want.id, want.key); got != want.hidden {
				t.Errorf("%s:%s hides %d, want %d", want.id, want.key, got, want.hidden)
			}
		}
		if r.HiddenCount() != 4 {
			t.Errorf("hidden count = %d, want the 4 resolutions that hide something", r.HiddenCount())
		}
		if again := buildResolved(recs, inv); len(again.Findings) != len(plain.Findings) || len(again.Resolved) != 0 {
			t.Errorf("with nothing resolved the report changed: %d findings, resolved %+v", len(again.Findings), again.Resolved)
		}
	})

	t.Run("the terminal report ends with how many findings or items resolutions hide", func(t *testing.T) {
		_, recs, inv := everyFinding(t)
		r := buildResolved(recs, inv,
			Resolution{ID: FindingContextBloat, ResolvedAt: now},
			Resolution{ID: FindingUnusedMCP, Key: "not-installed", ResolvedAt: now})
		var out strings.Builder
		if err := Render(&out, r, false); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "1 resolved · mindskein resolve --list") {
			t.Errorf("report lacks the footer counting the one resolution that hides something:\n%s", out.String())
		}
		out.Reset()
		if err := Render(&out, Build(recs, inv, now, 0, warsaw), false); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "resolved ·") {
			t.Errorf("report with nothing resolved prints a resolved footer:\n%s", out.String())
		}
	})

	t.Run("an id or item not in the report is an error naming the valid ids; an item of a finding without items too", func(t *testing.T) {
		_, recs, inv := everyFinding(t)
		r := Build(recs, inv, now, 0, warsaw)
		for _, ref := range []string{"hook-failure", "hook-failures:nope", "unused-skills:worktree"} {
			id, key, err := ParseRef(ref)
			if err != nil {
				t.Fatal(err)
			}
			err = Validate(r, id, key)
			if err == nil || !strings.Contains(err.Error(), "hook-failures, ") || !strings.Contains(err.Error(), FindingLongMemoryIndex) {
				t.Errorf("%s: err = %v, want an error listing the valid ids", ref, err)
			}
		}
		if err := Validate(r, FindingBigResults, "Read"); err == nil || !strings.Contains(err.Error(), "as a whole") {
			t.Errorf("big-results:Read: err = %v, want it resolvable only as a whole", err)
		}
		if id, key, _ := ParseRef("hook-failures:" + hookB); id != FindingHookFailures || key != hookB || Validate(r, id, key) == nil {
			t.Errorf("ParseRef split at %q / %q; want the id before the first colon and the hook string after", id, key)
		}
		if _, _, err := ParseRef(":x"); err == nil {
			t.Error("an empty id parses")
		}
		if err := Validate(r, FindingUnusedSkills, "unused-skill"); err != nil {
			t.Errorf("a listed item does not validate: %v", err)
		}
	})
}

func TestResolvedFile(t *testing.T) {
	t.Run("resolving again replaces the entry, unresolving something not resolved names it and writes nothing", func(t *testing.T) {
		s := &Resolved{Path: filepath.Join(t.TempDir(), ResolvedFile)}
		if all, err := s.Load(); err != nil || all != nil {
			t.Fatalf("missing file = %v, %v; want nothing resolved", all, err)
		}
		first := Resolution{ID: FindingHookFailures, Key: hookA, ResolvedAt: now, Note: "removed the plugin"}
		later := first
		later.ResolvedAt, later.Note = now.Add(time.Hour), "again"
		for _, r := range []Resolution{first, {ID: FindingBigResults, ResolvedAt: now}, later} {
			if err := s.Resolve(r); err != nil {
				t.Fatal(err)
			}
		}
		all, err := s.Load()
		if err != nil || len(all) != 2 || !all[1].ResolvedAt.Equal(later.ResolvedAt) || all[1].Note != "again" {
			t.Fatalf("entries = %+v, %v; want the hook entry replaced by the later one", all, err)
		}
		if _, err := s.Unresolve(FindingCacheWrites, ""); err == nil || !strings.Contains(err.Error(), "cache-writes is not resolved") {
			t.Errorf("unresolve of nothing = %v, want an error naming it", err)
		}
		gone, err := s.Unresolve(FindingHookFailures, hookA)
		if err != nil || gone.Note != "again" {
			t.Errorf("unresolve = %+v, %v", gone, err)
		}
		if all, _ := s.Load(); len(all) != 1 || all[0].ID != FindingBigResults {
			t.Errorf("after unresolve = %+v, want only big-results", all)
		}
	})
}
