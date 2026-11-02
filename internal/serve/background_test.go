//go:build unix

package serve

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/swilgosz/mindskein/internal/proc"
)

// fakeServer writes an executable that behaves as a started server does from
// the outside: it reports an address on the ready pipe and keeps running.
func fakeServer(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-mindskein")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStartRecordsTheServerAndStopRemovesIt(t *testing.T) {
	home := t.TempDir()
	binary := fakeServer(t, `echo 127.0.0.1:4321 >&3; exec sleep 30`)
	t.Cleanup(func() { Stop(home) })

	b, err := Start(home, binary, []string{"--port=4321"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if b.Port != 4321 || b.URL() != "http://127.0.0.1:4321" || len(b.Args) != 1 {
		t.Errorf("started = %+v, want port 4321 and the args it was given", b)
	}
	running, err := Running(home)
	if err != nil || running == nil || running.PID != b.PID {
		t.Fatalf("Running = %+v, %v; want pid %d", running, err, b.PID)
	}
	again, err := Start(home, binary, nil)
	if !errors.Is(err, ErrRunning) || again.PID != b.PID {
		t.Errorf("second Start = %+v, %v; want ErrRunning with pid %d", again, err, b.PID)
	}

	stopped, err := Stop(home)
	if err != nil || stopped == nil || stopped.PID != b.PID {
		t.Fatalf("Stop = %+v, %v", stopped, err)
	}
	if running, _ := Running(home); running != nil {
		t.Errorf("Running after Stop = %+v", running)
	}
}

func TestStopKillsAServerThatIgnoresSIGTERM(t *testing.T) {
	home := t.TempDir()
	stubborn := exec.Command("sh", "-c", `trap "" TERM; exec sleep 30`)
	if err := stubborn.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { stubborn.Wait(); close(exited) }()
	t.Cleanup(func() { stubborn.Process.Kill(); <-exited })
	time.Sleep(100 * time.Millisecond)

	started, err := proc.StartTime(stubborn.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeRecord(home, &Background{PID: stubborn.Process.Pid, PIDStartedAt: started, Port: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := stop(home, 200*time.Millisecond); err != nil {
		t.Fatalf("stop: %v", err)
	}
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("a server ignoring SIGTERM was not killed")
	}
	if _, err := os.Stat(filepath.Join(home, RecordFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("serve.json left after the kill (%v)", err)
	}
}

func TestStartSaysWhyAServerThatExitedAtOnceFailed(t *testing.T) {
	home := t.TempDir()
	binary := fakeServer(t, `echo "mindskein: address already in use" >&2; exit 1`)
	if _, err := Start(home, binary, nil); err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Errorf("Start = %v, want the server's own last words", err)
	}
	if _, err := os.Stat(filepath.Join(home, RecordFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("serve.json written for a server that never listened (%v)", err)
	}
}

func TestAnUnreadableRecordReadsAsNoneRunning(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, RecordFile), []byte("{not json"), 0o600)
	if b, err := Running(home); b != nil || err != nil {
		t.Errorf("Running = %+v, %v; want none", b, err)
	}
	if b, err := Stop(home); b != nil || err != nil {
		t.Errorf("Stop = %+v, %v; want none", b, err)
	}
	if _, err := os.Stat(filepath.Join(home, RecordFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unreadable serve.json left behind (%v)", err)
	}
}

func TestNotifyReadyDoesNothingInTheForeground(t *testing.T) {
	t.Setenv(readyEnv, "")
	NotifyReady("127.0.0.1:1")
}
