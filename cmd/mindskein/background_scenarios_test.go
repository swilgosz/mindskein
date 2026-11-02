//go:build unix

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/swilgosz/mindskein/internal/proc"
)

func pending(t *testing.T, behaviour string) {
	t.Helper()
	t.Fatalf("PENDING: %s", behaviour)
}

// servedRecord is serve.json as a reader outside this package sees it.
type servedRecord struct {
	PID          int       `json:"pid"`
	PIDStartedAt time.Time `json:"pid_started_at"`
	Port         int       `json:"port"`
	Args         []string  `json:"args"`
	StartedAt    time.Time `json:"started_at"`
}

// backgroundHome gives a scenario its own state directory, makes the test
// binary act as mindskein when serve --background re-executes it, and stops
// whatever server the scenario left recorded.
func backgroundHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("MINDSKEIN_HOME", home)
	t.Setenv(cliChild, "1")
	t.Cleanup(func() { run([]string{"serve", "--stop"}, nil, io.Discard, io.Discard) })
	return home
}

func launchBackground(t *testing.T, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	if err := run(append([]string{"serve", "--background"}, args...), nil, &out, &errOut); err != nil {
		t.Fatalf("serve --background: %v\n%s%s", err, out.String(), errOut.String())
	}
	return out.String()
}

func readServed(t *testing.T, home string) servedRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "serve.json"))
	if err != nil {
		t.Fatalf("reading serve.json: %v", err)
	}
	var rec servedRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("serve.json: %v\n%s", err, data)
	}
	return rec
}

