package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// engine records the last request it received and answers with a fixed body.
type engine struct {
	*httptest.Server
	got *http.Request
}

func newEngine(t *testing.T) *engine {
	e := &engine{}
	e.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.got = r.Clone(r.Context())
		io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(e.Close)
	return e
}

func newServer(t *testing.T, engineURL, key string) http.Handler {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hi</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := New(Config{EngineURL: engineURL, EngineKey: key, WebDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func do(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestServesStaticFilesAndHealth(t *testing.T) {
	h := newServer(t, "http://127.0.0.1:1", "")
	if w := do(h, "GET", "/", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "<h1>hi</h1>") {
		t.Fatalf("index: %d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", "/healthz", nil); w.Code != 200 {
		t.Fatalf("healthz: %d", w.Code)
	}
}

func TestProxiesV1ToTheEngine(t *testing.T) {
	e := newEngine(t)
	h := newServer(t, e.URL, "")
	w := do(h, "POST", "/v1/sessions", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("proxy: %d %s", w.Code, w.Body)
	}
	if e.got.URL.Path != "/v1/sessions" || e.got.Method != "POST" {
		t.Fatalf("engine saw %s %s", e.got.Method, e.got.URL.Path)
	}
}

func TestOnlyV1IsProxied(t *testing.T) {
	e := newEngine(t)
	h := newServer(t, e.URL, "")
	if w := do(h, "GET", "/metrics", nil); w.Code != http.StatusNotFound || e.got != nil {
		t.Fatalf("non-/v1 path reached the engine: %d", w.Code)
	}
}

func TestClientCannotChooseItsOwnPrincipal(t *testing.T) {
	e := newEngine(t)
	h := newServer(t, e.URL, "secret-key")
	do(h, "POST", "/v1/sessions", map[string]string{"Authorization": "tma forged", "X-Principal": "admin"})
	if got := e.got.Header.Get("Authorization"); got != "Bearer secret-key" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := e.got.Header.Get("X-Principal"); !strings.HasPrefix(got, "web:") || got == "web:" {
		t.Fatalf("X-Principal = %q", got)
	}
}

func TestVisitorKeepsOneAnonymousPrincipal(t *testing.T) {
	e := newEngine(t)
	h := newServer(t, e.URL, "secret-key")
	first := do(h, "POST", "/v1/sessions", nil)
	cookies := first.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie: %+v", cookies)
	}
	p1 := e.got.Header.Get("X-Principal")

	r := httptest.NewRequest("POST", "/v1/sessions", nil)
	r.AddCookie(cookies[0])
	h.ServeHTTP(httptest.NewRecorder(), r)
	if p2 := e.got.Header.Get("X-Principal"); p2 != p1 {
		t.Fatalf("same visitor, principals %q vs %q", p1, p2)
	}

	do(h, "POST", "/v1/sessions", nil) // a new visitor
	if p3 := e.got.Header.Get("X-Principal"); p3 == p1 {
		t.Fatal("different visitors must get different principals")
	}
}

func TestWithoutAKeyTheRequestIsForwardedUntouched(t *testing.T) {
	// local development against an engine running WEB_DEV_AUTH=1
	e := newEngine(t)
	h := newServer(t, e.URL, "")
	do(h, "POST", "/v1/sessions", map[string]string{"Authorization": "tma x"})
	if e.got.Header.Get("X-Principal") != "" {
		t.Fatal("no key: no principal header")
	}
}

func TestNewRejectsABadEngineURL(t *testing.T) {
	if _, err := New(Config{EngineURL: "not a url", WebDir: "."}); err == nil {
		t.Fatal("want an error")
	}
	if _, err := New(Config{EngineURL: "", WebDir: "."}); err == nil {
		t.Fatal("want an error for an empty ENGINE_URL")
	}
}
