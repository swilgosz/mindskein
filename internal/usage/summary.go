// Package usage summarises Claude Code transcripts into what a session cost
// and how it worked — tokens, tools, skills, friction — and reports what to
// improve across sessions.
package usage

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Tokens is one bucket of model usage. Turns counts assistant messages, not
// transcript lines: a message is written once per content block.
type Tokens struct {
	Turns        int64 `json:"turns"`
	Input        int64 `json:"input"`
	Output       int64 `json:"output"`
	CacheRead    int64 `json:"cache_read"`
	CacheWrite5m int64 `json:"cache_write_5m"`
	CacheWrite1h int64 `json:"cache_write_1h"`
	Thinking     int64 `json:"thinking"`
}

func (t *Tokens) add(o Tokens, sign int64) {
	t.Turns += sign * o.Turns
	t.Input += sign * o.Input
	t.Output += sign * o.Output
	t.CacheRead += sign * o.CacheRead
	t.CacheWrite5m += sign * o.CacheWrite5m
	t.CacheWrite1h += sign * o.CacheWrite1h
	t.Thinking += sign * o.Thinking
}

// InputAll is every input token the model read, cached or not.
func (t Tokens) InputAll() int64 {
	return t.Input + t.CacheRead + t.CacheWrite5m + t.CacheWrite1h
}

type ToolStat struct {
	Calls       int   `json:"calls"`
	Errors      int   `json:"errors"`
	ResultBytes int64 `json:"result_bytes"`
}

// BigResult is a tool result large enough to matter for context size.
type BigResult struct {
	Day   string `json:"day"`
	Tool  string `json:"tool"`
	Input string `json:"input"`
	Bytes int    `json:"bytes"`
}

// File is the summary of one transcript. It is built incrementally: Pending
// and Last carry the state a later chunk of the same file needs.
type File struct {
	SessionID   string    `json:"session_id"`
	CWD         string    `json:"cwd"`
	Title       string    `json:"title"`
	CustomTitle string    `json:"custom_title,omitempty"`
	FirstPrompt string    `json:"first_prompt,omitempty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`

	ContextMax   int64 `json:"context_max"`
	ContextBig   int   `json:"context_big"`
	ListingChars int   `json:"listing_chars"`

	// Keyed by local day, then by model, tool, use or event name.
	Usage  map[string]map[string]Tokens   `json:"usage"`
	Tools  map[string]map[string]ToolStat `json:"tools"`
	Uses   map[string]map[string]int      `json:"uses"`
	Events map[string]map[string]int      `json:"events"`

	HookErrors map[string]int `json:"hook_errors,omitempty"`
	Big        []BigResult    `json:"big,omitempty"`

	Pending map[string]pendingTool `json:"pending,omitempty"`
	Last    *lastMessage           `json:"last,omitempty"`
}

type pendingTool struct {
	Name  string `json:"name"`
	Input string `json:"input,omitempty"`
}

type lastMessage struct {
	ID     string `json:"id"`
	Day    string `json:"day"`
	Model  string `json:"model"`
	Tokens Tokens `json:"tokens"`
}

// Context thresholds: a turn above BigContext re-reads that much from cache,
// and a result above BigResultBytes stays in context for the rest of the
// session.
const (
	BigContext     = 150_000
	BigResultBytes = 20_000
	maxBig         = 15
	maxPending     = 500
	maxHint        = 140
)

// Event names stored per day.
const (
	EventPrompts       = "prompts"
	EventInterrupts    = "interrupts"
	EventDenials       = "denials"
	EventCompactAuto   = "compact_auto"
	EventCompactManual = "compact_manual"
	EventAPIErrors     = "api_errors"
	EventHookErrors    = "hook_errors"
)

// Use kinds, prefixed onto the name: "skill:worktree".
const (
	UseSkill   = "skill"
	UseCommand = "command"
	UseAgent   = "agent"
	UseMCP     = "mcp"
	UseBash    = "bash"
)

func newFile() *File {
	return &File{
		Usage:   map[string]map[string]Tokens{},
		Tools:   map[string]map[string]ToolStat{},
		Uses:    map[string]map[string]int{},
		Events:  map[string]map[string]int{},
		Pending: map[string]pendingTool{},
	}
}

