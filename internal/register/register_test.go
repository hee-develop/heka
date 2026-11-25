package register

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/index"
	"github.com/hee-develop/heka/internal/item"
	"github.com/hee-develop/heka/internal/ledger"
	"github.com/hee-develop/heka/internal/selector"
	"github.com/hee-develop/heka/internal/session"
)

var now = time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)

func newEnv(t *testing.T) Env {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("global_scopes: [personal]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	return Env{Home: home, Cfg: cfg, Now: now}
}

func base(id string) Input {
	return Input{ID: id, Text: "배포 점검표는 릴리스 전에 확인한다.", About: []string{"릴리스 점검"}, Scope: []string{"personal"}, Source: "doc"}
}

func exists(e Env, rel string) bool {
	_, err := os.Stat(filepath.Join(e.Home, rel))
	return err == nil
}

func mustAdd(t *testing.T, e Env, in Input, want string) Result {
	t.Helper()
	r, err := Add(e, in)
	if err != nil || r.Status != want {
		t.Fatalf("Add(%s) = %+v, %v; 기대 %s", in.ID, r, err, want)
	}
	return r
}

func selectTool(t *testing.T, e Env, action string) []string {
	t.Helper()
	if err := index.Ensure(e.Home, e.Cfg); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(e.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	res, err := selector.Select(context.Background(), ix, e.Cfg, selector.Moment{Kind: "tool", Action: action, Seen: map[string]bool{}, Today: "2026-11-16"})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, i := range res.Injections {
		out = append(out, i.ID)
	}
	return out
}

func TestAddInvalid(t *testing.T) {
	e := newEnv(t)
	in := base("Bad Id")
	in.About = nil
	r := mustAdd(t, e, in, "invalid")
	if len(r.Problems) < 2 || !strings.Contains(r.Text(), "등록하지 않았습니다") {
		t.Errorf("문제 목록 = %v", r.Problems)
	}
	if exists(e, "items") {
		t.Error("검증에 실패하면 아무것도 쓰면 안 됨")
	}
}

func TestAddWithoutSimilarIsLiveImmediately(t *testing.T) {
	e := newEnv(t)
	linkTranscript(t, e, "s1", filepath.Join(t.TempDir(), "s1.jsonl"), userLine("앞으로   커밋은\n한국어로 써 줘. 첫 줄은 짧게."))
	in := Input{ID: "commit-lang", Text: "커밋 메시지는 한국어로 쓴다.", Must: true, When: item.When{Action: item.List{"git.commit"}},
		About: []string{"커밋 메시지"}, Scope: []string{"personal"}, Source: "user", Quote: "커밋은 한국어로 써 줘.", Body: "배경."}
	r := mustAdd(t, e, in, "added")
	if r.QuoteUnverified {
		t.Error("공백을 정규화하면 인용이 확인돼야 함")
	}
	b, err := os.ReadFile(filepath.Join(e.Home, "items", "commit-lang.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nid: commit-lang\ntext: 커밋 메시지는 한국어로 쓴다.\nmust: true\nwhen:\n  action: git.commit\nabout: [커밋 메시지]\nscope: [personal]\nsource: user\nquote: 커밋은 한국어로 써 줘.\nchecked:\nexpires:\nconfirm: false\n---\n배경.\n"
	if string(b) != want {
		t.Errorf("파일 모양:\n%s\n기대:\n%s", b, want)
	}
	if got := selectTool(t, e, "git.commit"); len(got) != 1 || got[0] != "commit-lang" {
		t.Errorf("바로 주입돼야 함: %v", got)
	}
	if left, _ := filepath.Glob(filepath.Join(e.Home, "items", ".tmp-*")); len(left) > 0 {
		t.Errorf("임시 파일이 남음: %v", left)
	}
}

func TestUnverifiedQuoteIsNotInjected(t *testing.T) {
	e := newEnv(t)
	in := Input{ID: "commit-lang", Text: "커밋 메시지는 한국어로 쓴다.", Must: true, When: item.When{Action: item.List{"git.commit"}},
		About: []string{"커밋 메시지"}, Scope: []string{"personal"}, Source: "user", Quote: "한 적 없는 말"}
	r := mustAdd(t, e, in, "added")
	if !r.QuoteUnverified || !strings.Contains(r.Text(), "사람이 확인하기 전에는 주입되지 않습니다") {
		t.Errorf("결과 = %+v", r)
	}
	b, _ := os.ReadFile(filepath.Join(e.Home, "items", "commit-lang.md"))
	if strings.Contains(string(b), "quote:") || !strings.HasSuffix(string(b), "확인되지 않은 인용: \"한 적 없는 말\"\n") {
		t.Errorf("quote 를 비우고 본문 끝에 적어야 함:\n%s", b)
	}
	if got := selectTool(t, e, "git.commit"); len(got) != 0 {
		t.Errorf("확인 안 된 항목이 주입됨: %v", got)
	}
}

// 세션 기록 파일 한 줄을 만든다. 형식만 흉내 낸 지어낸 글이다.
func userLine(text string) map[string]any {
	return map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}}
}

func writeTranscript(t *testing.T, path string, lines ...map[string]any) {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		j, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// linkTranscript 는 기록 파일을 쓰고 세션 상태에 그 경로를 적는다.
func linkTranscript(t *testing.T, e Env, sid, path string, lines ...map[string]any) {
	t.Helper()
	writeTranscript(t, path, lines...)
	if err := session.Update(e.Home, sid, func(s *session.State) { s.Transcript = path }); err != nil {
		t.Fatal(err)
	}
}

func TestQuoteWindow(t *testing.T) {
	e := newEnv(t)
	linkTranscript(t, e, "old", filepath.Join(t.TempDir(), "old.jsonl"), userLine("옛날에 한 말"))
	if !quoteSeen(e, "옛날에 한 말") {
		t.Fatal("최근 세션이면 확인돼야 함")
	}
	old := now.Add(-25 * time.Hour)
	if err := os.Chtimes(session.Path(e.Home, "old"), old, old); err != nil {
		t.Fatal(err)
	}
	if quoteSeen(e, "옛날에 한 말") {
		t.Error("24시간 지난 세션 상태는 대조하지 않음")
	}
}

// 사용자가 친 글에 있는 말만 인용으로 인정한다.
func TestQuoteOnlyFromWhatUserTyped(t *testing.T) {
	e := newEnv(t)
	tr := filepath.Join(t.TempDir(), "s.jsonl")
	linkTranscript(t, e, "s", tr,
		userLine("커밋 메시지는   한국어로\n써 줘"),
		map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "AI 만 한 말입니다"}}}},
		map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "도구 결과에만 있는 말"}}}},
		userLine("<task-notification>도구가 만든 프롬프트에만 있는 말</task-notification>"),
		userLine("<system-reminder>시스템 안내에만 있는 말</system-reminder>"),
		map[string]any{"type": "user", "isMeta": true, "message": map[string]any{"role": "user", "content": "메타 줄에만 있는 말"}},
		map[string]any{"type": "user", "isSidechain": true, "message": map[string]any{"role": "user", "content": "곁가지 줄에만 있는 말"}},
		map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "배열로 온 사용자 글"}}}},
	)
	for _, q := range []string{"커밋 메시지는 한국어로 써 줘", "배열로 온 사용자 글"} {
		if !quoteSeen(e, q) {
			t.Errorf("사용자가 친 글인데 확인되지 않음: %q", q)
		}
	}
	for _, q := range []string{"AI 만 한 말입니다", "도구 결과에만 있는 말", "도구가 만든 프롬프트에만 있는 말", "시스템 안내에만 있는 말", "메타 줄에만 있는 말", "곁가지 줄에만 있는 말", ""} {
		if quoteSeen(e, q) {
			t.Errorf("사용자가 친 글이 아닌데 확인됨: %q", q)
		}
	}
}

