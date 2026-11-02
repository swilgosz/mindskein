// Package serve is the web view: one page per command, each reading JSON built
// by the code behind that command. The page is embedded and loads nothing from
// outside the binary.
package serve

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
)

//go:embed web
var embedded embed.FS

var web, _ = fs.Sub(embedded, "web")

// Source answers one page's endpoint with a value encoded as JSON.
type Source func(*http.Request) (any, error)

// Options configures the handler.
type Options struct {
	// User and Password, once Password is set, put HTTP Basic auth on every
	// request, the page included.
	User, Password string

	// Sources answer /api/<name>.
	Sources map[string]Source
}

// Listen binds the loopback interface only. Reaching it from another device
// takes a tunnel the user sets up on purpose.
func Listen(port int) (net.Listener, error) {
	return net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}

// Handler serves the embedded page and the JSON endpoints.
func Handler(opts Options) http.Handler {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeJSON(w, http.StatusMethodNotAllowed, errorBody{"read-only"})
			return
		}
		if name, ok := strings.CutPrefix(r.URL.Path, "/api/"); ok {
			src := opts.Sources[name]
			if src == nil {
				writeJSON(w, http.StatusNotFound, errorBody{"no such endpoint: " + name})
				return
			}
			v, err := src(r)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, errorBody{err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, v)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		data, err := fs.ReadFile(web, name)
		if err != nil || strings.Contains(name, "/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
		w.Write(data)
	})
	if opts.Password == "" {
		return h
	}
	return basicAuth(opts.User, opts.Password, h)
}

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// basicAuth compares digests rather than the strings, so neither the length
// of the password nor the position of the first wrong byte shows in timing.
func basicAuth(user, password string, next http.Handler) http.Handler {
	wantUser, wantPass := sha256.Sum256([]byte(user)), sha256.Sum256([]byte(password))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		gotUser, gotPass := sha256.Sum256([]byte(u)), sha256.Sum256([]byte(p))
		if !ok || subtle.ConstantTimeCompare(gotUser[:], wantUser[:])&subtle.ConstantTimeCompare(gotPass[:], wantPass[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="mindskein", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
