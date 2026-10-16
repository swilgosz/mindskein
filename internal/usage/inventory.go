package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Item is something installed that a session can use: a skill, a slash
// command, or an agent.
type Item struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Short      string `json:"short"`
	Source     string `json:"source"`
	Root       string `json:"root"`
	Path       string `json:"path"`
	DescChars  int    `json:"desc_chars"`
	Model      string `json:"model,omitempty"`
	PluginOn   bool   `json:"plugin_enabled"`
	ListingTok int    `json:"listing_tokens"`
}

type MCPServer struct {
	Name   string   `json:"name"`
	Root   string   `json:"root"`
	Scopes []string `json:"scopes"`
}

type Hook struct {
	Root    string `json:"root"`
	Event   string `json:"event"`
	Matcher string `json:"matcher,omitempty"`
	Command string `json:"command"`
}

// ContextFile is instruction text loaded into every session.
type ContextFile struct {
	Root  string `json:"root"`
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Lines int    `json:"lines"`
	Chars int    `json:"chars"`
}

// Inventory is what one config root has installed.
type Inventory struct {
	Root    string        `json:"root"`
	Items   []Item        `json:"items"`
	MCP     []MCPServer   `json:"mcp"`
	Hooks   []Hook        `json:"hooks"`
	Context []ContextFile `json:"context"`
}

// Scan reads a config root. cwds are the folders sessions ran in, whose
// project instruction files also load every session.
func Scan(root string, cwds []string) Inventory {
	inv := Inventory{Root: root}
	inv.Items = append(inv.Items, skills(filepath.Join(root, "skills"), "", root, "user", true)...)
	inv.Items = append(inv.Items, markdown(filepath.Join(root, "agents"), "agent", "", root, "user", true)...)
	inv.Items = append(inv.Items, markdown(filepath.Join(root, "commands"), "command", "", root, "user", true)...)

	settings := readJSON(filepath.Join(root, "settings.json"))
	enabled, _ := settings["enabledPlugins"].(map[string]any)
	mcp := map[string]*MCPServer{}
	addMCP := func(name, scope string) {
		m := mcp[name]
		if m == nil {
			m = &MCPServer{Name: name, Root: root}
			mcp[name] = m
		}
		m.Scopes = append(m.Scopes, scope)
	}

	installed := readJSON(filepath.Join(root, "plugins", "installed_plugins.json"))
	plugins, _ := installed["plugins"].(map[string]any)
	for key, v := range plugins {
		name, _, _ := strings.Cut(key, "@")
		on := true
		if b, ok := enabled[key].(bool); ok {
			on = b
		}
		list, _ := v.([]any)
		if len(list) == 0 {
			continue
		}
		first, _ := list[0].(map[string]any)
		path, _ := first["installPath"].(string)
		if path == "" {
			continue
		}
		src := "plugin:" + name
		inv.Items = append(inv.Items, skills(filepath.Join(path, "skills"), name, root, src, on)...)
		inv.Items = append(inv.Items, markdown(filepath.Join(path, "agents"), "agent", name, root, src, on)...)
		inv.Items = append(inv.Items, markdown(filepath.Join(path, "commands"), "command", name, root, src, on)...)
		pm := readJSON(filepath.Join(path, ".mcp.json"))
		servers, ok := pm["mcpServers"].(map[string]any)
		if !ok {
			servers = pm
		}
		if on {
			for s := range servers {
				addMCP(s, src)
			}
		}
	}

	cj := readJSON(claudeJSON(root))
	if servers, ok := cj["mcpServers"].(map[string]any); ok {
		for s := range servers {
			addMCP(s, "user")
		}
	}
	if projects, ok := cj["projects"].(map[string]any); ok {
		for p, v := range projects {
			pc, _ := v.(map[string]any)
			servers, _ := pc["mcpServers"].(map[string]any)
			for s := range servers {
				addMCP(s, "project:"+p)
			}
		}
	}
	for _, m := range mcp {
		sort.Strings(m.Scopes)
		inv.MCP = append(inv.MCP, *m)
	}
	sort.Slice(inv.MCP, func(i, j int) bool { return inv.MCP[i].Name < inv.MCP[j].Name })

	for _, name := range []string{"settings.json", "settings.local.json"} {
		inv.Hooks = append(inv.Hooks, hooks(root, readJSON(filepath.Join(root, name)))...)
	}
	inv.Context = contextFiles(root, cwds)
	for i := range inv.Items {
		it := &inv.Items[i]
		if it.Kind != "agent" {
			it.ListingTok = (len(it.Name) + it.DescChars + 4) / 4
		}
	}
	return inv
}

