package brief

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func block(heading string, lines ...string) Section {
	return Section{Heading: heading, Render: func(w io.Writer) error {
		fmt.Fprintln(w, heading)
		for _, line := range lines {
			fmt.Fprintln(w, "  "+line)
		}
		return nil
	}}
}

func failing(heading, reason string) Section {
	return Section{Heading: heading, Render: func(io.Writer) error {
		return errors.New(reason)
	}}
}

func TestBrief(t *testing.T) {
	t.Run("prints the sections in order, separated by a blank line", func(t *testing.T) {
		var buf bytes.Buffer
		if err := Render(&buf, block("ONE", "a"), block("TWO", "b"), block("THREE", "c")); err != nil {
			t.Fatalf("Render() = %v, want nil", err)
		}
		want := "ONE\n  a\n\nTWO\n  b\n\nTHREE\n  c\n"
		if buf.String() != want {
			t.Errorf("output = %q, want %q", buf.String(), want)
		}
	})

	t.Run("a section that fails prints its heading and one hint line", func(t *testing.T) {
		var buf bytes.Buffer
		if err := Render(&buf, failing("TWO", "no plan configured")); err != nil {
			t.Fatalf("Render() = %v, want nil", err)
		}
		want := "TWO\n  no plan configured\n"
		if buf.String() != want {
			t.Errorf("output = %q, want %q", buf.String(), want)
		}
	})

	t.Run("the remaining sections still print when one fails", func(t *testing.T) {
		var buf bytes.Buffer
		if err := Render(&buf, block("ONE", "a"), failing("TWO", "unreachable"), block("THREE", "c")); err != nil {
			t.Fatalf("Render() = %v, want nil", err)
		}
		for _, want := range []string{"ONE\n  a", "TWO\n  unreachable", "THREE\n  c"} {
			if !strings.Contains(buf.String(), want) {
				t.Errorf("output missing %q:\n%s", want, buf.String())
			}
		}
	})

	t.Run("a section that fails part-way leaves no half-written block above its hint", func(t *testing.T) {
		half := Section{Heading: "TWO", Render: func(w io.Writer) error {
			fmt.Fprintln(w, "TWO")
			fmt.Fprintln(w, "  first row")
			return errors.New("the store went away")
		}}
		var buf bytes.Buffer
		if err := Render(&buf, half); err != nil {
			t.Fatalf("Render() = %v, want nil", err)
		}
		if strings.Contains(buf.String(), "first row") {
			t.Errorf("the abandoned row survived into the brief:\n%s", buf.String())
		}
		want := "TWO\n  the store went away\n"
		if buf.String() != want {
			t.Errorf("output = %q, want %q", buf.String(), want)
		}
	})
}
