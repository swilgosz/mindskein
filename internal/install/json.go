// Package install registers and removes mindskein's hooks in a Claude Code
// settings.json.
//
// That file controls the user's entire setup and is hand-maintained. Nothing
// here rewrites a part of it mindskein does not own: values it does not model
// are carried through as raw bytes, and key order is preserved.
package install

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// object is a JSON object that remembers the order its keys arrived in.
//
// Decoding into a map and writing it back would alphabetise every key, turning
// a two-line edit into a whole-file diff the user has to read before they can
// trust it. Values stay raw, so unknown fields survive untouched.
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

func newObject() *object {
	return &object{vals: map[string]json.RawMessage{}}
}

func (o *object) get(key string) (json.RawMessage, bool) {
	v, ok := o.vals[key]
	return v, ok
}

func (o *object) set(key string, val json.RawMessage) {
	if _, seen := o.vals[key]; !seen {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = val
}

// setValue marshals v and stores it under key.
func (o *object) setValue(key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	o.set(key, raw)
	return nil
}

func (o *object) delete(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *object) len() int { return len(o.keys) }

// child decodes the object stored at key. A missing key yields a new empty
// object, so callers can build a path without checking each step.
func (o *object) child(key string) (*object, error) {
	raw, ok := o.get(key)
	if !ok {
		return newObject(), nil
	}
	child := newObject()
	if err := json.Unmarshal(raw, child); err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return child, nil
}

func (o *object) UnmarshalJSON(data []byte) error {
	o.keys = nil
	o.vals = map[string]json.RawMessage{}

	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("expected a JSON object, got %v", tok)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("expected an object key, got %v", tok)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		o.set(key, raw)
	}
	_, err = dec.Token()
	return err
}

func (o *object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		encoded, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		buf.Write(encoded)
		buf.WriteByte(':')
		buf.Write(o.vals[key])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// array decodes a JSON array of opaque elements.
func array(raw json.RawMessage) ([]json.RawMessage, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	return items, nil
}