func TestQuoteTranscriptMissingOrBroken(t *testing.T) {
	e := newEnv(t)
	if err := session.Update(e.Home, "gone", func(s *session.State) { s.Transcript = filepath.Join(t.TempDir(), "없는.jsonl") }); err != nil {
		t.Fatal(err)
	}
	if quoteSeen(e, "아무 말") {
		t.Error("기록 파일이 없으면 확인되지 않음")
	}
	// 깨진 줄은 건너뛰고 나머지를 본다.
	tr := filepath.Join(t.TempDir(), "b.jsonl")
	writeTranscript(t, tr, userLine("멀쩡한 줄의 말"))
	f, _ := os.OpenFile(tr, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("{깨진 줄 \"user\"\n")
	f.Close()
	if err := session.Update(e.Home, "broken", func(s *session.State) { s.Transcript = tr }); err != nil {
		t.Fatal(err)
	}
	if !quoteSeen(e, "멀쩡한 줄의 말") {
		t.Error("깨진 줄이 있어도 나머지는 읽어야 함")
	}
	// 기록 파일이 없을 때 등록은 인용을 비우고 본문에 적는다.
	in := Input{ID: "q-missing", Text: "커밋 메시지는 한국어로 쓴다.", About: []string{"커밋"}, Scope: []string{"personal"}, Source: "user", Quote: "한 적 없는 말"}
	r := mustAdd(t, e, in, "added")
	if !r.QuoteUnverified {
		t.Error("대조가 안 되면 QuoteUnverified")
	}
	b, _ := os.ReadFile(filepath.Join(e.Home, "items", "q-missing.md"))
	if !strings.HasSuffix(string(b), "확인되지 않은 인용: \"한 적 없는 말\"\n") {
		t.Errorf("본문 끝에 적어야 함:\n%s", b)
	}
}

// 설정의 claude_code.transcripts 글롭에 맞는 파일(최근 24시간 안에 고쳐진 것)도 본다.
func TestQuoteExtraTranscriptGlobs(t *testing.T) {
	archive := t.TempDir()
	e := newEnv(t)
	cfgText := "global_scopes: [personal]\nclaude_code:\n  transcripts: ['" + archive + "/sessions/**/*.jsonl']\n"
	if err := os.WriteFile(filepath.Join(e.Home, "config.yaml"), []byte(cfgText), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(e.Home)
	if err != nil {
		t.Fatal(err)
	}
	e.Cfg = cfg
	deep := filepath.Join(archive, "sessions", "2026", "11", "a.jsonl")
	writeTranscript(t, deep, userLine("보관함에 있는 사용자 말"))
	shallow := filepath.Join(archive, "sessions", "b.jsonl")
	writeTranscript(t, shallow, userLine("얕은 곳의 사용자 말"))
	stale := filepath.Join(archive, "sessions", "2025", "c.jsonl")
	writeTranscript(t, stale, userLine("오래된 파일의 말"))
	old := now.Add(-30 * time.Hour)
	os.Chtimes(stale, old, old)
	other := filepath.Join(archive, "elsewhere", "d.jsonl")
	writeTranscript(t, other, userLine("글롭 밖의 말"))
	for _, tt := range []struct {
		quote string
		want  bool
	}{{"보관함에 있는 사용자 말", true}, {"얕은 곳의 사용자 말", true}, {"오래된 파일의 말", false}, {"글롭 밖의 말", false}} {
		if got := quoteSeen(e, tt.quote); got != tt.want {
			t.Errorf("%q: %v, 기대 %v", tt.quote, got, tt.want)
		}
	}
}

func TestRetire(t *testing.T) {
	e := newEnv(t)
	mustAdd(t, e, base("old-rule"), "added")
	if _, err := Retire(e, "nope", "이유"); err == nil {
		t.Error("없는 항목은 오류")
	}
	if _, err := Retire(e, "../x", "이유"); err == nil {
		t.Error("id 모양이 틀리면 오류")
	}
	if _, err := Retire(e, "old-rule", "  "); err == nil {
		t.Error("사유가 없으면 오류")
	}
	if !exists(e, "items/old-rule.md") {
		t.Fatal("실패한 호출이 파일을 지움")
	}
	if _, err := Retire(e, "old-rule", "더 쓰지 않는다"); err != nil {
		t.Fatal(err)
	}
	if exists(e, "items/old-rule.md") {
		t.Error("파일이 남음")
	}
	rows := ledger.ReadAll(e.Home)
	last := rows[len(rows)-1]
	if last.Event != "retire" || last.Reason != "더 쓰지 않는다" || len(last.Injected) != 1 || last.Injected[0].ID != "old-rule" {
		t.Errorf("원장 줄 = %+v", last)
	}
	if got := selectTool(t, e, "x.y"); len(got) != 0 {
		t.Errorf("색인에 남음: %v", got)
	}
}

// 데이터 폴더가 git 저장소이면 사유를 메시지로 커밋을 시도한다.
func TestRetireCommitsInGitHome(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 없음")
	}
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.invalid", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.invalid"} {
		t.Setenv(k, v)
	}
	e := newEnv(t)
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", e.Home}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	mustAdd(t, e, base("old-rule"), "added")
	if _, err := Retire(e, "old-rule", "낡아서 뺀다"); err != nil {
		t.Fatal(err)
	}
	if msg := git("log", "-1", "--format=%s"); msg != "낡아서 뺀다" {
		t.Errorf("커밋 메시지 = %q", msg)
	}
	if st := git("status", "--porcelain", "--", "items"); st != "" {
		t.Errorf("items 에 남은 변경: %s", st)
	}
}