// claudeJSON is where a root keeps its MCP servers. The default root keeps it
// beside the directory rather than inside it.
func claudeJSON(root string) string {
	if home, err := os.UserHomeDir(); err == nil && root == filepath.Join(home, ".claude") {
		return filepath.Join(home, ".claude.json")
	}
	return filepath.Join(root, ".claude.json")
}

func readJSON(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return m
}

var frontKey = regexp.MustCompile(`^([A-Za-z_-]+):\s*(.*)$`)

// frontmatter reads the YAML header loosely: only scalar keys, with indented
// continuation lines folded into the previous value.
func frontmatter(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := string(data)
	if !strings.HasPrefix(text, "---") {
		return map[string]string{}
	}
	end := strings.Index(text[3:], "\n---")
	if end < 0 {
		return map[string]string{}
	}
	out := map[string]string{}
	key := ""
	for _, ln := range strings.Split(text[3:3+end], "\n") {
		if m := frontKey.FindStringSubmatch(ln); m != nil {
			key = m[1]
			out[key] = strings.Trim(strings.TrimSpace(m[2]), `"'`)
		} else if key != "" && (strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "\t")) {
			out[key] = strings.TrimSpace(out[key] + " " + strings.TrimSpace(ln))
		}
	}
	return out
}

func qualify(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + ":" + name
}

func skills(dir, prefix, root, source string, on bool) []Item {
	var out []Item
	walkSkills(dir, 0, func(p string) {
		fm := frontmatter(p)
		name := fm["name"]
		if name == "" {
			name = filepath.Base(filepath.Dir(p))
		}
		out = append(out, Item{Kind: "skill", Name: qualify(prefix, name), Short: name, Source: source, Root: root,
			Path: p, DescChars: len(fm["description"]), Model: fm["model"], PluginOn: on})
	})
	return out
}

// maxSkillDepth bounds the walk. Skill directories are commonly symlinks into
// a notes vault, so links are followed, and a cycle must not hang the report.
const maxSkillDepth = 6

func walkSkills(dir string, depth int, found func(string)) {
	if depth > maxSkillDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.Name() == "SKILL.md" {
			found(p)
			continue
		}
		if e.Name() == "node_modules" || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			walkSkills(p, depth+1, found)
		}
	}
}

func markdown(dir, kind, prefix, root, source string, on bool) []Item {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	var out []Item
	for _, p := range paths {
		fm := frontmatter(p)
		name := fm["name"]
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(p), ".md")
		}
		out = append(out, Item{Kind: kind, Name: qualify(prefix, name), Short: name, Source: source, Root: root,
			Path: p, DescChars: len(fm["description"]), Model: fm["model"], PluginOn: on})
	}
	return out
}

func hooks(root string, settings map[string]any) []Hook {
	events, _ := settings["hooks"].(map[string]any)
	var out []Hook
	for event, v := range events {
		groups, _ := v.([]any)
		for _, g := range groups {
			gm, _ := g.(map[string]any)
			matcher, _ := gm["matcher"].(string)
			hs, _ := gm["hooks"].([]any)
			for _, h := range hs {
				hm, _ := h.(map[string]any)
				cmd, _ := hm["command"].(string)
				out = append(out, Hook{Root: root, Event: event, Matcher: matcher, Command: cmd})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Event+out[i].Command < out[j].Event+out[j].Command })
	return out
}

func contextFiles(root string, cwds []string) []ContextFile {
	var out []ContextFile
	seen := map[string]bool{}
	add := func(kind, path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		out = append(out, ContextFile{Root: root, Kind: kind, Path: path, Lines: strings.Count(string(data), "\n"), Chars: len(data)})
	}
	add("global", filepath.Join(root, "CLAUDE.md"))
	mems, _ := filepath.Glob(filepath.Join(root, "projects", "*", "memory", "MEMORY.md"))
	for _, m := range mems {
		add("memory-index", m)
	}
	for _, cwd := range cwds {
		for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
			add("project", filepath.Join(cwd, name))
		}
	}
	return out
}
