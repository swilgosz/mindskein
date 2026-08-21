// Package brief composes the morning brief from the blocks the other packages
// render: priorities from plan.md, live sessions from the registry, and the
// last handoff per workstream.
//
// It renders none of them itself. The package that owns a record owns
// presenting it, so what is left here is the composition — the order, the
// spacing, and the guarantee that one unreadable source costs a single line
// rather than the whole brief.
package brief

import (
	"bytes"
	"fmt"
	"io"
)

// Section is one block. Heading is only used when Render fails: the block
// prints its own heading otherwise, and a failed one still needs to appear so
// the brief cannot silently come up a section short.
type Section struct {
	Heading string
	Render  func(io.Writer) error
}

// Render writes the sections in order, separated by a blank line.
//
// A section is rendered into a buffer first. A failure part-way through would
// otherwise leave a half-written block on the page above the line explaining
// that it could not be written.
func Render(w io.Writer, sections ...Section) error {
	for i, s := range sections {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}

		var block bytes.Buffer
		if err := s.Render(&block); err != nil {
			if _, err := fmt.Fprintf(w, "%s\n  %v\n", s.Heading, err); err != nil {
				return err
			}
			continue
		}
		if _, err := w.Write(block.Bytes()); err != nil {
			return err
		}
	}
	return nil
}
