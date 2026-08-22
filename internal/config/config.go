// Package config loads ~/.mindskein/config.toml.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// DefaultHideAfter is how long a session may go without an event before status
// stops listing it.
//
// Seven days, which has to clear every shorter horizon that could make it lie.
// The longest resumption measured across real transcripts was 48 hours, and a
// status is only marked untrustworthy at 72; a week is past both, so what this
// drops is a record whose process died without ever reporting it. Nothing else
// removes those, so without a horizon the registry only ever grows.
const DefaultHideAfter = 7 * 24 * time.Hour

// DefaultPruneSessions and DefaultPruneHandoffs are how long state survives
// before prune deletes it.
//
// Both are far past DefaultHideAfter. Hiding a record is a display decision
// and costs nothing if it is wrong; deleting one is permanent, so these are
// set beyond any plausible pause — a month is roughly four times the longest
// real gap measured between a session going quiet and being resumed.
//
// Handoffs outlive session records threefold. A session record is a live
// status that stops meaning anything once the process is gone; a handoff is
// the answer to "where did we leave off", which is what the tool exists to
// give back.
const (
	DefaultPruneSessions = 30 * 24 * time.Hour
	DefaultPruneHandoffs = 90 * 24 * time.Hour
)

// Config is the contents of ~/.mindskein/config.toml.
type Config struct {
	Status    Status    `toml:"status"`
	Vault     Vault     `toml:"vault"`
	Retention Retention `toml:"retention"`
}

// Vault locates the notes the brief reads. Both keys are hand-written and
// neither has a default: guessing a vault layout would be guessing at somebody
// else's filesystem, and a wrong guess reads as "no priorities today".
type Vault struct {
	// Path is the vault root, and may be written with a leading ~.
	Path string `toml:"path"`

	// Plan is the note holding the !1/!2 lines, either absolute or relative
	// to Path.
	Plan string `toml:"plan"`
}

// PlanPath is the absolute path of the plan note, or "" when the file has not
// configured one.
func (v Vault) PlanPath() string {
	plan := expandHome(v.Plan)
	switch {
	case plan == "":
		return ""
	case filepath.IsAbs(plan):
		return filepath.Clean(plan)
	}
	root := expandHome(v.Path)
	if root == "" {
		return ""
	}
	return filepath.Join(root, plan)
}

// expandHome resolves a leading ~, which is how a home-relative path is
// written by hand and is not otherwise meaningful to the filesystem.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}

// Status configures the live sessions listing.
type Status struct {
	// HideAfter drops sessions quiet for longer than this, whatever status
	// they claim. Zero hides nothing by age.
	HideAfter Duration `toml:"hide_after"`
}

// Retention is how long state is kept before prune deletes it. Both are far
// longer than the display horizon in [status]: hiding a record is reversible
// and deleting one is not.
type Retention struct {
	// Sessions drops session records quiet for longer than this. Zero keeps
	// them forever, which is what every version before this one did.
	Sessions Duration `toml:"sessions"`

	// Handoffs drops handoffs that ended longer ago than this. Kept longer
	// than Sessions: a handoff is the answer the tool exists to give.
	Handoffs Duration `toml:"handoffs"`
}

// Defaults is the configuration used when there is no file to read.
func Defaults() Config {
	return Config{
		Status: Status{HideAfter: Duration(DefaultHideAfter)},
		Retention: Retention{
			Sessions: Duration(DefaultPruneSessions),
			Handoffs: Duration(DefaultPruneHandoffs),
		},
	}
}

// Load reads path, falling back to Defaults for anything it does not set.
//
// A missing file is not an error: it is the normal state, and every key has a
// default. A malformed one returns both the defaults and the error, so the
// caller can say so and still print — a broken config must not take the
// listing down with it.
func Load(path string) (Config, error) {
	cfg := Defaults()

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("reading %s: %w", path, err)
	}

	var file Config
	meta, err := toml.Decode(string(data), &file)
	if err != nil {
		return cfg, fmt.Errorf("parsing %s: %w", path, err)
	}
	// IsDefined, not a zero check: hide_after = 0 disables hiding, and is not
	// the same as leaving the key out.
	if meta.IsDefined("status", "hide_after") {
		cfg.Status.HideAfter = file.Status.HideAfter
	}
	// Same reasoning as hide_after: an explicit 0 turns retention off, and is
	// not the same as leaving the key out.
	if meta.IsDefined("retention", "sessions") {
		cfg.Retention.Sessions = file.Retention.Sessions
	}
	if meta.IsDefined("retention", "handoffs") {
		cfg.Retention.Handoffs = file.Retention.Handoffs
	}
	cfg.Vault = file.Vault
	if cfg.Vault.Plan != "" && cfg.Vault.PlanPath() == "" {
		return cfg, fmt.Errorf("%s: vault.plan is relative but vault.path is not set", path)
	}
	// A misspelled key is the failure this whole file is meant to avoid: a
	// setting that appears to do nothing. Everything understood is applied
	// first, so one typo does not discard the keys that were right.
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		return cfg, fmt.Errorf("unknown setting in %s: %s", path, strings.Join(keys, ", "))
	}
	return cfg, nil
}

// Duration is a configured duration that also understands days, which
// time.ParseDuration does not — a retention horizon is naturally written "7d".
//
// It serves both the config file and the command-line flag, so the two can
// never disagree about what a given string means.
type Duration time.Duration

// Duration unwraps to the standard library type.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// UnmarshalText decodes a TOML string such as "7d" or "36h".
func (d *Duration) UnmarshalText(text []byte) error { return d.Set(string(text)) }

// Set implements flag.Value.
func (d *Duration) Set(s string) error {
	parsed, err := ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

// String implements flag.Value, spelling a horizon in the units it is
// configured in — "7d", "1d12h" — rather than the standard library's
// "168h0m0s", which no one would write in a config file.
func (d Duration) String() string {
	rest := time.Duration(d)
	if rest <= 0 {
		return "0"
	}
	var b strings.Builder
	for _, unit := range []struct {
		size   time.Duration
		suffix string
	}{{24 * time.Hour, "d"}, {time.Hour, "h"}, {time.Minute, "m"}} {
		if n := rest / unit.size; n > 0 {
			fmt.Fprintf(&b, "%d%s", n, unit.suffix)
			rest -= n * unit.size
		}
	}
	if rest > 0 {
		b.WriteString(rest.String())
	}
	return b.String()
}

// ParseDuration extends time.ParseDuration with a leading days component, so
// "7d" and "1d12h" parse. A negative horizon is rejected rather than silently
// hiding everything.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty duration")
	}

	original, total := s, time.Duration(0)
	if i := strings.IndexByte(s, 'd'); i >= 0 {
		days, err := strconv.ParseFloat(s[:i], 64)
		if err != nil {
			return 0, fmt.Errorf("parsing days in %q: %w", original, err)
		}
		total = time.Duration(days * 24 * float64(time.Hour))
		s = s[i+1:]
	}
	if s != "" {
		rest, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("parsing %q: %w", original, err)
		}
		total += rest
	}
	if total < 0 {
		return 0, fmt.Errorf("duration %q is negative", original)
	}
	return total, nil
}
