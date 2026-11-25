package regjudge

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/item"
	"github.com/hee-develop/heka/internal/ledger"
	"github.com/hee-develop/heka/internal/register"
	"github.com/hee-develop/heka/internal/review"
)

var now = time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)

func newEnv(t *testing.T, yaml string) register.Env {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("global_scopes: [personal]\n"+yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	return register.Env{Home: home, Cfg: cfg, Now: now, Spawn: func(string) error { return nil }}
}

func add(t *testing.T, e register.Env, id, text, source string, about ...string) {
	t.Helper()
	r, err := register.Add(e, register.Input{ID: id, Text: text, About: about, Scope: []string{"personal"}, Source: source, Body: "본문 " + id})
	if err != nil || r.Status != "added" {
		t.Fatalf("add %s: %+v %v", id, r, err)
	}
}

// script 는 표준 입력을 in.txt 에 저장하고 stdout 을 돌려주는 가짜 판정 명령이다.
func script(t *testing.T, stdout string) (command []string, inFile string) {
	t.Helper()
	dir := t.TempDir()
	inFile = filepath.Join(dir, "in.txt")
	out := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(out, []byte(stdout), 0o644); err != nil {
		t.Fatal(err)
	}
	return []string{"sh", "-c", `cat > "$0"; cat "$1"`, inFile, out}, inFile
}

func parseItem(t *testing.T, content string) *item.Item {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	it, err := item.Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

// 입력 글의 모양은 측정 도구와 같아야 하므로 글자 단위로 고정한다.
func TestBuildInputFormat(t *testing.T) {
	full := parseItem(t, "---\nid: x\ntext: 커밋은 한국어로 쓴다.\nmust: true\nwhen:\n  moment: [tool, prompt]\n  action: [git.commit, git.push]\n  repo: app\n  path: src/**\n  agent: Explore\nabout: [커밋, 메시지]\nscope: [personal, team/a]\nsource: user\nquote: q\n---\n첫 줄\n둘째 줄\n")
	empty := parseItem(t, "---\nid: x\ntext: 린트를 돌린다.\nabout: [린트]\nscope: [personal]\nsource: doc\n---\n")
	got := BuildInput("지시문\n", full, []*item.Item{empty})
	want := "지시문\n\n[새 항목]\nid: x\n출처: user\n범위: personal, team/a\n필수: true\n" +
		"조건: moment=tool|prompt; action=git.commit|git.push; repo=app; path=src/**; agent=Explore\n주제어: 커밋, 메시지\n주입문: 커밋은 한국어로 쓴다.\n본문:\n첫 줄\n둘째 줄\n\n" +
		"[후보 1]\nid: x\n출처: doc\n범위: personal\n필수: false\n조건: 없음\n주제어: 린트\n주입문: 린트를 돌린다.\n본문:\n\n"
	if got != want {
		t.Errorf("입력 글:\n%q\n기대:\n%q", got, want)
	}
	// 조건은 비지 않은 것만 이어 쓴다.
	one := parseItem(t, "---\nid: x\ntext: t\nwhen:\n  action: git.commit\nabout: [a]\nscope: [personal]\nsource: ai\n---\n")
	if !strings.Contains(Block(one), "조건: action=git.commit\n") {
		t.Errorf("조건: %q", Block(one))
	}
}

func TestBlockTruncatesBodyAt1500Runes(t *testing.T) {
	long := strings.Repeat("가", 1500)
	exact := parseItem(t, "---\nid: x\ntext: t\nabout: [a]\nscope: [personal]\nsource: doc\n---\n"+long+"\n")
	if !strings.HasSuffix(Block(exact), "본문:\n"+long) {
		t.Error("1500자는 자르지 않음")
	}
	over := parseItem(t, "---\nid: x\ntext: t\nabout: [a]\nscope: [personal]\nsource: doc\n---\n"+long+"나머지\n")
	if !strings.HasSuffix(Block(over), "본문:\n"+long+"…(생략)") {
		t.Errorf("1500자까지 자르고 끝에 …(생략): %q", Block(over)[len(Block(over))-20:])
	}
}

func TestParse(t *testing.T) {
	cands := []string{"a", "b", "c"}
	out := "설명 {무시}\n```json\n{\"verdicts\":[" +
		`{"id":"a","verdict":"separate","reason":"다름 } 괄호"},` +
		`{"id":"zzz","verdict":"separate","reason":"후보 아님"},` +
		`{"id":"b","verdict":"weird","reason":"모름"},` +
		`{"id":"c","verdict":"merge","reason":"같음","merged":{"text":"","body":"b","about":[]}},` +
		`{"id":"a","verdict":"conflict","reason":"중복은 처음 것만"}` +
		"]}\n```\n끝 {x}"
	// 첫 { 가 설명 속 괄호라서 읽기에 실패한다.
	if _, err := Parse(out, cands); err == nil {
		t.Error("첫 { 가 JSON 이 아니면 오류")
	}
	out = strings.TrimPrefix(out, "설명 {무시}\n")
	got, err := Parse(out, cands)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["a"].Verdict != "separate" || got["a"].Reason != "다름 } 괄호" {
		t.Errorf("a 만 남아야 함: %+v", got)
	}
	got, err = Parse(`앞글 {"verdicts":[{"id":"c","verdict":"merge","reason":"r","merged":{"text":"합침","body":"b","about":["x"]}}]} 뒷글`, cands)
	if err != nil || got["c"].Merged == nil || got["c"].Merged.Text != "합침" {
		t.Errorf("앞뒤 글: %+v %v", got, err)
	}
	for _, bad := range []string{"", "그냥 글", `{"verdicts":[`, `{"verdicts":"x"}`} {
		if _, err := Parse(bad, cands); err == nil {
			t.Errorf("%q: 오류여야 함", bad)
		}
	}
}

func entries(e register.Env) map[string]review.Entry {
	m := map[string]review.Entry{}
	for _, en := range review.List(e) {
		m[en.ID] = en
	}
	return m
}

func lastRow(t *testing.T, e register.Env, event string) ledger.Row {
	t.Helper()
	var found *ledger.Row
	for _, r := range ledger.ReadAll(e.Home) {
		if r.Event == event {
			r := r
			found = &r
		}
	}
	if found == nil {
		t.Fatalf("원장에 %s 줄이 없음", event)
	}
	return *found
}

func setup(t *testing.T, yaml string) register.Env {
	e := newEnv(t, yaml)
	add(t, e, "old-sep", "테스트는 푸시 전에 돌린다.", "doc", "푸시 규칙")
	add(t, e, "old-merge", "린트도 푸시 전에 돌린다.", "doc", "푸시 규칙")
	add(t, e, "old-conf", "푸시 전에는 아무것도 돌리지 않는다.", "doc", "푸시 규칙")
	add(t, e, "new-one", "푸시 전에 테스트와 린트를 돌린다.", "user", "푸시 규칙")
	return e
}

func TestRunVerdicts(t *testing.T) {
	notes := filepath.Join(t.TempDir(), "notes.txt")
	e := setup(t, "")
	reply := `{"verdicts":[` +
		`{"id":"old-conf","verdict":"conflict","reason":"반대로 지시"},` +
		`{"id":"old-merge","verdict":"merge","reason":"같은 규칙","merged":{"text":"푸시 전에 테스트와 린트를 돌린다.","body":"본문 new-one 본문 old-merge 합친 본문","about":["푸시 규칙"]}},` +
		`{"id":"old-sep","verdict":"separate","reason":"다른 내용"}]}`
	cmd, inFile := script(t, reply)
	e.Cfg.RegisterJudge.Command = cmd
	e.Cfg.Review.NotifyCommand = []string{"sh", "-c", `echo "$0" >> ` + notes}
	var out bytes.Buffer
	before, _ := os.ReadFile(filepath.Join(e.Home, "items", "new-one.md"))
	if err := Run(e, &out, "new-one"); err != nil {
		t.Fatal(err)
	}
	// 입력: 지시문, 새 항목, 후보 3건(자기 자신 제외).
	in, _ := os.ReadFile(inFile)
	if !strings.HasPrefix(string(in), "너는 개인 지식 저장소의 중복·모순 판정기다.") || !strings.Contains(string(in), "[새 항목]\nid: new-one\n") ||
		!strings.Contains(string(in), "[후보 3]\n") || strings.Contains(string(in), "[후보 4]") || strings.Contains(string(in), "[후보 1]\nid: new-one") {
		t.Errorf("입력:\n%s", in)
	}
	got := entries(e)
	if len(got) != 2 || got["new-one--old-sep"].ID != "" {
		t.Fatalf("separate 는 검토 항목이 아님: %v", got)
	}
	if c := got["new-one--old-conf"]; c.Kind != "conflict" || c.TargetID != "old-conf" || c.Reason != "반대로 지시" || c.Created != "2026-11-26T09:00:00Z" {
		t.Errorf("conflict = %+v", c)
	}
	if m := got["new-one--old-merge"]; m.Kind != "merge" || m.Merged == nil || m.Merged.Text != "푸시 전에 테스트와 린트를 돌린다." || m.Merged.Body != "본문 new-one 본문 old-merge 합친 본문" {
		t.Errorf("merge = %+v", m)
	}
	// 모순은 곧바로 알린다. 합치기·따로는 알리지 않는다.
	n, _ := os.ReadFile(notes)
	if string(n) != "heka: 새 항목 new-one 가 old-conf 과 모순으로 판정됐습니다. 세션에서 'heka 검토해줘', 터미널에서 heka review\n" {
		t.Errorf("알림 = %q", n)
	}
	// 판정은 항목 파일을 고치거나 지우지 않는다.
	if after, _ := os.ReadFile(filepath.Join(e.Home, "items", "new-one.md")); !bytes.Equal(before, after) {
		t.Error("항목 파일이 바뀜")
	}
	for _, id := range []string{"old-sep", "old-merge", "old-conf", "new-one"} {
		if _, err := os.Stat(filepath.Join(e.Home, "items", id+".md")); err != nil {
			t.Errorf("%s 가 지워짐", id)
		}
	}
	row := lastRow(t, e, "register_judge")
	if len(row.Injected) != 1 || row.Injected[0].ID != "new-one" || row.RegJudge == nil || row.RegJudge.MS <= 0 || row.RegJudge.Error || row.RegJudge.Timeout {
		t.Fatalf("원장 줄 = %+v", row)
	}
	var vs []string
	for _, v := range row.RegJudge.Verdicts {
		vs = append(vs, v.ID+":"+v.Verdict+":"+v.Reason)
	}
	slices.Sort(vs)
	if !reflect.DeepEqual(vs, []string{"old-conf:conflict:반대로 지시", "old-merge:merge:같은 규칙", "old-sep:separate:다른 내용"}) {
		t.Errorf("원장의 답 = %v", vs)
	}
	if lastRow(t, e, "review_notify").Moment != "conflict:command" {
		t.Error("알림도 원장에 남아야 함")
	}
}

func TestRunUnjudgedCases(t *testing.T) {
	cases := []struct {
		name        string
		command     func(t *testing.T) []string
		timeout     time.Duration
		timeoutFlag bool
	}{
		{"명령 없음", func(*testing.T) []string { return nil }, 0, false},
		{"깨진 답", func(t *testing.T) []string { c, _ := script(t, "판정할 수 없습니다"); return c }, 0, false},
		{"비정상 종료", func(*testing.T) []string { return []string{"sh", "-c", "cat >/dev/null; exit 3"} }, 0, false},
		{"시간 초과", func(*testing.T) []string { return []string{"sh", "-c", "sleep 5"} }, 200 * time.Millisecond, true},
		{"답이 일부 후보에 없음", func(t *testing.T) []string {
			c, _ := script(t, `{"verdicts":[{"id":"old-sep","verdict":"separate","reason":"r"},{"id":"nope","verdict":"conflict","reason":"r"}]}`)
			return c
		}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t, "")
			e.Cfg.RegisterJudge.Command = c.command(t)
			if c.timeout > 0 {
				e.Cfg.RegisterJudge.Timeout = c.timeout
			}
			if err := Run(e, &bytes.Buffer{}, "new-one"); err != nil {
				t.Fatal(err)
			}
			got := entries(e)
			en, ok := got["new-one--unjudged"]
			if !ok || en.Kind != "unjudged" || len(got) != 1 {
				t.Fatalf("미판정 항목 하나만 있어야 함: %v", got)
			}
			want := []string{"old-conf", "old-merge", "old-sep"}
			if c.name == "답이 일부 후보에 없음" {
				want = []string{"old-conf", "old-merge"}
			}
			slices.Sort(en.Candidates)
			if !reflect.DeepEqual(en.Candidates, want) {
				t.Errorf("후보 = %v, 기대 %v", en.Candidates, want)
			}
			row := lastRow(t, e, "register_judge")
			isErr := c.name != "답이 일부 후보에 없음"
			if row.RegJudge.Error != isErr || row.RegJudge.Timeout != c.timeoutFlag {
				t.Errorf("원장 = %+v", row.RegJudge)
			}
		})
	}
}

