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

	t.Run("a tool result's size is the text it adds to the context; an image in it adds nothing to the size", func(t *testing.T) {
		screenshot := strings.Repeat("A", 500_000)
		f := summarise(t,
			assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5,
				toolUse("toolu_1", "Read", map[string]any{"file_path": "/x/shot.png"})),
			line(t, map[string]any{
				"type": "user", "timestamp": "2026-10-15T10:00:01Z",
				"message": map[string]any{"role": "user", "content": []map[string]any{{
					"type": "tool_result", "tool_use_id": "toolu_1", "content": []map[string]any{
						{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": screenshot}},
						{"type": "text", "text": "caption"},
					},
				}}},
			}),
		)
		if got := f.Tools["2026-10-15"]["Read"].ResultBytes; got != int64(len("caption")) {
			t.Errorf("Read result = %d, want %d: only the text block reaches the context as text", got, len("caption"))
		}
		if len(f.Big) != 0 {
			t.Errorf("big results = %+v, want none: a screenshot is not a large text dump", f.Big)
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
		if _, err := store.Capture(main, ""); err != nil {
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
		if res, _ := store.Capture(path, ""); res.Parsed != 1 {
			t.Fatalf("first capture parsed %d", res.Parsed)
		}
		if res, _ := store.Capture(path, ""); res.Parsed != 0 || res.Skipped != 1 {
			t.Errorf("second capture = %+v, want nothing parsed", res)
		}
	})

	t.Run("appended lines are added to the summary without counting earlier ones twice", func(t *testing.T) {
		root := t.TempDir()
		first := assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5)
		path := writeTranscript(t, root, "sess0001", first)
		store := &Store{Dir: t.TempDir(), Loc: warsaw}
		store.Capture(path, "")
		more := assistant(t, "2026-10-15T10:00:01Z", "msg_1", "claude-opus-5-5", 40) + assistant(t, "2026-10-15T10:01:00Z", "msg_2", "claude-opus-5-5", 2)
		fh, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		fh.WriteString(more)
		fh.Close()
		future := time.Now().Add(time.Minute)
		os.Chtimes(path, future, future)
		if _, err := store.Capture(path, ""); err != nil {
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
		res, err := store.Backfill([]string{root}, time.Time{})
		if err != nil || res.Parsed != 2 {
			t.Fatalf("backfill = %+v, %v", res, err)
		}
		recs, _ := store.List()
		if len(recs) != 2 || recs[0].Root != root {
			t.Fatalf("records = %d, root %q", len(recs), recs[0].Root)
		}
		if res, _ := store.Backfill([]string{root}, time.Time{}); res.Parsed != 0 {
			t.Errorf("second backfill parsed %d", res.Parsed)
		}
	})

	t.Run("backfill skips a transcript last written before the usage horizon, so it never restores a record prune deleted", func(t *testing.T) {
		root := t.TempDir()
		old := writeTranscript(t, root, "sess0001", assistant(t, "2026-03-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5))
		writeTranscript(t, root, "sess0002", assistant(t, "2026-10-15T11:00:00Z", "msg_1", "claude-opus-5-5", 5))
		aged := time.Now().Add(-200 * 24 * time.Hour)
		os.Chtimes(old, aged, aged)

		store := &Store{Dir: t.TempDir(), Loc: warsaw}
		res, err := store.Backfill([]string{root}, time.Now().Add(-180*24*time.Hour))
		if err != nil || res.Parsed != 1 || res.Expired != 1 {
			t.Fatalf("backfill = %+v, %v; want one read and one past the horizon", res, err)
		}
		if _, err := store.Load("sess0001"); err == nil {
			t.Error("backfill restored a session older than the horizon")
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

	t.Run("an unused finding resting on less than 90 days of history says backfill adds only transcripts still on disk, and what deletes older ones", func(t *testing.T) {
		root := skillRoot(t)
		f := summarise(t, assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5))
		r := Build([]*Record{sessionRecord(root, f)}, []Inventory{Scan(root, nil)}, now, 30, warsaw)
		for _, fd := range r.Findings {
			if !strings.Contains(fd.Title, "unused in 90 days") {
				continue
			}
			if !strings.Contains(fd.Advice, "still on disk") || !strings.Contains(fd.Advice, "cleanupPeriodDays") {
				t.Errorf("advice = %q, want it to say backfill reaches only transcripts still on disk, and name cleanupPeriodDays", fd.Advice)
			}
			return
		}
		t.Fatalf("no unused finding: %+v", r.Findings)
	})

	t.Run("an instruction file counts as always loaded only where Claude Code loads it", func(t *testing.T) {
		vault := t.TempDir()
		cwd := filepath.Join(vault, "spaces", "business")
		write := func(path, body string) {
			os.MkdirAll(filepath.Dir(path), 0o700)
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		write(filepath.Join(vault, "CLAUDE.md"), "vault-wide instructions\n")
		write(filepath.Join(vault, "AGENTS.md"), "the same, for another agent\n")
		write(filepath.Join(cwd, "CLAUDE.md"), "@AGENTS.md\n")
		write(filepath.Join(cwd, "AGENTS.md"), "shared instructions\n")

		loaded := map[string]bool{}
		for _, c := range Scan(t.TempDir(), []string{cwd}).Context {
			loaded[c.Path] = true
		}
		for _, want := range []string{filepath.Join(vault, "CLAUDE.md"), filepath.Join(cwd, "CLAUDE.md"), filepath.Join(cwd, "AGENTS.md")} {
			if !loaded[want] {
				t.Errorf("%s is loaded into every session started in %s; context = %v", want, cwd, loaded)
			}
		}
		if notLoaded := filepath.Join(vault, "AGENTS.md"); loaded[notLoaded] {
			t.Errorf("%s is imported by no CLAUDE.md and never loaded", notLoaded)
		}
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

	t.Run("two accounts are reported together and can be read one at a time", func(t *testing.T) {
		work, personal := skillRoot(t), skillRoot(t)
		a := summarise(t, assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5))
		b := summarise(t, assistant(t, "2026-10-15T11:00:00Z", "msg_1", "claude-opus-5-5", 5))
		recs := []*Record{sessionRecord(work, a), sessionRecord(personal, b)}
		recs[1].SessionID = "sess0002"
		invs := []Inventory{Scan(work, nil), Scan(personal, nil)}

		all := Build(recs, invs, now, 30, warsaw)
		if all.Sessions != 2 || len(all.Accounts) != 2 {
			t.Fatalf("combined: %d sessions, accounts %+v", all.Sessions, all.Accounts)
		}
		var out bytes.Buffer
		Render(&out, all, false)
		if !strings.Contains(out.String(), "accounts") {
			t.Errorf("a two-account report names both accounts:\n%s", out.String())
		}

		rs, is := ForRoot(recs, invs, personal)
		one := Build(rs, is, now, 30, warsaw)
		if one.Sessions != 1 || len(one.Accounts) != 1 || one.Accounts[0].Name != personal || len(one.Inventory) != 1 {
			t.Errorf("one account: %d sessions, accounts %+v, %d inventories", one.Sessions, one.Accounts, len(one.Inventory))
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

// agedRecord stores a session whose last turn ended age before now. The file
// itself is written today, as a backfill would write it.
func agedRecord(t *testing.T, store *Store, id string, start, end time.Time) string {
	t.Helper()
	f := newFile()
	f.SessionID, f.Start, f.End = id, start, end
	if err := store.save(&Record{Version: recordVersion, SessionID: id, File: f}); err != nil {
		t.Fatal(err)
	}
	path, _ := store.path(id)
	return path
}

func TestPrune(t *testing.T) {
	const day = 24 * time.Hour
	horizon := 180 * day
	quiet := func(age time.Duration) (time.Time, time.Time) { return now.Add(-age - time.Hour), now.Add(-age) }

	t.Run("deletes a usage record whose session has been quiet longer than the horizon, and keeps a younger one", func(t *testing.T) {
		store := &Store{Dir: t.TempDir()}
		s, e := quiet(horizon + day)
		old := agedRecord(t, store, "old00001", s, e)
		s, e = quiet(horizon - day)
		young := agedRecord(t, store, "new00001", s, e)

		res, err := store.Prune(now, horizon, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Removed) != 1 || res.Removed[0] != "old00001" || res.Kept != 1 {
			t.Errorf("Removed = %v, Kept = %d; want old00001 removed and one kept", res.Removed, res.Kept)
		}
		if _, err := os.Stat(old); err == nil {
			t.Error("reported as removed but still on disk")
		}
		if _, err := os.Stat(young); err != nil {
			t.Error("a record inside the horizon was deleted")
		}
	})

	t.Run("a session's age is its last turn, not its first", func(t *testing.T) {
		store := &Store{Dir: t.TempDir()}
		path := agedRecord(t, store, "long0001", now.Add(-400*day), now.Add(-day))
		if _, err := store.Prune(now, horizon, false); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Error("a session that ran yesterday was deleted for when it started")
		}
	})

	t.Run("an explicit zero keeps every record", func(t *testing.T) {
		store := &Store{Dir: t.TempDir()}
		s, e := quiet(10 * 365 * day)
		path := agedRecord(t, store, "old00001", s, e)
		res, err := store.Prune(now, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Removed) != 0 {
			t.Errorf("retention off must mean off: %v", res.Removed)
		}
		if _, err := os.Stat(path); err != nil {
			t.Error("deleted with retention off")
		}
	})

	t.Run("a dry run reports without deleting, and names what it pruned", func(t *testing.T) {
		store := &Store{Dir: t.TempDir()}
		s, e := quiet(horizon + day)
		path := agedRecord(t, store, "old00001", s, e)
		res, _ := store.Prune(now, horizon, true)
		if len(res.Removed) != 1 {
			t.Fatalf("a dry run must still say what would go: %v", res.Removed)
		}
		if _, err := os.Stat(path); err != nil {
			t.Error("the dry run deleted the file it was only asked about")
		}
		if got := res.Summary(); !strings.Contains(got, "would remove 1 usage record") || !strings.Contains(got, "180d") {
			t.Errorf("summary = %q, want what would go, as usage records, under which rule", got)
		}
	})

	t.Run("a record that does not parse is judged by its file's age", func(t *testing.T) {
		store := &Store{Dir: t.TempDir()}
		stale := filepath.Join(store.Dir, "brok0001.json")
		fresh := filepath.Join(store.Dir, "brok0002.json")
		for _, p := range []string{stale, fresh} {
			os.WriteFile(p, []byte(`{"version":`), 0o600)
		}
		aged := now.Add(-horizon - day)
		os.Chtimes(stale, aged, aged)
		os.Chtimes(fresh, now, now)

		res, err := store.Prune(now, horizon, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Removed) != 1 || res.Removed[0] != "brok0001" {
			t.Errorf("Removed = %v, want the stale unreadable record only", res.Removed)
		}
		if _, err := os.Stat(fresh); err != nil {
			t.Error("a record being written was collected mid-write")
		}
	})

	t.Run("leaves a file it does not own alone", func(t *testing.T) {
		store := &Store{Dir: t.TempDir()}
		stray := filepath.Join(store.Dir, "README.txt")
		os.WriteFile(stray, []byte("mine"), 0o600)
		aged := now.Add(-10 * 365 * day)
		os.Chtimes(stray, aged, aged)
		if _, err := store.Prune(now, horizon, false); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(stray); err != nil {
			t.Error("prune deleted a file that was never its to manage")
		}
	})
}

func TestHealth(t *testing.T) {
	t.Run("lists failing hooks with their counts, the hooks installed per root, always-loaded instruction files and MCP servers", func(t *testing.T) {
		root := fullRoot(t)
		failing := line(t, map[string]any{"type": "attachment", "timestamp": "2026-10-15T10:00:00Z", "attachment": map[string]any{
			"type": "hook_non_blocking_error", "hookName": "SessionStart:startup", "command": "run-hook.cmd session-start"}})
		f := summarise(t, assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5), failing, failing)
		r := Build([]*Record{sessionRecord(root, f)}, []Inventory{Scan(root, nil)}, now, 30, warsaw)

		h := HealthOf(r)
		if len(h.HookErrors) != 1 || h.HookErrors[0].Name != "SessionStart:startup :: run-hook.cmd session-start" || h.HookErrors[0].Count != 2 {
			t.Errorf("hook errors = %+v, want the one hook failing twice", h.HookErrors)
		}
		if len(h.Roots) != 1 || h.Roots[0].Root != root {
			t.Fatalf("roots = %+v, want the one scanned", h.Roots)
		}
		rh := h.Roots[0]
		if len(rh.Hooks) != 1 || rh.Hooks[0].Command != "notify" {
			t.Errorf("hooks = %+v, want the Stop hook from settings.json", rh.Hooks)
		}
		if len(rh.Context) == 0 || !strings.HasSuffix(rh.Context[0].Path, "CLAUDE.md") {
			t.Errorf("context = %+v, want the global CLAUDE.md", rh.Context)
		}
		names := map[string]bool{}
		for _, m := range rh.MCP {
			names[m.Name] = true
		}
		if !names["github"] || !names["slack"] {
			t.Errorf("mcp = %+v, want the plugin's and the user's servers", rh.MCP)
		}
	})
}

func TestReportTabs(t *testing.T) {
	t.Run("carries cost per model for every day in the range, days without work included, and each day's events", func(t *testing.T) {
		f := summarise(t,
			assistant(t, "2026-10-14T10:00:00Z", "msg_1", "claude-opus-5-5", 5),
			prompt(t, "2026-10-16T09:00:00Z", "ship it"),
			assistant(t, "2026-10-16T09:00:01Z", "msg_2", "claude-sonnet-5", 5))
		r := Build([]*Record{sessionRecord("", f)}, nil, now, 3, warsaw)
		if len(r.Daily) != 3 || r.Daily[0].Day != "2026-10-14" || r.Daily[1].Day != "2026-10-15" || r.Daily[2].Day != "2026-10-16" {
			t.Fatalf("daily = %+v, want the three days of the range in order", r.Daily)
		}
		if r.Daily[0].Cost["opus-5-5"] <= 0 || len(r.Daily[1].Cost) != 0 || r.Daily[2].Cost["sonnet-5"] <= 0 {
			t.Errorf("daily cost = %+v / %+v / %+v, want opus on the first day, nothing on the second, sonnet on the third",
				r.Daily[0].Cost, r.Daily[1].Cost, r.Daily[2].Cost)
		}
		if r.Daily[2].Events[EventPrompts] != 1 || r.Daily[0].Events[EventPrompts] != 0 {
			t.Errorf("daily events = %+v / %+v, want the one prompt on its day", r.Daily[0].Events, r.Daily[2].Events)
		}
	})

	t.Run("carries prompts by weekday and hour, in local time", func(t *testing.T) {
		// 22:30 UTC on Thursday is 00:30 on Friday in Warsaw.
		f := summarise(t,
			prompt(t, "2026-10-15T22:30:00Z", "late one"),
			assistant(t, "2026-10-15T22:30:01Z", "msg_1", "claude-opus-5-5", 5))
		r := Build([]*Record{sessionRecord("", f)}, nil, now, 30, warsaw)
		total := 0
		for _, hours := range r.PromptHours {
			for _, n := range hours {
				total += n
			}
		}
		if r.PromptHours[4][0] != 1 || total != 1 {
			t.Errorf("prompt hours = %v, want one prompt on Friday (row 4) at hour 0", r.PromptHours)
		}
	})

	t.Run("carries the largest tool results in the range, biggest first", func(t *testing.T) {
		f := summarise(t,
			assistant(t, "2026-10-15T10:00:00Z", "msg_1", "claude-opus-5-5", 5,
				toolUse("tu1", "Read", map[string]any{"file_path": "/a.md"}),
				toolUse("tu2", "Bash", map[string]any{"command": "cat big.log"})),
			toolResult(t, "2026-10-15T10:00:01Z", "tu1", strings.Repeat("a", 40000), false),
			toolResult(t, "2026-10-15T10:00:02Z", "tu2", strings.Repeat("b", 60000), false))
		r := Build([]*Record{sessionRecord("", f)}, nil, now, 30, warsaw)
		if len(r.BigResults) != 2 || r.BigResults[0].Tool != "Bash" || r.BigResults[0].Bytes <= r.BigResults[1].Bytes {
			t.Errorf("big results = %+v, want both, the Bash dump first", r.BigResults)
		}
	})

	t.Run("carries each installed item's and MCP server's uses in the last 90 days, beside the item", func(t *testing.T) {
		root := fullRoot(t)
		f := summarise(t,
			prompt(t, "2026-10-15T10:00:00Z", "<command-name>/worktree</command-name>"),
			assistant(t, "2026-10-15T10:00:01Z", "msg_1", "claude-opus-5-5", 5,
				toolUse("tu1", "mcp__github__search", map[string]any{"q": "x"})))
		r := Build([]*Record{sessionRecord(root, f)}, []Inventory{Scan(root, nil)}, now, 30, warsaw)
		uses := map[string]int{}
		for _, inv := range r.Inventory {
			for _, it := range inv.Items {
				uses[it.Kind+":"+it.Name] = it.Uses90
			}
			for _, m := range inv.MCP {
				uses["mcp:"+m.Name] = m.Uses90
			}
		}
		if uses["skill:worktree"] != 1 || uses["skill:unused-skill"] != 0 || uses["mcp:github"] != 1 || uses["mcp:slack"] != 0 {
			t.Errorf("90-day uses = %v, want worktree and github once, the rest never", uses)
		}
	})

	t.Run("carries how often each skill, command, agent and MCP server ran in the range", func(t *testing.T) {
		root := skillRoot(t)
		f := summarise(t,
			prompt(t, "2026-09-01T10:00:00Z", "<command-name>/worktree</command-name>"),
			prompt(t, "2026-10-15T10:00:00Z", "<command-name>/worktree</command-name>"),
			prompt(t, "2026-10-15T11:00:00Z", "<command-name>/worktree</command-name>"),
			assistant(t, "2026-10-15T11:00:01Z", "msg_1", "claude-opus-5-5", 5,
				toolUse("tu1", "Task", map[string]any{"subagent_type": "Explore", "prompt": "find"})))
		r := Build([]*Record{sessionRecord(root, f)}, nil, now, 30, warsaw)
		got := map[string]int{}
		for _, u := range r.Uses {
			got[u.Kind+":"+u.Name] = u.Count
			if u.Root != root {
				t.Errorf("use %+v lost its root", u)
			}
		}
		if got["command:worktree"] != 2 || got["agent:Explore"] != 1 {
			t.Errorf("uses = %v, want the command twice in the range — not the third, 45 days back — and the agent once", got)
		}
		if len(r.Uses) > 1 && r.Uses[0].Count < r.Uses[1].Count {
			t.Errorf("uses = %+v, want most first", r.Uses)
		}
	})
}

func TestBashUses(t *testing.T) {
	t.Run("a Bash use is named by the program it runs, past assignments, a leading cd and quoted paths", func(t *testing.T) {
		for cmd, want := range map[string]string{
			`S=/tmp/x/scratchpad; mkdir -p "$S"`:                         "mkdir",
			`~/SecondBrain/"4. Extras/Apps/scripts/obs.sh" check "x.md"`: "obs.sh",
			`cd "/Users/s/6. Spaces/x" && git status`:                    "git",
			`cd /tmp; ls`: "ls",
			`"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" -h`: "Google Chrome",
			`FOO=1 BAR="a b" go test ./...`:                                     "go",
			`echo hi | head -1`:                                                 "echo",
			"\n  git log --oneline\n":                                           "git",
		} {
			if got := firstCommand(cmd); got != want {
				t.Errorf("firstCommand(%q) = %q, want %q", cmd, got, want)
			}
		}
	})
}
