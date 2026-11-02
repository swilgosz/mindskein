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

	// Size is how big the file of a resolved heavy-instructions or
	// long-memory-index item was: tokens for an instruction file, lines for
	// a memory index. Zero for every other finding, and for entries written
	// before sizes were recorded, which stay hidden whatever the size.
	Size int `json:"size,omitempty"`

	// Items is what a state finding listed when it was resolved as a whole,
	// each with its size where the finding has one. Only those stay hidden;
	// an item that appears later is reported as new.
	Items []ResolvedItem `json:"items,omitempty"`
}

// ResolvedItem is one item a whole resolution covers.
type ResolvedItem struct {
	Key  string `json:"key"`
	Size int    `json:"size,omitempty"`
}

// NewResolution resolves finding id, or its item key, of r at at. For a
// state finding it records what r lists now: the item's size, or, resolving
// the whole finding, each of its items with theirs.
func NewResolution(r *Report, id, key string, at time.Time, note string) Resolution {
	res := Resolution{ID: id, Key: key, ResolvedAt: at, Note: note}
	if findingKinds[id] != stateKind {
		return res
	}
	sizes := map[string]int{}
	for _, in := range r.Inventory {
		for _, c := range in.Context {
			sizes[c.Path] = contextSize(c)
		}
	}
	if key != "" {
		res.Size = sizes[key]
		return res
	}
	for _, f := range r.Findings {
		if f.ID != id {
			continue
		}
		for _, it := range f.Items {
			res.Items = append(res.Items, ResolvedItem{Key: it.Key, Size: sizes[it.Key]})
		}
	}
	return res
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

	// Cleared is set once an unused item the entry covers ran again on or
	// after the day it was resolved: the entry hides nothing any more.
	Cleared bool `json:"cleared,omitempty"`

	// trimmed is set when some items of a whole entry were cleared.
	trimmed bool
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

// Settle writes back what a report found of entries: those cleared are
// dropped, and a whole entry keeps only the items not cleared. Entries are
// matched on id, key and resolve time, so one resolved again since is kept.
// It writes nothing when nothing was cleared.
func (s *Resolved) Settle(entries []ResolvedEntry) error {
	if !slices.ContainsFunc(entries, func(e ResolvedEntry) bool { return e.Cleared || e.trimmed }) {
		return nil
	}
	all, err := s.Load()
	if err != nil {
		return err
	}
	kept := make([]Resolution, 0, len(all))
	for _, r := range all {
		i := slices.IndexFunc(entries, func(e ResolvedEntry) bool {
			return e.ID == r.ID && e.Key == r.Key && e.ResolvedAt.Equal(r.ResolvedAt)
		})
		switch {
		case i < 0:
		case entries[i].Cleared:
			continue
		case entries[i].trimmed:
			r.Items = entries[i].Items
		}
		kept = append(kept, r)
	}
	return s.save(kept)
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

// hides returns the resolution that keeps an occurrence of event finding
// id, on item key ("" for findings without items), out of the report, or nil
// when it shows. at is when the occurrence happened: one after the resolve
// time is new.
func (rs resolutions) hides(id, key string, at time.Time) *ResolvedEntry {
	return rs.find(id, key, func(e *ResolvedEntry) bool { return !at.After(e.ResolvedAt) })
}

// keeps returns the resolution that keeps item key of state finding id out
// of the report, or nil when it shows. size is the item's size now, zero for
// items without one.
func (rs resolutions) keeps(id, key string, size int) *ResolvedEntry {
	return rs.find(id, key, func(e *ResolvedEntry) bool { return e.covers(key, size) })
}

// whole returns the resolution of finding id as a whole, or nil.
func (rs resolutions) whole(id string) *ResolvedEntry {
	if rs == nil {
		return nil
	}
	return rs[target{id, ""}]
}

// find returns the resolution of id, or of its item key, for which holds;
// one of the item takes the count over one of the whole finding.
func (rs resolutions) find(id, key string, holds func(*ResolvedEntry) bool) *ResolvedEntry {
	if rs == nil {
		return nil
	}
	candidates := []*ResolvedEntry{rs[target{id, ""}]}
	if key != "" {
		candidates = append(candidates, rs[target{id, key}])
	}
	var by *ResolvedEntry
	for _, e := range candidates {
		if e != nil && holds(e) {
			by = e
		}
	}
	return by
}

// covers says whether e still hides item key, now size big. A whole entry
// written before items were recorded covers every item.
func (e *ResolvedEntry) covers(key string, size int) bool {
	if e.Cleared {
		return false
	}
	if e.Key != "" {
		return withinSize(e.Size, size)
	}
	if len(e.Items) == 0 {
		return true
	}
	i := slices.IndexFunc(e.Items, func(it ResolvedItem) bool { return it.Key == key })
	return i >= 0 && withinSize(e.Items[i].Size, size)
}

// withinSize says whether a file now size big has not grown more than 10%
// past the size recorded at resolve time. An unrecorded size never grows.
func withinSize(recorded, size int) bool {
	return recorded == 0 || size*10 <= recorded*11
}

// clearUsed clears what the resolutions of unused items cover once the item
// ran on or after the local day it was resolved. It had no use when it was
// resolved, so such a use is new, and a later unused stretch is reported as
// new too. last holds the last day each root|kind:name ran.
func (rs resolutions) clearUsed(inv []Inventory, last map[string]string, loc *time.Location) {
	if rs == nil {
		return
	}
	for _, in := range inv {
		for _, it := range in.Items {
			id := FindingUnusedSkills
			if it.Kind == "agent" {
				id = FindingUnusedAgents
			}
			rs.clearIfUsed(id, it.Name, itemLastUse(it, last), loc)
		}
		for _, m := range in.MCP {
			rs.clearIfUsed(FindingUnusedMCP, m.Name, mcpLastUse(m, last), loc)
		}
	}
}

func (rs resolutions) clearIfUsed(id, key, lastDay string, loc *time.Location) {
	if lastDay == "" {
		return
	}
	usedSince := func(e *ResolvedEntry) bool {
		return e != nil && !e.Cleared && lastDay >= e.ResolvedAt.In(loc).Format(time.DateOnly)
	}
	if e := rs[target{id, key}]; usedSince(e) {
		e.Cleared = true
	}
	if e := rs[target{id, ""}]; usedSince(e) {
		i := slices.IndexFunc(e.Items, func(it ResolvedItem) bool { return it.Key == key })
		if i < 0 {
			return
		}
		e.Items = slices.Delete(slices.Clone(e.Items), i, i+1)
		e.trimmed = true
		e.Cleared = len(e.Items) == 0
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