// 다시 판정해 모두 답을 얻으면 미판정 항목이 사라진다.
func TestRerunClearsUnjudged(t *testing.T) {
	e := setup(t, "")
	if err := Run(e, &bytes.Buffer{}, "new-one"); err != nil {
		t.Fatal(err)
	}
	if _, ok := entries(e)["new-one--unjudged"]; !ok {
		t.Fatal("미판정 항목이 있어야 함")
	}
	cmd, _ := script(t, `{"verdicts":[{"id":"old-sep","verdict":"separate","reason":"r"},{"id":"old-merge","verdict":"separate","reason":"r"},{"id":"old-conf","verdict":"separate","reason":"r"}]}`)
	e.Cfg.RegisterJudge.Command = cmd
	if err := Run(e, &bytes.Buffer{}, "new-one"); err != nil {
		t.Fatal(err)
	}
	if got := entries(e); len(got) != 0 {
		t.Errorf("검토 항목이 남음: %v", got)
	}
}

func TestRunNoCandidatesAndMissingItem(t *testing.T) {
	e := newEnv(t, "")
	add(t, e, "lonely", "혼자 있는 글", "doc", "혼자")
	cmd, inFile := script(t, `{"verdicts":[]}`)
	e.Cfg.RegisterJudge.Command = cmd
	if err := Run(e, &bytes.Buffer{}, "lonely"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(inFile); err == nil {
		t.Error("후보가 없으면 명령을 부르지 않음")
	}
	if err := Run(e, &bytes.Buffer{}, "nothing"); err == nil {
		t.Error("없는 항목은 오류")
	}
}

func TestVerdictJSONRoundTrip(t *testing.T) {
	b, _ := json.Marshal(ledger.RegisterJudged{Verdicts: []ledger.Verdict{{ID: "a", Verdict: "separate"}}, MS: 5})
	if string(b) != `{"verdicts":[{"id":"a","verdict":"separate"}],"ms":5}` {
		t.Errorf("%s", b)
	}
}

func TestDraftProblem(t *testing.T) {
	a := "## 검사\n검사 절 내용입니다.\n\n## 배경\n배경 내용"
	b := "## 예시\n예시 내용"
	full := "## 검사\n검사 절 내용입니다.\n\n## 배경\n배경 내용\n\n## 예시\n예시 내용"
	cases := []struct {
		name, draft, want string
		orig              []string
	}{
		{"통과", full, "", []string{a, b}},
		{"절 누락", "## 배경\n배경 내용\n\n## 예시\n예시 내용 그리고 긴 글을 더 붙여서 길이는 충분하다 충분하다 충분하다", "초안에 '## 검사' 절이 없음", []string{a, b}},
		{"길이 부족", "## 검사\n짧음", "초안 본문이 원본의 ", []string{"## 검사\n" + strings.Repeat("가", 100)}},
		{"정확히 70%", strings.Repeat("가", 70), "", []string{strings.Repeat("가", 100)}},
		{"70% 미만", strings.Repeat("가", 69), "초안 본문이 원본의 69%", []string{strings.Repeat("가", 100)}},
		{"둘 다 빔", "", "", []string{"", "\n"}},
		{"원본은 비고 초안만 있음", "내용", "", []string{""}},
	}
	for _, c := range cases {
		got := DraftProblem(c.draft, c.orig...)
		if !strings.HasPrefix(got, c.want) || (c.want == "" && got != "") {
			t.Errorf("%s: %q, 기대 %q", c.name, got, c.want)
		}
	}
}

func TestRunMergeDraftMachineCheck(t *testing.T) {
	e := newEnv(t, "")
	r, err := register.Add(e, register.Input{ID: "old", Text: "푸시 전에 테스트를 돌린다.", About: []string{"푸시 규칙"}, Scope: []string{"personal"}, Source: "doc", Body: "## 검사\n푸시 전에 확인할 것들"})
	if err != nil || r.Status != "added" {
		t.Fatal(r, err)
	}
	add(t, e, "new-one", "푸시 전에 린트를 돌린다.", "doc", "푸시 규칙")
	// 초안이 '## 검사' 절을 빠뜨린 경우
	reply := `{"verdicts":[{"id":"old","verdict":"merge","reason":"같은 규칙","merged":{"text":"합친 글","body":"절 없는 짧은 글","about":["푸시 규칙"]}}]}`
	cmd, _ := script(t, reply)
	e.Cfg.RegisterJudge.Command = cmd
	if err := Run(e, &bytes.Buffer{}, "new-one"); err != nil {
		t.Fatal(err)
	}
	en, ok := entries(e)["new-one--old"]
	if !ok || en.Kind != "merge" || en.Merged != nil || en.DraftProblem != "초안에 '## 검사' 절이 없음" {
		t.Fatalf("검토 항목 = %+v", en)
	}
	row := lastRow(t, e, "register_judge")
	if len(row.RegJudge.Verdicts) != 1 || row.RegJudge.Verdicts[0].Verdict != "merge" || row.RegJudge.Verdicts[0].DraftProblem != en.DraftProblem {
		t.Errorf("원장 = %+v", row.RegJudge.Verdicts)
	}
	// 통과하는 초안은 그대로 남는다.
	reply = `{"verdicts":[{"id":"old","verdict":"merge","reason":"같은 규칙","merged":{"text":"합친 글","body":"## 검사\n푸시 전에 확인할 것들 그리고 린트까지","about":["푸시 규칙"]}}]}`
	cmd, _ = script(t, reply)
	e.Cfg.RegisterJudge.Command = cmd
	if err := Run(e, &bytes.Buffer{}, "new-one"); err != nil {
		t.Fatal(err)
	}
	if en := entries(e)["new-one--old"]; en.Merged == nil || en.DraftProblem != "" {
		t.Errorf("통과한 초안 = %+v", en)
	}
}

// 한 번의 judge-add 에서 모순이 여러 건이어도 알림은 한 번이고 건수를 적는다.
func TestRunNotifiesOncePerJudge(t *testing.T) {
	notes := filepath.Join(t.TempDir(), "notes.txt")
	e := setup(t, "")
	cmd, _ := script(t, `{"verdicts":[{"id":"old-conf","verdict":"conflict","reason":"r"},{"id":"old-merge","verdict":"conflict","reason":"r"},{"id":"old-sep","verdict":"separate","reason":"r"}]}`)
	e.Cfg.RegisterJudge.Command = cmd
	e.Cfg.Review.NotifyCommand = []string{"sh", "-c", `echo "$0" >> ` + notes}
	if err := Run(e, &bytes.Buffer{}, "new-one"); err != nil {
		t.Fatal(err)
	}
	n, _ := os.ReadFile(notes)
	if string(n) != "heka: 새 항목 new-one 가 2건과 모순으로 판정됐습니다. 세션에서 'heka 검토해줘', 터미널에서 heka review\n" {
		t.Errorf("알림 = %q", n)
	}
}
