package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hee-develop/heka/internal/ledger"
)

const itemFile = `---
id: %s
text: %s
source: user
about: [예시]
scope: [repo/sample-app]
%s---
본문 %s
`

// fixture 는 지어낸 항목·원장으로 HEKA_HOME 을 만든다.
func fixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	item := func(id, text, extra string) string {
		return fmt.Sprintf(itemFile, id, text, extra, id)
	}
	write("items/alpha-rule.md", item("alpha-rule", "알파 규칙을 지킨다.", "must: true\nwhen: {moment: [prompt]}\ncheck: {target: command, pattern: foo, mode: log}\n"))
	write("items/beta-note.md", item("beta-note", "베타 참고.", "push: false\n"))
	write("items/broken.md", "머리말 없음\n")
	write("items/_pending/gamma.md", item("gamma", "대기 중.", ""))
	write("ledger/errors.log", "t\tPreToolUse\t오류1\nt\tStop\t오류2\n")
	write("config.yaml", "claude_code:\n  shadow: true\n")

	rows := []ledger.Row{
		{TS: "2026-11-01T01:00:00.000Z", Session: "s-1", Event: "SessionStart", Moment: "session_start", DurationMS: 5},
		{TS: "2026-11-01T01:00:10.000Z", Session: "s-1", Event: "UserPromptSubmit", Moment: "prompt", Prompt: "첫 프롬프트 " + strings.Repeat("가", 100),
			Injected: []ledger.Injected{{ID: "alpha-rule"}, {ID: "ghost-item"}},
			Dropped:  []ledger.Dropped{{ID: "beta-note", Stage: "판정"}, {ID: "beta-note", Stage: "범위"}, {ID: "alpha-rule", Stage: "중복"}},
			Asked:    []string{"git.push"}, Tokens: 40, DurationMS: 100,
			Judges: []ledger.Judged{{ID: "beta-note", Inject: false, MS: 1000}, {ID: "alpha-rule", Inject: true, MS: 3000}, {ID: "x", Error: true, Timeout: true, MS: 5000}},
			Pick:   &ledger.Pick{Listed: 3, Items: []ledger.Picked{{ID: "alpha-rule", Inject: true}, {ID: "beta-note"}}}},
		{TS: "2026-11-01T01:00:20.000Z", Session: "s-1", Event: "PreToolUse", Moment: "tool", Agent: "ag-1", Shadow: true, DurationMS: 20,
			Checks:  []ledger.Checked{{ID: "alpha-rule", Result: "fail", Blocked: true}, {ID: "alpha-rule", Result: "pass"}, {ID: "alpha-rule", Result: "skip"}},
			Confirm: []string{"alpha-rule"}},
		{TS: "2026-11-02T01:00:00.000Z", Session: "s-2", Event: "UserPromptSubmit", Moment: "prompt", ToolMade: true, Prompt: "<task-notification>", DurationMS: 300},
		{TS: "2026-11-02T01:00:05.000Z", Session: "s-2", Event: "UserPromptSubmit", Moment: "prompt", Prompt: "둘째 세션 프롬프트", DurationMS: 200,
			Injected: []ledger.Injected{{ID: "alpha-rule"}}},
		{TS: "2026-11-03T01:00:00.000Z", Event: "find", Moment: "find", Prompt: "질의", Injected: []ledger.Injected{{ID: "alpha-rule"}}},
		{TS: "2026-11-03T02:00:00.000Z", Event: "flag", Moment: "flag", Injected: []ledger.Injected{{ID: "alpha-rule", Why: "틀림"}}},
	}
	var sb strings.Builder
	for _, r := range rows {
		b, _ := json.Marshal(r)
		sb.Write(b)
		sb.WriteByte('\n')
	}
	sb.WriteString("깨진 줄\n")
	write("ledger/2026-11.jsonl", sb.String())
	return home
}

// do 는 핸들러에 요청을 보내 상태와 본문을 돌려준다.
func do(t *testing.T, h http.Handler, method, target, host string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func getJSON(t *testing.T, h http.Handler, target string, v any) int {
	t.Helper()
	rec := do(t, h, "GET", target, "127.0.0.1:7788")
	if ct := rec.Header().Get("Content-Type"); rec.Code == 200 && ct != "application/json; charset=utf-8" {
		t.Errorf("%s Content-Type = %q", target, ct)
	}
	if v != nil && rec.Code == 200 {
		if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
			t.Fatalf("%s: JSON 해석 실패: %v\n%s", target, err, rec.Body.String())
		}
	}
	return rec.Code
}

