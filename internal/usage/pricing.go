package usage

import "strings"

// Price is USD per million tokens.
type Price struct {
	Input, Output, CacheRead, CacheWrite5m, CacheWrite1h float64
}

// prices are Anthropic API list prices, matched by the longest model-id
// prefix. Cache writes cost 1.25× input for 5 minutes and 2× for an hour, and a
// read 0.1× unless the model lists its own.
var prices = map[string]Price{
	"claude-fable-5-1":  list(10, 50, 0.25),
	"claude-fable-5":    list(10, 50, 0),
	"claude-mythos-5":   list(10, 50, 0),
	"claude-opus-5-5":   list(4, 20, 0.2),
	"claude-opus-5":     list(5, 25, 0),
	"claude-opus-4":     list(5, 25, 0),
	"claude-sonnet-5-5": list(2, 10, 0.2),
	"claude-sonnet-5":   list(2, 10, 0),
	"claude-sonnet-4":   list(3, 15, 0),
	"claude-haiku-5-5":  list(0.1, 0.5, 0),
	"claude-haiku-4":    list(1, 5, 0),
}

func list(in, out, read float64) Price {
	if read == 0 {
		read = in * 0.1
	}
	return Price{Input: in, Output: out, CacheRead: read, CacheWrite5m: in * 1.25, CacheWrite1h: in * 2}
}

func priceOf(model string) (Price, bool) {
	best := ""
	for k := range prices {
		if strings.HasPrefix(model, k) && len(k) > len(best) {
			best = k
		}
	}
	p, ok := prices[best]
	return p, ok
}

// Cost is the API-equivalent cost of t, broken down by token kind.
type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

func (c Cost) Total() float64 { return c.Input + c.Output + c.CacheRead + c.CacheWrite }

func (c *Cost) add(o Cost) {
	c.Input += o.Input
	c.Output += o.Output
	c.CacheRead += o.CacheRead
	c.CacheWrite += o.CacheWrite
}

func costOf(model string, t Tokens) Cost {
	p, ok := priceOf(model)
	if !ok {
		return Cost{}
	}
	m := 1e6
	return Cost{
		Input:      float64(t.Input) * p.Input / m,
		Output:     float64(t.Output) * p.Output / m,
		CacheRead:  float64(t.CacheRead) * p.CacheRead / m,
		CacheWrite: (float64(t.CacheWrite5m)*p.CacheWrite5m + float64(t.CacheWrite1h)*p.CacheWrite1h) / m,
	}
}

// ModelName drops the vendor prefix and any date suffix.
func ModelName(model string) string {
	m := strings.TrimPrefix(model, "claude-")
	if i := strings.LastIndex(m, "-"); i > 0 && len(m)-i == 9 {
		m = m[:i]
	}
	return m
}
