package server

import (
	"encoding/json"
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

// --- abuse limits ---

func limited(t *testing.T, engineURL string, l Limits, trustProxy bool) http.Handler {
	t.Helper()
	dir := t.TempDir()
	h, err := New(Config{EngineURL: engineURL, EngineKey: "secret-key", WebDir: dir, Limits: l, TrustProxy: trustProxy})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// roomy is a Limits where only the field under test bites.
func roomy() Limits {
	return Limits{VisitorPerMin: 1000, VisitorPerDay: 1000, IPPerMin: 1000, IPPerDay: 1000,
		SessionsVisitorPerHour: 1000, SessionsIPPerHour: 1000, GlobalPerDay: 1000}
}

const msgPath = "/v1/sessions/abc/messages"

func post(h http.Handler, path, ip string, cookie *http.Cookie, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
	r.RemoteAddr = ip + ":5555"
	if strings.Contains(ip, ":") { // IPv6 needs brackets
		r.RemoteAddr = "[" + ip + "]:5555"
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func visitor(h http.Handler, ip string) *http.Cookie {
	return post(h, "/v1/sessions", ip, nil, nil).Result().Cookies()[0]
}

func TestMessagesAreLimitedPerVisitor(t *testing.T) {
	e := newEngine(t)
	l := roomy()
	l.VisitorPerMin = 2
	h := limited(t, e.URL, l, false)
	c := visitor(h, "10.0.0.1")
	for i := 0; i < 2; i++ {
		if w := post(h, msgPath, "10.0.0.1", c, nil); w.Code != 200 {
			t.Fatalf("message %d: %d", i+1, w.Code)
		}
	}
	e.got = nil
	w := post(h, msgPath, "10.0.0.1", c, nil)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("over the limit: %d %v %s", w.Code, w.Header(), w.Body)
	}
	if e.got != nil {
		t.Fatal("a refused request must not reach the engine")
	}
	// another visitor from another address is unaffected
	if w := post(h, msgPath, "10.0.0.2", visitor(h, "10.0.0.2"), nil); w.Code != 200 {
		t.Fatalf("other visitor: %d", w.Code)
	}
}

func TestMessagesAreLimitedPerIPAcrossVisitors(t *testing.T) {
	l := roomy()
	l.IPPerMin = 2
	h := limited(t, newEngine(t).URL, l, false)
	for i := 0; i < 2; i++ {
		post(h, msgPath, "10.0.0.1", visitor(h, "10.0.0.1"), nil) // a fresh cookie each time
	}
	if w := post(h, msgPath, "10.0.0.1", visitor(h, "10.0.0.1"), nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("same IP, new cookie: %d", w.Code)
	}
}

func TestSessionsAreLimited(t *testing.T) {
	l := roomy()
	l.SessionsIPPerHour = 2
	h := limited(t, newEngine(t).URL, l, false)
	post(h, "/v1/sessions", "10.0.0.1", nil, nil)
	post(h, "/v1/sessions", "10.0.0.1", nil, nil)
	if w := post(h, "/v1/sessions", "10.0.0.1", nil, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("third session: %d", w.Code)
	}
}

func TestSiteWideDailyBudget(t *testing.T) {
	l := roomy()
	l.GlobalPerDay = 2
	h := limited(t, newEngine(t).URL, l, false)
	post(h, msgPath, "10.0.0.1", visitor(h, "10.0.0.1"), nil)
	post(h, msgPath, "10.0.0.2", visitor(h, "10.0.0.2"), nil)
	if w := post(h, msgPath, "10.0.0.3", visitor(h, "10.0.0.3"), nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("over the site budget: %d", w.Code)
	}
}

func TestReadsAreNotLimited(t *testing.T) {
	l := roomy()
	l.IPPerMin, l.GlobalPerDay = 1, 1
	h := limited(t, newEngine(t).URL, l, false)
	for i := 0; i < 5; i++ {
		r := httptest.NewRequest("GET", "/v1/sessions/abc/cart", nil)
		r.RemoteAddr = "10.0.0.1:1"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("GET %d: %d", i, w.Code)
		}
	}
}

func TestForwardedForOnlyCountsBehindATrustedProxy(t *testing.T) {
	l := roomy()
	l.IPPerMin = 1
	// not trusted: the header is ignored, both requests are 10.0.0.1
	h := limited(t, newEngine(t).URL, l, false)
	post(h, msgPath, "10.0.0.1", visitor(h, "10.0.0.1"), map[string]string{"X-Forwarded-For": "1.1.1.1"})
	if w := post(h, msgPath, "10.0.0.1", visitor(h, "10.0.0.1"), map[string]string{"X-Forwarded-For": "2.2.2.2"}); w.Code != http.StatusTooManyRequests {
		t.Fatalf("untrusted XFF must not split an IP: %d", w.Code)
	}
	// trusted: the rightmost entry is the client the proxy saw; a forged left part is ignored
	h = limited(t, newEngine(t).URL, l, true)
	post(h, msgPath, "10.0.0.1", visitor(h, "10.0.0.1"), map[string]string{"X-Forwarded-For": "9.9.9.9, 1.1.1.1"})
	if w := post(h, msgPath, "10.0.0.1", visitor(h, "10.0.0.1"), map[string]string{"X-Forwarded-For": "8.8.8.8, 2.2.2.2"}); w.Code != 200 {
		t.Fatalf("different real clients behind the proxy: %d", w.Code)
	}
	if w := post(h, msgPath, "10.0.0.1", visitor(h, "10.0.0.1"), map[string]string{"X-Forwarded-For": "7.7.7.7, 1.1.1.1"}); w.Code != http.StatusTooManyRequests {
		t.Fatalf("same real client, forged left part: %d", w.Code)
	}
}

func TestDefaultLimitsApplyWhenUnset(t *testing.T) {
	d := DefaultLimits()
	if d.VisitorPerMin != 6 || d.VisitorPerDay != 60 || d.IPPerMin != 12 || d.IPPerDay != 120 ||
		d.SessionsVisitorPerHour != 10 || d.SessionsIPPerHour != 20 || d.GlobalPerDay != 150 {
		t.Fatalf("defaults drifted from the agreed table: %+v", d)
	}
}

func TestClientCannotSupplyAVisitorIDInDevMode(t *testing.T) {
	l := roomy()
	l.VisitorPerMin = 1
	dir := t.TempDir()
	e := newEngine(t)
	h, _ := New(Config{EngineURL: e.URL, WebDir: dir, Limits: l}) // no engine key: no visitors
	for i := 0; i < 3; i++ {
		if w := post(h, msgPath, "10.0.0.1", nil, map[string]string{"X-Visitor-Id": "x"}); w.Code != 200 {
			t.Fatalf("request %d: %d (visitor rules must not apply without visitors)", i+1, w.Code)
		}
	}
}

func TestFailedEngineCallsAreRefunded(t *testing.T) {
	// a request the engine rejects (unknown session) spent nothing and must not eat the budget
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "bogus") {
			http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
			return
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()
	l := roomy()
	l.GlobalPerDay = 2
	h := limited(t, srv.URL, l, false)
	c := visitor(h, "10.0.0.1")
	for i := 0; i < 10; i++ {
		if w := post(h, "/v1/sessions/bogus/messages", "10.0.0.1", c, nil); w.Code != http.StatusNotFound {
			t.Fatalf("bogus %d: %d", i, w.Code)
		}
	}
	for i := 0; i < 2; i++ {
		if w := post(h, msgPath, "10.0.0.1", c, nil); w.Code != 200 {
			t.Fatalf("real message %d: %d (the budget was eaten by failures)", i+1, w.Code)
		}
	}
	if w := post(h, msgPath, "10.0.0.1", c, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("budget still enforced: %d", w.Code)
	}
}

func TestIPv6ClientsShareTheirSlash64(t *testing.T) {
	l := roomy()
	l.IPPerMin = 1
	h := limited(t, newEngine(t).URL, l, false)
	post(h, msgPath, "2001:db8:1:2::1", visitor(h, "2001:db8:1:2::1"), nil)
	// hosts are in brackets in RemoteAddr: post() appends ":5555" to the bare address
	if w := post(h, msgPath, "2001:db8:1:2::ffff", visitor(h, "2001:db8:1:2::ffff"), nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("same /64, different host bits: %d", w.Code)
	}
	if w := post(h, msgPath, "2001:db8:1:3::1", visitor(h, "2001:db8:1:3::1"), nil); w.Code != 200 {
		t.Fatalf("another /64: %d", w.Code)
	}
}

func TestForwardedForUsesTheLastHeaderLine(t *testing.T) {
	l := roomy()
	l.IPPerMin = 1
	h := limited(t, newEngine(t).URL, l, true)
	do := func(lines ...string) int {
		r := httptest.NewRequest("POST", msgPath, strings.NewReader(`{}`))
		r.RemoteAddr = "10.0.0.1:1"
		for _, ln := range lines {
			r.Header.Add("X-Forwarded-For", ln)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	do("9.9.9.9", "1.1.1.1") // a proxy that adds its own line: the client-supplied first line is ignored
	if c := do("8.8.8.8", "1.1.1.1"); c != http.StatusTooManyRequests {
		t.Fatalf("same real client behind a line-adding proxy: %d", c)
	}
}

func TestRefusalIsValidJSON(t *testing.T) {
	l := roomy()
	l.GlobalPerDay = 1
	h := limited(t, newEngine(t).URL, l, false)
	post(h, msgPath, "10.0.0.1", nil, nil)
	w := post(h, msgPath, "10.0.0.1", nil, nil)
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Error == "" {
		t.Fatalf("body %q: %v", w.Body, err)
	}
}
