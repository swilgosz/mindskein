package usage

import "time"

// Health is the setup half of a report: what each config root has installed
// that runs or loads every session, and which hooks fail.
type Health struct {
	GeneratedAt time.Time    `json:"generated_at"`
	Days        int          `json:"days"`
	HookErrors  []Count      `json:"hook_errors"`
	Roots       []RootHealth `json:"roots"`
}

// RootHealth is one config root's hooks, always-loaded instruction files and
// MCP servers.
type RootHealth struct {
	Root    string        `json:"root"`
	Hooks   []Hook        `json:"hooks"`
	Context []ContextFile `json:"context"`
	MCP     []MCPServer   `json:"mcp"`
}

// HealthOf takes the setup half out of a report.
func HealthOf(r *Report) Health {
	h := Health{GeneratedAt: r.GeneratedAt, Days: r.Days, HookErrors: r.HookErrors}
	for _, inv := range r.Inventory {
		h.Roots = append(h.Roots, RootHealth{Root: inv.Root, Hooks: inv.Hooks, Context: inv.Context, MCP: inv.MCP})
	}
	return h
}
