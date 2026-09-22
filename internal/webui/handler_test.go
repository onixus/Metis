package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestNFO01_SPARoutesKeepAPIAndAssetsSeparate(t *testing.T) {
	files := fstest.MapFS{
		"index.html":         {Data: []byte("<!doctype html><html><head></head><body>metis</body></html>")},
		"assets/app-hash.js": {Data: []byte("console.info('metis')")},
	}
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "api response", http.StatusUnauthorized) })
	h, err := New(files, api, Config{AuthMode: "token"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		code int
		body string
	}{
		{"/", 200, "runtime-config.js"},
		{"/products/123/roadmap", 200, "<html>"},
		{"/callback", 200, "<html>"},
		{"/index.html", 200, "runtime-config.js"},
		{"/assets/app-hash.js", 200, "console.info"},
		{"/assets/missing.js", 404, "404"},
		{"/assets", 404, "404"},
		{"/missing.css", 404, "404"},
		{"/../index.html", 404, "404"},
		{"/api", 401, "api response"},
		{"/api/v1/missing", 401, "api response"},
		{"/healthz", 401, "api response"},
		{"/readyz", 401, "api response"},
		{"/metrics", 401, "api response"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.body) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/login", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST SPA: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/products/123", nil))
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatalf("HEAD SPA: %d %s", w.Code, w.Body.String())
	}
}

func TestAD01_PublicConfigTracksAuthenticationWithoutSecrets(t *testing.T) {
	files := fstest.MapFS{"index.html": {Data: []byte("<head></head>")}}
	for _, mode := range []string{"token", "oidc"} {
		h, err := New(files, http.NotFoundHandler(), Config{AuthMode: mode, OIDCIssuer: "https://idp.example.test/realms/metis", OIDCClientID: "metis-web"})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/runtime-config.js", nil))
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(w.Body.String(), `"authMode":"`+mode+`"`) {
			t.Fatalf("config: %s %s", w.Header(), w.Body.String())
		}
		if strings.Contains(w.Body.String(), "oidcIssuer") != (mode == "oidc") {
			t.Fatalf("лишние или отсутствующие настройки OIDC: %s", w.Body.String())
		}
	}
	for _, issuer := range []string{"", "javascript:alert(1)", "https://user:password@idp.test", "https://idp.test/?secret=oops"} {
		if _, err := New(files, http.NotFoundHandler(), Config{AuthMode: "oidc", OIDCIssuer: issuer, OIDCClientID: "metis"}); err == nil {
			t.Fatalf("опасный issuer принят: %s", issuer)
		}
	}
}
