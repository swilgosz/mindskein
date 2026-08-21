package handoff

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// Transcript is what one pass over a session transcript yields.
type Transcript struct {
	Title       string
	StartedAt   time.Time
	SegmentAt   time.Time
	LastAt      time.Time
	LastTool    string
	LastMessage string
}

// Duration covers the current stretch of work, measured from the last rename
// rather than from the session start: renaming is how one session becomes a
// second piece of work.
func (t *Transcript) Duration() time.Duration {
	if t.SegmentAt.IsZero() || t.LastAt.Before(t.SegmentAt) {
		return 0
	}
	return t.LastAt.Sub(t.SegmentAt)
}

const maxMessage = 1500

// maxDecode skips lines too big to be anything but a tool payload. Six such
// lines carried 10 of one 13.6 MB transcript, and decoding them was 140 ms of a
// 190 ms parse on a hook that runs every turn.
const maxDecode = 256 << 10

// interesting prefilters lines before the cost of unmarshalling. The whole line
// is searched, not a prefix: key order is not stable, and assistant records
// carry the entire message object before their top-level type. A false positive
// is harmless, since the decoded type decides.
var interesting = []string{
	`"type":"user"`,
	`"type":"assistant"`,
	`"type":"ai-title"`,
	`"type":"custom-title"`,
}

type record struct {
	Type         string    `json:"type"`
	Timestamp    time.Time `json:"timestamp"`
	PromptSource string    `json:"promptSource"`
	IsSidechain  bool      `json:"isSidechain"`
	AITitle      string    `json:"aiTitle"`
	CustomTitle  string    `json:"customTitle"`
	Message      struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"`
}

func ReadTranscript(path string) (*Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening transcript: %w", err)
	}
	defer f.Close()
	return parseTranscript(f)
}

func parseTranscript(r io.Reader) (*Transcript, error) {
	var (
		t              Transcript
		aiTitle        string
		customTitle    string
		firstMessage   string
		segmentPending bool
	)

	// bufio.Reader rather than Scanner: tool_result lines routinely exceed
	// Scanner's 64 KB token limit, and it would stop at the first one.
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			if rec, ok := decode(line); ok {
				switch rec.Type {
				case "custom-title":
					// Only an actual change opens a new stretch of work. These
					// records are re-emitted every turn, so reacting to each one
					// would reset the segment continuously and report every
					// session as minutes long.
					if rec.CustomTitle != "" && rec.CustomTitle != customTitle {
						customTitle = rec.CustomTitle
						segmentPending = true
					}
				case "ai-title":
					if rec.AITitle != "" {
						aiTitle = rec.AITitle
					}
				case "user":
					stamp(&t, rec.Timestamp)
					// promptSource is what separates a human prompt from the
					// tool results also recorded as user records; content shape
					// is not a reliable discriminator.
					if !asked(rec.PromptSource) {
						break
					}
					if text := textOf(rec.Message.Content); text != "" {
						if t.StartedAt.IsZero() {
							t.StartedAt = rec.Timestamp
						}
						if firstMessage == "" {
							firstMessage = text
						}
						if segmentPending || t.SegmentAt.IsZero() {
							t.SegmentAt = rec.Timestamp
							segmentPending = false
						}
						t.LastMessage = truncate(text, maxMessage)
					}
				case "assistant":
					stamp(&t, rec.Timestamp)
					if tool := toolOf(rec.Message.Content); tool != "" {
						t.LastTool = tool
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("reading transcript: %w", err)
		}
	}

	if t.SegmentAt.IsZero() {
		t.SegmentAt = t.StartedAt
	}

	// Not "the last title record wins": both kinds are re-emitted and
	// interleave, so the final record is usually the generated one even in a
	// session that was explicitly renamed.
	switch {
	case customTitle != "":
		t.Title = customTitle
	case aiTitle != "":
		t.Title = aiTitle
	default:
		t.Title = truncate(strings.TrimSpace(firstMessage), 60)
	}
	return &t, nil
}

// decode tolerates malformed lines: a transcript being appended to while it is
// read can end mid-line, which is no reason to lose the handoff.
func decode(line string) (record, bool) {
	if len(line) > maxDecode {
		return record{}, false
	}
	match := false
	for _, m := range interesting {
		if strings.Contains(line, m) {
			match = true
			break
		}
	}
	if !match {
		return record{}, false
	}
	var rec record
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return record{}, false
	}
	// Subagent records carry the parent's session id but describe work nobody
	// typed, so they must not become the last message.
	if rec.IsSidechain {
		return record{}, false
	}
	return rec, true
}

// asked reports whether a user record is something a person actually asked.
//
// "system" is not, and it is the one that matters: every such record on this
// machine is a task-notification the runtime injects, so accepting it makes
// the last message read "<task-notification>" rather than the last thing the
// person said. "sdk" is kept — those are real prompts arriving from another
// client, not machine chatter. An empty source is a tool result.
func asked(source string) bool {
	switch source {
	case "typed", "queued", "sdk":
		return true
	}
	return false
}

func stamp(t *Transcript, ts time.Time) {
	if !ts.IsZero() && ts.After(t.LastAt) {
		t.LastAt = ts
	}
}

// textOf reads a message content field, which is a bare string for a typed
// prompt and an array of blocks once images or attachments are involved.
func textOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var blocks []block
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(parts, "\n")
}

func toolOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var blocks []block
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	name := ""
	for _, b := range blocks {
		if b.Type == "tool_use" && b.Name != "" {
			name = b.Name
		}
	}
	return name
}

// truncate cuts on a rune boundary; splitting a multi-byte character leaves
// invalid UTF-8 that survives into the rendered file.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.TrimSpace(s[:n]) + "…"
}