func TestServeInTheBackground(t *testing.T) {
	t.Run("serve --background returns at once, the server answers on its port, and serve.json records its pid, start time and arguments", func(t *testing.T) {
		home := backgroundHome(t)
		begin := time.Now()
		out := launchBackground(t, "--port", "0")
		if took := time.Since(begin); took > 5*time.Second {
			t.Errorf("serve --background took %s, want it to return once the server listens", took)
		}

		rec := readServed(t, home)
		url := fmt.Sprintf("http://127.0.0.1:%d", rec.Port)
		resp, err := http.Get(url + "/")
		if err != nil {
			t.Fatalf("the background server does not answer on %s: %v", url, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s/ = %d, want 200", url, resp.StatusCode)
		}

		if rec.PID <= 0 || rec.PID == os.Getpid() {
			t.Fatalf("recorded pid = %d, want the detached server's", rec.PID)
		}
		started, err := proc.StartTime(rec.PID)
		if err != nil || started.Unix() != rec.PIDStartedAt.Unix() {
			t.Errorf("recorded start time = %s, the process says %s (%v)", rec.PIDStartedAt, started, err)
		}
		if !reflect.DeepEqual(rec.Args, []string{"--port=0"}) {
			t.Errorf("recorded args = %q, want the flags it was started with, --background left out", rec.Args)
		}
		if time.Since(rec.StartedAt) > time.Minute {
			t.Errorf("recorded started_at = %s, want now", rec.StartedAt)
		}
		if pgid, err := syscall.Getpgid(rec.PID); err != nil || pgid != rec.PID {
			t.Errorf("process group = %d (%v), want its own, %d", pgid, err, rec.PID)
		}
		if !strings.Contains(out, url) || !strings.Contains(out, strconv.Itoa(rec.PID)) {
			t.Errorf("output = %q, want the URL %s and the pid %d", out, url, rec.PID)
		}
		if log, _ := os.ReadFile(filepath.Join(home, "serve.log")); !strings.Contains(string(log), url) {
			t.Errorf("serve.log = %q, want the server's own output", log)
		}
	})

	t.Run("serve --background while one is running says so with its URL and starts nothing", func(t *testing.T) {
		home := backgroundHome(t)
		launchBackground(t, "--port", "0")
		before, _ := os.ReadFile(filepath.Join(home, "serve.json"))
		rec := readServed(t, home)

		out := launchBackground(t, "--port", "0")
		url := fmt.Sprintf("http://127.0.0.1:%d", rec.Port)
		if !strings.Contains(out, "already running") || !strings.Contains(out, url) {
			t.Errorf("output = %q, want it to say one is already running on %s", out, url)
		}
		if after, _ := os.ReadFile(filepath.Join(home, "serve.json")); !bytes.Equal(before, after) {
			t.Errorf("serve.json changed, so a second server was started:\nbefore %s\nafter  %s", before, after)
		}
	})

	t.Run("serve --stop stops the recorded server and removes serve.json; with none running it says so and exits 0", func(t *testing.T) {
		home := backgroundHome(t)
		launchBackground(t, "--port", "0")
		rec := readServed(t, home)

		var out bytes.Buffer
		if err := run([]string{"serve", "--stop"}, nil, &out, io.Discard); err != nil {
			t.Fatalf("serve --stop: %v", err)
		}
		if !strings.Contains(out.String(), "stopped") || !strings.Contains(out.String(), strconv.Itoa(rec.PID)) {
			t.Errorf("output = %q, want it to say it stopped pid %d", out.String(), rec.PID)
		}
		if _, err := proc.StartTime(rec.PID); !errors.Is(err, proc.ErrNotRunning) {
			t.Errorf("pid %d still running after serve --stop (%v)", rec.PID, err)
		}
		if _, err := os.Stat(filepath.Join(home, "serve.json")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("serve.json still there after serve --stop (%v)", err)
		}

		out.Reset()
		if err := run([]string{"serve", "--stop"}, nil, &out, io.Discard); err != nil {
			t.Fatalf("serve --stop with none running = %v, want exit 0", err)
		}
		if !strings.Contains(out.String(), "no background server") {
			t.Errorf("output = %q, want it to say none is running", out.String())
		}

		dead := exec.Command("sleep", "0")
		if err := dead.Run(); err != nil {
			t.Fatal(err)
		}
		stale := fmt.Sprintf(`{"pid":%d,"pid_started_at":%q,"port":8787,"args":[],"started_at":%q}`,
			dead.Process.Pid, time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
		os.WriteFile(filepath.Join(home, "serve.json"), []byte(stale), 0o600)
		out.Reset()
		if err := run([]string{"serve", "--stop"}, nil, &out, io.Discard); err != nil {
			t.Fatalf("serve --stop with a stale serve.json = %v, want exit 0", err)
		}
		if !strings.Contains(out.String(), "no background server") {
			t.Errorf("output = %q, want a stale record read as none running", out.String())
		}
		if _, err := os.Stat(filepath.Join(home, "serve.json")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("stale serve.json left behind (%v)", err)
		}
	})

	t.Run("serve --stop does not signal a process whose start time differs from the recorded one", func(t *testing.T) {
		home := backgroundHome(t)
		stranger := exec.Command("sleep", "30")
		if err := stranger.Start(); err != nil {
			t.Fatal(err)
		}
		exited := make(chan struct{})
		go func() { stranger.Wait(); close(exited) }()
		t.Cleanup(func() { stranger.Process.Kill(); <-exited })

		started, err := proc.StartTime(stranger.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		reused := fmt.Sprintf(`{"pid":%d,"pid_started_at":%q,"port":8787,"args":[],"started_at":%q}`,
			stranger.Process.Pid, started.Add(-time.Hour).UTC().Format(time.RFC3339), started.Add(-time.Hour).UTC().Format(time.RFC3339))
		os.WriteFile(filepath.Join(home, "serve.json"), []byte(reused), 0o600)

		var out bytes.Buffer
		if err := run([]string{"serve", "--stop"}, nil, &out, io.Discard); err != nil {
			t.Fatalf("serve --stop: %v", err)
		}
		select {
		case <-exited:
			t.Fatalf("serve --stop signalled pid %d, which only reuses the recorded pid: %s", stranger.Process.Pid, stranger.ProcessState)
		case <-time.After(500 * time.Millisecond):
		}
		if !strings.Contains(out.String(), "no background server") {
			t.Errorf("output = %q, want a reused pid read as none running", out.String())
		}
	})

	t.Run("serve --background together with --stop is a usage error", func(t *testing.T) {
		home := backgroundHome(t)
		if err := run([]string{"serve", "--background", "--stop"}, nil, io.Discard, io.Discard); err == nil {
			t.Error("serve --background --stop succeeded, want a usage error")
		}
		if _, err := os.Stat(filepath.Join(home, "serve.json")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("serve --background --stop started a server (%v)", err)
		}
	})
}
