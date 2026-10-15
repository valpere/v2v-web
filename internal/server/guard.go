package server

import (
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// maxBody caps a /v1 request body; the engine caps text at 500 characters.
const maxBody = 16 << 10

// csp allows only our own files: the client never uses inline script or style,
// so a reflected or stored injection cannot run.
const csp = "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// guard adds the browser-facing protections around the whole site:
//   - security headers on every response;
//   - for /v1: no caching, a body cap and, for state-changing methods, a CSRF
//     check on top of the SameSite cookie (which older browsers and sibling
//     subdomains do not honour).
func guard(next http.Handler, publicOrigin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if strings.HasPrefix(publicOrigin, "https://") {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")

		if r.URL.Path == "/v1" || strings.HasPrefix(r.URL.Path, "/v1/") {
			h.Set("Cache-Control", "no-store")
			if r.ContentLength > maxBody {
				jsonError(w, http.StatusRequestEntityTooLarge, "request too large")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBody)
			if !safeMethod(r.Method) {
				if code, msg := csrfCheck(r, publicOrigin); code != 0 {
					jsonError(w, code, msg)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// csrfCheck refuses a state-changing request that a page on another origin could
// have caused. Fetch metadata is authoritative when the browser sends it; Origin
// covers browsers that send only that; and a JSON body is required because a
// cross-site form cannot produce one without a preflight we never answer.
// A request with none of these headers (curl, tests) is not a browser and is
// only as trusted as its cookie.
func csrfCheck(r *http.Request, publicOrigin string) (int, string) {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return http.StatusForbidden, "cross-site request refused"
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		if !sameOrigin(origin, publicOrigin, r.Host) {
			return http.StatusForbidden, "cross-origin request refused"
		}
	}
	// Any declared type must be JSON (an empty cross-site form POST declares one too);
	// a body with no declared type is refused as well.
	if ct := r.Header.Get("Content-Type"); ct != "" || r.ContentLength != 0 {
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil || mt != "application/json" {
			return http.StatusUnsupportedMediaType, "content type must be application/json"
		}
	}
	return 0, ""
}

// sameOrigin reports whether an Origin header names this site. With a configured
// public origin the comparison is exact (scheme, host and port); otherwise it
// falls back to the Host header, which a TLS-terminating or Host-rewriting proxy
// can make unreliable — set PUBLIC_ORIGIN in production.
func sameOrigin(origin, publicOrigin, host string) bool {
	if publicOrigin != "" {
		return strings.EqualFold(origin, publicOrigin)
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, host)
}

func jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
