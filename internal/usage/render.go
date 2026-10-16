package usage

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// EmptyHint is what an empty store prints instead of a page of zeroes.
const EmptyHint = "No usage recorded yet. Run `mindskein usage --backfill` to summarise past sessions; new ones are captured as they run."

// Render prints the report. all lifts the caps on findings and their evidence.
func Render(w io.Writer, r *Report, all bool) error {
	p := &printer{w: w}
	if r.Sessions == 0 {
		p.line(EmptyHint)
		return p.err
	}

	span := "all time"
	if r.Days > 0 {
		span = fmt.Sprintf("last %d days", r.Days)
	}
	p.line(fmt.Sprintf("USAGE  %s · %d sessions · %d prompts", span, r.Sessions, r.Prompts))
	total := r.Cost.Total()
	sub := 0.0
	if total > 0 {
		sub = r.SubCost / total
	}
	p.line(fmt.Sprintf("  cost       %s API-equivalent · cache hit %s · subagents %s", money(total), pct(r.CacheHit), pct(sub)))
	p.line(fmt.Sprintf("  spend      cache read %s · cache write %s · output %s · input %s",
		money(r.Cost.CacheRead), money(r.Cost.CacheWrite), money(r.Cost.Output), money(r.Cost.Input)))
	p.line("  models     " + spendList(r.Models, 4))
	p.line("  where      " + spendList(r.Workstreams, 4))
	p.line(fmt.Sprintf("  friction   %d interrupts · %d denials · %d compactions · %d hook errors",
		r.Events[EventInterrupts], r.Events[EventDenials],
		r.Events[EventCompactAuto]+r.Events[EventCompactManual], r.Events[EventHookErrors]))

	p.line("")
	if len(r.Findings) == 0 {
		p.line("FIX FIRST")
		p.line("  nothing to flag in this range")
		return p.err
	}
	p.line("FIX FIRST")
	limit, evidence := 5, 3
	if all {
		limit, evidence = len(r.Findings), 1<<30
	}
	for i, f := range r.Findings {
		if i == limit {
			p.line(fmt.Sprintf("\n  %d more (--all)", len(r.Findings)-limit))
			break
		}
		mark := "·"
		if f.Severity <= Serious {
			mark = "!"
		}
		title := f.Title
		if f.Saving != "" {
			title += "  (" + f.Saving + ")"
		}
		p.line(fmt.Sprintf("  %s %s", mark, title))
		p.line("      " + Tilde(f.Advice))
		for j, e := range f.Evidence {
			if j == evidence {
				p.line(fmt.Sprintf("      … %d more", len(f.Evidence)-evidence))
				break
			}
			p.line("      - " + Tilde(oneLine(e)))
		}
	}
	p.line("")
	p.line("  Costs use API list prices: on a subscription they are the value consumed, not the bill.")
	return p.err
}

func spendList(s []Spend, n int) string {
	var parts []string
	for i, x := range s {
		if i == n {
			parts = append(parts, fmt.Sprintf("+%d more", len(s)-n))
			break
		}
		parts = append(parts, fmt.Sprintf("%s %s", x.Name, money(x.Cost)))
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, " · ")
}

func money(v float64) string {
	if v >= 100 {
		return fmt.Sprintf("$%.0f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

type printer struct {
	w   io.Writer
	err error
}

func (p *printer) line(s string) {
	if p.err == nil {
		_, p.err = fmt.Fprintln(p.w, s)
	}
}

// Tilde shortens the home directory wherever it appears in s.
func Tilde(s string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return s
	}
	return strings.ReplaceAll(s, home, "~")
}