func read(e Env, rel string) string {
	b, _ := os.ReadFile(filepath.Join(e.Home, rel))
	return string(b)
}

// spawnRecorder 는 judge-add 를 띄우는 자리를 기록으로 바꾼다.
func spawnRecorder(e *Env) *[]string {
	var calls []string
	e.Spawn = func(id string) error { calls = append(calls, id); return nil }
	return &calls
}

func TestSimilarKinds(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Input)
		reason string
	}{
		{"범위와 동작", func(in *Input) {
			in.Text = "전혀 다른 글 하나"
			in.About = []string{"다른 주제"}
			in.When.Action = item.List{"git.push"}
		}, "동작(git.push)이 겹침"},
		{"주제어", func(in *Input) {
			in.Text = "전혀 다른 글 하나"
			in.About = []string{"푸시  규칙"}
			in.When.Action = nil
		}, "주제어가 같음"},
		{"낱말", func(in *Input) {
			in.About = []string{"다른 주제"}
			in.When.Action = nil
			in.Text = "푸시하기 전에 테스트를 먼저 돌린다."
		}, "낱말 점수"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			calls := spawnRecorder(&e)
			first := Input{ID: "first", Text: "푸시하기 전에 테스트를 먼저 돌린다.", About: []string{"푸시 규칙"}, Scope: []string{"personal"},
				Source: "doc", When: item.When{Action: item.List{"git.push"}}}
			mustAdd(t, e, first, "added")
			if len(*calls) != 0 {
				t.Fatalf("후보가 없으면 판정을 띄우지 않음: %v", *calls)
			}
			in := first
			in.ID = "second"
			c.mutate(&in)
			r := mustAdd(t, e, in, "added")
			if len(r.Candidates) != 1 || r.Candidates[0].ID != "first" || !strings.Contains(r.Candidates[0].Reason, c.reason) {
				t.Fatalf("후보 = %+v", r.Candidates)
			}
			if txt := r.Text(); !strings.Contains(txt, "등록했습니다: second") || !strings.Contains(txt, "비슷한 후보 1건은 뒤에서 판정합니다. 사용자에게 물을 필요는 없습니다.") ||
				!strings.Contains(txt, "- first: 푸시하기 전에 테스트를 먼저 돌린다. (") || strings.Contains(txt, "물어 주세요") {
				t.Errorf("응답 글:\n%s", txt)
			}
			if !exists(e, "items/second.md") || exists(e, "items/_pending") {
				t.Error("언제나 items 에 써야 함")
			}
			if len(*calls) != 1 || (*calls)[0] != "second" {
				t.Errorf("판정을 띄워야 함: %v", *calls)
			}
			assertIndexed(t, e, "second", true) // 대기 없이 바로 색인에 든다
		})
	}
}

