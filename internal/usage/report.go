package usage

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// UnusedDays is the window an installed item must go unused in before the
// report calls it unused, whatever range the report covers.
const UnusedDays = 90

// Spend is one row of a cost breakdown.
type Spend struct {
	Name   string  `json:"name"`
	Cost   float64 `json:"cost"`
	Turns  int64   `json:"turns"`
	Output int64   `json:"output"`
	Share  float64 `json:"share"`
}

type ToolRow struct {
	Name string `json:"name"`
	ToolStat
}

type SessionRow struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Repo       string    `json:"repo"`
	Start      time.Time `json:"start"`
	Prompts    int       `json:"prompts"`
	Turns      int64     `json:"turns"`
	Cost       float64   `json:"cost"`
	SubCost    float64   `json:"subagent_cost"`
	ContextMax int64     `json:"context_max"`
	ContextBig int       `json:"context_big"`
}

// Severity orders findings; the order of the constants is the order printed.
type Severity int

const (
	Critical Severity = iota
	Serious
	Warning
)

func (s Severity) String() string {
	return [...]string{"critical", "serious", "warning"}[s]
}

func (s Severity) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

func (s *Severity) UnmarshalText(b []byte) error {
	for _, v := range []Severity{Critical, Serious, Warning} {
		if v.String() == string(b) {
			*s = v
			return nil
		}
	}
	return fmt.Errorf("unknown severity %q", b)
}

