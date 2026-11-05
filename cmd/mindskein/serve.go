package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/swilgosz/mindskein/internal/config"
	"github.com/swilgosz/mindskein/internal/handoff"
	"github.com/swilgosz/mindskein/internal/install"
	"github.com/swilgosz/mindskein/internal/serve"
	"github.com/swilgosz/mindskein/internal/session"
	insight "github.com/swilgosz/mindskein/internal/usage"
)

// cmdServe starts the dashboard. A config that does not parse stops it: the
// password lives there, and serving on defaults would serve without one.
func cmdServe(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	port := fs.Int("port", 8787, "port to listen on, on 127.0.0.1")
	background := fs.Bool("background", false, "start detached, logging to "+serve.LogFile+" in ~/.mindskein, and return")
	stop := fs.Bool("stop", false, "stop the server started with --background")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *background && *stop {
		return errors.New("--background and --stop cannot be used together")
	}
	if *stop {
		return stopBackground(stdout)
	}
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("%w — fix it before serving, the password is read from it", err)
	}
	if *background {
		return startBackground(stdout, cfg, passThrough(fs, "background"))
	}
	ln, err := serve.Listen(*port)
	if err != nil {
		return err
	}
	announce(stdout, ln.Addr().String(), cfg.Serve.Password != "")
	serve.NotifyReady(ln.Addr().String())
	srv := &http.Server{Handler: serveHandler(cfg), ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(ln)
}

// passThrough is every flag set on the command line except skip, as
// --name=value, so a detached server runs with what the user asked for.
func passThrough(fs *flag.FlagSet, skip string) []string {
	args := []string{}
	fs.Visit(func(f *flag.Flag) {
		if f.Name != skip {
			args = append(args, "--"+f.Name+"="+f.Value.String())
		}
	})
	return args
}

func startBackground(stdout io.Writer, cfg config.Config, args []string) error {
	home, err := session.Home()
	if err != nil {
		return err
	}
	binary, err := binaryPath()
	if err != nil {
		return err
	}
	b, err := serve.Start(home, binary, args)
	if errors.Is(err, serve.ErrRunning) {
		fmt.Fprintf(stdout, "a background server is already running on %s (pid %d)\n", b.URL(), b.PID)
		return nil
	}
	if err != nil {
		return err
	}
	announce(stdout, strings.TrimPrefix(b.URL(), "http://"), cfg.Serve.Password != "")
	fmt.Fprintf(stdout, "running in the background as pid %d, logging to %s\nstop it with: mindskein serve --stop\n",
		b.PID, filepath.Join(home, serve.LogFile))
	return nil
}

func stopBackground(stdout io.Writer) error {
	home, err := session.Home()
	if err != nil {
		return err
	}
	b, err := serve.Stop(home)
	if err != nil {
		return err
	}
	if b == nil {
		fmt.Fprintln(stdout, "no background server is running")
		return nil
	}
	fmt.Fprintf(stdout, "stopped the background server on %s (pid %d)\n", b.URL(), b.PID)
	return nil
}

// announce says where the dashboard is, and what it exposes when nothing
// guards it.
func announce(w io.Writer, addr string, passwordSet bool) {
	fmt.Fprintf(w, "mindskein dashboard on http://%s\n", addr)
	if !passwordSet {
		fmt.Fprintf(w, "no password set: anything that reaches this port sees your session titles, prompts and tool inputs.\n"+
			"Set user and password under [serve] in %s before tunnelling it anywhere.\n", configFile())
	}
}

func serveHandler(cfg config.Config) http.Handler {
	return serve.Handler(serve.Options{
		User:     cfg.Serve.User,
		Password: cfg.Serve.Password,
		Sources: map[string]serve.Source{
			"usage": func(r *http.Request) (any, error) {
				store, err := insight.DefaultStore()
				if err != nil {
					return nil, err
				}
				window, err := queryWindow(r)
				if err != nil {
					return nil, err
				}
				resolved, err := loadResolved()
				if err != nil {
					return nil, err
				}
				return buildUsage(store, time.Now(), window, expandRoot(r.URL.Query().Get("root")), resolved)
			},
			"brief": func(r *http.Request) (any, error) {
				all := r.URL.Query().Get("all") == "1"
				return buildLocalBrief(cfg, all, cfg.Status.HideAfter.Duration(), time.Now().UTC()), nil
			},
			"reviews": func(r *http.Request) (any, error) {
				now := time.Now().UTC()
				if r.URL.Query().Get("cached") == "1" {
					return buildCachedReviews(cfg, now, os.Stderr), nil
				}
				return buildReviews(cfg, now, os.Stderr), nil
			},
			"setup": func(*http.Request) (any, error) {
				return buildSetup()
			},
			"health": func(r *http.Request) (any, error) {
				store, err := insight.DefaultStore()
				if err != nil {
					return nil, err
				}
				report, err := buildUsage(store, time.Now(), insight.Window{Days: queryDays(r)}, "", nil)
				if err != nil {
					return nil, err
				}
				return insight.HealthOf(report), nil
			},
		},
	})
}

