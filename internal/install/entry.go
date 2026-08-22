package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// owns reports whether a hook entry is one of mindskein's.
//
// Matching on the command string is what lets an install find a registration
// written by an older version at a different path and update it in place,
// rather than adding a second entry beside it.
func owns(entry *object) bool {
	raw, ok := entry.get("command")
	if !ok {
		return false
	}
	var cmd string
	if err := json.Unmarshal(raw, &cmd); err != nil {
		return false
	}
	return strings.Contains(cmd, "mindskein") && strings.Contains(cmd, " hook ")
}

// desired fills in the fields mindskein owns, leaving any others the user
// added to their own copy of the entry alone.
func desired(entry *object, reg registration, opts Options) error {
	if err := entry.setValue("type", "command"); err != nil {
		return err
	}
	if err := entry.setValue("command", fmt.Sprintf("%s hook %s", opts.Binary, reg.Arg)); err != nil {
		return err
	}
	if err := entry.setValue("timeout", opts.Timeout); err != nil {
		return err
	}
	return entry.setValue("async", opts.Async)
}

// apply installs or repairs one event, returning a description of what it
// changed, or "" when the registration was already correct.
func apply(hooks *object, reg registration, opts Options) (string, error) {
	groups, err := groupsFor(hooks, reg.Event)
	if err != nil {
		return "", err
	}

	for gi, groupRaw := range groups {
		group := newObject()
		if err := json.Unmarshal(groupRaw, group); err != nil {
			continue // not an object we understand; leave it exactly as it is
		}
		entriesRaw, ok := group.get("hooks")
		if !ok {
			continue
		}
		entries, err := array(entriesRaw)
		if err != nil {
			continue
		}
		for ei, entryRaw := range entries {
			entry := newObject()
			if err := json.Unmarshal(entryRaw, entry); err != nil || !owns(entry) {
				continue
			}
			before, err := json.Marshal(entry)
			if err != nil {
				return "", err
			}
			was := describe(entry)
			if err := desired(entry, reg, opts); err != nil {
				return "", err
			}
			after, err := json.Marshal(entry)
			if err != nil {
				return "", err
			}
			if bytes.Equal(before, after) {
				return "", nil
			}
			entries[ei] = after
			if err := group.setValue("hooks", entries); err != nil {
				return "", err
			}
			regrouped, err := json.Marshal(group)
			if err != nil {
				return "", err
			}
			groups[gi] = regrouped
			if err := hooks.setValue(reg.Event, groups); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s: repaired existing registration (%s)", reg.Event, was), nil
		}
	}

	entry := newObject()
	if err := desired(entry, reg, opts); err != nil {
		return "", err
	}
	group := newObject()
	if reg.Matcher != "" {
		if err := group.setValue("matcher", reg.Matcher); err != nil {
			return "", err
		}
	}
	if err := group.setValue("hooks", []*object{entry}); err != nil {
		return "", err
	}
	raw, err := json.Marshal(group)
	if err != nil {
		return "", err
	}
	if err := hooks.setValue(reg.Event, append(groups, raw)); err != nil {
		return "", err
	}
	return reg.Event + ": registered", nil
}

// describe names what was wrong with an existing registration, so a repair
// says what it fixed rather than only that it fixed something.
func describe(entry *object) string {
	var missing []string
	if _, ok := entry.get("async"); !ok {
		missing = append(missing, "no async")
	}
	if _, ok := entry.get("timeout"); !ok {
		missing = append(missing, "no timeout")
	}
	if len(missing) == 0 {
		return "changed"
	}
	return strings.Join(missing, ", ")
}

// remove strips mindskein's entry from one event, dropping the group and the
// event itself once nothing else is left in them.
func remove(hooks *object, reg registration) (bool, error) {
	groups, err := groupsFor(hooks, reg.Event)
	if err != nil {
		return false, err
	}
	var kept []json.RawMessage
	var removed bool

	for _, groupRaw := range groups {
		group := newObject()
		if err := json.Unmarshal(groupRaw, group); err != nil {
			kept = append(kept, groupRaw)
			continue
		}
		entriesRaw, ok := group.get("hooks")
		if !ok {
			kept = append(kept, groupRaw)
			continue
		}
		entries, err := array(entriesRaw)
		if err != nil {
			kept = append(kept, groupRaw)
			continue
		}
		var keptEntries []json.RawMessage
		for _, entryRaw := range entries {
			entry := newObject()
			if err := json.Unmarshal(entryRaw, entry); err == nil && owns(entry) {
				removed = true
				continue
			}
			keptEntries = append(keptEntries, entryRaw)
		}
		if len(keptEntries) == 0 {
			continue // the group existed only for us
		}
		if err := group.setValue("hooks", keptEntries); err != nil {
			return false, err
		}
		raw, err := json.Marshal(group)
		if err != nil {
			return false, err
		}
		kept = append(kept, raw)
	}

	if len(kept) == 0 {
		hooks.delete(reg.Event)
		return removed, nil
	}
	return removed, hooks.setValue(reg.Event, kept)
}

func groupsFor(hooks *object, event string) ([]json.RawMessage, error) {
	raw, ok := hooks.get(event)
	if !ok {
		return nil, nil
	}
	groups, err := array(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", event, err)
	}
	return groups, nil
}

// indent formats the rendered settings the way the file is written by hand:
// two spaces, and no HTML escaping of the characters that appear in matchers.
func indent(w io.Writer, raw []byte) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}
