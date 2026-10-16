package usage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	warsaw = time.FixedZone("CEST", 2*3600)
	now    = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
)

// line builds one transcript record. Field order is irrelevant to the parser,
// which decodes rather than matches prefixes.
func line(t *testing.T, v map[string]any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

func assistant(t *testing.T, ts, id, model string, out int64, blocks ...map[string]any) string {
	if blocks == nil {
		blocks = []map[string]any{{"type": "text", "text": "ok"}}
	}
	return line(t, map[string]any{
		"type": "assistant", "timestamp": ts, "sessionId": "sess0001", "cwd": "/tmp/project",
		"message": map[string]any{
			"id": id, "model": model, "content": blocks,
			"usage": map[string]any{
				"input_tokens": 10, "output_tokens": out, "cache_read_input_tokens": 1000,
				"cache_creation_input_tokens": 200,
				"cache_creation":              map[string]any{"ephemeral_5m_input_tokens": 200, "ephemeral_1h_input_tokens": 0},
			},
		},
	})
}

func toolUse(id, name string, input map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
}

func toolResult(t *testing.T, ts, id, content string, isError bool) string {
	return line(t, map[string]any{
		"type": "user", "timestamp": ts,
		"message": map[string]any{"role": "user", "content": []map[string]any{
			{"type": "tool_result", "tool_use_id": id, "content": content, "is_error": isError},
		}},
	})
}

func prompt(t *testing.T, ts, text string) string {
	return line(t, map[string]any{
		"type": "user", "timestamp": ts, "promptSource": "user",
		"message": map[string]any{"role": "user", "content": text},
	})
}

func summarise(t *testing.T, lines ...string) *File {
	t.Helper()
	f, err := Summarise(strings.NewReader(strings.Join(lines, "")), warsaw)
	if err != nil {
		t.Fatalf("Summarise: %v", err)
	}
	return f
}

func TestSummarise(t *testing.T) {
	t.Run("a turn's usage is counted once, although the transcript repeats it on every content block of that message", func(t *testing.T) {
		f := summarise(t,
			assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5),
			assistant(t, "2026-10-15T10:00:01Z", "msg_1", "claude-opus-5-5", 40),
			assistant(t, "2026-10-15T10:00:05Z", "msg_2", "claude-opus-5-5", 7),
		)
		got := f.Usage["2026-10-15"]["claude-opus-5-5"]
		if got.Turns != 2 {
			t.Errorf("turns = %d, want 2", got.Turns)
		}
		if got.Output != 47 {
			t.Errorf("output = %d, want 47: the repeated block carries the final count and replaces the first", got.Output)
		}
		if got.CacheRead != 2000 {
			t.Errorf("cache read = %d, want 2000", got.CacheRead)
		}
	})

	t.Run("tokens are attributed to the local day and the model the turn ran on", func(t *testing.T) {
		f := summarise(t,
			assistant(t, "2026-10-15T23:30:00Z", "msg_1", "claude-opus-5-5", 5),
			assistant(t, "2026-10-15T20:00:00Z", "msg_2", "claude-sonnet-5-5", 5),
		)
		if f.Usage["2026-10-16"]["claude-opus-5-5"].Turns != 1 {
			t.Errorf("23:30 UTC is the next day in Warsaw: %v", f.Usage)
		}
		if f.Usage["2026-10-15"]["claude-sonnet-5-5"].Turns != 1 {
			t.Errorf("sonnet turn missing from its own model bucket: %v", f.Usage)
		}
	})

	t.Run("a tool result's size and error flag are attributed to the tool that produced it", func(t *testing.T) {
		big := strings.Repeat("x", 25_000)
		f := summarise(t,
			assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5,
				toolUse("toolu_1", "Bash", map[string]any{"command": "cd /x && git log"}),
				toolUse("toolu_2", "Read", map[string]any{"file_path": "/x/big.go"})),
			toolResult(t, "2026-10-15T10:00:01Z", "toolu_1", "fatal: not a repo", true),
			toolResult(t, "2026-10-15T10:00:01Z", "toolu_2", big, false),
		)
		day := f.Tools["2026-10-15"]
		if day["Bash"].Calls != 1 || day["Bash"].Errors != 1 {
			t.Errorf("Bash = %+v, want 1 call, 1 error", day["Bash"])
		}
		if day["Read"].Errors != 0 || day["Read"].ResultBytes < 25_000 {
			t.Errorf("Read = %+v, want its 25K result and no error", day["Read"])
		}
		if len(f.Big) != 1 || f.Big[0].Tool != "Read" || f.Big[0].Input != "/x/big.go" {
			t.Errorf("big results = %+v, want the Read with its input", f.Big)
		}
		if f.Uses["2026-10-15"]["bash:git"] != 1 {
			t.Errorf("the program behind a leading cd is what ran: %v", f.Uses)
		}
	})

	t.Run("prompts, slash commands and interrupts are told apart", func(t *testing.T) {
		f := summarise(t,
			prompt(t, "2026-10-15T10:00:00Z", "fix the flaky test"),
			prompt(t, "2026-10-15T10:01:00Z", "<command-name>/worktree</command-name>"),
			prompt(t, "2026-10-15T10:02:00Z", "[Request interrupted by user]"),
		)
		ev := f.Events["2026-10-15"]
		if ev[EventPrompts] != 1 || ev[EventInterrupts] != 1 {
			t.Errorf("events = %v, want 1 prompt and 1 interrupt", ev)
		}
		if f.Uses["2026-10-15"]["command:worktree"] != 1 {
			t.Errorf("uses = %v", f.Uses)
		}
		if f.DisplayTitle() != "fix the flaky test" {
			t.Errorf("title = %q", f.DisplayTitle())
		}
	})
}