// ensure restores the maps a decoded record may lack.
func (f *File) ensure() {
	if f.Usage == nil {
		f.Usage = map[string]map[string]Tokens{}
	}
	if f.Tools == nil {
		f.Tools = map[string]map[string]ToolStat{}
	}
	if f.Uses == nil {
		f.Uses = map[string]map[string]int{}
	}
	if f.Events == nil {
		f.Events = map[string]map[string]int{}
	}
	if f.Pending == nil {
		f.Pending = map[string]pendingTool{}
	}
}

func bump[V any](m map[string]map[string]V, day, key string, fn func(*V)) {
	inner := m[day]
	if inner == nil {
		inner = map[string]V{}
		m[day] = inner
	}
	v := inner[key]
	fn(&v)
	inner[key] = v
}

func (f *File) event(day, name string, n int) {
	if day == "" {
		return
	}
	bump(f.Events, day, name, func(v *int) { *v += n })
}

func (f *File) use(day, kind, name string) {
	if day == "" || name == "" {
		return
	}
	bump(f.Uses, day, kind+":"+name, func(v *int) { *v++ })
}

// Summarise reads a whole transcript.
func Summarise(r io.Reader, loc *time.Location) (*File, error) {
	f := newFile()
	if _, err := f.consume(r, loc); err != nil {
		return nil, err
	}
	return f, nil
}

// consume folds every complete line from r into f and returns the bytes it
// consumed. A trailing line without a newline is left for the next call: the
// transcript is being appended to while it is read.
func (f *File) consume(r io.Reader, loc *time.Location) (int64, error) {
	f.ensure()
	br := bufio.NewReaderSize(r, 64*1024)
	var n int64
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return n, nil
			}
			return n, fmt.Errorf("reading transcript: %w", err)
		}
		n += int64(len(line))
		f.line(line, loc)
	}
}

type record struct {
	Type         string          `json:"type"`
	Subtype      string          `json:"subtype"`
	Timestamp    time.Time       `json:"timestamp"`
	SessionID    string          `json:"sessionId"`
	CWD          string          `json:"cwd"`
	PromptSource string          `json:"promptSource"`
	IsMeta       bool            `json:"isMeta"`
	AITitle      string          `json:"aiTitle"`
	CustomTitle  string          `json:"customTitle"`
	Message      json.RawMessage `json:"message"`
	Attachment   json.RawMessage `json:"attachment"`
	Compact      *struct {
		Trigger string `json:"trigger"`
	} `json:"compactMetadata"`
}