func TestMeta(t *testing.T) {
	h := NewHandler(fixture(t), "v-test")
	var m meta
	if code := getJSON(t, h, "/api/meta", &m); code != 200 {
		t.Fatalf("code = %d", code)
	}
	if m.Version != "v-test" || m.Items != (metaItems{Total: 2, Push: 1, FindOnly: 1, Must: 1, Pending: 1}) {
		t.Errorf("meta = %+v", m)
	}
	if m.Ledger.Rows != 7 || m.Ledger.FirstTS != "2026-11-01T01:00:00.000Z" || m.Ledger.LastTS != "2026-11-03T02:00:00.000Z" {
		t.Errorf("ledger = %+v", m.Ledger)
	}
	if m.Errors != 2 || !m.Shadow {
		t.Errorf("errors=%d shadow=%v", m.Errors, m.Shadow)
	}
}

func TestOverview(t *testing.T) {
	h := NewHandler(fixture(t), "v")
	var ov Overview
	if code := getJSON(t, h, "/api/overview", &ov); code != 200 {
		t.Fatalf("code = %d", code)
	}
	if ov.Range.Rows != 7 || ov.Range.Sessions != 2 {
		t.Errorf("range = %+v", ov.Range)
	}
	var names []string
	for _, m := range ov.Moments {
		names = append(names, m.Moment)
	}
	if strings.Join(names, ",") != "session_start,prompt,tool,subagent_start,stop" {
		t.Fatalf("순간 = %v", names)
	}
	by := map[string]MomentSummary{}
	for _, m := range ov.Moments {
		by[m.Moment] = m
	}
	p := by["prompt"]
	if p.Rows != 3 || p.RowsInjected != 2 || p.RowsSilent != 1 || p.ToolMade != 1 || p.Injected != 3 || p.Tokens != 40 || p.Asked != 1 {
		t.Errorf("prompt = %+v", p)
	}
	wantDrop := map[string]int{"범위": 1, "자격": 0, "일치": 0, "중복": 1, "예산": 0, "판정": 1}
	for k, v := range wantDrop {
		if p.Dropped[k] != v {
			t.Errorf("dropped[%s] = %d, 기대 %d", k, p.Dropped[k], v)
		}
	}
	if len(p.Dropped) != len(wantDrop) {
		t.Errorf("dropped 키 = %v", p.Dropped)
	}
	if p.Judges.N != 3 || p.Judges.Inject != 1 || p.Judges.Error != 1 || p.Judges.Timeout != 1 || p.Judges.MS != (Dist{3000, 5000, 5000}) {
		t.Errorf("judges = %+v", p.Judges)
	}
	if p.Pick != (PickSummary{N: 1, Picked: 2, PickedInject: 1}) {
		t.Errorf("pick = %+v", p.Pick)
	}
	if p.Duration != (Dist{200, 300, 300}) {
		t.Errorf("duration = %+v", p.Duration)
	}
	if len(p.TopItems) != 2 || p.TopItems[0] != (TopItem{"alpha-rule", 2}) || p.TopItems[1] != (TopItem{"ghost-item", 1}) {
		t.Errorf("top = %+v", p.TopItems)
	}
	tl := by["tool"]
	if tl.Checks != (Checks{Pass: 1, Fail: 1, Skip: 1, Blocked: 1}) || tl.Confirm != 1 || tl.ShadowRows != 1 || tl.RowsSilent != 1 {
		t.Errorf("tool = %+v", tl)
	}
	if by["stop"].Rows != 0 || by["stop"].TopItems == nil || by["stop"].Dropped["판정"] != 0 {
		t.Errorf("빈 순간 = %+v", by["stop"])
	}
	if ov.Events["find"] != 1 || ov.Events["flag"] != 1 || len(ov.Events) != 2 {
		t.Errorf("events = %v", ov.Events)
	}
}

func TestOverviewRange(t *testing.T) {
	h := NewHandler(fixture(t), "v")
	var ov Overview
	getJSON(t, h, "/api/overview?since=2026-11-02&until=2026-11-02", &ov)
	if ov.Range.Rows != 2 || ov.Range.Sessions != 1 || ov.Range.Since != "2026-11-02" {
		t.Errorf("range = %+v", ov.Range)
	}
	getJSON(t, h, "/api/overview?since=2026-11-03", &ov)
	if ov.Range.Rows != 2 || len(ov.Events) != 2 {
		t.Errorf("since 만: %+v", ov.Range)
	}
	getJSON(t, h, "/api/overview?until=2026-11-01", &ov)
	if ov.Range.Rows != 3 {
		t.Errorf("until 만: %+v", ov.Range)
	}
	for _, q := range []string{"since=2026-1-1", "until=abc", "since=2026-13-40", "since=2026-11-01x"} {
		if code := getJSON(t, h, "/api/overview?"+q, nil); code != 400 {
			t.Errorf("%s: code = %d, 기대 400", q, code)
		}
	}
	if code := getJSON(t, h, "/api/sessions?since=bad", nil); code != 400 {
		t.Errorf("sessions 잘못된 날짜 code = %d", code)
	}
}

func TestSessions(t *testing.T) {
	h := NewHandler(fixture(t), "v")
	var list []SessionSummary
	if code := getJSON(t, h, "/api/sessions", &list); code != 200 {
		t.Fatalf("code = %d", code)
	}
	if len(list) != 2 || list[0].ID != "s-2" || list[1].ID != "s-1" {
		t.Fatalf("목록 = %+v", list)
	}
	s1 := list[1]
	if s1.Rows != 3 || s1.Injected != 2 || s1.Dropped != 3 || s1.Agents != 1 || s1.Blocked != 1 || s1.Confirm != 1 || !s1.Shadow {
		t.Errorf("s-1 = %+v", s1)
	}
	if s1.FirstTS != "2026-11-01T01:00:00.000Z" || s1.LastTS != "2026-11-01T01:00:20.000Z" {
		t.Errorf("s-1 시각 = %s %s", s1.FirstTS, s1.LastTS)
	}
	if want := "첫 프롬프트 " + strings.Repeat("가", 80-len([]rune("첫 프롬프트 "))); s1.Prompt != want {
		t.Errorf("prompt = %q", s1.Prompt)
	}
	if list[0].Prompt != "둘째 세션 프롬프트" || list[0].Shadow {
		t.Errorf("s-2 = %+v", list[0])
	}
	getJSON(t, h, "/api/sessions?limit=1", &list)
	if len(list) != 1 || list[0].ID != "s-2" {
		t.Errorf("limit=1: %+v", list)
	}
	getJSON(t, h, "/api/sessions?until=2026-11-01", &list)
	if len(list) != 1 || list[0].ID != "s-1" {
		t.Errorf("until: %+v", list)
	}
	for _, q := range []string{"limit=0", "limit=x", "limit=-3"} {
		if code := getJSON(t, h, "/api/sessions?"+q, nil); code != 400 {
			t.Errorf("%s: code = %d", q, code)
		}
	}
	if code := getJSON(t, h, "/api/sessions?limit=99999", nil); code != 200 {
		t.Errorf("limit 상한은 잘라서 받아야 함: %d", code)
	}
}

func TestSessionDetail(t *testing.T) {
	h := NewHandler(fixture(t), "v")
	var d struct {
		ID    string
		Rows  []ledger.Row
		Items map[string]ItemRef
	}
	if code := getJSON(t, h, "/api/sessions/s-1", &d); code != 200 {
		t.Fatalf("code = %d", code)
	}
	if d.ID != "s-1" || len(d.Rows) != 3 || d.Rows[0].Event != "SessionStart" || d.Rows[2].Event != "PreToolUse" {
		t.Errorf("rows = %+v", d.Rows)
	}
	a := d.Items["alpha-rule"]
	if !a.Exists || !a.Must || a.Text != "알파 규칙을 지킨다." {
		t.Errorf("alpha = %+v", a)
	}
	if g := d.Items["ghost-item"]; g.Exists || g.Text != "" {
		t.Errorf("ghost = %+v", g)
	}
	for _, id := range []string{"beta-note", "x"} { // 탈락·판정에만 나온 id
		if _, ok := d.Items[id]; !ok {
			t.Errorf("items 에 %s 가 없음", id)
		}
	}
	if code := getJSON(t, h, "/api/sessions/nope", nil); code != 404 {
		t.Errorf("없는 세션 code = %d", code)
	}
}

func TestItems(t *testing.T) {
	h := NewHandler(fixture(t), "v")
	var list []ItemView
	if code := getJSON(t, h, "/api/items", &list); code != 200 {
		t.Fatalf("code = %d", code)
	}
	if len(list) != 2 || list[0].ID != "alpha-rule" || list[1].ID != "beta-note" {
		t.Fatalf("목록 = %+v", list)
	}
	a := list[0]
	if !a.Must || !a.Push || !a.HasCheck || a.Source != "user" || a.Confirm != "false" || len(a.When.Moment) != 1 {
		t.Errorf("alpha = %+v", a)
	}
	// 훅의 줄만 센다: find 줄의 주입은 세지 않는다. flag 는 flag 줄에서 센다.
	if a.Stats.Injected != 2 || a.Stats.LastInjected != "2026-11-02T01:00:05.000Z" || a.Stats.CheckFail != 1 || a.Stats.Flagged != 1 || a.Stats.Dropped != 1 {
		t.Errorf("alpha stats = %+v", a.Stats)
	}
	b := list[1]
	if b.Push || b.Stats.Dropped != 1 || b.Stats.Injected != 0 { // 범위 탈락은 세지 않는다
		t.Errorf("beta = %+v", b)
	}
}

func TestItemDetail(t *testing.T) {
	h := NewHandler(fixture(t), "v")
	var d struct {
		ItemView
		Body   string
		Recent []ledger.Row
	}
	if code := getJSON(t, h, "/api/items/alpha-rule", &d); code != 200 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(d.Body, "본문 alpha-rule") {
		t.Errorf("body = %q", d.Body)
	}
	if d.Stats.ByStage["중복"] != 1 || d.Stats.ByStage["판정"] != 0 || len(d.Stats.ByStage) != 3 {
		t.Errorf("by_stage = %v", d.Stats.ByStage)
	}
	if len(d.Recent) != 5 || d.Recent[0].Event != "flag" || d.Recent[4].Event != "UserPromptSubmit" {
		t.Errorf("recent = %d줄 %+v", len(d.Recent), d.Recent)
	}
	if code := getJSON(t, h, "/api/items/nope", nil); code != 404 {
		t.Errorf("없는 항목 code = %d", code)
	}
	if code := getJSON(t, h, "/api/items/broken", nil); code != 404 {
		t.Errorf("깨진 항목 code = %d", code)
	}
	if code := getJSON(t, h, "/api/items/..%2Fconfig", nil); code != 404 {
		t.Errorf("경로 탈출 code = %d", code)
	}
}

func TestRecentLimit(t *testing.T) {
	home := fixture(t)
	f, err := os.OpenFile(filepath.Join(home, "ledger", "2026-12.jsonl"), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		b, _ := json.Marshal(ledger.Row{TS: "2026-12-01T00:00:00.000Z", Session: "s-9", Event: "PreToolUse", Moment: "tool", Injected: []ledger.Injected{{ID: "beta-note"}}})
		f.Write(append(b, '\n'))
	}
	f.Close()
	var d struct{ Recent []ledger.Row }
	getJSON(t, NewHandler(home, "v"), "/api/items/beta-note", &d)
	if len(d.Recent) != 30 {
		t.Errorf("recent = %d, 기대 30", len(d.Recent))
	}
}

func TestEmptyHome(t *testing.T) {
	h := NewHandler(filepath.Join(t.TempDir(), "없는폴더"), "v")
	for _, p := range []string{"/api/meta", "/api/overview", "/api/sessions", "/api/items", "/"} {
		if code := do(t, h, "GET", p, "localhost:1").Code; code != 200 {
			t.Errorf("%s: code = %d", p, code)
		}
	}
	rec := do(t, h, "GET", "/api/sessions", "localhost:1")
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("빈 sessions = %q", rec.Body.String())
	}
	rec = do(t, h, "GET", "/api/items", "localhost:1")
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("빈 items = %q", rec.Body.String())
	}
	var ov Overview
	getJSON(t, h, "/api/overview", &ov)
	if len(ov.Moments) != 5 || ov.Range.Rows != 0 {
		t.Errorf("빈 overview = %+v", ov.Range)
	}
}

func TestGuard(t *testing.T) {
	h := NewHandler(fixture(t), "v")
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		rec := do(t, h, m, "/api/items", "127.0.0.1:7788")
		if rec.Code != 405 {
			t.Errorf("%s code = %d, 기대 405", m, rec.Code)
		}
	}
	for _, host := range []string{"127.0.0.1:7788", "localhost:7788", "[::1]:7788", "localhost"} {
		if code := do(t, h, "GET", "/api/meta", host).Code; code != 200 {
			t.Errorf("Host %q code = %d", host, code)
		}
	}
	for _, host := range []string{"evil.example:7788", "127.0.0.1.evil.example", "192.168.0.2:7788", "", "localhost.evil.example:1"} {
		if code := do(t, h, "GET", "/api/meta", host).Code; code != 403 {
			t.Errorf("Host %q code = %d, 기대 403", host, code)
		}
	}
	if code := do(t, h, "POST", "/api/meta", "evil.example").Code; code != 403 {
		t.Errorf("틀린 Host 의 POST = %d", code)
	}
	if code := do(t, h, "HEAD", "/", "127.0.0.1:1").Code; code != 200 {
		t.Errorf("HEAD = %d", code)
	}
	for _, p := range []string{"/nope", "/api", "/api/nope", "/index.html", "/web/app.js", "/api/sessions/a/b"} {
		if code := do(t, h, "GET", p, "127.0.0.1:1").Code; code != 404 {
			t.Errorf("%s code = %d, 기대 404", p, code)
		}
	}
}

func TestHeadersAndStatic(t *testing.T) {
	h := NewHandler(fixture(t), "v")
	cases := []struct{ path, ctype string }{
		{"/", "text/html; charset=utf-8"},
		{"/app.js", "text/javascript; charset=utf-8"},
		{"/style.css", "text/css; charset=utf-8"},
		{"/api/meta", "application/json; charset=utf-8"},
	}
	for _, c := range cases {
		rec := do(t, h, "GET", c.path, "127.0.0.1:1")
		if rec.Code != 200 || rec.Header().Get("Content-Type") != c.ctype || rec.Body.Len() == 0 {
			t.Errorf("%s: code=%d type=%q len=%d", c.path, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
		}
	}
	for _, c := range []struct {
		method, path, host string
		code               int
	}{{"GET", "/api/meta", "127.0.0.1:1", 200}, {"GET", "/nope", "127.0.0.1:1", 404}, {"POST", "/", "127.0.0.1:1", 405}, {"GET", "/", "evil", 403}} {
		rec := do(t, h, c.method, c.path, c.host)
		if rec.Code != c.code {
			t.Errorf("%s %s code = %d", c.method, c.path, rec.Code)
		}
		want := map[string]string{
			"X-Content-Type-Options":  "nosniff",
			"Cache-Control":           "no-store",
			"Content-Security-Policy": "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:",
		}
		for k, v := range want {
			if got := rec.Header().Get(k); got != v {
				t.Errorf("%s %s: %s = %q", c.method, c.path, k, got)
			}
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("CORS 머리말이 있음")
		}
	}
}

// 서버는 원장·항목·설정을 바꾸지 않는다.
func TestReadOnly(t *testing.T) {
	home := fixture(t)
	snap := func() string {
		var sb strings.Builder
		filepath.Walk(home, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				b, _ := os.ReadFile(p)
				sb.WriteString(p + "\x00" + string(b) + "\x00")
			}
			return nil
		})
		return sb.String()
	}
	before := snap()
	h := NewHandler(home, "v")
	for _, p := range []string{"/api/meta", "/api/overview", "/api/sessions", "/api/sessions/s-1", "/api/items", "/api/items/alpha-rule"} {
		do(t, h, "GET", p, "127.0.0.1:1")
	}
	if snap() != before {
		t.Error("요청 뒤 HEKA_HOME 의 내용이 바뀜")
	}
}
