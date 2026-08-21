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

// Plan is what one file yielded: the priority items, and enough about what was
// *not* counted to explain an empty result. A reader who is told only "nothing
// found" cannot tell a quiet week from a file written to another convention.
type Plan struct {
	Items []Item
	// Checkboxes is every task line seen, tagged or not.
	Checkboxes int
	// Source is the file the items came from, empty when parsed from a reader.
	Source string
}

// itemPattern matches a priority line: a checkbox, then the level token. Both
// halves are required, because plan.md is prose — it carries checkboxes with no
// priority and priorities with no checkbox (a legend table explaining the
// convention), and neither is work.
var itemPattern = regexp.MustCompile(`^[-*+]\s+\[([ xX])\]\s+!([123])\s+(.*)$`)

// checkboxPattern is deliberately looser than itemPattern: it is not used to
// select work, only to answer "was this file full of tasks I did not
// understand, or empty of them?"
var checkboxPattern = regexp.MustCompile(`^(?:[-*+]|\d+[.)])\s+\[[ xX]\]`)

// linkPattern matches an Obsidian wikilink, embed marker included.
var linkPattern = regexp.MustCompile(`!?\[\[([^\[\]]+)\]\]`)

// Parse reads priority lines in the order the plan declares them.
func Parse(r io.Reader) (Plan, error) {
	var plan Plan
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
		if checkboxPattern.MatchString(line) {
			plan.Checkboxes++
		}
		match := itemPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		label, note := split(match[3])
		if label == "" {
			continue
		}
		plan.Items = append(plan.Items, Item{
			Level: Level(match[2][0] - '0'),
			Label: label,
			Note:  note,
			Done:  match[1] != " ",
		})
	}
	return plan, scanner.Err()
}

// ParseFile reads the plan note at path.
func ParseFile(path string) (Plan, error) {
	f, err := os.Open(path)
	if err != nil {
		return Plan{}, err
	}
	defer f.Close()

	plan, err := Parse(f)
	plan.Source = path
	return plan, err
}

// split separates the label from the status prose, and resolves whichever
// wikilink names the work.
func split(rest string) (label, note string) {
	head, tail := rest, ""
	if at, width := separator(rest); at >= 0 {
		head, tail = rest[:at], rest[at+width:]
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

// separators are tried a tier at a time. The em dash is what this convention
// was written with, so a line that has one splits there wherever it sits; the
// rest are what someone typing the same idea on an ordinary keyboard produces,
// and only get a say when there is no em dash to defer to.
//
// The fallbacks require surrounding spaces because a bare hyphen is not a
// separator in prose — it is inside "3-email" and "2026-08-12", and splitting
// there would cut a date in half.
var separators = [][]string{
	{"—"},
	{" – ", " - ", ": "},
}

// separator finds where the label ends, ignoring anything inside a wikilink: a
// note title may contain a dash, and splitting there cuts the label in half.
func separator(s string) (at, width int) {
	for _, tier := range separators {
		if at, width = firstOutsideLink(s, tier); at >= 0 {
			return at, width
		}
	}
	return -1, 0
}

func firstOutsideLink(s string, candidates []string) (at, width int) {
	depth := 0
	for i := range s {
		switch {
		case strings.HasPrefix(s[i:], "[["):
			depth++
		case strings.HasPrefix(s[i:], "]]"):
			depth--
		case depth <= 0:
			for _, candidate := range candidates {
				if strings.HasPrefix(s[i:], candidate) {
					return i, len(candidate)
				}
			}
		}
	}
	return -1, 0
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
