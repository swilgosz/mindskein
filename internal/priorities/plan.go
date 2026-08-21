// Package priorities parses the !1 and !2 checkbox lines out of the vault's
// plan.md, resolving Obsidian wikilinks to readable labels and truncating the
// long trailing status prose.
package priorities

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// Level is how urgent an item is: !1 is the current focus, !2 is next, !3 is
// the backlog.
type Level int

// The three levels the plan uses. Their order is their urgency, which is also
// the order they are printed in.
const (
	Now Level = iota + 1
	Next
	Backlog
)

// String spells a level the way the plan writes it.
func (l Level) String() string { return fmt.Sprintf("!%d", int(l)) }

// Item is one priority line.
type Item struct {
	Level Level
	// Label names the work, resolved from the line's wikilink where it has
	// one. It is what the reader recognises the project by.
	Label string
	// Note is the trailing status prose, stripped of markdown and left at
	// full length; the renderer decides how much of it fits.
	Note string
	// Done records a ticked checkbox. Kept rather than dropped so a caller
	// can tell "finished" from "never written down".
	Done bool
}

// itemPattern matches a priority line: a checkbox, then the level token. Both
// halves are required, because plan.md is prose — it carries checkboxes with no
// priority and priorities with no checkbox (a legend table explaining the
// convention), and neither is work.
var itemPattern = regexp.MustCompile(`^[-*+]\s+\[([ xX])\]\s+!([123])\s+(.*)$`)

// linkPattern matches an Obsidian wikilink, embed marker included.
var linkPattern = regexp.MustCompile(`!?\[\[([^\[\]]+)\]\]`)

// Parse reads priority lines in the order the plan declares them.
func Parse(r io.Reader) ([]Item, error) {
	var items []Item
	scanner := bufio.NewScanner(r)
	// A single !1 line runs to a paragraph of dated commentary; the default
	// 64 KB token is ample, but a markdown table in the same file need not be.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	fenced := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fenced = !fenced
			continue
		}
		// The convention documents itself with a worked example, so the file
		// contains lines that look exactly like work and are not.
		if fenced {
			continue
		}
		match := itemPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		label, note := split(match[3])
		if label == "" {
			continue
		}
		items = append(items, Item{
			Level: Level(match[2][0] - '0'),
			Label: label,
			Note:  note,
			Done:  match[1] != " ",
		})
	}
	return items, scanner.Err()
}

// ParseFile reads the plan note at path.
func ParseFile(path string) ([]Item, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// split separates the label from the status prose at the em dash the task
// convention puts between them, and resolves whichever wikilink names the work.
func split(rest string) (label, note string) {
	head, tail := rest, ""
	if i := dash(rest); i >= 0 {
		head, tail = rest[:i], rest[i+len("—"):]
	}
	if link := linkPattern.FindStringSubmatch(head); link != nil {
		label = linkLabel(link[1])
	} else {
		label = clean(head)
	}
	note = clean(tail)
	// A line that opens with the dash names nothing, but dropping it would
	// take work out of the brief for a typo. The prose becomes the label.
	if label == "" {
		return note, ""
	}
	return label, note
}

// dash finds the separating em dash, ignoring any inside a wikilink: a note
// title may contain one, and splitting there would cut the label in half.
func dash(s string) int {
	depth := 0
	for i, r := range s {
		switch {
		case strings.HasPrefix(s[i:], "[["):
			depth++
		case strings.HasPrefix(s[i:], "]]"):
			depth--
		case r == '—' && depth <= 0:
			return i
		}
	}
	return -1
}

// linkLabel turns a wikilink target into something worth printing in a narrow
// column.
func linkLabel(target string) string {
	if _, alias, ok := strings.Cut(target, "|"); ok {
		return clean(alias)
	}
	name, _, _ := strings.Cut(target, "#")
	name = strings.TrimSuffix(strings.TrimSpace(name), ".md")

	segments := strings.Split(name, "/")
	last := segments[len(segments)-1]
	// An _index note takes its identity from its folder; every project in the
	// vault has one, so the file name alone would label them all the same.
	if last == "_index" && len(segments) > 1 {
		last = segments[len(segments)-2]
	}
	return clean(last)
}

var emphasis = strings.NewReplacer("**", "", "__", "", "`", "")

// clean renders inline markdown as the text it decorates: wikilinks become
// their labels, emphasis markers disappear, and wrapped whitespace collapses.
func clean(s string) string {
	s = linkPattern.ReplaceAllStringFunc(s, func(link string) string {
		return linkLabel(linkPattern.FindStringSubmatch(link)[1])
	})
	return strings.Join(strings.Fields(emphasis.Replace(s)), " ")
}
