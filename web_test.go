package v2vweb

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(Client(), name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func set(re *regexp.Regexp, s string) map[string]bool {
	out := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out[m[1]] = true
	}
	return out
}

// An id the script reads but the page does not have fails only in a browser
// (a null dereference on click) — this catches it at `make check`.
func TestScriptIdsExistInThePage(t *testing.T) {
	html := read(t, "index.html")
	have := set(regexp.MustCompile(`\bid="([^"]+)"`), html)
	for id := range set(regexp.MustCompile(`\$\("([\w-]+)"\)`), read(t, "assets/app.js")) {
		if !have[id] {
			t.Errorf("app.js uses $(%q) but index.html has no such id", id)
		}
	}
}

// Every #id the stylesheet styles must exist (a renamed element leaves dead rules).
func TestStylesheetIdsExistInThePage(t *testing.T) {
	have := set(regexp.MustCompile(`\bid="([^"]+)"`), read(t, "index.html"))
	for id := range set(regexp.MustCompile(`#([a-z][\w-]*)\s*[{,.\s:]`), read(t, "assets/app.css")) {
		if !have[id] {
			t.Errorf("app.css styles #%s but index.html has no such id", id)
		}
	}
}

// Regression 2026-10-18: a bad edit dropped the :root line, --gap became undefined
// and the whole page lost its spacing; and #topics{display:grid} beat the hidden
// attribute, so the picker stayed above the chat.
func TestStylesheetBasics(t *testing.T) {
	css := read(t, "assets/app.css")
	if strings.Count(css, "{") != strings.Count(css, "}") {
		t.Error("unbalanced braces in app.css")
	}
	defined := set(regexp.MustCompile(`(--[\w-]+)\s*:`), css)
	for v := range set(regexp.MustCompile(`var\((--[\w-]+)`), css) {
		if !defined[v] {
			t.Errorf("app.css uses var(%s) but never defines it", v)
		}
	}
	if !regexp.MustCompile(`\[hidden\]\s*\{[^}]*display:\s*none\s*!important`).MatchString(css) {
		t.Error("app.css must make [hidden] win over display rules")
	}
	if strings.HasPrefix(strings.TrimSpace(css), "{") {
		t.Error("app.css starts with a bare block (selector lost)")
	}
}

func TestPageLoadsItsAssets(t *testing.T) {
	html := read(t, "index.html")
	for _, m := range regexp.MustCompile(`(?:src|href)="(assets/[^"]+)"`).FindAllStringSubmatch(html, -1) {
		if _, err := fs.Stat(Client(), m[1]); err != nil {
			t.Errorf("index.html references %s: %v", m[1], err)
		}
	}
}