type message struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *struct {
		Input         int64 `json:"input_tokens"`
		Output        int64 `json:"output_tokens"`
		CacheRead     int64 `json:"cache_read_input_tokens"`
		CacheCreation int64 `json:"cache_creation_input_tokens"`
		Split         *struct {
			FiveMin int64 `json:"ephemeral_5m_input_tokens"`
			OneHour int64 `json:"ephemeral_1h_input_tokens"`
		} `json:"cache_creation"`
		Details *struct {
			Thinking int64 `json:"thinking_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Text      string          `json:"text"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

type attachment struct {
	Type     string `json:"type"`
	HookName string `json:"hookName"`
	Event    string `json:"hookEvent"`
	Command  string `json:"command"`
	Content  string `json:"content"`
}

var commandName = regexp.MustCompile(`<command-name>/?([^<]+)</command-name>`)

const (
	denialPrefix = "The user doesn't want to proceed"
	interrupted  = "[Request interrupted by user"
)

func (f *File) line(line string, loc *time.Location) {
	var rec record
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return
	}
	switch rec.Type {
	case "ai-title":
		if rec.AITitle != "" {
			f.Title = rec.AITitle
		}
		return
	case "custom-title":
		if rec.CustomTitle != "" {
			f.CustomTitle = rec.CustomTitle
		}
		return
	}

	var day string
	if !rec.Timestamp.IsZero() {
		ts := rec.Timestamp
		day = ts.In(loc).Format(time.DateOnly)
		if f.Start.IsZero() || ts.Before(f.Start) {
			f.Start = ts
		}
		if ts.After(f.End) {
			f.End = ts
		}
	}
	if f.SessionID == "" && rec.SessionID != "" {
		f.SessionID = rec.SessionID
	}
	if f.CWD == "" && rec.CWD != "" {
		f.CWD = rec.CWD
	}

	switch rec.Type {
	case "assistant":
		f.assistant(rec, day)
	case "user":
		f.user(rec, day, len(line))
	case "system":
		switch rec.Subtype {
		case "compact_boundary":
			if rec.Compact != nil && rec.Compact.Trigger == "manual" {
				f.event(day, EventCompactManual, 1)
			} else {
				f.event(day, EventCompactAuto, 1)
			}
		case "api_error":
			f.event(day, EventAPIErrors, 1)
		}
	case "attachment":
		var a attachment
		if json.Unmarshal(rec.Attachment, &a) != nil {
			return
		}
		switch a.Type {
		case "hook_non_blocking_error":
			f.event(day, EventHookErrors, 1)
			name := a.HookName
			if name == "" {
				name = a.Event
			}
			if f.HookErrors == nil {
				f.HookErrors = map[string]int{}
			}
			f.HookErrors[name+" :: "+clip(a.Command, 200)]++
		case "skill_listing":
			if len(a.Content) > f.ListingChars {
				f.ListingChars = len(a.Content)
			}
		}
	}
}

func (f *File) assistant(rec record, day string) {
	var m message
	if json.Unmarshal(rec.Message, &m) != nil {
		return
	}
	if m.Usage != nil && day != "" && m.Model != "<synthetic>" {
		u := m.Usage
		t := Tokens{Turns: 1, Input: u.Input, Output: u.Output, CacheRead: u.CacheRead}
		if u.Split != nil {
			t.CacheWrite5m, t.CacheWrite1h = u.Split.FiveMin, u.Split.OneHour
		} else {
			t.CacheWrite5m = u.CacheCreation
		}
		if u.Details != nil {
			t.Thinking = u.Details.Thinking
		}
		f.countMessage(m.ID, day, m.Model, t)
	}

	var blocks []contentBlock
	if json.Unmarshal(m.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		if b.Type != "tool_use" || b.Name == "" {
			continue
		}
		bump(f.Tools, day, b.Name, func(s *ToolStat) { s.Calls++ })
		var in map[string]any
		_ = json.Unmarshal(b.Input, &in)
		f.toolUse(day, b.Name, in)
		if len(f.Pending) < maxPending {
			f.Pending[b.ID] = pendingTool{Name: b.Name, Input: hint(in)}
		}
	}
}

// countMessage counts a message's usage once. The transcript writes a message
// once per content block, each carrying the usage so far, so a repeat of the
// last id replaces what was counted for it rather than adding to it.
func (f *File) countMessage(id, day, model string, t Tokens) {
	if f.Last != nil && id != "" && f.Last.ID == id {
		prev := f.Last
		bump(f.Usage, prev.Day, prev.Model, func(v *Tokens) { v.add(prev.Tokens, -1) })
		bump(f.Usage, day, model, func(v *Tokens) { v.add(t, 1) })
		f.Last = &lastMessage{ID: id, Day: day, Model: model, Tokens: t}
		return
	}
	bump(f.Usage, day, model, func(v *Tokens) { v.add(t, 1) })
	f.Last = &lastMessage{ID: id, Day: day, Model: model, Tokens: t}
	ctx := t.InputAll()
	if ctx > f.ContextMax {
		f.ContextMax = ctx
	}
	if ctx > BigContext {
		f.ContextBig++
	}
}

func (f *File) toolUse(day, name string, in map[string]any) {
	str := func(k string) string { s, _ := in[k].(string); return s }
	switch {
	case name == "Skill":
		f.use(day, UseSkill, str("skill"))
	case name == "Agent" || name == "Task":
		agent := str("subagent_type")
		if agent == "" {
			agent = "general-purpose"
		}
		f.use(day, UseAgent, agent)
	case strings.HasPrefix(name, "mcp__"):
		if parts := strings.SplitN(name, "__", 3); len(parts) == 3 {
			f.use(day, UseMCP, parts[1])
		}
	case name == "Bash":
		f.use(day, UseBash, firstCommand(str("command")))
	}
}

// firstCommand names the program a shell line runs, skipping a leading cd.
func firstCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, "cd ") {
		if _, rest, ok := strings.Cut(cmd, "&&"); ok {
			cmd = strings.TrimSpace(rest)
		}
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	return clip(filepath.Base(fields[0]), 40)
}