func TestAddWithoutCandidatesDoesNotSpawn(t *testing.T) {
	e := newEnv(t)
	calls := spawnRecorder(&e)
	r := mustAdd(t, e, base("only"), "added")
	if len(*calls) != 0 || strings.Contains(r.Text(), "후보") {
		t.Errorf("calls=%v text=%q", *calls, r.Text())
	}
}

func TestAddSameIDIsRejected(t *testing.T) {
	e := newEnv(t)
	calls := spawnRecorder(&e)
	mustAdd(t, e, base("dup"), "added")
	before := read(e, "items/dup.md")
	in := base("dup")
	in.Text = "다른 글"
	r := mustAdd(t, e, in, "invalid")
	if len(r.Problems) != 1 || r.Problems[0] != "error: 같은 id 의 항목 dup 가 이미 있음. 파일을 직접 고치거나 다른 id 를 쓰세요" {
		t.Errorf("문제 = %v", r.Problems)
	}
	if !strings.Contains(r.Text(), "등록하지 않았습니다") || read(e, "items/dup.md") != before || len(*calls) != 0 {
		t.Error("같은 id 는 쓰지도 판정을 띄우지도 않아야 함")
	}
}

func TestAddSpawnFailureStillSucceeds(t *testing.T) {
	e := newEnv(t)
	e.Spawn = func(string) error { return os.ErrPermission }
	mustAdd(t, e, base("a-one"), "added")
	in := base("a-two")
	mustAdd(t, e, in, "added")
	if !exists(e, "items/a-two.md") || !strings.Contains(read(e, "ledger/errors.log"), "등록 판정을 띄우지 못함") {
		t.Errorf("오류 기록: %q", read(e, "ledger/errors.log"))
	}
}