// writeTranscript lays out a config root the way Claude Code does.
func writeTranscript(t *testing.T, root, id, body string) string {
	t.Helper()
	dir := filepath.Join(root, "projects", "-tmp-project")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCapture(t *testing.T) {
	t.Run("a subagent transcript's usage lands in its parent session, under its agent type", func(t *testing.T) {
		root := t.TempDir()
		main := writeTranscript(t, root, "sess0001", assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5))
		subDir := filepath.Join(strings.TrimSuffix(main, ".jsonl"), "subagents")
		os.MkdirAll(subDir, 0o700)
		os.WriteFile(filepath.Join(subDir, "agent-a1.jsonl"), []byte(assistant(t, "2026-10-15T10:00:02Z", "msg_9", "claude-opus-5-5", 3)), 0o600)
		os.WriteFile(filepath.Join(subDir, "agent-a1.meta.json"), []byte(`{"agentType":"Explore"}`), 0o600)

		store := &Store{Dir: t.TempDir(), Loc: warsaw}
		if _, err := store.Capture(main); err != nil {
			t.Fatal(err)
		}
		rec, err := store.Load("sess0001")
		if err != nil {
			t.Fatal(err)
		}
		sa := rec.Subagents["agent-a1.jsonl"]
		if sa == nil || sa.AgentType != "Explore" || sa.File.Usage["2026-10-15"]["claude-opus-5-5"].Turns != 1 {
			t.Fatalf("subagent = %+v", sa)
		}
		r := Build([]*Record{rec}, nil, now, 30, warsaw)
		if r.SubCost <= 0 || r.SubCost >= r.Cost.Total() {
			t.Errorf("subagent cost %.4f should be a part of the session's %.4f", r.SubCost, r.Cost.Total())
		}
	})

	t.Run("a transcript unchanged since its last summary is not parsed again", func(t *testing.T) {
		root := t.TempDir()
		path := writeTranscript(t, root, "sess0001", assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5))
		store := &Store{Dir: t.TempDir(), Loc: warsaw}
		if res, _ := store.Capture(path); res.Parsed != 1 {
			t.Fatalf("first capture parsed %d", res.Parsed)
		}
		if res, _ := store.Capture(path); res.Parsed != 0 || res.Skipped != 1 {
			t.Errorf("second capture = %+v, want nothing parsed", res)
		}
	})

	t.Run("appended lines are added to the summary without counting earlier ones twice", func(t *testing.T) {
		root := t.TempDir()
		first := assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5)
		path := writeTranscript(t, root, "sess0001", first)
		store := &Store{Dir: t.TempDir(), Loc: warsaw}
		store.Capture(path)
		more := assistant(t, "2026-10-15T10:00:01Z", "msg_1", "claude-opus-5-5", 40) + assistant(t, "2026-10-15T10:01:00Z", "msg_2", "claude-opus-5-5", 2)
		fh, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		fh.WriteString(more)
		fh.Close()
		future := time.Now().Add(time.Minute)
		os.Chtimes(path, future, future)
		if _, err := store.Capture(path); err != nil {
			t.Fatal(err)
		}
		rec, _ := store.Load("sess0001")
		got := rec.File.Usage["2026-10-15"]["claude-opus-5-5"]
		if got.Turns != 2 || got.Output != 42 {
			t.Errorf("after an append: %+v, want 2 turns and 42 output", got)
		}
	})

	t.Run("backfill summarises a transcript no hook ever saw, and a second run parses nothing", func(t *testing.T) {
		root := t.TempDir()
		writeTranscript(t, root, "sess0001", assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5))
		writeTranscript(t, root, "sess0002", assistant(t, "2026-10-15T11:00:00Z", "msg_1", "claude-opus-5-5", 5))
		store := &Store{Dir: t.TempDir(), Loc: warsaw}
		res, err := store.Backfill([]string{root})
		if err != nil || res.Parsed != 2 {
			t.Fatalf("backfill = %+v, %v", res, err)
		}
		recs, _ := store.List()
		if len(recs) != 2 || recs[0].Root != root {
			t.Fatalf("records = %d, root %q", len(recs), recs[0].Root)
		}
		if res, _ := store.Backfill([]string{root}); res.Parsed != 0 {
			t.Errorf("second backfill parsed %d", res.Parsed)
		}
	})
}

func skillRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, desc := range map[string]string{"worktree": "Switch to a worktree", "unused-skill": strings.Repeat("d", 400)} {
		dir := filepath.Join(root, "skills", name)
		os.MkdirAll(dir, 0o700)
		os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: "+desc+"\n---\nbody\n"), 0o600)
	}
	return root
}

func sessionRecord(root string, f *File) *Record {
	return &Record{Version: recordVersion, SessionID: f.SessionID, Root: root, Repo: "project", File: f}
}

func TestReport(t *testing.T) {
	t.Run("reports a skill installed in a config root and unused in 90 days, with the listing tokens it costs", func(t *testing.T) {
		root := skillRoot(t)
		f := summarise(t,
			prompt(t, "2026-10-15T10:00:00Z", "<command-name>/worktree</command-name>"),
			assistant(t, "2026-10-15T10:00:01Z", "msg_1", "claude-opus-5-5", 5))
		r := Build([]*Record{sessionRecord(root, f)}, []Inventory{Scan(root, nil)}, now, 30, warsaw)
		var found *Finding
		for i := range r.Findings {
			if strings.Contains(r.Findings[i].Title, "skills and commands unused") {
				found = &r.Findings[i]
			}
		}
		if found == nil {
			t.Fatalf("no unused-skill finding: %+v", r.Findings)
		}
		joined := strings.Join(found.Evidence, "\n")
		if !strings.Contains(joined, "unused-skill") || strings.Contains(joined, "worktree") {
			t.Errorf("evidence = %q, want only the unused skill", joined)
		}
		if !strings.Contains(joined, "104 tok") {
			t.Errorf("evidence = %q, want the 104 listing tokens its name and description cost", joined)
		}
	})

	t.Run("unused findings on a store shorter than 90 days say how much history they rest on", func(t *testing.T) {
		root := skillRoot(t)
		f := summarise(t, assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5))
		r := Build([]*Record{sessionRecord(root, f)}, []Inventory{Scan(root, nil)}, now, 30, warsaw)
		for _, fd := range r.Findings {
			if !strings.Contains(fd.Title, "unused in 90 days") {
				continue
			}
			if fd.Severity != Warning || !strings.Contains(fd.Advice, "only 2 days") || !strings.Contains(fd.Advice, "--backfill") {
				t.Errorf("finding = %+v, want a warning that names the 2 days of history and the backfill", fd)
			}
			return
		}
		t.Fatalf("no unused finding: %+v", r.Findings)
	})

	t.Run("a skill invoked through the Skill tool and the same skill run as a slash command count as uses of one skill", func(t *testing.T) {
		root := skillRoot(t)
		viaTool := summarise(t, assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5,
			toolUse("toolu_1", "Skill", map[string]any{"skill": "unused-skill"})))
		inv := Scan(root, nil)
		uses := map[string]int{}
		for k, n := range viaTool.Uses["2026-10-15"] {
			uses[root+"|"+k] += n
		}
		viaCommand := map[string]int{root + "|command:unused-skill": 1}
		for _, it := range inv.Items {
			if it.Name != "unused-skill" {
				continue
			}
			if itemUses(it, uses) != 1 || itemUses(it, viaCommand) != 1 {
				t.Errorf("Skill tool counted %d, slash command %d; both are one use", itemUses(it, uses), itemUses(it, viaCommand))
			}
		}
	})

	t.Run("reports failing hooks with the hook name and command", func(t *testing.T) {
		f := summarise(t,
			assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5),
			line(t, map[string]any{"type": "attachment", "timestamp": "2026-10-15T10:00:00Z", "attachment": map[string]any{
				"type": "hook_non_blocking_error", "hookName": "SessionStart:startup", "command": "run-hook.cmd session-start"}}))
		r := Build([]*Record{sessionRecord("", f)}, nil, now, 30, warsaw)
		if len(r.Findings) == 0 || r.Findings[0].Severity == Warning || !strings.Contains(strings.Join(r.Findings[0].Evidence, ""), "SessionStart:startup :: run-hook.cmd session-start") {
			t.Errorf("findings = %+v", r.Findings)
		}
	})

	t.Run("the JSON carries every number the terminal report prints", func(t *testing.T) {
		f := summarise(t,
			prompt(t, "2026-10-15T10:00:00Z", "hello"),
			assistant(t, "2026-10-15T10:00:01Z", "msg_1", "claude-opus-5-5", 5))
		r := Build([]*Record{sessionRecord("", f)}, nil, now, 30, warsaw)
		var text bytes.Buffer
		if err := Render(&text, r, true); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(r)
		var back Report
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatal(err)
		}
		var again bytes.Buffer
		back.GeneratedAt = r.GeneratedAt
		if err := Render(&again, &back, true); err != nil {
			t.Fatal(err)
		}
		if text.String() != again.String() {
			t.Errorf("the report rendered from its JSON differs:\n%s\n---\n%s", text.String(), again.String())
		}
		for _, want := range []string{"1 sessions", "1 prompts", fmt.Sprintf("$%.2f", r.Cost.Total())} {
			if !strings.Contains(text.String(), want) {
				t.Errorf("report lacks %q:\n%s", want, text.String())
			}
		}
	})

	t.Run("an empty usage store says how to fill it, rather than printing a report of zeroes", func(t *testing.T) {
		var out bytes.Buffer
		if err := Render(&out, Build(nil, nil, now, 30, warsaw), false); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "--backfill") || strings.Contains(out.String(), "$0") {
			t.Errorf("empty report = %q", out.String())
		}
	})
}

