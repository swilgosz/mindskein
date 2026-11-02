package serve

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/swilgosz/mindskein/internal/proc"
)

// Background is a server running detached, as recorded in serve.json in the
// mindskein home.
type Background struct {
	PID int `json:"pid"`

	// PIDStartedAt is what tells this server apart from whatever process the
	// kernel hands its pid to once it has exited.
	PIDStartedAt time.Time `json:"pid_started_at"`
	Port         int       `json:"port"`

	// Args are the serve flags it was started with, so it can be started
	// again the same way.
	Args      []string  `json:"args"`
	StartedAt time.Time `json:"started_at"`
}

// URL is where the background server answers.
func (b *Background) URL() string {
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(b.Port))
}

const (
	// RecordFile and LogFile are the background server's files in the
	// mindskein home.
	RecordFile = "serve.json"
	LogFile    = "serve.log"

	// StopGrace is how long Stop waits after SIGTERM before it sends SIGKILL.
	StopGrace = 5 * time.Second

	readyTimeout = 10 * time.Second
	pollEvery    = 50 * time.Millisecond

	// readyEnv tells a server started by Start to report its address on the
	// pipe it inherits as file descriptor 3.
	readyEnv = "MINDSKEIN_SERVE_READY"
	readyFD  = 3
)

// ErrRunning is returned by Start when a background server is already
// running; the server it returns alongside is that one.
var ErrRunning = errors.New("a background server is already running")

// Running returns the background server recorded in home, or nil when none is
// running. A record whose pid is gone, or now held by a process that started
// at another time, is stale and reads as none running.
func Running(home string) (*Background, error) {
	b, err := readRecord(home)
	if b == nil || err != nil {
		return nil, err
	}
	alive, err := isAlive(b)
	if err != nil || !alive {
		return nil, err
	}
	return b, nil
}

// Start runs binary's serve command with args, detached: a session and process
// group of its own, stdin from /dev/null, stdout and stderr appended to
// serve.log. It returns once the server listens, with serve.json written.
//
// When a background server is already running it starts nothing and returns
// that one with ErrRunning.
func Start(home, binary string, args []string) (*Background, error) {
	if running, err := Running(home); err != nil {
		return nil, err
	} else if running != nil {
		return running, ErrRunning
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", home, err)
	}
	logPath := filepath.Join(home, LogFile)
	log, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", logPath, err)
	}
	defer log.Close()
	ready, readyW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer ready.Close()

	cmd := exec.Command(binary, append([]string{"serve"}, args...)...)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = append(os.Environ(), readyEnv+"=1")
	cmd.ExtraFiles = []*os.File{readyW}
	if err := detach(cmd); err != nil {
		readyW.Close()
		return nil, err
	}
	err = cmd.Start()
	readyW.Close()
	if err != nil {
		return nil, fmt.Errorf("starting %s: %w", binary, err)
	}
	// Reaps the server should it exit while this process is still running;
	// otherwise it lingers as a zombie that still answers to its pid.
	go cmd.Wait()

	b, err := recordStarted(home, cmd.Process.Pid, ready, args)
	if err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("the background server did not start: %w (log: %s)", err, logPath)
	}
	return b, nil
}

func recordStarted(home string, pid int, ready *os.File, args []string) (*Background, error) {
	addr, err := awaitReady(ready)
	if err != nil {
		if last := lastLine(filepath.Join(home, LogFile)); last != "" {
			return nil, fmt.Errorf("%w: %s", err, last)
		}
		return nil, err
	}
	_, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("unreadable address %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, fmt.Errorf("unreadable address %q: %w", addr, err)
	}
	started, err := proc.StartTime(pid)
	if err != nil {
		return nil, fmt.Errorf("reading the start time of pid %d: %w", pid, err)
	}
	b := &Background{
		PID:          pid,
		PIDStartedAt: started.UTC(),
		Port:         port,
		Args:         append([]string{}, args...),
		StartedAt:    time.Now().UTC(),
	}
	if err := writeRecord(home, b); err != nil {
		return nil, err
	}
	return b, nil
}

func awaitReady(ready *os.File) (string, error) {
	ready.SetReadDeadline(time.Now().Add(readyTimeout))
	line, err := bufio.NewReader(ready).ReadString('\n')
	switch {
	case err == nil:
		return strings.TrimSpace(line), nil
	case errors.Is(err, io.EOF):
		return "", errors.New("it exited before listening")
	case errors.Is(err, os.ErrDeadlineExceeded):
		return "", fmt.Errorf("it was not listening after %s", readyTimeout)
	default:
		return "", err
	}
}

// NotifyReady tells the process that ran Start where this server listens. A
// server started any other way has nobody to tell, and it does nothing.
func NotifyReady(addr string) {
	if os.Getenv(readyEnv) != "1" {
		return
	}
	os.Unsetenv(readyEnv)
	f := os.NewFile(readyFD, "ready")
	if f == nil {
		return
	}
	fmt.Fprintln(f, addr)
	f.Close()
}

// Stop stops the background server recorded in home: SIGTERM, StopGrace for
// it to exit, then SIGKILL. It removes serve.json, stale or not, and returns
// the server it stopped, or nil when none was running.
//
// Only the recorded process is ever signalled: a pid whose start time differs
// from the recorded one belongs to someone else.
func Stop(home string) (*Background, error) { return stop(home, StopGrace) }

func stop(home string, grace time.Duration) (*Background, error) {
	b, err := Running(home)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, removeRecord(home)
	}
	if err := terminate(b.PID); err != nil {
		return nil, fmt.Errorf("stopping pid %d: %w", b.PID, err)
	}
	if !awaitExit(b, grace) {
		if alive, err := isAlive(b); err == nil && alive {
			if err := kill(b.PID); err != nil {
				return nil, fmt.Errorf("killing pid %d: %w", b.PID, err)
			}
		}
		if !awaitExit(b, time.Second) {
			return nil, fmt.Errorf("pid %d is still running after SIGKILL", b.PID)
		}
	}
	return b, removeRecord(home)
}

func awaitExit(b *Background, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if alive, err := isAlive(b); err == nil && !alive {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(pollEvery)
	}
}

func isAlive(b *Background) (bool, error) {
	same, err := proc.SameProcess(b.PID, b.PIDStartedAt)
	if errors.Is(err, proc.ErrNotRunning) {
		return false, nil
	}
	return same, err
}

// readRecord returns nil for a record that cannot name a process, unreadable
// ones included: nothing this package wrote looks like that, so nothing it
// started can be behind it.
func readRecord(home string) (*Background, error) {
	data, err := os.ReadFile(filepath.Join(home, RecordFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var b Background
	if json.Unmarshal(data, &b) != nil || b.PID <= 0 || b.PIDStartedAt.IsZero() {
		return nil, nil
	}
	return &b, nil
}

func writeRecord(home string, b *Background) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(home, ".tmp-serve-*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", home, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmpName, err)
	}
	path := filepath.Join(home, RecordFile)
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmpName, path, err)
	}
	return nil
}

func removeRecord(home string) error {
	if err := os.Remove(filepath.Join(home, RecordFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// lastLine is the last non-empty line of the log, where a server that failed
// to start says why.
func lastLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	const tail = 4096
	if info, err := f.Stat(); err == nil && info.Size() > tail {
		f.Seek(info.Size()-tail, io.SeekStart)
	}
	data, _ := io.ReadAll(f)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
