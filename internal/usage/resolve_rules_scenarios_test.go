package usage

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// turn is one assistant turn of a rate session: the context it read from the
// cache and what it wrote to the cache, in tokens.
type turn struct{ read, write int }

// rateSession is one session started at start, loading a skill listing of
// listing characters (none when 0), then running turns a second apart.
func rateSession(t *testing.T, id, start string, listing int, turns ...turn) *Record {
	t.Helper()
	at := mustTime(t, start)
	var lines []string
	if listing > 0 {
		lines = append(lines, line(t, map[string]any{"type": "attachment", "timestamp": start,
			"attachment": map[string]any{"type": "skill_listing", "content": strings.Repeat("s", listing)}}))
	}
	for i, tu := range turns {
		lines = append(lines, line(t, map[string]any{
			"type": "assistant", "timestamp": at.Add(time.Duration(i) * time.Second).Format(time.RFC3339), "sessionId": id,
			"message": map[string]any{"id": fmt.Sprintf("msg_%s_%d", id, i), "model": "claude-sonnet-5",
				"content": []map[string]any{{"type": "text", "text": "ok"}},
				"usage": map[string]any{"input_tokens": 10, "output_tokens": 10,
					"cache_read_input_tokens": tu.read, "cache_creation_input_tokens": tu.write}},
		}))
	}
	f := summarise(t, lines...)
	f.SessionID = id
	return sessionRecord("", f)
}

// turns repeats tu n times.
func turns(n int, tu turn) []turn {
	out := make([]turn, n)
	for i := range out {
		out[i] = tu
	}
	return out
}

func joined(parts ...[]turn) []turn { return slices.Concat(parts...) }

var (
	small = turn{read: 1000}
	huge  = turn{read: 200_000}
)

// usedSession runs the given slash commands at at, in a session of root.
func usedSession(t *testing.T, root, id, at string, commands ...string) *Record {
	t.Helper()
	lines := []string{assistant(t, at, "msg_"+id, "claude-sonnet-5", 5)}
	for _, c := range commands {
		lines = append(lines, prompt(t, at, "<command-name>/"+c+"</command-name>"))
	}
	f := summarise(t, lines...)
	f.SessionID = id
	return sessionRecord(root, f)
}

func entryOf(t *testing.T, r *Report, id, key string) ResolvedEntry {
	t.Helper()
	for _, e := range r.Resolved {
		if e.ID == id && e.Key == key {
			return e
		}
	}
	t.Fatalf("no resolved entry %s:%s in %+v", id, key, r.Resolved)
	return ResolvedEntry{}
}

