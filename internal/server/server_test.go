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
	"testing/fstest"
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
	r.Header.Set("Content-Type", "application/json")
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
	r.Header.Set("Content-Type", "application/json")
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
	if d.VisitorPerMin != 15 || d.VisitorPerDay != 200 || d.IPPerMin != 30 || d.IPPerDay != 400 ||
		d.SessionsVisitorPerHour != 10 || d.SessionsIPPerHour != 20 || d.GlobalPerDay != 1000 {
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
		r.Header.Set("Content-Type", "application/json")
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

// --- CSRF and browser hardening ---

func guarded(t *testing.T) (http.Handler, *engine) {
	e := newEngine(t)
	return limited(t, e.URL, roomy(), false), e
}

func send(h http.Handler, method, path string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = "demo.example"
	r.RemoteAddr = "10.0.0.1:1"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var jsonCT = map[string]string{"Content-Type": "application/json"}

func with(base map[string]string, kv ...string) map[string]string {
	m := map[string]string{}
	for k, v := range base {
		m[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func TestCrossSiteStateChangesAreRefused(t *testing.T) {
	h, e := guarded(t)
	for name, hdr := range map[string]map[string]string{
		"sec-fetch cross-site":  with(jsonCT, "Sec-Fetch-Site", "cross-site"),
		"sec-fetch same-site":   with(jsonCT, "Sec-Fetch-Site", "same-site"), // a sibling subdomain is not us
		"foreign origin":        with(jsonCT, "Origin", "https://evil.example"),
		"origin null (sandbox)": with(jsonCT, "Origin", "null"),
	} {
		e.got = nil
		if w := send(h, "POST", "/v1/sessions", hdr, `{}`); w.Code != http.StatusForbidden || e.got != nil {
			t.Errorf("%s: %d (engine reached: %v)", name, w.Code, e.got != nil)
		}
	}
}

func TestSameOriginStateChangesPass(t *testing.T) {
	h, _ := guarded(t)
	for name, hdr := range map[string]map[string]string{
		"same-origin fetch":  with(jsonCT, "Sec-Fetch-Site", "same-origin", "Origin", "https://demo.example"),
		"no browser headers": jsonCT, // curl, tests, older clients: the cookie rules still apply
		"origin with a port": with(jsonCT, "Origin", "http://demo.example"),
		"direct navigation":  with(jsonCT, "Sec-Fetch-Site", "none"),
	} {
		if w := send(h, "POST", "/v1/sessions", hdr, `{}`); w.Code != 200 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
}

func TestStateChangesNeedJSON(t *testing.T) {
	h, e := guarded(t)
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", ""} {
		e.got = nil
		hdr := map[string]string{}
		if ct != "" {
			hdr["Content-Type"] = ct
		}
		if w := send(h, "POST", "/v1/sessions", hdr, `{"topic":"x"}`); w.Code != http.StatusUnsupportedMediaType || e.got != nil {
			t.Errorf("content-type %q: %d", ct, w.Code)
		}
	}
	// a body-less DELETE has no content type to check
	if w := send(h, "DELETE", "/v1/sessions/abc/cart", nil, ""); w.Code != 200 {
		t.Errorf("DELETE: %d", w.Code)
	}
	if w := send(h, "POST", "/v1/sessions", with(jsonCT, "Content-Type", "application/json; charset=utf-8"), `{}`); w.Code != 200 {
		t.Errorf("json with charset: %d", w.Code)
	}
}

func TestReadsAreNotSubjectToTheCSRFGuard(t *testing.T) {
	h, _ := guarded(t)
	if w := send(h, "GET", "/v1/topics", with(nil, "Sec-Fetch-Site", "cross-site"), ""); w.Code != 200 {
		t.Fatalf("GET from another site: %d", w.Code)
	}
}

func TestBodiesAreCapped(t *testing.T) {
	h, _ := guarded(t)
	big := `{"text":"` + strings.Repeat("a", 20<<10) + `"}`
	w := send(h, "POST", "/v1/sessions/abc/messages", jsonCT, big)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("20 KiB body: %d", w.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h, _ := guarded(t)
	w := send(h, "GET", "/", nil, "")
	csp := w.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "base-uri 'none'", "form-action 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("headers: %v", w.Header())
	}
	if v := send(h, "GET", "/v1/topics", nil, "").Header().Get("Cache-Control"); v != "no-store" {
		t.Errorf("/v1 must not be cached: %q", v)
	}
}

// --- review findings on the guard ---

func originGuarded(t *testing.T, origin string) http.Handler {
	t.Helper()
	h, err := New(Config{EngineURL: newEngine(t).URL, EngineKey: "k", WebDir: t.TempDir(), Limits: roomy(), PublicOrigin: origin})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestPublicOriginIsComparedExactlyAndSurvivesHostRewrites(t *testing.T) {
	h := originGuarded(t, "https://demo.example")
	r := func(origin string) int {
		req := httptest.NewRequest("POST", "/v1/sessions", strings.NewReader(`{}`))
		req.Host = "127.0.0.1:8086" // the proxy rewrote Host
		req.RemoteAddr = "10.0.0.1:1"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	if c := r("https://demo.example"); c != 200 {
		t.Errorf("our origin: %d", c)
	}
	for _, bad := range []string{"http://demo.example", "https://demo.example:8443", "https://evil.example", "null"} {
		if c := r(bad); c != http.StatusForbidden {
			t.Errorf("origin %q: %d", bad, c)
		}
	}
}

func TestWithoutPublicOriginHostAndPortMustMatch(t *testing.T) {
	h, _ := guarded(t) // Host demo.example
	for origin, want := range map[string]int{
		"http://demo.example":      200,
		"http://demo.example:8080": http.StatusForbidden,
		"https://demo.example:443": http.StatusForbidden, // an explicit port is a different host string
	} {
		if w := send(h, "POST", "/v1/sessions", with(jsonCT, "Origin", origin), `{}`); w.Code != want {
			t.Errorf("origin %q: %d, want %d", origin, w.Code, want)
		}
	}
}

func TestANonJSONContentTypeIsRefusedEvenWithAnEmptyBody(t *testing.T) {
	h, e := guarded(t)
	for _, ct := range []string{"application/x-www-form-urlencoded", "text/plain", "multipart/form-data; boundary=x"} {
		e.got = nil
		if w := send(h, "POST", "/v1/sessions/abc/checkout", map[string]string{"Content-Type": ct}, ""); w.Code != http.StatusUnsupportedMediaType || e.got != nil {
			t.Errorf("empty body, %q: %d", ct, w.Code)
		}
	}
}

func TestOversizeResponsesAreJSONAndChunkedBodiesAreCapped(t *testing.T) {
	h, _ := guarded(t)
	w := send(h, "POST", "/v1/sessions/abc/messages", jsonCT, `{"text":"`+strings.Repeat("a", 20<<10)+`"}`)
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("413 content type %q", ct)
	}
	// unknown length (chunked): the proxy hits the cap while streaming
	req := httptest.NewRequest("POST", "/v1/sessions/abc/messages", io.NopCloser(strings.NewReader(`{"text":"`+strings.Repeat("a", 20<<10)+`"}`)))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.1:1"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("chunked oversize: %d", w.Code)
	}
}

func TestBareV1IsGuardedToo(t *testing.T) {
	h, _ := guarded(t)
	if w := send(h, "POST", "/v1", with(jsonCT, "Sec-Fetch-Site", "cross-site"), `{}`); w.Code != http.StatusForbidden {
		t.Errorf("POST /v1 cross-site: %d", w.Code)
	}
}

func TestHSTSWhenThePublicOriginIsHTTPS(t *testing.T) {
	if v := send(originGuarded(t, "https://demo.example"), "GET", "/", nil, "").Header().Get("Strict-Transport-Security"); !strings.HasPrefix(v, "max-age=") {
		t.Errorf("HSTS: %q", v)
	}
	if v := send(originGuarded(t, ""), "GET", "/", nil, "").Header().Get("Strict-Transport-Security"); v != "" {
		t.Errorf("no HSTS without an https public origin: %q", v)
	}
}

func TestServesAnEmbeddedClientWhenGivenAnFS(t *testing.T) {
	h, err := New(Config{EngineURL: "http://127.0.0.1:1", Static: fstest.MapFS{"index.html": {Data: []byte("<h1>embedded</h1>")}}})
	if err != nil {
		t.Fatal(err)
	}
	if w := do(h, "GET", "/", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "embedded") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// --- voice ---

const voicePath = "/v1/sessions/abc/voice?seconds=3"

func TestVoiceUploadsPassWithAnAudioType(t *testing.T) {
	h, e := guarded(t)
	audio := with(nil, "Content-Type", "audio/webm;codecs=opus")
	if w := send(h, "POST", voicePath, audio, strings.Repeat("a", 200<<10)); w.Code != 200 || e.got == nil {
		t.Fatalf("200 KiB recording: %d", w.Code)
	}
}

func TestVoiceUploadsAreStillGuarded(t *testing.T) {
	h, e := guarded(t)
	audio := with(nil, "Content-Type", "audio/webm")
	if w := send(h, "POST", voicePath, with(audio, "Sec-Fetch-Site", "cross-site"), "x"); w.Code != http.StatusForbidden {
		t.Errorf("cross-site: %d", w.Code)
	}
	if w := send(h, "POST", voicePath, audio, strings.Repeat("a", 2<<20)); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("2 MiB: %d", w.Code)
	}
	e.got = nil
	for _, ct := range []string{"text/plain", "application/json", ""} {
		hdr := map[string]string{}
		if ct != "" {
			hdr["Content-Type"] = ct
		}
		if w := send(h, "POST", voicePath, hdr, "x"); w.Code != http.StatusUnsupportedMediaType || e.got != nil {
			t.Errorf("content-type %q: %d", ct, w.Code)
		}
	}
	// the larger cap is for the voice route only
	if w := send(h, "POST", "/v1/sessions/abc/messages", jsonCT, `{"text":"`+strings.Repeat("a", 20<<10)+`"}`); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a big message: %d", w.Code)
	}
}

func TestVoiceCountsLikeAMessage(t *testing.T) {
	e := newEngine(t)
	l := roomy()
	l.VisitorPerMin = 2
	h := limited(t, e.URL, l, false)
	c := visitor(h, "10.0.0.1")
	voice := func() int {
		r := httptest.NewRequest("POST", voicePath, strings.NewReader("x"))
		r.Header.Set("Content-Type", "audio/webm")
		r.RemoteAddr = "10.0.0.1:5555"
		r.AddCookie(c)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if a, b := voice(), post(h, msgPath, "10.0.0.1", c, nil).Code; a != 200 || b != 200 {
		t.Fatalf("within the limit: %d %d", a, b)
	}
	if code := voice(); code != http.StatusTooManyRequests {
		t.Fatalf("third turn (voice) over the per-minute limit: %d", code)
	}
}