// queryDays reads ?days=, defaulting to the same 30 as the command.
func queryDays(r *http.Request) int {
	if n, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && n >= 0 {
		return n
	}
	return 30
}

// queryWindow reads ?days=, ?since= and ?until=, checked as the command
// checks its flags.
func queryWindow(r *http.Request) (insight.Window, error) {
	q := r.URL.Query()
	return insight.ParseWindow(queryDays(r), q.Get("since"), q.Get("until"))
}

type setupReport struct {
	Version   string        `json:"version"`
	Binary    string        `json:"binary"`
	Config    setupConfig   `json:"config"`
	Hooks     []hookState   `json:"hooks"`
	Stores    []storeState  `json:"stores"`
	LastPrune string        `json:"last_prune"`
	Failures  []hookFailure `json:"failures"`
}

type setupConfig struct {
	Path           string            `json:"path"`
	Exists         bool              `json:"exists"`
	Error          string            `json:"error,omitempty"`
	PrioritiesFile string            `json:"priorities_file"`
	HideAfter      string            `json:"hide_after"`
	Retention      map[string]string `json:"retention"`
	PasswordSet    bool              `json:"password_set"`
}

// hookState is whether one config root's settings.json holds mindskein's hooks
// as init would write them now.
type hookState struct {
	Root     string   `json:"root"`
	Settings string   `json:"settings"`
	Current  bool     `json:"current"`
	Changes  []string `json:"changes"`
	Error    string   `json:"error,omitempty"`
}

type storeState struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Records   int    `json:"records"`
	Retention string `json:"retention"`
}

type hookFailure struct {
	Time  string `json:"time"`
	Event string `json:"event"`
	Error string `json:"error"`
}

// maxFailures caps the hooks.log tail; the file is append-only and never
// rotated, so reading it whole is fine but listing it whole is not.
const maxFailures = 50

// buildSetup reports the install as it stands. It only reads: the hook check
// is init's own dry run, which neither writes settings.json nor backs it up.
func buildSetup() (*setupReport, error) {
	cfg, cfgErr := loadConfig()
	rep := &setupReport{Version: buildVersion()}
	rep.Config = setupConfig{
		Path:           configFile(),
		PrioritiesFile: cfg.Priorities.Path(),
		HideAfter:      cfg.Status.HideAfter.String(),
		Retention: map[string]string{
			"sessions": cfg.Retention.Sessions.String(),
			"handoffs": cfg.Retention.Handoffs.String(),
			"usage":    cfg.Retention.Usage.String(),
		},
		PasswordSet: cfg.Serve.Password != "",
	}
	if _, err := os.Stat(configFile()); err == nil {
		rep.Config.Exists = true
	}
	if cfgErr != nil {
		rep.Config.Error = cfgErr.Error()
	}

	binary, err := binaryPath()
	if err != nil {
		return nil, err
	}
	rep.Binary = binary
	usage, err := insight.DefaultStore()
	if err != nil {
		return nil, err
	}
	for _, root := range usage.Roots() {
		h := hookState{Root: root, Settings: filepath.Join(root, "settings.json"), Changes: []string{}}
		r, err := install.Register(h.Settings, install.Options{Binary: binary, Timeout: defaultTimeout, Async: true}, true)
		if err != nil {
			h.Error = err.Error()
		} else {
			h.Changes = append(h.Changes, r.Changes...)
			h.Current = len(r.Changes) == 0
		}
		rep.Hooks = append(rep.Hooks, h)
	}

	sessions, err := session.DefaultStore()
	if err != nil {
		return nil, err
	}
	handoffs, err := handoff.DefaultStore()
	if err != nil {
		return nil, err
	}
	rep.Stores = []storeState{
		{Name: "sessions", Path: sessions.Dir, Records: countFiles(sessions.Dir, "*.json"), Retention: cfg.Retention.Sessions.String()},
		{Name: "handoffs", Path: handoffs.Dir, Records: countFiles(handoffs.Dir, "*.md"), Retention: cfg.Retention.Handoffs.String()},
		{Name: "usage", Path: usage.Dir, Records: countFiles(usage.Dir, "*.json"), Retention: cfg.Retention.Usage.String()},
	}

	home, err := session.Home()
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(filepath.Join(home, pruneStamp)); err == nil {
		rep.LastPrune = info.ModTime().UTC().Format(time.RFC3339)
	}
	rep.Failures = readFailures(filepath.Join(home, "hooks.log"), maxFailures)
	return rep, nil
}

func countFiles(dir, pattern string) int {
	paths, _ := filepath.Glob(filepath.Join(dir, pattern))
	return len(paths)
}

// readFailures returns the last n lines of hooks.log, newest first.
func readFailures(path string, n int) []hookFailure {
	out := []hookFailure{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 3)
		if len(parts) != 3 {
			continue
		}
		out = append(out, hookFailure{Time: parts[0], Event: parts[1], Error: parts[2]})
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out[:min(len(out), n)]
}