// Finding is one thing worth fixing, with the evidence behind it.
type Finding struct {
	Severity Severity `json:"severity"`
	Title    string   `json:"title"`
	Advice   string   `json:"advice"`
	Saving   string   `json:"saving,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
}

// Report is the JSON contract every usage surface reads.
type Report struct {
	GeneratedAt time.Time `json:"generated_at"`
	Days        int       `json:"days"`
	From        string    `json:"from"`

	Sessions int     `json:"sessions"`
	Prompts  int     `json:"prompts"`
	Tokens   Tokens  `json:"tokens"`
	Cost     Cost    `json:"cost"`
	CacheHit float64 `json:"cache_hit"`
	SubCost  float64 `json:"subagent_cost"`

	Models      []Spend        `json:"models"`
	Workstreams []Spend        `json:"workstreams"`
	Agents      []Spend        `json:"agents"`
	Tools       []ToolRow      `json:"tools"`
	TopSessions []SessionRow   `json:"top_sessions"`
	Events      map[string]int `json:"events"`
	Findings    []Finding      `json:"findings"`

	Inventory []Inventory `json:"inventory"`
}

// Build aggregates records over the last days (0 means all time) and judges
// them against the inventory.
func Build(records []*Record, inv []Inventory, now time.Time, days int, loc *time.Location) *Report {
	if loc == nil {
		loc = time.Local
	}
	r := &Report{GeneratedAt: now, Days: days, Events: map[string]int{}, Inventory: inv}
	from := ""
	if days > 0 {
		from = now.In(loc).AddDate(0, 0, -days+1).Format(time.DateOnly)
		r.From = from
	}
	inRange := func(day string) bool { return day != "" && day >= from }
	cutoff90 := now.In(loc).AddDate(0, 0, -UnusedDays+1).Format(time.DateOnly)

	models := map[string]*Spend{}
	repos := map[string]*Spend{}
	agents := map[string]*Spend{}
	tools := map[string]*ToolStat{}
	hookErrors := map[string]int{}
	uses90 := map[string]int{} // root|kind:name
	var big []BigResult
	var listing []int
	turns, bigTurns := int64(0), 0

	addSpend := func(m map[string]*Spend, name string, c float64, t Tokens) {
		s := m[name]
		if s == nil {
			s = &Spend{Name: name}
			m[name] = s
		}
		s.Cost += c
		s.Turns += t.Turns
		s.Output += t.Output
	}

	for _, rec := range records {
		f := rec.File
		files := []struct {
			agent string
			file  *File
		}{{"", f}}
		for _, sa := range rec.Subagents {
			files = append(files, struct {
				agent string
				file  *File
			}{sa.AgentType, sa.File})
		}

		row := SessionRow{ID: rec.SessionID, Title: f.DisplayTitle(), Repo: rec.Repo, Start: f.Start,
			ContextMax: f.ContextMax, ContextBig: f.ContextBig}
		active := false
		for _, x := range files {
			for day, byModel := range x.file.Usage {
				if !inRange(day) {
					continue
				}
				for model, t := range byModel {
					if t.Turns == 0 {
						continue
					}
					active = true
					c := costOf(model, t)
					r.Tokens.add(t, 1)
					r.Cost.add(c)
					addSpend(models, ModelName(model), c.Total(), t)
					addSpend(repos, orUnknown(rec.Repo), c.Total(), t)
					agent := x.agent
					if agent == "" {
						agent = "(main session)"
					} else {
						r.SubCost += c.Total()
						row.SubCost += c.Total()
					}
					addSpend(agents, agent+" on "+ModelName(model), c.Total(), t)
					row.Cost += c.Total()
					row.Turns += t.Turns
				}
			}
			for day, byTool := range x.file.Tools {
				if !inRange(day) {
					continue
				}
				for name, s := range byTool {
					ts := tools[name]
					if ts == nil {
						ts = &ToolStat{}
						tools[name] = ts
					}
					ts.Calls += s.Calls
					ts.Errors += s.Errors
					ts.ResultBytes += s.ResultBytes
				}
			}
			for day, uses := range x.file.Uses {
				if day < cutoff90 {
					continue
				}
				for k, n := range uses {
					uses90[rec.Root+"|"+k] += n
				}
			}
			for _, b := range x.file.Big {
				if inRange(b.Day) {
					big = append(big, b)
				}
			}
		}
		for day, ev := range f.Events {
			if !inRange(day) {
				continue
			}
			for k, n := range ev {
				r.Events[k] += n
				if k == EventPrompts {
					row.Prompts += n
				}
			}
		}
		if !active && row.Prompts == 0 {
			continue
		}
		r.Sessions++
		r.Prompts += row.Prompts
		turns += row.Turns
		bigTurns += f.ContextBig
		for k, n := range f.HookErrors {
			hookErrors[k] += n
		}
		if f.ListingChars > 0 {
			listing = append(listing, f.ListingChars)
		}
		r.TopSessions = append(r.TopSessions, row)
	}

	total := r.Cost.Total()
	if in := r.Tokens.InputAll(); in > 0 {
		r.CacheHit = float64(r.Tokens.CacheRead) / float64(in)
	}
	r.Models = ranked(models, total)
	r.Workstreams = ranked(repos, total)
	r.Agents = ranked(agents, total)
	for name, s := range tools {
		r.Tools = append(r.Tools, ToolRow{Name: name, ToolStat: *s})
	}
	sort.Slice(r.Tools, func(i, j int) bool { return r.Tools[i].Calls > r.Tools[j].Calls })
	sort.Slice(r.TopSessions, func(i, j int) bool { return r.TopSessions[i].Cost > r.TopSessions[j].Cost })
	if len(r.TopSessions) > 20 {
		r.TopSessions = r.TopSessions[:20]
	}

	r.Findings = findings(r, inv, uses90, hookErrors, big, listing, turns, bigTurns)
	return r
}

func ranked(m map[string]*Spend, total float64) []Spend {
	out := make([]Spend, 0, len(m))
	for _, s := range m {
		if total > 0 {
			s.Share = s.Cost / total
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cost > out[j].Cost })
	return out
}

func orUnknown(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return s
}

// itemUses counts an item's uses in the last UnusedDays. A skill can be run
// through the Skill tool or typed as a slash command, and both are one skill.
func itemUses(it Item, uses map[string]int) int {
	pre := it.Root + "|"
	if it.Kind == "agent" {
		n := uses[pre+UseAgent+":"+it.Name]
		if it.Short != it.Name {
			n += uses[pre+UseAgent+":"+it.Short]
		}
		return n
	}
	return uses[pre+UseSkill+":"+it.Name] + uses[pre+UseCommand+":"+it.Name]
}

func mcpUses(m MCPServer, uses map[string]int) int {
	pre := m.Root + "|" + UseMCP + ":"
	n := 0
	for k, v := range uses {
		if !strings.HasPrefix(k, pre) {
			continue
		}
		s := strings.TrimPrefix(k, pre)
		if s == m.Name || strings.HasSuffix(s, "_"+m.Name) {
			n += v
		}
	}
	return n
}

func findings(r *Report, inv []Inventory, uses map[string]int, hookErrors map[string]int,
	big []BigResult, listing []int, turns int64, bigTurns int) []Finding {
	var out []Finding
	total := r.Cost.Total()

	if n := sumInts(hookErrors); n > 0 {
		sev := Serious
		if n > 50 {
			sev = Critical
		}
		out = append(out, Finding{Severity: sev, Title: fmt.Sprintf("%d hook failures", n),
			Advice:   "A hook that fails every session start adds latency and its context never arrives. Fix the path or remove the hook or plugin.",
			Evidence: topCounts(hookErrors, 8)})
	}

	var unusedSkills, unusedAgents []Item
	perRoot := map[string]int{}
	for _, in := range inv {
		for _, it := range in.Items {
			if !it.PluginOn || itemUses(it, uses) > 0 {
				continue
			}
			if it.Kind == "agent" {
				unusedAgents = append(unusedAgents, it)
			} else {
				unusedSkills = append(unusedSkills, it)
				perRoot[it.Root] += it.ListingTok
			}
		}
	}
	if len(unusedSkills) > 0 {
		worst := 0
		var parts []string
		for root, tok := range perRoot {
			parts = append(parts, fmt.Sprintf("≈%d in %s", tok, root))
			worst = max(worst, tok)
		}
		sort.Strings(parts)
		sort.Slice(unusedSkills, func(i, j int) bool { return unusedSkills[i].ListingTok > unusedSkills[j].ListingTok })
		var ev []string
		for _, it := range unusedSkills[:min(len(unusedSkills), 15)] {
			ev = append(ev, fmt.Sprintf("%s (%s, %s, %d tok)", it.Name, it.Source, it.Root, it.ListingTok))
		}
		sev := Warning
		if worst > 2000 {
			sev = Serious
		}
		out = append(out, Finding{Severity: sev,
			Title:    fmt.Sprintf("%d skills and commands unused in %d days", len(unusedSkills), UnusedDays),
			Advice:   "Their names and descriptions load into every session's skill listing (" + strings.Join(parts, ", ") + " tokens). Archive what you don't use, or disable whole plugins.",
			Saving:   fmt.Sprintf("≈%d tokens/session", worst),
			Evidence: ev})
	}
	if len(unusedAgents) > 0 {
		var ev []string
		for _, it := range unusedAgents[:min(len(unusedAgents), 15)] {
			ev = append(ev, it.Name+" ("+it.Source+", "+it.Root+")")
		}
		out = append(out, Finding{Severity: Warning,
			Title:    fmt.Sprintf("%d agents never spawned in %d days", len(unusedAgents), UnusedDays),
			Advice:   "Agent descriptions are listed in every session. Remove agents you only reach through a slash command.",
			Evidence: ev})
	}

	var unusedMCP []string
	for _, in := range inv {
		for _, m := range in.MCP {
			if mcpUses(m, uses) == 0 {
				unusedMCP = append(unusedMCP, m.Name+" ("+m.Root+": "+strings.Join(m.Scopes, ", ")+")")
			}
		}
	}
	if len(unusedMCP) > 0 {
		out = append(out, Finding{Severity: Warning,
			Title:    fmt.Sprintf("%d MCP servers with no calls in %d days", len(unusedMCP), UnusedDays),
			Advice:   "Each server adds tools and a connection at startup. Remove it or scope it to the projects that use it.",
			Evidence: unusedMCP})
	}

	if turns > 0 {
		share := float64(bigTurns) / float64(turns)
		if share > 0.1 {
			sev := Warning
			if share > 0.3 {
				sev = Serious
			}
			out = append(out, Finding{Severity: sev,
				Title:  fmt.Sprintf("%s of turns run above %dK context", pct(share), BigContext/1000),
				Advice: "Every turn re-reads the whole context. /clear between unrelated tasks and keep the auto-compact window modest."})
		}
	}

	if total > 0 && r.Cost.CacheWrite/total > 0.25 {
		out = append(out, Finding{Severity: Warning,
			Title:  fmt.Sprintf("Cache writes are %s of spend", pct(r.Cost.CacheWrite/total)),
			Advice: "New sessions, model or MCP switches, compaction, and breaks longer than the cache lifetime all rewrite the context. Resume within the cache window or start fresh."})
	}

	if len(big) > 0 {
		sort.Slice(big, func(i, j int) bool { return big[i].Bytes > big[j].Bytes })
		n := 0
		var ev []string
		for _, b := range big {
			if b.Bytes < 30_000 {
				continue
			}
			n++
			if len(ev) < 8 {
				ev = append(ev, fmt.Sprintf("%dK %s %s", b.Bytes/1000, b.Tool, b.Input))
			}
		}
		if n > 0 {
			out = append(out, Finding{Severity: Warning,
				Title:    fmt.Sprintf("%d tool results over 30K characters", n),
				Advice:   "Large outputs stay in context for the rest of the session. Narrow reads and searches, or push them into a subagent.",
				Evidence: ev})
		}
	}

	var pricey []string
	pc := 0.0
	for _, a := range r.Agents {
		if strings.HasPrefix(a.Name, "(main") || a.Cost < 5 {
			continue
		}
		if strings.Contains(a.Name, "opus") || strings.Contains(a.Name, "fable") {
			pricey = append(pricey, fmt.Sprintf("%s: $%.2f", a.Name, a.Cost))
			pc += a.Cost
		}
	}
	if len(pricey) > 0 {
		out = append(out, Finding{Severity: Warning, Title: "Subagents running on top-tier models",
			Advice:   "Search-shaped subagents rarely need Opus or Fable. Pin model: sonnet or haiku in the agent definition.",
			Saving:   fmt.Sprintf("$%.2f in range", pc),
			Evidence: pricey})
	}

	if len(listing) > 0 {
		avg := sumSlice(listing) / len(listing) / 4
		if avg > 5000 {
			out = append(out, Finding{Severity: Warning,
				Title:  fmt.Sprintf("Skill listing averages ≈%d tokens per session", avg),
				Advice: "Shorten long descriptions and prune unused skills."})
		}
	}

	var heavy, longIdx []string
	for _, in := range inv {
		for _, c := range in.Context {
			switch {
			case c.Kind != "memory-index" && c.Chars/4 > 4000:
				heavy = append(heavy, fmt.Sprintf("%s (≈%d tok)", c.Path, c.Chars/4))
			case c.Kind == "memory-index" && c.Lines > 120:
				longIdx = append(longIdx, fmt.Sprintf("%s (%d lines)", c.Path, c.Lines))
			}
		}
	}
	if len(heavy) > 0 {
		out = append(out, Finding{Severity: Warning,
			Title:    fmt.Sprintf("%d always-loaded instruction files above 4K tokens", len(heavy)),
			Advice:   "Move rarely needed sections into skills or linked docs that load on demand.",
			Evidence: heavy})
	}
	if len(longIdx) > 0 {
		out = append(out, Finding{Severity: Warning,
			Title:    fmt.Sprintf("%d memory indexes over 120 lines", len(longIdx)),
			Advice:   "The index loads every session. Merge or delete stale entries.",
			Evidence: longIdx})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Severity < out[j].Severity })
	return out
}

func topCounts(m map[string]int, n int) []string {
	type kv struct {
		k string
		v int
	}
	var all []kv
	for k, v := range m {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v })
	var out []string
	for _, x := range all[:min(len(all), n)] {
		out = append(out, fmt.Sprintf("%d× %s", x.v, x.k))
	}
	return out
}

func sumInts(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func sumSlice(s []int) int {
	n := 0
	for _, v := range s {
		n += v
	}
	return n
}

func pct(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }
