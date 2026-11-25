package gui

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/item"
	"github.com/hee-develop/heka/internal/ledger"
	"github.com/hee-develop/heka/internal/review"
)

const (
	defaultLimit = 100
	maxLimit     = 1000
)

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// loadItems 는 items/*.md 를 읽어 id 별로 돌려준다. 읽을 수 없거나 검증에 실패한 파일은 건너뛴다.
func (s *server) loadItems() map[string]*item.Item {
	out := map[string]*item.Item{}
	paths, _ := filepath.Glob(filepath.Join(s.home, "items", "*.md"))
	for _, p := range paths {
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			continue
		}
		it, err := item.Parse(p)
		if err != nil || !it.Valid() {
			continue
		}
		out[it.ID] = it
	}
	return out
}

// dateParam 은 쿼리의 날짜(YYYY-MM-DD)를 읽는다. 없으면 빈 값, 틀리면 400 을 쓰고 false 다.
func dateParam(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return "", true
	}
	if _, err := time.Parse("2006-01-02", v); err != nil || !dateRe.MatchString(v) {
		writeErr(w, http.StatusBadRequest, name+" 는 YYYY-MM-DD 여야 합니다")
		return "", false
	}
	return v, true
}

func rangeParams(w http.ResponseWriter, r *http.Request) (since, until string, ok bool) {
	if since, ok = dateParam(w, r, "since"); !ok {
		return
	}
	until, ok = dateParam(w, r, "until")
	return
}

type metaItems struct {
	Total    int `json:"total"`
	Push     int `json:"push"`
	FindOnly int `json:"find_only"`
	Must     int `json:"must"`
	Review   int `json:"review"` // 검토 대기 건수(review/*.json, 지워진 항목을 가리키는 것은 뺌)
}

type metaLedger struct {
	Rows    int    `json:"rows"`
	FirstTS string `json:"first_ts"`
	LastTS  string `json:"last_ts"`
}

type meta struct {
	Version string     `json:"version"`
	Home    string     `json:"home"`
	Items   metaItems  `json:"items"`
	Ledger  metaLedger `json:"ledger"`
	Errors  int        `json:"errors"`
	Shadow  bool       `json:"shadow"`
}

func (s *server) meta(w http.ResponseWriter, r *http.Request) {
	m := meta{Version: s.version, Home: s.home}
	for _, it := range s.loadItems() {
		m.Items.Total++
		if it.Pushes() {
			m.Items.Push++
		} else {
			m.Items.FindOnly++
		}
		if it.Must {
			m.Items.Must++
		}
	}
	live, _ := review.Live(s.home) // 읽기만 한다. 목록 정리(지우기)는 하지 않는다
	m.Items.Review = len(live)
	rows := ledger.ReadAll(s.home)
	m.Ledger.Rows = len(rows)
	for _, row := range rows {
		if row.TS == "" {
			continue
		}
		if m.Ledger.FirstTS == "" || row.TS < m.Ledger.FirstTS {
			m.Ledger.FirstTS = row.TS
		}
		if row.TS > m.Ledger.LastTS {
			m.Ledger.LastTS = row.TS
		}
	}
	if b, err := os.ReadFile(filepath.Join(s.home, "ledger", "errors.log")); err == nil {
		m.Errors = bytes.Count(b, []byte("\n"))
		if len(b) > 0 && b[len(b)-1] != '\n' {
			m.Errors++
		}
	}
	if cfg, err := config.Load(s.home); err == nil {
		m.Shadow = cfg.ClaudeCode.Shadow
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *server) overview(w http.ResponseWriter, r *http.Request) {
	since, until, ok := rangeParams(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, computeOverview(ledger.ReadAll(s.home), since, until))
}

func (s *server) sessions(w http.ResponseWriter, r *http.Request) {
	since, until, ok := rangeParams(w, r)
	if !ok {
		return
	}
	limit := defaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeErr(w, http.StatusBadRequest, "limit 는 1 이상의 정수여야 합니다")
			return
		}
		limit = min(n, maxLimit)
	}
	writeJSON(w, http.StatusOK, computeSessions(ledger.ReadAll(s.home), since, until, limit))
}

func (s *server) session(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rows := sessionRows(ledger.ReadAll(s.home), id)
	if id == "" || len(rows) == 0 {
		writeErr(w, http.StatusNotFound, "세션이 없습니다")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		ID    string             `json:"id"`
		Rows  []ledger.Row       `json:"rows"`
		Items map[string]ItemRef `json:"items"`
	}{id, rows, itemRefs(rows, s.loadItems())})
}

func (s *server) items(w http.ResponseWriter, r *http.Request) {
	stats := ledger.StatsByItem(ledger.ReadAll(s.home))
	writeJSON(w, http.StatusOK, computeItems(s.loadItems(), stats))
}

func (s *server) item(w http.ResponseWriter, r *http.Request) {
	it := s.loadItems()[r.PathValue("id")]
	if it == nil {
		writeErr(w, http.StatusNotFound, "항목이 없습니다")
		return
	}
	rows := ledger.ReadAll(s.home)
	writeJSON(w, http.StatusOK, computeItemDetail(it, rows, ledger.StatsByItem(rows)))
}
