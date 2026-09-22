// Package webui поставляет SPA на одном origin с API и только публичную конфигурацию входа.
package webui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Config содержит исключительно публичные параметры браузерного клиента.
// Секреты сервера никогда не передаются этому пакету.
type Config struct {
	AuthMode     string `json:"authMode"`
	OIDCIssuer   string `json:"oidcIssuer,omitempty"`
	OIDCClientID string `json:"oidcClientId,omitempty"`
	OIDCScope    string `json:"oidcScope,omitempty"`
}

// New читает готовую сборку Vite и возвращает обработчик SPA, служебных маршрутов и API.
// Конфигурация и index.html не кешируются, чтобы смена IdP и обновления не требовали очистки браузера.
func New(files fs.FS, api http.Handler, cfg Config) (http.Handler, error) {
	if cfg.AuthMode != "token" && cfg.AuthMode != "oidc" {
		return nil, fmt.Errorf("webui: неизвестный режим входа %q", cfg.AuthMode)
	}
	if cfg.AuthMode == "oidc" {
		u, err := url.Parse(cfg.OIDCIssuer)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || cfg.OIDCClientID == "" {
			return nil, fmt.Errorf("webui: требуются публичный HTTP(S) issuer и OIDC client ID")
		}
	} else {
		cfg.OIDCIssuer, cfg.OIDCClientID, cfg.OIDCScope = "", "", ""
	}
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		return nil, fmt.Errorf("webui: чтение index.html: %w", err)
	}
	if !bytes.Contains(index, []byte("<head>")) {
		return nil, fmt.Errorf("webui: в index.html отсутствует head")
	}
	index = bytes.Replace(index, []byte("<head>"), []byte("<head>\n<script src=\"/runtime-config.js\"></script>"), 1)
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("webui: сериализация публичной конфигурации: %w", err)
	}
	configJS := append([]byte("window.__METIS_CONFIG__ = "), configJSON...)
	configJS = append(configJS, ';', '\n')
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'; base-uri 'self'; object-src 'none'")
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || r.URL.Path == "/metrics" {
			api.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/runtime-config.js" {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			http.ServeContent(w, r, "runtime-config.js", time.Time{}, bytes.NewReader(configJS))
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" && (!fs.ValidPath(name) || strings.Contains(name, "\\")) {
			http.NotFound(w, r)
			return
		}
		if name != "" && name != "index.html" {
			info, statErr := fs.Stat(files, name)
			if statErr == nil && !info.IsDir() {
				body, readErr := fs.ReadFile(files, name)
				if readErr != nil {
					http.Error(w, "asset unavailable", http.StatusInternalServerError)
					return
				}
				if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
					w.Header().Set("Content-Type", contentType)
				}
				w.Header().Set("Cache-Control", "no-cache")
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				http.ServeContent(w, r, name, info.ModTime(), bytes.NewReader(body))
				return
			}
			if path.Ext(name) != "" || strings.HasPrefix(name, "assets/") || (statErr == nil && info.IsDir()) {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	}), nil
}
