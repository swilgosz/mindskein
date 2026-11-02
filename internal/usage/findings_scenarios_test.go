package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pending(t *testing.T, behaviour string) {
	t.Helper()
	t.Fatalf("PENDING: %s", behaviour)
}

func TestFindingIdentity(t *testing.T) {
	t.Run("every finding in usage --json has an id from the list, and the same id on the next run", func(t *testing.T) {
		pending(t, "every finding in usage --json has an id from the list, and the same id on the next run")
	})

	t.Run("findings that list things carry items keyed by hook, name, subagent row or path, and evidence is unchanged", func(t *testing.T) {
		pending(t, "findings that list things carry items keyed by hook, name, subagent row or path, and evidence is unchanged")
	})

	t.Run("the terminal report shows each finding's id, and --all shows each item's key", func(t *testing.T) {
		pending(t, "the terminal report shows each finding's id, and --all shows each item's key")
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

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}