func TestResolvedRatesAndStates(t *testing.T) {
	t.Run("resolve context-bloat hides it until sessions started after the resolve time run above the threshold share again", func(t *testing.T) {
		before := rateSession(t, "sess0001", "2026-10-14T10:00:00Z", 0, joined(turns(5, huge), turns(5, small))...)
		res := Resolution{ID: FindingContextBloat, ResolvedAt: mustTime(t, "2026-10-14T12:00:00Z")}
		if f, ok := findingByID(Build([]*Record{before}, nil, now, 0, warsaw), FindingContextBloat); !ok || f.Title != "50% of turns run above 150K context" {
			t.Fatalf("unresolved finding = %+v, want half the turns above 150K", f)
		}

		calm := rateSession(t, "sess0002", "2026-10-15T09:00:00Z", 0, joined(turns(1, huge), turns(9, small))...)
		r := buildResolved([]*Record{before, calm}, nil, res)
		if f, ok := findingByID(r, FindingContextBloat); ok {
			t.Errorf("a later session at the threshold share brought it back: %+v", f)
		}
		if n := hiddenBy(t, r, FindingContextBloat, ""); n != 1 {
			t.Errorf("hidden = %d, want the finding the whole window still shows", n)
		}

		bloated := rateSession(t, "sess0003", "2026-10-15T15:00:00Z", 0, joined(turns(4, huge), turns(6, small))...)
		r = buildResolved([]*Record{before, calm, bloated}, nil, res)
		f, ok := findingByID(r, FindingContextBloat)
		if !ok {
			t.Fatalf("later sessions above the threshold share do not bring it back: %+v", r.Findings)
		}
		if f.Title != "25% of turns run above 150K context" || f.Severity != Warning {
			t.Errorf("finding = %q (%s), want the rate over the 2 later sessions only, 5 of 20 turns", f.Title, f.Severity)
		}
		if n := hiddenBy(t, r, FindingContextBloat, ""); n != 0 {
			t.Errorf("hidden = %d, want nothing hidden once it shows", n)
		}

		straddling := rateSession(t, "sess0004", "2026-10-14T11:30:00Z", 0, turns(10, huge)...)
		if f, ok := findingByID(buildResolved([]*Record{before, straddling, calm}, nil, res), FindingContextBloat); ok {
			t.Errorf("a session started before the resolve time counts: %+v", f)
		}

		one := rateSession(t, "sess0005", "2026-10-15T16:00:00Z", 0, huge)
		if f, ok := findingByID(buildResolved([]*Record{before, one}, nil, res), FindingContextBloat); !ok || f.Title != "100% of turns run above 150K context" || f.Severity != Serious {
			t.Errorf("one later session = %+v, want it judged alone, with no minimum number of sessions", f)
		}

		windowed := BuildResolved([]*Record{before, calm, bloated}, nil, now, Window{Since: "2026-10-15", Until: "2026-10-15"}, warsaw, []Resolution{res})
		if f, _ := findingByID(windowed, FindingContextBloat); f.Title != "25% of turns run above 150K context" {
			t.Errorf("within a window = %q, want the same rate over the later sessions in it", f.Title)
		}
		early := BuildResolved([]*Record{before, calm, bloated}, nil, now, Window{Since: "2026-10-15", Until: "2026-10-15"}, warsaw,
			[]Resolution{{ID: FindingContextBloat, ResolvedAt: mustTime(t, "2026-10-01T00:00:00Z")}})
		if f, _ := findingByID(early, FindingContextBloat); f.Title != "25% of turns run above 150K context" {
			t.Errorf("resolved before the window = %q, want only the sessions in the window counted", f.Title)
		}
	})

	t.Run("cache-writes and skill-listing follow the same rate rule", func(t *testing.T) {
		writes := turn{read: 1000, write: 200_000}
		before := rateSession(t, "sess0001", "2026-10-14T10:00:00Z", 24_000, turns(5, writes)...)
		resolvedAt := mustTime(t, "2026-10-14T12:00:00Z")
		res := []Resolution{{ID: FindingCacheWrites, ResolvedAt: resolvedAt}, {ID: FindingSkillListing, ResolvedAt: resolvedAt}}
		plain := Build([]*Record{before}, nil, now, 0, warsaw)
		for _, id := range []string{FindingCacheWrites, FindingSkillListing} {
			if _, ok := findingByID(plain, id); !ok {
				t.Fatalf("%s not reported before it is resolved: %+v", id, plain.Findings)
			}
		}

		calm := rateSession(t, "sess0002", "2026-10-15T09:00:00Z", 20_000, turns(5, small)...)
		r := buildResolved([]*Record{before, calm}, nil, res...)
		for _, id := range []string{FindingCacheWrites, FindingSkillListing} {
			if f, ok := findingByID(r, id); ok {
				t.Errorf("%s back after a later session below its threshold: %+v", id, f)
			}
			if n := hiddenBy(t, r, id, ""); n != 1 {
				t.Errorf("%s hides %d, want 1", id, n)
			}
		}

		heavy := rateSession(t, "sess0003", "2026-10-15T15:00:00Z", 40_000, turns(5, turn{read: 1_000_000, write: 200_000})...)
		r = buildResolved([]*Record{before, calm, heavy}, nil, res...)
		later := Build([]*Record{calm, heavy}, nil, now, 0, warsaw)
		whole := Build([]*Record{before, calm, heavy}, nil, now, 0, warsaw)
		for _, id := range []string{FindingCacheWrites, FindingSkillListing} {
			f, ok := findingByID(r, id)
			if !ok {
				t.Errorf("%s not back after a later session above its threshold: %+v", id, r.Findings)
				continue
			}
			want, _ := findingByID(later, id)
			all, _ := findingByID(whole, id)
			if f.Title != want.Title || f.Title == all.Title {
				t.Errorf("%s title = %q, want the rate over the later sessions %q, not the window's %q", id, f.Title, want.Title, all.Title)
			}
		}
	})

	t.Run("a resolved unused skill stays hidden while unused; using it clears the entry, and a later unused stretch is reported as new", func(t *testing.T) {
		root := skillRoot(t)
		inv := []Inventory{Scan(root, nil)}
		quiet := usedSession(t, root, "sess0001", "2026-10-15T10:00:00Z", "worktree")
		res := Resolution{ID: FindingUnusedSkills, Key: "unused-skill", ResolvedAt: mustTime(t, "2026-10-15T12:00:00Z")}
		months := now.AddDate(0, 4, 0)

		for _, at := range []time.Time{now, months} {
			r := BuildResolved([]*Record{quiet}, inv, at, Window{}, warsaw, []Resolution{res})
			if f, _ := findingByID(r, FindingUnusedSkills); slices.Contains(itemKeys(f), "unused-skill") {
				t.Errorf("at %s the resolved unused skill is reported: %+v", at.Format(time.DateOnly), f)
			}
			if e := entryOf(t, r, FindingUnusedSkills, "unused-skill"); e.Cleared || e.Hidden != 1 {
				t.Errorf("at %s entry = %+v, want it hiding the skill, not cleared", at.Format(time.DateOnly), e)
			}
		}

		used := usedSession(t, root, "sess0002", "2026-10-16T09:00:00Z", "unused-skill")
		r := buildResolved([]*Record{quiet, used}, inv, res)
		if e := entryOf(t, r, FindingUnusedSkills, "unused-skill"); !e.Cleared || e.Hidden != 0 {
			t.Errorf("after a use the entry = %+v, want it cleared and hiding nothing", e)
		}
		r = BuildResolved([]*Record{quiet, used}, inv, months, Window{}, warsaw, []Resolution{res})
		if f, _ := findingByID(r, FindingUnusedSkills); !slices.Contains(itemKeys(f), "unused-skill") {
			t.Errorf("a new unused stretch after the use is hidden: %+v", f)
		}

		lateNight := res
		lateNight.ResolvedAt = mustTime(t, "2026-10-15T23:30:00Z")
		for _, c := range []struct {
			at      string
			cleared bool
		}{{"2026-10-15T22:30:00Z", true}, {"2026-10-15T21:30:00Z", false}} {
			r := buildResolved([]*Record{usedSession(t, root, "sess0003", c.at, "unused-skill")}, inv, lateNight)
			if e := entryOf(t, r, FindingUnusedSkills, "unused-skill"); e.Cleared != c.cleared {
				t.Errorf("a use at %s against a resolve on the next local day: cleared = %v, want %v", c.at, e.Cleared, c.cleared)
			}
		}

		file := &Resolved{Path: filepath.Join(t.TempDir(), ResolvedFile)}
		other := Resolution{ID: FindingHookFailures, ResolvedAt: now}
		for _, x := range []Resolution{res, other} {
			if err := file.Resolve(x); err != nil {
				t.Fatal(err)
			}
		}
		if err := file.Settle(buildResolved([]*Record{quiet}, inv, res, other).Resolved); err != nil {
			t.Fatal(err)
		}
		if all, _ := file.Load(); len(all) != 2 {
			t.Errorf("nothing cleared, yet the file now holds %+v", all)
		}
		if err := file.Settle(buildResolved([]*Record{quiet, used}, inv, res, other).Resolved); err != nil {
			t.Fatal(err)
		}
		if all, _ := file.Load(); len(all) != 1 || all[0].ID != FindingHookFailures {
			t.Errorf("after settling the file holds %+v, want the cleared entry dropped", all)
		}
	})

	t.Run("resolving unused-skills as a whole hides the skills unused now; a skill that becomes unused later is reported", func(t *testing.T) {
		root := skillRoot(t)
		inv := []Inventory{Scan(root, nil)}
		recs := []*Record{usedSession(t, root, "sess0001", "2026-10-15T10:00:00Z", "worktree")}
		at := mustTime(t, "2026-10-15T12:00:00Z")
		res := NewResolution(Build(recs, inv, now, 0, warsaw), FindingUnusedSkills, "", at, "archived")
		if len(res.Items) != 1 || res.Items[0].Key != "unused-skill" || res.Items[0].Size != 0 {
			t.Fatalf("whole resolution items = %+v, want the one skill unused now", res.Items)
		}

		r := buildResolved(recs, inv, res)
		if f, ok := findingByID(r, FindingUnusedSkills); ok {
			t.Errorf("finding resolved as a whole still reported: %+v", f)
		}
		if n := hiddenBy(t, r, FindingUnusedSkills, ""); n != 1 {
			t.Errorf("hidden = %d, want the 1 skill", n)
		}

		months := now.AddDate(0, 4, 0)
		r = BuildResolved(recs, inv, months, Window{}, warsaw, []Resolution{res})
		f, ok := findingByID(r, FindingUnusedSkills)
		if !ok || !slices.Equal(itemKeys(f), []string{"worktree"}) || !strings.HasPrefix(f.Title, "1 ") {
			t.Errorf("months later = %+v, want worktree, unused since the resolve, reported alone", f)
		}

		dir := filepath.Join(root, "skills", "fresh")
		os.MkdirAll(dir, 0o700)
		os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: fresh\ndescription: New\n---\n"), 0o600)
		r = buildResolved(recs, []Inventory{Scan(root, nil)}, res)
		if f, _ := findingByID(r, FindingUnusedSkills); !slices.Equal(itemKeys(f), []string{"fresh"}) {
			t.Errorf("after installing a skill = %q, want only the new one", itemKeys(f))
		}

		used := usedSession(t, root, "sess0002", "2026-10-16T09:00:00Z", "unused-skill")
		r = buildResolved(append(recs, used), inv, res)
		if e := entryOf(t, r, FindingUnusedSkills, ""); !e.Cleared || len(e.Items) != 0 {
			t.Errorf("after its only item ran = %+v, want the whole entry cleared", e)
		}
	})

	t.Run("a resolved instruction file is reported again once it grows more than 10% past its size at resolve time; a memory index likewise by lines", func(t *testing.T) {
		root := fullRoot(t)
		claude := filepath.Join(root, "CLAUDE.md")
		memory := filepath.Join(root, "projects", "-p", "memory", "MEMORY.md")
		scan := func() []Inventory { return []Inventory{Scan(root, []string{root})} }
		at := mustTime(t, "2026-10-15T12:00:00Z")
		plain := Build(nil, scan(), now, 0, warsaw)
		res := []Resolution{
			NewResolution(plain, FindingHeavyInstructions, claude, at, ""),
			NewResolution(plain, FindingLongMemoryIndex, "", at, ""),
		}
		if res[0].Size != 19_500/4 || len(res[1].Items) != 1 || res[1].Items[0] != (ResolvedItem{Key: memory, Size: 130}) {
			t.Fatalf("resolutions = %+v, want CLAUDE.md's tokens and the index's lines recorded", res)
		}

		grow := func(tokens, lines int) *Report {
			os.WriteFile(claude, []byte(strings.Repeat("x", tokens*4)), 0o600)
			os.WriteFile(memory, []byte(strings.Repeat("- [a](a.md) — b\n", lines)), 0o600)
			return buildResolved(nil, scan(), res...)
		}
		r := grow(19_500/4*11/10, 143)
		for _, id := range []string{FindingHeavyInstructions, FindingLongMemoryIndex} {
			if f, ok := findingByID(r, id); ok {
				t.Errorf("%s back at 10%% growth: %+v", id, f)
			}
		}
		r = grow(19_500/4*11/10+1, 144)
		for _, id := range []string{FindingHeavyInstructions, FindingLongMemoryIndex} {
			if _, ok := findingByID(r, id); !ok {
				t.Errorf("%s not back past 10%% growth: %+v", id, r.Findings)
			}
		}
		if hiddenBy(t, r, FindingHeavyInstructions, claude) != 0 || hiddenBy(t, r, FindingLongMemoryIndex, "") != 0 {
			t.Errorf("resolved = %+v, want entries grown past to hide nothing", r.Resolved)
		}

		legacy := buildResolved(nil, scan(), Resolution{ID: FindingHeavyInstructions, Key: claude, ResolvedAt: at})
		if f, ok := findingByID(legacy, FindingHeavyInstructions); ok {
			t.Errorf("an entry with no recorded size shows the grown file: %+v", f)
		}
	})
}