// 후보는 범위·동작이 겹친 것 먼저, 그다음 낱말 점수 내림차순, 같으면 id 순이고 candidates 건까지다.
func TestSimilarOrderAndLimit(t *testing.T) {
	e := newEnv(t)
	e.Cfg.RegisterJudge.Candidates = 3
	spawnRecorder(&e)
	mk := func(id, text string, act ...string) {
		mustAdd(t, e, Input{ID: id, Text: text, About: []string{"주제 " + id}, Scope: []string{"personal"}, Source: "doc", When: item.When{Action: item.List(act)}}, "added")
	}
	mk("zz-act", "완전히 다른 글", "git.push")
	mk("aa-same", "푸시하기 전에 테스트를 먼저 돌린다.")
	mk("bb-same", "푸시하기 전에 테스트를 먼저 돌린다.")
	mk("cc-same", "푸시하기 전에 테스트를 먼저 돌린다.")
	it, err := validate(Input{ID: "new-one", Text: "푸시하기 전에 테스트를 먼저 돌린다.", About: []string{"새 주제"}, Scope: []string{"personal"}, Source: "doc", When: item.When{Action: item.List{"git.push"}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Similar(e, it)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, ",") != "zz-act,aa-same,bb-same" {
		t.Errorf("순서와 상한 = %v", ids)
	}
	it.ID = "aa-same" // 자기 자신은 뺀다
	got, _ = Similar(e, it)
	for _, c := range got {
		if c.ID == "aa-same" {
			t.Error("자기 자신이 후보에 듦")
		}
	}
}

func assertIndexed(t *testing.T, e Env, id string, want bool) {
	t.Helper()
	ix, err := index.Open(e.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if _, err := ix.Item(id); (err == nil) != want {
		t.Errorf("색인의 %s: err=%v, 있어야 함=%v", id, err, want)
	}
}

func TestGitCommit(t *testing.T) {
	e := newEnv(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 이 없음")
	}
	for _, kv := range []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"} {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	if out, err := exec.Command("git", "-C", e.Home, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	mustAdd(t, e, base("rel-check"), "added")
	out, err := exec.Command("git", "-C", e.Home, "log", "--format=%s").Output()
	if err != nil || strings.TrimSpace(string(out)) != "add: rel-check" {
		t.Errorf("커밋 = %q, %v", out, err)
	}
}

func TestNoGitIsFine(t *testing.T) {
	e := newEnv(t)
	mustAdd(t, e, base("rel-check"), "added")
	if exists(e, ".git") {
		t.Error("git 저장소가 아니면 아무것도 하지 않아야 함")
	}
}

// initGit 은 데이터 폴더를 git 저장소로 만들고 git 을 부르는 함수를 돌려준다.
func initGit(t *testing.T, e Env) func(args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 이 없음")
	}
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.invalid", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.invalid"} {
		t.Setenv(k, v)
	}
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", e.Home}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	return git
}

// 자동 커밋은 이번 등록의 파일만 담고, 손으로 고쳐 두고 커밋하지 않은 다른 항목은 담지 않는다.
func TestGitCommitOnlyOwnFiles(t *testing.T) {
	e := newEnv(t)
	git := initGit(t, e)
	mustAdd(t, e, base("hand-edited"), "added")
	if err := os.WriteFile(filepath.Join(e.Home, "items/hand-edited.md"), []byte(read(e, "items/hand-edited.md")+"손으로 더한 줄.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := base("other")
	other.Text, other.About = "커밋 메시지는 한국어로 쓴다.", []string{"커밋 메시지"}
	mustAdd(t, e, other, "added")
	if msg := git("log", "-1", "--format=%s"); msg != "add: other" {
		t.Errorf("커밋 메시지 = %q", msg)
	}
	if files := git("show", "--name-only", "--format=", "HEAD"); files != "items/other.md" {
		t.Errorf("커밋에 담긴 파일 = %q", files)
	}
	if st := git("status", "--porcelain", "--", "items"); st != "M items/hand-edited.md" {
		t.Errorf("손으로 고친 파일은 커밋하지 않고 남아야 함: %q", st)
	}
}

// 자동 커밋은 줄이 다룬 항목 파일과 review 의 변경을 담고, review 폴더가 없어도 실패하지 않는다.
// 등록 판정은 항목 파일을 다루지 않으므로 손으로 고친 항목이 섞이지 않는다.
func TestFinishRowCommitsOwnItemsAndReview(t *testing.T) {
	e := newEnv(t)
	git := initGit(t, e)
	mustAdd(t, e, base("one"), "added") // review 폴더가 없다
	if msg := git("log", "-1", "--format=%s"); msg != "add: one" || read(e, "ledger/errors.log") != "" {
		t.Fatalf("review 폴더 없이 커밋: %q 오류 %q", msg, read(e, "ledger/errors.log"))
	}
	if err := os.WriteFile(filepath.Join(e.Home, "items/one.md"), []byte(read(e, "items/one.md")+"손으로 더한 줄.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.Home, "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.Home, "review", "a--b.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	judge := ledger.Row{Event: "register_judge", Moment: "judge-add", Injected: []ledger.Injected{{ID: "one"}}}
	FinishRow(e, judge, "judge: one")
	if files := git("show", "--name-only", "--format=", "HEAD"); files != "review/a--b.json" {
		t.Errorf("판정은 review 파일만 커밋해야 함: %q", files)
	}
	if st := git("status", "--porcelain", "--", "items"); st != "M items/one.md" {
		t.Errorf("손으로 고친 항목은 남아야 함: %q", st)
	}
	os.Remove(filepath.Join(e.Home, "review", "a--b.json")) // 지운 것도 커밋된다
	FinishRow(e, ledger.Row{Event: "review", Moment: "keep", Injected: []ledger.Injected{{ID: "one"}}}, "review: 지움")
	if st := git("status", "--porcelain", "--", "review"); st != "" || git("log", "-1", "--format=%s") != "review: 지움" {
		t.Errorf("지움이 커밋돼야 함: %q", st)
	}
	if files := git("show", "--name-only", "--format=", "HEAD"); files != "review/a--b.json" {
		t.Errorf("keep 은 항목 파일을 담지 않음: %q", files)
	}
	FinishRow(e, ledger.Row{Event: "review", Moment: "z"}, "변경 없음") // 바꾼 것이 없으면 커밋도 오류도 없다
	if git("log", "-1", "--format=%s") != "review: 지움" || read(e, "ledger/errors.log") != "" {
		t.Errorf("변경 없음: %q", read(e, "ledger/errors.log"))
	}
	// 합치기는 줄에 든 항목 파일(고쳐 쓴 대상)을 담는다.
	FinishRow(e, ledger.Row{Event: "review", Moment: "merge", Injected: []ledger.Injected{{ID: "one"}}}, "review: 합침")
	if files := git("show", "--name-only", "--format=", "HEAD"); files != "items/one.md" {
		t.Errorf("merge 는 대상 항목 파일을 담아야 함: %q", files)
	}
}
