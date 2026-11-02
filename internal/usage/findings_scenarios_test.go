package usage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestFindingIdentity(t *testing.T) {
	t.Run("every finding in usage --json has an id from the list, and the same id on the next run", func(t *testing.T) {
		root, recs, inv := everyFinding(t)
		first := findingsJSON(t, Build(recs, inv, now, 0, warsaw))
		ids := map[string]bool{}
		for _, f := range first {
			if !slices.Contains(findingIDs, f.ID) {
				t.Errorf("finding %q has id %q, not one of %v", f.Title, f.ID, findingIDs)
			}
			ids[f.ID] = true
		}
		if len(ids) != len(findingIDs) || len(first) != len(findingIDs) {
			t.Errorf("ids = %v over %d findings, want each of the %d ids once", ids, len(first), len(findingIDs))
		}

		again := summarise(t,
			assistant(t, "2026-10-16T08:00:00Z", "msg_1", "claude-sonnet-5", 5),
			hookError(t, "2026-10-16T08:00:01Z", "PostToolUse:Bash", "node hook.mjs"),
			hookError(t, "2026-10-16T08:00:02Z", "PostToolUse:Bash", "node hook.mjs"))
		again.SessionID = "sess0002"
		next := findingsJSON(t, Build(append(recs, sessionRecord(root, again)), inv, now.Add(time.Hour), 0, warsaw))
		byTitle := func(fs []Finding, prefix string) Finding {
			for _, f := range fs {
				if strings.HasPrefix(f.Title, prefix) {
					return f
				}
			}
			return Finding{}
		}
		before, after := byTitle(first, "1 hook failures"), byTitle(next, "3 hook failures")
		if before.ID != FindingHookFailures || after.ID != before.ID {
			t.Errorf("hook failures id = %q, then %q once its count changed; want %q both times", before.ID, after.ID, FindingHookFailures)
		}
		nextIDs := map[string]bool{}
		for _, f := range next {
			nextIDs[f.ID] = true
		}
		if !maps.Equal(ids, nextIDs) {
			t.Errorf("ids on the next run = %v, want %v", nextIDs, ids)
		}
	})

	t.Run("findings that list things carry items keyed by hook, name, subagent row or path, and evidence is unchanged", func(t *testing.T) {
		root, recs, inv := everyFinding(t)
		r := Build(recs, inv, now, 0, warsaw)
		byID := map[string]Finding{}
		for _, f := range findingsJSON(t, r) {
			byID[f.ID] = f
		}
		keys := func(id string) []string {
			var out []string
			for _, it := range byID[id].Items {
				out = append(out, it.Key)
			}
			return out
		}
		want := map[string][]string{
			FindingHookFailures:      {"SessionStart:startup :: run-hook.cmd session-start"},
			FindingUnusedAgents:      {"eng:reviewer", "planner"},
			FindingUnusedMCP:         {"sentry", "slack"},
			FindingTopTierSubagents:  {"Explore on opus-5-5"},
			FindingHeavyInstructions: {filepath.Join(root, "CLAUDE.md")},
			FindingLongMemoryIndex:   {filepath.Join(root, "projects", "-p", "memory", "MEMORY.md")},
		}
		for id, wantKeys := range want {
			got := keys(id)
			slices.Sort(got)
			if !slices.Equal(got, wantKeys) {
				t.Errorf("%s item keys = %q, want %q", id, got, wantKeys)
			}
		}
		for _, id := range []string{FindingBigResults, FindingContextBloat, FindingCacheWrites, FindingSkillListing} {
			if len(byID[id].Items) != 0 {
				t.Errorf("%s names nothing to address, yet has items %+v", id, byID[id].Items)
			}
		}

		skills := byID[FindingUnusedSkills]
		if len(skills.Items) != 22 || len(skills.Evidence) != 15 {
			t.Fatalf("unused skills: %d items, %d evidence; want all 22 skills and commands and evidence capped at 15", len(skills.Items), len(skills.Evidence))
		}
		for i, e := range skills.Evidence {
			if skills.Items[i].Text != e {
				t.Errorf("evidence %d = %q, item text = %q; the evidence is the items' first lines", i, e, skills.Items[i].Text)
			}
		}
		if !slices.Contains(keys(FindingUnusedSkills), "unused-skill") || slices.Contains(keys(FindingUnusedSkills), "worktree") {
			t.Errorf("unused skill keys = %q, want the unused skill by name and not the used one", keys(FindingUnusedSkills))
		}
		if ev := byID[FindingHookFailures].Evidence; len(ev) != 1 || ev[0] != "1× SessionStart:startup :: run-hook.cmd session-start" {
			t.Errorf("hook evidence = %q, want the count and hook as before", ev)
		}
		if ev := byID[FindingTopTierSubagents].Evidence; len(ev) != 1 || !strings.HasPrefix(ev[0], "Explore on opus-5-5: $") {
			t.Errorf("subagent evidence = %q, want the row and its cost as before", ev)
		}
	})

	t.Run("the terminal report shows each finding's id, and --all shows each item's key", func(t *testing.T) {
		_, recs, inv := everyFinding(t)
		r := Build(recs, inv, now, 0, warsaw)
		var short, all bytes.Buffer
		if err := Render(&short, r, false); err != nil {
			t.Fatal(err)
		}
		if err := Render(&all, r, true); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(short.String(), "! 1 hook failures  [hook-failures]") {
			t.Errorf("report lacks the hook finding with its id:\n%s", short.String())
		}
		for _, f := range r.Findings {
			if !strings.Contains(all.String(), "["+f.ID+"]") {
				t.Errorf("--all lacks the id of %q", f.Title)
			}
			for _, it := range f.Items {
				if !strings.Contains(all.String(), "["+f.ID+":"+it.Key+"]") {
					t.Errorf("--all lacks the item %s:%s", f.ID, it.Key)
				}
			}
		}
		if strings.Contains(short.String(), "[unused-skills:") {
			t.Errorf("the default report shows evidence, not item keys:\n%s", short.String())
		}
	})

	t.Run("a captured transcript records the last failure time per hook and each big result's time", func(t *testing.T) {
		fixture, err := os.ReadFile(filepath.Join("testdata", "hook-errors.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		body := string(fixture) + toolResult(t, "2026-10-27T12:51:10.456Z", "toolu_015KHKS8FCauL1wfBfrKKxU2", strings.Repeat("l", 30_000), false)
		path := writeTranscript(t, t.TempDir(), "sess0001", body)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		store := &Store{Dir: t.TempDir(), Loc: warsaw}
		stale := &Record{Version: recordVersion - 1, SessionID: "sess0001", File: newFile(),
			Main: Source{Path: path, Size: info.Size(), ModTime: info.ModTime(), Offset: info.Size()}}
		if err := store.save(stale); err != nil {
			t.Fatal(err)
		}
		if res, err := store.Capture(path, ""); err != nil || res.Parsed != 1 {
			t.Fatalf("capture = %+v, %v; a record from before times were kept is read again while its transcript is on disk", res, err)
		}
		rec, err := store.Load("sess0001")
		if err != nil {
			t.Fatal(err)
		}
		f := rec.File
		post := `PostToolUse:Bash :: node "${CLAUDE_PLUGIN_ROOT}/hooks/posttooluse-shadcn-font-fix.mjs"`
		start := `SessionStart:startup :: node "${CLAUDE_PLUGIN_ROOT}/hooks/session-start.mjs"`
		for hook, want := range map[string]string{post: "2026-10-27T12:52:03.120Z", start: "2026-10-27T12:50:00.012Z"} {
			if got := f.HookFailedAt(hook); !got.Equal(mustTime(t, want)) {
				t.Errorf("%s last failed at %s, want %s, to the millisecond of its line", hook, got, want)
			}
		}
		if f.HookErrors[post] != 2 {
			t.Errorf("hook errors = %v, want the PostToolUse hook twice", f.HookErrors)
		}
		if len(f.Big) != 1 || !f.ResultAt(f.Big[0]).Equal(mustTime(t, "2026-10-27T12:51:10.456Z")) {
			t.Errorf("big results = %+v, want the one result at the time its line carries", f.Big)
		}
	})

	t.Run("a record without per-hook or per-result times falls back to the session's end time", func(t *testing.T) {
		end := mustTime(t, "2026-10-15T18:00:00Z")
		f := newFile()
		f.Start, f.End = end.Add(-8*time.Hour), end
		f.HookErrors = map[string]int{"SessionStart:startup :: run-hook.cmd": 3}
		f.Big = []BigResult{{Day: "2026-10-15", Tool: "Read", Bytes: 40_000}}
		data, err := json.Marshal(&Record{Version: 3, SessionID: "old00001", File: f})
		if err != nil {
			t.Fatal(err)
		}
		var old Record
		if err := json.Unmarshal(data, &old); err != nil {
			t.Fatal(err)
		}
		if got := old.File.HookFailedAt("SessionStart:startup :: run-hook.cmd"); !got.Equal(end) {
			t.Errorf("hook failure without its own time = %s, want the session's end %s", got, end)
		}
		if got := old.File.ResultAt(old.File.Big[0]); !got.Equal(end) {
			t.Errorf("big result without its own time = %s, want the session's end %s", got, end)
		}

		r := Build([]*Record{sessionRecord("", summarise(t, assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5))), &old}, nil, now, 30, warsaw)
		if len(r.BigResults) != 1 || !r.BigResults[0].At.Equal(end) {
			t.Errorf("report big results = %+v, want the old result dated at its session's end", r.BigResults)
		}
	})
}

var findingIDs = []string{
	"hook-failures", "unused-skills", "unused-agents", "unused-mcp", "context-bloat", "cache-writes",
	"big-results", "top-tier-subagents", "skill-listing", "heavy-instructions", "long-memory-index",
}

func hookError(t *testing.T, ts, hook, command string) string {
	return line(t, map[string]any{"type": "attachment", "timestamp": ts, "attachment": map[string]any{
		"type": "hook_non_blocking_error", "hookName": hook, "command": command}})
}

// everyFinding is a config root and a session that raise each finding once,
// with more unused skills than the evidence shows.
func everyFinding(t *testing.T) (string, []*Record, []Inventory) {
	t.Helper()
	root := fullRoot(t)
	for i := range 20 {
		dir := filepath.Join(root, "skills", fmt.Sprintf("idle-%02d", i))
		os.MkdirAll(dir, 0o700)
		os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(fmt.Sprintf("---\nname: idle-%02d\ndescription: Idle\n---\n", i)), 0o600)
	}
	big := strings.Repeat("y", 40_000)
	lines := []string{
		prompt(t, "2026-10-15T09:59:00Z", "<command-name>/worktree</command-name>"),
		hookError(t, "2026-10-15T10:00:00Z", "SessionStart:startup", "run-hook.cmd session-start"),
		line(t, map[string]any{"type": "attachment", "timestamp": "2026-10-15T10:00:00Z",
			"attachment": map[string]any{"type": "skill_listing", "content": strings.Repeat("s", 24_000)}}),
	}
	for i := range 5 {
		ts := fmt.Sprintf("2026-10-15T10:0%d:00Z", i)
		lines = append(lines, line(t, map[string]any{
			"type": "assistant", "timestamp": ts, "sessionId": "sess0001",
			"message": map[string]any{"id": fmt.Sprintf("msg_%d", i), "model": "claude-opus-5-5",
				"content": []map[string]any{toolUse(fmt.Sprintf("toolu_%d", i), "mcp__plugin_eng_github__get", map[string]any{"query": "pr"})},
				"usage":   map[string]any{"input_tokens": 1, "output_tokens": 10, "cache_read_input_tokens": 10, "cache_creation_input_tokens": 200_000}},
		}))
		lines = append(lines, toolResult(t, ts, fmt.Sprintf("toolu_%d", i), big, false))
	}
	rec := sessionRecord(root, summarise(t, lines...))
	rec.Subagents = map[string]*Subagent{"a.jsonl": {AgentType: "Explore",
		File: summarise(t, assistant(t, "2026-10-15T10:00:00Z", "msg_s", "claude-opus-5-5", 300_000))}}
	return root, []*Record{rec}, []Inventory{Scan(root, []string{root})}
}

// findingsJSON reads the findings back the way a --json reader would.
func findingsJSON(t *testing.T, r *Report) []Finding {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Findings []Finding `json:"findings"`
	}
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	return back.Findings
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}
