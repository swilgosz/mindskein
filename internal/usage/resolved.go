package usage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/swilgosz/mindskein/internal/session"
)

// ResolvedFile is the name of the resolutions file in the mindskein home.
const ResolvedFile = "resolved.json"

// Resolution marks a finding, or one item of it, as fixed at a moment. The
// report hides what it covers until the finding happens again.
type Resolution struct {
	ID         string    `json:"id"`
	Key        string    `json:"key,omitempty"`
	ResolvedAt time.Time `json:"resolved_at"`
	Note       string    `json:"note,omitempty"`
}

// Ref is how the resolution is addressed on the command line: <id> or
// <id>:<key>.
func (r Resolution) Ref() string {
	if r.Key == "" {
		return r.ID
	}
	return r.ID + ":" + r.Key
}

// ResolvedEntry is a resolution as the report saw it. Hidden counts what it
// kept out of the report: occurrences for hook failures, big results and
// subagent runs, items for the findings that list things, one for a whole
// finding that lists nothing.
type ResolvedEntry struct {
	Resolution
	Hidden int `json:"hidden"`
}

// ParseRef splits <id>[:<key>] at the first colon; hook keys contain colons
// of their own.
func ParseRef(ref string) (id, key string, err error) {
	id, key, _ = strings.Cut(ref, ":")
	if id == "" {
		return "", "", fmt.Errorf("%q: want <id> or <id>:<key>", ref)
	}
	return id, key, nil
}

// wholeOnly are the findings that name nothing to address one by one.
var wholeOnly = map[string]bool{
	FindingBigResults:   true,
	FindingContextBloat: true,
	FindingCacheWrites:  true,
	FindingSkillListing: true,
}

// Validate checks that id, and key when given, name something in r, so a
// typo is an error rather than a resolution that hides nothing.
func Validate(r *Report, id, key string) error {
	var ids []string
	var found *Finding
	for i, f := range r.Findings {
		ids = append(ids, f.ID)
		if f.ID == id {
			found = &r.Findings[i]
		}
	}
	valid := "none in the current report"
	if len(ids) > 0 {
		valid = strings.Join(ids, ", ")
	}
	if found == nil {
		return fmt.Errorf("no finding %q in the current report; valid ids: %s", id, valid)
	}
	if key == "" {
		return nil
	}
	if wholeOnly[id] {
		return fmt.Errorf("%s lists no items, resolve it as a whole: mindskein resolve %s", id, id)
	}
	for _, it := range found.Items {
		if it.Key == key {
			return nil
		}
	}
	return fmt.Errorf("no item %q of %s in the current report (mindskein usage --all lists each as [id:key]); valid ids: %s", key, id, valid)
}

// Resolved is the file of resolutions. Only resolve and unresolve write it;
// the report and the dashboard read it.
type Resolved struct {
	Path string
}

// DefaultResolved lives in the mindskein home, beside config.toml and the
// stores, and outside every directory prune sweeps.
func DefaultResolved() (*Resolved, error) {
	home, err := session.Home()
	if err != nil {
		return nil, err
	}
	return &Resolved{Path: filepath.Join(home, ResolvedFile)}, nil
}

type resolvedDoc struct {
	Resolved []Resolution `json:"resolved"`
}

// Load reads every resolution, oldest first. A missing or empty file means
// nothing is resolved.
func (s *Resolved) Load() ([]Resolution, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var doc resolvedDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", s.Path, err)
	}
	return doc.Resolved, nil
}

// Resolve records res, replacing an earlier resolution of the same finding
// or item.
func (s *Resolved) Resolve(res Resolution) error {
	all, err := s.Load()
	if err != nil {
		return err
	}
	all = slices.DeleteFunc(all, func(r Resolution) bool { return r.ID == res.ID && r.Key == res.Key })
	return s.save(append(all, res))
}

// Unresolve removes the resolution of id and key, and fails, writing
// nothing, when there is none.
func (s *Resolved) Unresolve(id, key string) (Resolution, error) {
	all, err := s.Load()
	if err != nil {
		return Resolution{}, err
	}
	i := slices.IndexFunc(all, func(r Resolution) bool { return r.ID == id && r.Key == key })
	if i < 0 {
		return Resolution{}, fmt.Errorf("%s is not resolved", Resolution{ID: id, Key: key}.Ref())
	}
	gone := all[i]
	return gone, s.save(slices.Delete(all, i, i+1))
}

func (s *Resolved) save(all []Resolution) error {
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	if all == nil {
		all = []Resolution{}
	}
	data, err := json.MarshalIndent(resolvedDoc{Resolved: all}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-resolved-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, filePerm); err != nil {
		return err
	}
	return os.Rename(tmpName, s.Path)
}

type findingKind int

const (
	eventKind findingKind = iota
	rateKind
	stateKind
)

// findingKinds sorts findings into events, which happen at a time; rates,
// which are a share over sessions; and states of the setup.

var findingKinds = map[string]findingKind{
	FindingHookFailures:      eventKind,
	FindingBigResults:        eventKind,
	FindingTopTierSubagents:  eventKind,
	FindingContextBloat:      rateKind,
	FindingCacheWrites:       rateKind,
	FindingSkillListing:      rateKind,
	FindingUnusedSkills:      stateKind,
	FindingUnusedAgents:      stateKind,
	FindingUnusedMCP:         stateKind,
	FindingHeavyInstructions: stateKind,
	FindingLongMemoryIndex:   stateKind,
}

type target struct{ id, key string }

// resolutions is what a report is built against. The nil value resolves
// nothing.
type resolutions map[target]*ResolvedEntry

func newResolutions(entries []ResolvedEntry) resolutions {
	if len(entries) == 0 {
		return nil
	}
	rs := resolutions{}
	for i := range entries {
		rs[target{entries[i].ID, entries[i].Key}] = &entries[i]
	}
	return rs
}

// hides returns the resolution that keeps an occurrence of finding id, on
// item key ("" for findings without items), out of the report, or nil when
// it shows. at is when the occurrence happened. A resolution of the item
// takes the count over one of the whole finding.
func (rs resolutions) hides(id, key string, at time.Time) *ResolvedEntry {
	if rs == nil {
		return nil
	}
	candidates := []*ResolvedEntry{rs[target{id, ""}]}
	if key != "" {
		candidates = append(candidates, rs[target{id, key}])
	}
	var by *ResolvedEntry
	for _, e := range candidates {
		if e != nil && !happenedAgain(id, e.Resolution, at) {
			by = e
		}
	}
	return by
}

// happenedAgain is the rule of each kind of finding: whether an occurrence
// at at undoes resolution r. An event comes back with an occurrence after
// the resolve time; a resolved rate or state finding stays hidden.
func happenedAgain(id string, r Resolution, at time.Time) bool {
	switch findingKinds[id] {
	case eventKind:
		return at.After(r.ResolvedAt)
	default:
		return false
	}
}

// HiddenCount is how many findings and items the report leaves out because
// they were resolved.
func (r *Report) HiddenCount() int {
	n := 0
	for _, e := range r.Resolved {
		if e.Hidden > 0 {
			n++
		}
	}
	return n
}