func (f *File) user(rec record, day string, lineLen int) {
	var m message
	if json.Unmarshal(rec.Message, &m) != nil {
		return
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		f.userText(rec, day, s)
		return
	}
	var blocks []contentBlock
	if json.Unmarshal(m.Content, &blocks) != nil {
		return
	}
	var text []string
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			f.toolResult(day, b, lineLen)
		case "text":
			text = append(text, b.Text)
		}
	}
	if len(text) > 0 {
		f.userText(rec, day, strings.Join(text, "\n"))
	}
}

func (f *File) userText(rec record, day, s string) {
	if strings.Contains(s, interrupted) {
		f.event(day, EventInterrupts, 1)
	}
	if m := commandName.FindStringSubmatch(s); m != nil {
		f.use(day, UseCommand, strings.TrimSpace(m[1]))
		return
	}
	// promptSource separates what a person typed from what the runtime wrote
	// as the user; "system" is injected notifications.
	if rec.PromptSource == "" || rec.PromptSource == "system" || rec.IsMeta {
		return
	}
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, interrupted) {
		return
	}
	f.event(day, EventPrompts, 1)
	if f.FirstPrompt == "" {
		f.FirstPrompt = clip(oneLine(s), 120)
	}
}

func (f *File) toolResult(day string, b contentBlock, lineLen int) {
	p, ok := f.Pending[b.ToolUseID]
	name := p.Name
	if !ok {
		name = "?"
	}
	delete(f.Pending, b.ToolUseID)
	size := len(b.Content)
	if size > lineLen {
		size = lineLen
	}
	bump(f.Tools, day, name, func(s *ToolStat) {
		s.ResultBytes += int64(size)
		if b.IsError {
			s.Errors++
		}
	})
	if bytesHavePrefix(b.Content, denialPrefix) {
		f.event(day, EventDenials, 1)
	}
	if size >= BigResultBytes {
		f.addBig(BigResult{Day: day, Tool: name, Input: p.Input, Bytes: size})
	}
}

func (f *File) addBig(r BigResult) {
	f.Big = append(f.Big, r)
	if len(f.Big) <= maxBig {
		return
	}
	smallest := 0
	for i, b := range f.Big {
		if b.Bytes < f.Big[smallest].Bytes {
			smallest = i
		}
	}
	f.Big = append(f.Big[:smallest], f.Big[smallest+1:]...)
}

// bytesHavePrefix checks a result's text without decoding the whole payload:
// it is either a JSON string or an array whose first text block carries it.
func bytesHavePrefix(raw json.RawMessage, prefix string) bool {
	head := raw
	if len(head) > 512 {
		head = head[:512]
	}
	return strings.Contains(string(head), prefix)
}

func hint(in map[string]any) string {
	for _, k := range []string{"command", "file_path", "pattern", "url", "query", "skill", "subagent_type", "description"} {
		if s, ok := in[k].(string); ok && s != "" {
			return clip(oneLine(s), maxHint)
		}
	}
	return ""
}

// DisplayTitle prefers a human rename, then the generated title, then the
// first prompt.
func (f *File) DisplayTitle() string {
	switch {
	case f.CustomTitle != "":
		return f.CustomTitle
	case f.Title != "":
		return f.Title
	default:
		return f.FirstPrompt
	}
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// summariseFile reads path from offset into f, returning the new offset.
func summariseFile(path string, f *File, offset int64, loc *time.Location) (int64, error) {
	fh, err := os.Open(path)
	if err != nil {
		return offset, err
	}
	defer fh.Close()
	if offset > 0 {
		if _, err := fh.Seek(offset, io.SeekStart); err != nil {
			return offset, err
		}
	}
	n, err := f.consume(fh, loc)
	return offset + n, err
}