// fullRoot installs one of everything a config root can carry.
func fullRoot(t *testing.T) string {
	t.Helper()
	root := skillRoot(t)
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	plugin := filepath.Join(root, "plugins", "cache", "eng")
	write("plugins/installed_plugins.json", `{"plugins":{"eng@market":[{"installPath":"`+plugin+`"}]}}`)
	write("plugins/cache/eng/commands/review.md", "---\ndescription: Review a diff\n---\n")
	write("plugins/cache/eng/agents/reviewer.md", "---\nname: reviewer\ndescription: Reviews\nmodel: opus\n---\n")
	write("plugins/cache/eng/.mcp.json", `{"mcpServers":{"github":{}}}`)
	write("agents/planner.md", "---\nname: planner\ndescription: Plans\n---\n")
	write("settings.json", `{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"notify"}]}]}}`)
	write(".claude.json", `{"mcpServers":{"slack":{}},"projects":{"/p":{"mcpServers":{"sentry":{}}}}}`)
	write("CLAUDE.md", strings.Repeat("instructions\n", 1500))
	write("projects/-p/memory/MEMORY.md", strings.Repeat("- [a](a.md) — b\n", 130))
	return root
}

func TestReportFindings(t *testing.T) {
	root := fullRoot(t)
	inv := Scan(root, []string{root})
	kinds := map[string]int{}
	for _, it := range inv.Items {
		kinds[it.Kind]++
	}
	if kinds["skill"] != 2 || kinds["command"] != 1 || kinds["agent"] != 2 {
		t.Errorf("items by kind = %v", kinds)
	}
	if len(inv.Context) != 2 {
		t.Errorf("context files = %+v, want CLAUDE.md once and the memory index", inv.Context)
	}
	if len(inv.MCP) != 3 || len(inv.Hooks) != 1 {
		t.Errorf("mcp = %+v, hooks = %+v", inv.MCP, inv.Hooks)
	}

	big := strings.Repeat("y", 40_000)
	var lines []string
	for i := 0; i < 5; i++ {
		ts := fmt.Sprintf("2026-10-15T10:0%d:00Z", i)
		lines = append(lines, line(t, map[string]any{
			"type": "assistant", "timestamp": ts, "sessionId": "sess0001",
			"message": map[string]any{"id": fmt.Sprintf("msg_%d", i), "model": "claude-opus-5-5",
				"content": []map[string]any{toolUse(fmt.Sprintf("toolu_%d", i), "mcp__plugin_eng_github__get", map[string]any{"query": "pr"})},
				"usage":   map[string]any{"input_tokens": 1, "output_tokens": 10, "cache_read_input_tokens": 10, "cache_creation_input_tokens": 200_000}},
		}))
		lines = append(lines, toolResult(t, ts, fmt.Sprintf("toolu_%d", i), big, false))
	}
	lines = append(lines, line(t, map[string]any{"type": "attachment", "timestamp": "2026-10-15T10:00:00Z",
		"attachment": map[string]any{"type": "skill_listing", "content": strings.Repeat("s", 24_000)}}))
	f := summarise(t, lines...)
	sub := summarise(t, assistant(t, "2026-10-15T10:00:00Z", "msg_s", "claude-opus-5-5", 300_000))
	rec := sessionRecord(root, f)
	rec.Subagents = map[string]*Subagent{"a.jsonl": {AgentType: "Explore", File: sub}}

	r := Build([]*Record{rec}, []Inventory{inv}, now, 0, warsaw)
	titles := ""
	for _, fd := range r.Findings {
		titles += fd.Title + "\n"
	}
	for _, want := range []string{
		"agents never spawned", "MCP servers with no calls", "of turns run above 150K context",
		"Cache writes are", "tool results over 30K", "Subagents running on top-tier models",
		"Skill listing averages", "always-loaded instruction files", "memory indexes over 120 lines",
	} {
		if !strings.Contains(titles, want) {
			t.Errorf("missing finding %q in:\n%s", want, titles)
		}
	}
	if strings.Contains(strings.Join(r.Findings[1].Evidence, ""), "github") {
		t.Errorf("github is used through its plugin-prefixed tool name and must not be reported unused")
	}

	var out bytes.Buffer
	if err := Render(&out, r, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "more (--all)") {
		t.Errorf("the default report caps findings and says how to see the rest:\n%s", out.String())
	}

	store := &Store{Dir: t.TempDir()}
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	if roots := store.Roots(); len(roots) == 0 || roots[0] != root {
		t.Errorf("roots = %v, want the configured root first", roots)
	}
	if ModelName("claude-haiku-4-5-20251001") != "haiku-4-5" {
		t.Errorf("ModelName = %q", ModelName("claude-haiku-4-5-20251001"))
	}
}
