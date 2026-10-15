// Package server is the v2v-web backend-for-frontend: it serves the static web
// client and forwards /v1 to the engine's end-user API. Browsers have no
// Telegram initData, so with ENGINE_KEY set this server vouches for each
// visitor itself — it gives every browser an anonymous principal (an HttpOnly
// cookie) and tells the engine who it is in X-Principal, authenticated by a
// shared secret. A client can never choose its own principal.
package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	_ "time/tzdata" // Europe/Kyiv even on a host without a zoneinfo database

	"github.com/valpere/v2v-web/internal/limit"
)

const cookieName = "v2v_visitor"

// Config is what New needs.
type Config struct {
	EngineURL string // base URL of the engine's end-user /v1 API
	EngineKey string // shared secret of the trusted-frontend scheme; empty = forward as is (dev)
	WebDir    string // static files on disk (development)
	Static    fs.FS  // static files from memory (the embedded client); wins over WebDir
	Limits    Limits // zero = DefaultLimits()
	// PublicOrigin is the site's own origin, e.g. https://demo.example (no path). Behind a proxy that
	// rewrites Host it is what Origin is checked against; empty = compare with the Host header.
	PublicOrigin string
	TrustProxy   bool // read the client address from X-Forwarded-For (only behind a reverse proxy you control)
}

// New returns the handler: /healthz, /v1/* -> engine, everything else static.
func New(cfg Config) (http.Handler, error) {
	if cfg.EngineURL == "" {
		return nil, errors.New("ENGINE_URL is required")
	}
	target, err := url.Parse(cfg.EngineURL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return nil, fmt.Errorf("ENGINE_URL %q: want http(s)://host[:port]", cfg.EngineURL)
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Header.Del("X-Principal") // never taken from the client
			r.Out.Header.Del("X-Visitor-Id")
			if cfg.EngineKey != "" {
				r.Out.Header.Set("Authorization", "Bearer "+cfg.EngineKey)
				r.Out.Header.Set("X-Principal", "web:"+visitorID(r.In))
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) { // a chunked body that outgrew the cap while being forwarded
				jsonError(w, http.StatusRequestEntityTooLarge, "request too large")
				return
			}
			jsonError(w, http.StatusBadGateway, "engine unavailable")
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		return nil, fmt.Errorf("time zone: %w", err)
	}
	limits := cfg.Limits
	if limits == (Limits{}) {
		limits = DefaultLimits()
	}
	lim := limit.New(loc, nil)
	mux.Handle("/v1/", withVisitor(cfg.EngineKey != "", withLimits(limits, lim, cfg.TrustProxy, proxy)))
	var static http.FileSystem = http.Dir(cfg.WebDir)
	if cfg.Static != nil {
		static = http.FS(cfg.Static)
	}
	mux.Handle("/", http.FileServer(static))
	return guard(mux, strings.TrimRight(cfg.PublicOrigin, "/")), nil
}

// withVisitor makes sure the request carries a visitor id: an existing cookie,
// or a fresh one that is also set on the response. The id is passed on in a
// request header the proxy's Rewrite reads (and never forwards as such).
func withVisitor(on bool, next http.Handler) http.Handler {
	if !on { // no engine key (dev): there is no visitor, and a client-supplied id is not one
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r2 := r.Clone(r.Context())
			r2.Header.Del("X-Visitor-Id")
			next.ServeHTTP(w, r2)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := ""
		if c, err := r.Cookie(cookieName); err == nil && validID(c.Value) {
			id = c.Value
		} else {
			var b [16]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
			http.SetCookie(w, &http.Cookie{
				Name: cookieName, Value: id, Path: "/", MaxAge: 365 * 24 * 3600,
				HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
			})
		}
		r2 := r.Clone(r.Context())
		r2.Header.Set("X-Visitor-Id", id)
		next.ServeHTTP(w, r2)
	})
}

// visitorID reads what withVisitor set; the header is internal and stripped by
// the proxy's hop (Rewrite copies only what it names, but remove it explicitly).
func visitorID(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("X-Visitor-Id"))
}

func validID(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
