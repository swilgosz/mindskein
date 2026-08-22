package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/swilgosz/mindskein/internal/hook"
)

// Options is how mindskein wants itself registered.
type Options struct {
	// Binary is the absolute path written into each command. A hook runs
	// with no PATH guarantee, so a bare name is not enough.
	Binary string

	// Timeout is the per-hook limit in seconds.
	Timeout int

	// Async lets the hook run outside the critical path of the event. Without
	// it every tool call waits on the observer watching it.
	Async bool
}

// registration is one Claude Code event and how mindskein attaches to it.
type registration struct {
	Event   string
	Arg     hook.Event
	Matcher string
}

// registrations is the full set mindskein installs. The matchers are the
// values the handler acts on; without them the hook fires for every subtype
// and is discarded in mindskein rather than never run at all.
var registrations = []registration{
	{"PreToolUse", hook.EventPreToolUse, ""},
	{"Notification", hook.EventNotification, "idle_prompt|permission_prompt|agent_needs_input"},
	{"Stop", hook.EventStop, ""},
	{"SessionEnd", hook.EventSessionEnd, "clear|resume|logout|prompt_input_exit|other"},
}

// Report is what a register or unregister did, or would do for a dry run.
type Report struct {
	Path    string
	Backup  string
	Changes []string
	DryRun  bool
}

func (r *Report) String() string {
	if len(r.Changes) == 0 {
		return fmt.Sprintf("%s: already up to date", r.Path)
	}
	verb := "updated"
	if r.DryRun {
		verb = "would update"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s:\n", verb, r.Path)
	for _, c := range r.Changes {
		fmt.Fprintf(&b, "  %s\n", c)
	}
	if r.Backup != "" {
		fmt.Fprintf(&b, "  backup: %s\n", r.Backup)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Register installs every mindskein hook, updating an existing registration in
// place rather than adding a second one.
func Register(path string, opts Options, dryRun bool) (*Report, error) {
	root, original, err := load(path)
	if err != nil {
		return nil, err
	}
	rep := &Report{Path: path, DryRun: dryRun}

	hooks, err := root.child("hooks")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, reg := range registrations {
		change, err := apply(hooks, reg, opts)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if change != "" {
			rep.Changes = append(rep.Changes, change)
		}
	}
	if err := root.setValue("hooks", hooks); err != nil {
		return nil, err
	}
	return finish(rep, root, original, path, dryRun)
}

// Unregister removes every mindskein hook and nothing else.
func Unregister(path string, dryRun bool) (*Report, error) {
	root, original, err := load(path)
	if err != nil {
		return nil, err
	}
	rep := &Report{Path: path, DryRun: dryRun}

	hooks, err := root.child("hooks")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, reg := range registrations {
		removed, err := remove(hooks, reg)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if removed {
			rep.Changes = append(rep.Changes, reg.Event+": removed")
		}
	}
	// An empty hooks object is the leftover that makes an uninstall look
	// like it half-worked.
	if hooks.len() == 0 {
		root.delete("hooks")
	} else if err := root.setValue("hooks", hooks); err != nil {
		return nil, err
	}
	return finish(rep, root, original, path, dryRun)
}

// load reads and parses the settings file. A missing file is an empty object:
// registering is how it gets created. A malformed one is an error and nothing
// is written — overwriting would destroy a config we could not read.
func load(path string) (*object, []byte, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return newObject(), nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}
	root := newObject()
	if len(strings.TrimSpace(string(data))) == 0 {
		return root, data, nil
	}
	if err := json.Unmarshal(data, root); err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w (nothing was changed)", path, err)
	}
	return root, data, nil
}

// finish writes the file only when something actually changed.
//
// Comparing the rendered output against the original would be wrong: this
// renderer reflows whitespace, so a file that needs no edit at all would come
// back looking different and be rewritten on every run. Nothing changed means
// the file is not opened for writing and no backup is taken.
func finish(rep *Report, root *object, original []byte, path string, dryRun bool) (*Report, error) {
	if len(rep.Changes) == 0 || dryRun {
		return rep, nil
	}
	rendered, err := render(root)
	if err != nil {
		return nil, err
	}
	if len(original) > 0 {
		backup := path + ".bak-" + time.Now().Format("20060102-150405")
		if err := os.WriteFile(backup, original, 0o600); err != nil {
			return nil, fmt.Errorf("backing up %s: %w", path, err)
		}
		rep.Backup = backup
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, rendered, 0o600); err != nil {
		return nil, fmt.Errorf("writing %s: %w", path, err)
	}
	return rep, nil
}

func render(root *object) ([]byte, error) {
	raw, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	if err := indent(&out, raw); err != nil {
		return nil, err
	}
	return []byte(out.String() + "\n"), nil
}
