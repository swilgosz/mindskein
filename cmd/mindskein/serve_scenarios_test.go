package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/swilgosz/mindskein/internal/config"
)

func apiGet(t *testing.T, h http.Handler, path string, v any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s = %d: %s", path, rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, rec.Body)
	}
}

func TestServeCommand(t *testing.T) {
	t.Run("serve with no password says on start that anything reaching the port sees titles, prompts and tool inputs", func(t *testing.T) {
		var open, locked bytes.Buffer
		announce(&open, "127.0.0.1:8787", false)
		announce(&locked, "127.0.0.1:8787", true)
		if !strings.Contains(open.String(), "titles, prompts and tool inputs") || !strings.Contains(open.String(), "[serve]") {
			t.Errorf("no-password banner = %q, want the exposure named and where to set a password", open.String())
		}
		if strings.Contains(locked.String(), "titles, prompts and tool inputs") || !strings.Contains(locked.String(), "http://127.0.0.1:8787") {
			t.Errorf("password banner = %q, want the address and no warning", locked.String())
		}
	})

	t.Run("the password in config.toml guards the server", func(t *testing.T) {
		home := pruneHome(t)
		os.WriteFile(filepath.Join(home, "config.toml"), []byte("[serve]\nuser = \"admin\"\npassword = \"hakunamatata\"\n"), 0o600)
		cfg, err := loadConfig()
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		serveHandler(cfg).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/brief", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("/api/brief without the configured password = %d, want 401", rec.Code)
		}
	})

	t.Run("/api/usage returns what usage --json prints for the same days and root", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("MINDSKEIN_HOME", home)
		// Two accounts, and a session older than the default 30 days, so an
		// endpoint ignoring either parameter answers differently.
		when := time.Now().UTC().Add(-60 * 24 * time.Hour).Format(time.RFC3339)
		var roots []string
		for _, id := range []string{"sess0001", "sess0002"} {
			dir := filepath.Join(t.TempDir(), "projects", "-tmp")
			os.MkdirAll(dir, 0o700)
			transcript := filepath.Join(dir, id+".jsonl")
			os.WriteFile(transcript, []byte(`{"type":"assistant","timestamp":"`+when+`","sessionId":"`+id+`",`+
				`"message":{"id":"msg_1","model":"claude-opus-5-5","content":[],"usage":{"input_tokens":1,"output_tokens":2}}}`+"\n"), 0o600)
			if err := run([]string{"hook", "stop"}, strings.NewReader(stopPayload(t, id, transcript)), io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			roots = append(roots, filepath.Dir(filepath.Dir(dir)))
		}

		var cli bytes.Buffer
		if err := run([]string{"usage", "--json", "--days", "0", "--root", roots[0]}, nil, &cli, io.Discard); err != nil {
			t.Fatal(err)
		}
		var want, got map[string]any
		json.Unmarshal(cli.Bytes(), &want)
		apiGet(t, serveHandler(defaultConfig(t)), "/api/usage?days=0&root="+roots[0], &got)
		delete(want, "generated_at")
		delete(got, "generated_at")
		if want["sessions"] != float64(1) || !reflect.DeepEqual(want, got) {
			t.Errorf("/api/usage differs from usage --json:\napi: %v\ncli: %v", got, want)
		}
	})

	t.Run("/api/setup says, per config root, whether mindskein's hooks are registered and current, and leaves settings.json untouched", func(t *testing.T) {
		pruneHome(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		registered := filepath.Join(home, ".claude", "settings.json")
		if err := run([]string{"init", "--settings", registered}, nil, io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(home, ".claude-personal")
		os.MkdirAll(other, 0o700)
		bare := filepath.Join(other, "settings.json")
		os.WriteFile(bare, []byte("{}\n"), 0o600)
		t.Setenv("CLAUDE_CONFIG_DIR", other)
		before := map[string][]byte{}
		for _, p := range []string{registered, bare} {
			before[p], _ = os.ReadFile(p)
		}

		var s struct {
			Hooks []struct {
				Root    string
				Current bool
				Changes []string
			}
		}
		apiGet(t, serveHandler(defaultConfig(t)), "/api/setup", &s)
		state := map[string]bool{}
		changes := map[string]int{}
		for _, h := range s.Hooks {
			state[h.Root], changes[h.Root] = h.Current, len(h.Changes)
		}
		if !state[filepath.Join(home, ".claude")] || state[other] || changes[other] == 0 {
			t.Errorf("hooks = %+v, want ~/.claude current and %s not, with what init would change", s.Hooks, other)
		}
		for p, b := range before {
			if after, _ := os.ReadFile(p); !bytes.Equal(after, b) {
				t.Errorf("%s changed: the page only reads", p)
			}
			if backups, _ := filepath.Glob(p + ".bak-*"); len(backups) > 0 {
				t.Errorf("%s was backed up, so something meant to write it", p)
			}
		}
	})

	t.Run("/api/setup lists the newest failures in hooks.log, newest first", func(t *testing.T) {
		home := pruneHome(t)
		log := "2026-10-14T08:00:00Z\tstop\tfirst\n2026-10-15T08:00:00Z\tpre-tool-use\tsecond\n2026-10-16T08:00:00Z\tstop\tthird\n"
		os.WriteFile(filepath.Join(home, "hooks.log"), []byte(log), 0o600)
		var s struct {
			Failures []struct{ Time, Event, Error string }
		}
		apiGet(t, serveHandler(defaultConfig(t)), "/api/setup", &s)
		if len(s.Failures) != 3 || s.Failures[0].Error != "third" || s.Failures[2].Error != "first" || s.Failures[1].Event != "pre-tool-use" {
			t.Errorf("failures = %+v, want all three, newest first", s.Failures)
		}
	})

	t.Run("/api/health answers with the health of each config root", func(t *testing.T) {
		pruneHome(t)
		var h struct {
			HookErrors []any `json:"hook_errors"`
			Roots      []any
		}
		apiGet(t, serveHandler(defaultConfig(t)), "/api/health?days=30", &h)
	})
}

func defaultConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
