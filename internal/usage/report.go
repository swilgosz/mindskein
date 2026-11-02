package usage

import (
	"fmt"
	"math"
	"sort"
	"strconv"
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

	// Runs counts subagent transcripts with turns in the range; main-session
	// rows have none.
	Runs int `json:"runs,omitempty"`
}

type ToolRow struct {
	Name string `json:"name"`
	ToolStat
}

type SessionRow struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Repo       string    `json:"repo"`
	Project    string    `json:"project,omitempty"`
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

// Day is one local day: cost by model and the events counted on it.
type Day struct {
	Day    string             `json:"day"`
	Cost   map[string]float64 `json:"cost"`
	Events map[string]int     `json:"events"`
}

// Use is how often one skill, command, agent or MCP server ran.
type Use struct {
	Root  string `json:"root"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// maxBigResults caps the largest-results list; past twenty they repeat a
// pattern already visible.
const maxBigResults = 20

// Count is one named tally.
type Count struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func counts(m map[string]int) []Count {
	out := make([]Count, 0, len(m))
	for k, v := range m {
		out = append(out, Count{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Finding ids name a kind of finding. They stay the same from run to run
// whatever the numbers in the title, so a finding can be referred to later.
const (
	FindingHookFailures      = "hook-failures"
	FindingUnusedSkills      = "unused-skills"
	FindingUnusedAgents      = "unused-agents"
	FindingUnusedMCP         = "unused-mcp"
	FindingContextBloat      = "context-bloat"
	FindingCacheWrites       = "cache-writes"
	FindingBigResults        = "big-results"
	FindingTopTierSubagents  = "top-tier-subagents"
	FindingSkillListing      = "skill-listing"
	FindingHeavyInstructions = "heavy-instructions"
	FindingLongMemoryIndex   = "long-memory-index"
)

// Finding is one thing worth fixing, with the evidence behind it.
type Finding struct {
	ID       string   `json:"id"`
	Severity Severity `json:"severity"`
	Title    string   `json:"title"`
	Advice   string   `json:"advice"`
	Saving   string   `json:"saving,omitempty"`

	// Evidence is a capped, readable sample of what the finding rests on.
	Evidence []string `json:"evidence,omitempty"`

	// Items lists every thing the finding names, uncapped, for the findings
	// that name things.
	Items []FindingItem `json:"items,omitempty"`
}

// FindingItem is one thing a finding names. Key is the same on every run and
// addresses the item as <finding id>:<key>; Text is how it reads.
type FindingItem struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// Report is the JSON contract every usage surface reads.
type Report struct {
	GeneratedAt time.Time `json:"generated_at"`
	Days        int       `json:"days"`
	From        string    `json:"from"`

	// Until is the last day of a fixed window, empty when the range ends
	// today.
	Until string `json:"until,omitempty"`

	Sessions int     `json:"sessions"`
	Prompts  int     `json:"prompts"`
	Tokens   Tokens  `json:"tokens"`
	Cost     Cost    `json:"cost"`
	CacheHit float64 `json:"cache_hit"`
	SubCost  float64 `json:"subagent_cost"`

	Models      []Spend        `json:"models"`
	Accounts    []Spend        `json:"accounts"`
	Workstreams []Spend        `json:"workstreams"`
	Agents      []Spend        `json:"agents"`
	Tools       []ToolRow      `json:"tools"`
	TopSessions []SessionRow   `json:"top_sessions"`
	Events      map[string]int `json:"events"`
	Findings    []Finding      `json:"findings"`

	// Resolved is every resolution the findings were judged against, with
	// what each kept out of them. Empty when the report includes everything.
	Resolved []ResolvedEntry `json:"resolved"`

	Inventory []Inventory `json:"inventory"`

	// HookErrors counts each failing hook in the range, most failures first.
	HookErrors []Count `json:"hook_errors"`

	// Daily is every local day of the range, oldest first, days without
	// work included so a chart's gaps are real.
	Daily []Day `json:"daily"`

	// PromptHours counts prompts by local weekday, Monday first, and hour.
	PromptHours [7][24]int `json:"prompt_hours"`

	// BigResults are the largest tool results in the range, biggest first.
	BigResults []BigResult `json:"big_results"`

	// Uses counts each skill, command, agent and MCP server run in the
	// range, most first.
	Uses []Use `json:"uses"`

	// HistoryDays is how far back the store reaches. Unused-item findings are
	// only as good as it is long.
	HistoryDays int `json:"history_days"`
}

func historyDays(records []*Record, now time.Time) int {
	var first time.Time
	for _, r := range records {
		if s := r.File.Start; !s.IsZero() && (first.IsZero() || s.Before(first)) {
			first = s
		}
	}
	if first.IsZero() {
		return 0
	}
	return int(now.Sub(first).Hours()/24) + 1
}

// ForRoot keeps only the records and inventory of one config root, so a
// report can be read per account.
func ForRoot(records []*Record, inv []Inventory, root string) ([]*Record, []Inventory) {
	var rs []*Record
	for _, r := range records {
		if r.Root == root {
			rs = append(rs, r)
		}
	}
	var is []Inventory
	for _, i := range inv {
		if i.Root == root {
			is = append(is, i)
		}
	}
	return rs, is
}

// Window is the range of local days a report covers. Since and Until are
// YYYY-MM-DD and both included. Without Since the range is the last Days days
// up to Until, or up to today; Days 0 then means all time.
type Window struct {
	Days  int
	Since string
	Until string
}

// ParseWindow checks a requested range, naming the flag at fault, so a typo
// is an error rather than a report of nothing.
func ParseWindow(days int, since, until string) (Window, error) {
	if days < 0 {
		return Window{}, fmt.Errorf("--days %d: want 0 or more", days)
	}
	for _, d := range [...]struct{ flag, value string }{{"--since", since}, {"--until", until}} {
		if _, err := time.Parse(time.DateOnly, d.value); d.value != "" && err != nil {
			return Window{}, fmt.Errorf("%s %q: want a date as YYYY-MM-DD", d.flag, d.value)
		}
	}
	if since != "" && until != "" && since > until {
		return Window{}, fmt.Errorf("--since %s is after --until %s", since, until)
	}
	return Window{Days: days, Since: since, Until: until}, nil
}

// Build aggregates records over the last days (0 means all time) and judges
// them against the inventory.
func Build(records []*Record, inv []Inventory, now time.Time, days int, loc *time.Location) *Report {
	return BuildWindow(records, inv, now, Window{Days: days}, loc)
}

// BuildWindow is Build over any window. Unused-item findings still look back
// from now: they judge the setup as it is installed today.
func BuildWindow(records []*Record, inv []Inventory, now time.Time, w Window, loc *time.Location) *Report {
	return BuildResolved(records, inv, now, w, loc, nil)
}

// BuildResolved is BuildWindow with the findings and items the resolutions
// cover left out until they happen again. Only the findings change: every
// other section still counts everything in the window.
func BuildResolved(records []*Record, inv []Inventory, now time.Time, w Window, loc *time.Location, resolved []Resolution) *Report {
	if loc == nil {
		loc = time.Local
	}
	today := now.In(loc).Format(time.DateOnly)
	last, _ := time.ParseInLocation(time.DateOnly, today, loc)
	if w.Until != "" {
		if t, err := time.ParseInLocation(time.DateOnly, w.Until, loc); err == nil {
			last = t
		}
	}
	r := &Report{GeneratedAt: now, Days: w.Days, Until: w.Until, Events: map[string]int{}, Inventory: inv,
		Resolved: make([]ResolvedEntry, len(resolved))}
	for i, res := range resolved {
		r.Resolved[i].Resolution = res
	}
	rs := newResolutions(r.Resolved)
	from := w.Since
	if from == "" && w.Days > 0 {
		from = last.AddDate(0, 0, -w.Days+1).Format(time.DateOnly)
	}
	r.From = from
	if first, err := time.ParseInLocation(time.DateOnly, w.Since, loc); err == nil {
		r.Days = int(math.Round(last.Sub(first).Hours()/24)) + 1
		r.Until = last.Format(time.DateOnly)
	}
	until := r.Until
	inRange := func(day string) bool { return day != "" && day >= from && (until == "" || day <= until) }
	cutoff90 := now.In(loc).AddDate(0, 0, -UnusedDays+1).Format(time.DateOnly)

	models := map[string]*Spend{}
	repos := map[string]*Spend{}
	accounts := map[string]*Spend{}
	agents := map[string]*Spend{}
	tools := map[string]*ToolStat{}
	hookErrors := map[string]int{}
	hooksShown := map[string]int{}
	subagentsShown := map[string]float64{}
	uses90 := map[string]int{} // root|kind:name
	usesInRange := map[string]int{}
	dailyCost := map[string]map[string]float64{}
	dailyEvents := map[string]map[string]int{}
	var big, bigShown []BigResult
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

		ws := rec.Repo
		if rec.Project != "" {
			ws = rec.Project
		}
		row := SessionRow{ID: rec.SessionID, Title: f.DisplayTitle(), Repo: rec.Repo, Project: rec.Project, Start: f.Start,
			ContextMax: f.ContextMax, ContextBig: f.ContextBig}
		active := false
		for _, x := range files {
			ran := map[string]bool{}
			runCost := map[string]float64{}
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
					if dailyCost[day] == nil {
						dailyCost[day] = map[string]float64{}
					}
					dailyCost[day][ModelName(model)] += c.Total()
					addSpend(repos, orUnknown(ws), c.Total(), t)
					addSpend(accounts, orUnknown(rec.Root), c.Total(), t)
					agent := x.agent
					if agent == "" {
						agent = "(main session)"
					} else {
						r.SubCost += c.Total()
						row.SubCost += c.Total()
						ran[agent+" on "+ModelName(model)] = true
						runCost[agent+" on "+ModelName(model)] += c.Total()
					}
					addSpend(agents, agent+" on "+ModelName(model), c.Total(), t)
					row.Cost += c.Total()
					row.Turns += t.Turns
				}
			}
			for name := range ran {
				agents[name].Runs++
			}
			started := x.file.Start
			if started.IsZero() {
				started = f.Start
			}
			for name, c := range runCost {
				if e := rs.hides(FindingTopTierSubagents, name, started); e != nil {
					if topTier(name) {
						e.Hidden++
					}
					continue
				}
				subagentsShown[name] += c
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
					if inRange(day) {
						usesInRange[rec.Root+"|"+k] += n
					}
				}
			}
			for _, b := range x.file.Big {
				if !inRange(b.Day) {
					continue
				}
				b.At = x.file.ResultAt(b)
				big = append(big, b)
				if e := rs.hides(FindingBigResults, "", b.At); e == nil {
					bigShown = append(bigShown, b)
				} else if b.Bytes >= bigResultChars {
					e.Hidden++
				}
			}
		}
		for day, ev := range f.Events {
			if !inRange(day) {
				continue
			}
			if dailyEvents[day] == nil {
				dailyEvents[day] = map[string]int{}
			}
			for k, n := range ev {
				r.Events[k] += n
				dailyEvents[day][k] += n
				if k == EventPrompts {
					row.Prompts += n
				}
			}
		}
		if !active && row.Prompts == 0 {
			continue
		}
		for day, hours := range f.Hours {
			if !inRange(day) {
				continue
			}
			d, err := time.ParseInLocation(time.DateOnly, day, loc)
			if err != nil {
				continue
			}
			for h, n := range hours {
				if hour, err := strconv.Atoi(h); err == nil && hour >= 0 && hour < 24 {
					r.PromptHours[(int(d.Weekday())+6)%7][hour] += n
				}
			}
		}
		r.Sessions++
		r.Prompts += row.Prompts
		turns += row.Turns
		bigTurns += f.ContextBig
		for k, n := range f.HookErrors {
			hookErrors[k] += n
			if e := rs.hides(FindingHookFailures, k, f.HookFailedAt(k)); e != nil {
				e.Hidden += n
			} else {
				hooksShown[k] += n
			}
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
	r.Accounts = ranked(accounts, total)
	r.Agents = ranked(agents, total)
	for name, s := range tools {
		r.Tools = append(r.Tools, ToolRow{Name: name, ToolStat: *s})
	}
	sort.Slice(r.Tools, func(i, j int) bool { return r.Tools[i].Calls > r.Tools[j].Calls })
	sort.Slice(r.TopSessions, func(i, j int) bool { return r.TopSessions[i].Cost > r.TopSessions[j].Cost })
	if len(r.TopSessions) > 20 {
		r.TopSessions = r.TopSessions[:20]
	}

	r.HookErrors = counts(hookErrors)
	r.Daily = dailyRows(from, last, dailyCost, dailyEvents)
	r.BigResults = append([]BigResult(nil), big...)
	sort.SliceStable(r.BigResults, func(i, j int) bool { return r.BigResults[i].Bytes > r.BigResults[j].Bytes })
	r.BigResults = r.BigResults[:min(len(r.BigResults), maxBigResults)]
	r.Uses = usesOf(usesInRange)
	r.Inventory = withUses(inv, uses90)
	r.Findings = findings(r, rs, inv, uses90, hooksShown, bigShown, subagentsShown, listing, turns, bigTurns)
	r.HistoryDays = historyDays(records, now)
	if r.HistoryDays < UnusedDays {
		caveat := fmt.Sprintf("Recorded history covers only %d days, so this counts as unused anything not used in those days. `mindskein usage --backfill` adds transcripts still on disk, but Claude Code deletes older ones per `cleanupPeriodDays`; from here on the store keeps every session, so the full %d days fill in as you work.", r.HistoryDays, UnusedDays)
		for i := range r.Findings {
			if id := r.Findings[i].ID; id == FindingUnusedSkills || id == FindingUnusedAgents || id == FindingUnusedMCP {
				r.Findings[i].Severity = Warning
				r.Findings[i].Advice += " " + caveat
			}
		}
		sort.SliceStable(r.Findings, func(i, j int) bool { return r.Findings[i].Severity < r.Findings[j].Severity })
	}
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

// topTier says whether a subagent row runs on a model search-shaped work
// rarely needs.
func topTier(row string) bool {
	return !strings.HasPrefix(row, "(main") && (strings.Contains(row, "opus") || strings.Contains(row, "fable"))
}

// bigResultChars is the size from which a tool result is worth a finding.
const bigResultChars = 30_000

// findings judges what the report counted. hookErrors, big and subagents
// hold only what rs leaves in: they are the occurrences the findings count.
func findings(r *Report, rs resolutions, inv []Inventory, uses map[string]int, hookErrors map[string]int,
	big []BigResult, subagents map[string]float64, listing []int, turns int64, bigTurns int) []Finding {
	var out []Finding
	total := r.Cost.Total()
	shown := func(id, key string) bool {
		if e := rs.hides(id, key, time.Time{}); e != nil {
			e.Hidden++
			return false
		}
		return true
	}

	if n := sumInts(hookErrors); n > 0 {
		sev := Serious
		if n > 50 {
			sev = Critical
		}
		var items []FindingItem
		for _, c := range counts(hookErrors) {
			items = append(items, FindingItem{Key: c.Name, Text: fmt.Sprintf("%d× %s", c.Count, c.Name)})
		}
		out = append(out, Finding{ID: FindingHookFailures, Severity: sev, Title: fmt.Sprintf("%d hook failures", n),
			Advice:   "A hook that fails every session start adds latency and its context never arrives. Fix the path or remove the hook or plugin.",
			Evidence: topCounts(hookErrors, 8), Items: items})
	}

	var unusedSkills, unusedAgents []Item
	perRoot := map[string]int{}
	for _, in := range inv {
		for _, it := range in.Items {
			if !it.PluginOn || itemUses(it, uses) > 0 {
				continue
			}
			if it.Kind == "agent" {
				if shown(FindingUnusedAgents, it.Name) {
					unusedAgents = append(unusedAgents, it)
				}
			} else if shown(FindingUnusedSkills, it.Name) {
				unusedSkills = append(unusedSkills, it)
				perRoot[it.Root] += it.ListingTok
			}
		}
	}
	if len(unusedSkills) > 0 {
		worst := 0
		var parts []string
		for root, tok := range perRoot {
			parts = append(parts, fmt.Sprintf("≈%d tokens in %s", tok, root))
			worst = max(worst, tok)
		}
		sort.Strings(parts)
		sort.Slice(unusedSkills, func(i, j int) bool {
			if unusedSkills[i].ListingTok != unusedSkills[j].ListingTok {
				return unusedSkills[i].ListingTok > unusedSkills[j].ListingTok
			}
			return unusedSkills[i].Name < unusedSkills[j].Name
		})
		var ev []string
		var items []FindingItem
		for i, it := range unusedSkills {
			text := fmt.Sprintf("%s (%s, %s, %d tok)", it.Name, it.Source, it.Root, it.ListingTok)
			if i < 15 {
				ev = append(ev, text)
			}
			items = append(items, FindingItem{Key: it.Name, Text: text})
		}
		sev := Warning
		if worst > 2000 {
			sev = Serious
		}
		out = append(out, Finding{ID: FindingUnusedSkills, Severity: sev,
			Title:    fmt.Sprintf("%d skills and commands unused in %d days", len(unusedSkills), UnusedDays),
			Advice:   "Their names and descriptions load into every session's skill listing (" + strings.Join(parts, ", ") + "). Archive what you don't use, or disable whole plugins.",
			Saving:   fmt.Sprintf("≈%d tokens/session", worst),
			Evidence: ev, Items: items})
	}
	if len(unusedAgents) > 0 {
		var ev []string
		var items []FindingItem
		for i, it := range unusedAgents {
			text := it.Name + " (" + it.Source + ", " + it.Root + ")"
			if i < 15 {
				ev = append(ev, text)
			}
			items = append(items, FindingItem{Key: it.Name, Text: text})
		}
		out = append(out, Finding{ID: FindingUnusedAgents, Severity: Warning,
			Title:    fmt.Sprintf("%d agents never spawned in %d days", len(unusedAgents), UnusedDays),
			Advice:   "Agent descriptions are listed in every session. Remove agents you only reach through a slash command.",
			Evidence: ev, Items: items})
	}

	var unusedMCP []string
	var mcpItems []FindingItem
	for _, in := range inv {
		for _, m := range in.MCP {
			if mcpUses(m, uses) == 0 && shown(FindingUnusedMCP, m.Name) {
				text := m.Name + " (" + m.Root + ": " + strings.Join(m.Scopes, ", ") + ")"
				unusedMCP = append(unusedMCP, text)
				mcpItems = append(mcpItems, FindingItem{Key: m.Name, Text: text})
			}
		}
	}
	if len(unusedMCP) > 0 {
		out = append(out, Finding{ID: FindingUnusedMCP, Severity: Warning,
			Title:    fmt.Sprintf("%d MCP servers with no calls in %d days", len(unusedMCP), UnusedDays),
			Advice:   "Each server adds tools and a connection at startup. Remove it or scope it to the projects that use it.",
			Evidence: unusedMCP, Items: mcpItems})
	}

	if turns > 0 {
		share := float64(bigTurns) / float64(turns)
		if share > 0.1 && shown(FindingContextBloat, "") {
			sev := Warning
			if share > 0.3 {
				sev = Serious
			}
			out = append(out, Finding{ID: FindingContextBloat, Severity: sev,
				Title:  fmt.Sprintf("%s of turns run above %dK context", pct(share), BigContext/1000),
				Advice: "Every turn re-reads the whole context. /clear between unrelated tasks and keep the auto-compact window modest."})
		}
	}

	if total > 0 && r.Cost.CacheWrite/total > 0.25 && shown(FindingCacheWrites, "") {
		out = append(out, Finding{ID: FindingCacheWrites, Severity: Warning,
			Title:  fmt.Sprintf("Cache writes are %s of spend", pct(r.Cost.CacheWrite/total)),
			Advice: "New sessions, model or MCP switches, compaction, and breaks longer than the cache lifetime all rewrite the context. Resume within the cache window or start fresh."})
	}

	if len(big) > 0 {
		sort.Slice(big, func(i, j int) bool { return big[i].Bytes > big[j].Bytes })
		n := 0
		var ev []string
		for _, b := range big {
			if b.Bytes < bigResultChars {
				continue
			}
			n++
			if len(ev) < 8 {
				ev = append(ev, fmt.Sprintf("%dK %s %s", b.Bytes/1000, b.Tool, b.Input))
			}
		}
		if n > 0 {
			out = append(out, Finding{ID: FindingBigResults, Severity: Warning,
				Title:    fmt.Sprintf("%d tool results over 30K characters", n),
				Advice:   "Large outputs stay in context for the rest of the session. Narrow reads and searches, or push them into a subagent.",
				Evidence: ev})
		}
	}

	var pricey []string
	var priceyItems []FindingItem
	pc := 0.0
	for _, a := range ranked(spendOf(subagents), 0) {
		if a.Cost < 5 || !topTier(a.Name) {
			continue
		}
		text := fmt.Sprintf("%s: $%.2f", a.Name, a.Cost)
		pricey = append(pricey, text)
		priceyItems = append(priceyItems, FindingItem{Key: a.Name, Text: text})
		pc += a.Cost
	}
	if len(pricey) > 0 {
		out = append(out, Finding{ID: FindingTopTierSubagents, Severity: Warning, Title: "Subagents running on top-tier models",
			Advice:   "Search-shaped subagents rarely need Opus or Fable. Pin model: sonnet or haiku in the agent definition.",
			Saving:   fmt.Sprintf("$%.2f in range", pc),
			Evidence: pricey, Items: priceyItems})
	}

	if len(listing) > 0 {
		avg := sumSlice(listing) / len(listing) / 4
		if avg > 5000 && shown(FindingSkillListing, "") {
			out = append(out, Finding{ID: FindingSkillListing, Severity: Warning,
				Title:  fmt.Sprintf("Skill listing averages ≈%d tokens per session", avg),
				Advice: "Shorten long descriptions and prune unused skills."})
		}
	}

	var heavy, longIdx []string
	var heavyItems, longIdxItems []FindingItem
	for _, in := range inv {
		for _, c := range in.Context {
			switch {
			case c.Kind != "memory-index" && c.Chars/4 > 4000 && shown(FindingHeavyInstructions, c.Path):
				text := fmt.Sprintf("%s (≈%d tok)", c.Path, c.Chars/4)
				heavy = append(heavy, text)
				heavyItems = append(heavyItems, FindingItem{Key: c.Path, Text: text})
			case c.Kind == "memory-index" && c.Lines > 120 && shown(FindingLongMemoryIndex, c.Path):
				text := fmt.Sprintf("%s (%d lines)", c.Path, c.Lines)
				longIdx = append(longIdx, text)
				longIdxItems = append(longIdxItems, FindingItem{Key: c.Path, Text: text})
			}
		}
	}
	if len(heavy) > 0 {
		out = append(out, Finding{ID: FindingHeavyInstructions, Severity: Warning,
			Title:    fmt.Sprintf("%d always-loaded instruction files above 4K tokens", len(heavy)),
			Advice:   "Move rarely needed sections into skills or linked docs that load on demand.",
			Evidence: heavy, Items: heavyItems})
	}
	if len(longIdx) > 0 {
		out = append(out, Finding{ID: FindingLongMemoryIndex, Severity: Warning,
			Title:    fmt.Sprintf("%d memory indexes over 120 lines", len(longIdx)),
			Advice:   "The index loads every session. Merge or delete stale entries.",
			Evidence: longIdx, Items: longIdxItems})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Severity < out[j].Severity })
	return out
}

func spendOf(costs map[string]float64) map[string]*Spend {
	out := make(map[string]*Spend, len(costs))
	for name, c := range costs {
		out[name] = &Spend{Name: name, Cost: c}
	}
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

// dailyRows lays out every day from the first through today. An all-time
// range starts at the first day anything happened.
func dailyRows(from string, end time.Time, cost map[string]map[string]float64, events map[string]map[string]int) []Day {
	if from == "" {
		for day := range cost {
			if from == "" || day < from {
				from = day
			}
		}
		for day := range events {
			if from == "" || day < from {
				from = day
			}
		}
	}
	start, err := time.ParseInLocation(time.DateOnly, from, end.Location())
	if err != nil {
		return []Day{}
	}
	last := end.Format(time.DateOnly)
	out := []Day{}
	for d := start; ; d = d.AddDate(0, 0, 1) {
		day := d.Format(time.DateOnly)
		if day > last {
			break
		}
		row := Day{Day: day, Cost: cost[day], Events: events[day]}
		if row.Cost == nil {
			row.Cost = map[string]float64{}
		}
		if row.Events == nil {
			row.Events = map[string]int{}
		}
		out = append(out, row)
	}
	return out
}

// usesOf turns root|kind:name counts into rows, most first.
func usesOf(m map[string]int) []Use {
	out := []Use{}
	for key, n := range m {
		root, kindName, _ := strings.Cut(key, "|")
		kind, name, _ := strings.Cut(kindName, ":")
		out = append(out, Use{Root: root, Kind: kind, Name: name, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Kind+out[i].Name < out[j].Kind+out[j].Name
	})
	return out
}

// withUses copies the inventory with each item's and server's 90-day uses
// filled in, leaving the caller's slices alone.
func withUses(inv []Inventory, uses90 map[string]int) []Inventory {
	out := make([]Inventory, len(inv))
	for i, in := range inv {
		in.Items = append([]Item(nil), in.Items...)
		for j := range in.Items {
			in.Items[j].Uses90 = itemUses(in.Items[j], uses90)
		}
		in.MCP = append([]MCPServer(nil), in.MCP...)
		for j := range in.MCP {
			in.MCP[j].Uses90 = mcpUses(in.MCP[j], uses90)
		}
		out[i] = in
	}
	return out
}
