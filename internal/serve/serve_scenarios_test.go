package serve

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fake() map[string]Source {
	ok := func(*http.Request) (any, error) { return map[string]int{"n": 1}, nil }
	return map[string]Source{"usage": ok, "brief": ok, "setup": ok, "health": ok}
}

func get(t *testing.T, h http.Handler, path string, auth func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if auth != nil {
		auth(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServe(t *testing.T) {
	t.Run("serve answers on 127.0.0.1 only", func(t *testing.T) {
		ln, err := Listen(0)
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		host, _, _ := net.SplitHostPort(ln.Addr().String())
		if host != "127.0.0.1" {
			t.Errorf("listening on %s, want 127.0.0.1 only", ln.Addr())
		}
	})

	t.Run("with a password configured, every page and endpoint answers 401 without it and with a wrong one, and serves with the right one", func(t *testing.T) {
		h := Handler(Options{User: "admin", Password: "hakunamatata", Sources: fake()})
		paths := []string{"/", "/app.js", "/app.css", "/api/usage", "/api/brief", "/api/setup", "/api/health", "/nope"}
		for _, p := range paths {
			if rec := get(t, h, p, nil); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
				t.Errorf("%s without a password = %d, want 401 with a challenge", p, rec.Code)
			}
			wrong := func(r *http.Request) { r.SetBasicAuth("admin", "hakunamatat") }
			if rec := get(t, h, p, wrong); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s with a wrong password = %d, want 401", p, rec.Code)
			}
			wrongUser := func(r *http.Request) { r.SetBasicAuth("root", "hakunamatata") }
			if rec := get(t, h, p, wrongUser); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s with a wrong user = %d, want 401", p, rec.Code)
			}
			right := func(r *http.Request) { r.SetBasicAuth("admin", "hakunamatata") }
			want := http.StatusOK
			if p == "/nope" {
				want = http.StatusNotFound
			}
			if rec := get(t, h, p, right); rec.Code != want {
				t.Errorf("%s with the password = %d, want %d", p, rec.Code, want)
			}
		}
	})

	t.Run("without a password every page is served", func(t *testing.T) {
		h := Handler(Options{Sources: fake()})
		if rec := get(t, h, "/api/usage", nil); rec.Code != http.StatusOK {
			t.Errorf("/api/usage = %d, want 200", rec.Code)
		}
	})

	t.Run("an unknown path is 404, and an endpoint that fails answers with its error as JSON", func(t *testing.T) {
		sources := fake()
		sources["usage"] = func(*http.Request) (any, error) { return nil, errors.New("usage store unreadable") }
		h := Handler(Options{Sources: sources})
		for _, p := range []string{"/nope", "/api/nope", "/api/", "/web/app.js"} {
			if rec := get(t, h, p, nil); rec.Code != http.StatusNotFound {
				t.Errorf("%s = %d, want 404", p, rec.Code)
			}
		}
		rec := get(t, h, "/api/usage", nil)
		var body struct{ Error string }
		if rec.Code != http.StatusInternalServerError || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") ||
			json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error != "usage store unreadable" {
			t.Errorf("failing endpoint = %d %q %s, want 500 with the error as JSON", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
		}
	})

	t.Run("the embedded page references nothing outside the binary", func(t *testing.T) {
		files := 0
		fs.WalkDir(web, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			files++
			data, _ := fs.ReadFile(web, path)
			for _, ref := range []string{"http://", "https://", `src="//`, `href="//`, "url(//", "@import"} {
				if strings.Contains(string(data), ref) {
					t.Errorf("%s contains %q: the page must load nothing from outside the binary", path, ref)
				}
			}
			return nil
		})
		if files < 3 {
			t.Errorf("embedded %d files, want the page, its stylesheet and its script", files)
		}
	})
}
