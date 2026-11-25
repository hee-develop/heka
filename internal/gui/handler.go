// Package gui 는 `heka gui` 의 읽기 전용 서버다. 원장과 항목을 요청마다 읽어 JSON 으로 답하고
// 화면 자산(web/)을 낸다. 어디에도 쓰지 않는다.
package gui

import (
	"embed"
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

//go:embed web
var webFS embed.FS

const csp = "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:"

// 화면 자산의 경로와 내려줄 형식이다. 그 밖의 경로는 404 다.
var assets = map[string]struct{ file, ctype string }{
	"/":          {"web/index.html", "text/html; charset=utf-8"},
	"/app.js":    {"web/app.js", "text/javascript; charset=utf-8"},
	"/style.css": {"web/style.css", "text/css; charset=utf-8"},
}

type server struct {
	home, version string
}

// NewHandler 는 home(HEKA_HOME)을 읽는 핸들러를 만든다. GET·HEAD 만 받고,
// Host 가 loopback 이름(127.0.0.1, localhost, ::1)이 아니면 403 이다(DNS 리바인딩 방지).
func NewHandler(home, version string) http.Handler {
	s := &server{home: home, version: version}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/meta", s.meta)
	mux.HandleFunc("/api/overview", s.overview)
	mux.HandleFunc("/api/sessions", s.sessions)
	mux.HandleFunc("/api/sessions/{id}", s.session)
	mux.HandleFunc("/api/items", s.items)
	mux.HandleFunc("/api/items/{id}", s.item)
	mux.HandleFunc("/", s.static)
	return guard(mux)
}

// guard 는 보안 머리말을 달고 Host·메서드를 검사한다.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", csp)
		if !loopbackHost(r.Host) {
			http.Error(w, "허용하지 않는 Host 입니다", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.Set("Allow", "GET, HEAD")
			http.Error(w, "읽기 전용입니다", http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loopbackHost 는 Host 머리말의 호스트 부분이 loopback 이름인가. 포트는 보지 않는다.
func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	} else {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	switch strings.ToLower(host) {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

func (s *server) static(w http.ResponseWriter, r *http.Request) {
	a, ok := assets[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	b, err := webFS.ReadFile(a.file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", a.ctype)
	_, _ = w.Write(b)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
