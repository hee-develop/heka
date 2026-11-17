package selector

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/index"
	"github.com/hee-develop/heka/internal/item"
)

// pushEnv 는 env 와 같되, findOnly 에 든 id 는 검증을 거친 뒤 push: false 로 바꿔 색인에 넣는다.
// 검증은 조회 전용 항목에 when 을 막으므로, 선별기가 스스로 거르는지 보려면 이렇게 우회해야 한다.
func pushEnv(t *testing.T, items map[string]string, findOnly ...string) (*index.Index, *config.Config) {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "items"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(testCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	for id, body := range items {
		if err := os.WriteFile(filepath.Join(home, "items", id+".md"), []byte("---\nid: "+id+"\n"+body+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	its, err := item.LoadDir(filepath.Join(home, "items"), nil)
	if err != nil {
		t.Fatal(err)
	}
	off := false
	n := 0
	for _, it := range its {
		if !it.Valid() {
			t.Fatalf("시험 항목이 틀림: %v", it.Problems)
		}
		for _, id := range findOnly {
			if it.ID == id {
				it.Push = &off
				n++
			}
		}
	}
	if n != len(findOnly) {
		t.Fatalf("조회 전용으로 바꿀 항목을 못 찾음: %v", findOnly)
	}
	if _, err := index.Build(home, its, cfg, ""); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix, cfg
}

// noFO 는 id 가 "fo-" 로 시작하는 조회 전용 항목이 결과 어디에도 없는지 본다.
func noFO(t *testing.T, r Result) {
	t.Helper()
	for _, i := range r.Injections {
		if strings.HasPrefix(i.ID, "fo-") {
			t.Errorf("조회 전용 항목이 넣어짐: %s", i.ID)
		}
	}
	for _, d := range r.Drops {
		if strings.HasPrefix(d.ID, "fo-") {
			t.Errorf("조회 전용 항목이 탈락으로 남음: %+v", d)
		}
	}
	for _, j := range r.Judges {
		if strings.HasPrefix(j.ID, "fo-") {
			t.Errorf("조회 전용 항목에 판정 명령을 부름: %s", j.ID)
		}
	}
	if r.Pick != nil {
		for _, p := range r.Pick.Items {
			if strings.HasPrefix(p.ID, "fo-") {
				t.Errorf("조회 전용 항목이 모델 후보에 오름: %s", p.ID)
			}
		}
	}
}

func TestFindOnlySessionStart(t *testing.T) {
	ix, cfg := pushEnv(t, map[string]string{
		"start-ok": u + "text: 세션 시작 안내.\nmust: true\nwhen: {moment: session_start}\nabout: [안내]\nscope: [personal]",
		"fo-start": u + "text: 조회 전용 시작 안내.\nmust: true\nwhen: {moment: session_start}\nabout: [안내]\nscope: [personal]",
	}, "fo-start")
	r := sel1(t, ix, cfg, Moment{Kind: "session_start"})
	wantIDs(t, r, "start-ok")
	noFO(t, r)
}

func TestFindOnlyToolAndPromptAction(t *testing.T) {
	ix, cfg := pushEnv(t, map[string]string{
		"commit-ok": u + "text: 커밋 규칙.\nmust: true\nwhen: {action: git.commit}\nabout: [커밋]\nscope: [personal]",
		"fo-commit": u + "text: 조회 전용 커밋 규칙.\nmust: true\nwhen: {action: git.commit}\nabout: [커밋]\nscope: [personal]\nconfirm: true",
	}, "fo-commit")
	r := sel1(t, ix, cfg, Moment{Kind: "tool", Action: "git.commit"})
	wantIDs(t, r, "commit-ok")
	noFO(t, r)
	// 프롬프트의 동작 갈래
	r = sel1(t, ix, cfg, Moment{Kind: "prompt", Text: "커밋을 해 줘"})
	wantIDs(t, r, "commit-ok")
	noFO(t, r)
}

func TestFindOnlyAdmitRecDefensive(t *testing.T) {
	s := &sel{got: map[string]bool{}, scopes: map[string]bool{"personal": true}, m: Moment{Kind: "prompt", Today: "2026-11-15"}}
	r := &index.Record{ID: "fo-x", Text: "t", Source: "user", Quote: "q", Scope: []string{"personal"}, Must: true, Push: false}
	if c := s.admitRec(r, func(*index.Record) string { return "" }); c != nil || len(s.drops) != 0 || s.got["fo-x"] {
		t.Errorf("조회 전용 항목은 조용히 건너뛰어야 함: %+v %+v", c, s.drops)
	}
	r.Push = true
	if c := s.admitRec(r, func(*index.Record) string { return "" }); c == nil {
		t.Error("push 항목은 통과해야 함")
	}
}

func TestFindOnlyTopicAbout(t *testing.T) {
	ix, cfg := pushEnv(t, map[string]string{
		"topic-ok": u + "text: 배포는 금요일에 하지 않는다.\nabout: [배포 절차]\nscope: [personal]",
		"fo-about": u + "text: 조회 전용 배포 메모.\nabout: [배포 절차]\nscope: [personal]",
	}, "fo-about")
	cfg.Judge.Command = script(t, yes)
	r := sel1(t, ix, cfg, Moment{Kind: "prompt", Text: "배포 절차가 뭐야"})
	wantIDs(t, r, "topic-ok")
	noFO(t, r)
}

func TestFindOnlyTopicLexicalFillsCandidates(t *testing.T) {
	items := map[string]string{
		"fo-top":  u + "text: 정규식 문법 정규식 문법 정규식 문법.\nabout: [다른말]\nscope: [personal]",
		"low-ok":  u + "text: 정규식은 RE2 문법이다.\nabout: [또다른말]\nscope: [personal]",
		"low-ok2": u + "text: 문법 검사는 따로 한다.\nabout: [또또다른말]\nscope: [personal]",
	}
	ix, cfg := pushEnv(t, items, "fo-top")
	const q = "정규식 문법 알려 줘"
	ranked, err := ix.Lexical(q, 10)
	if err != nil || len(ranked) == 0 || ranked[0].ID != "fo-top" {
		t.Fatalf("조회 전용 항목이 낱말 점수 1위여야 함: %+v %v", ranked, err)
	}
	cfg.Judge.Command = script(t, yes)
	cfg.Judge.Candidates = 1
	r := sel1(t, ix, cfg, Moment{Kind: "prompt", Text: q})
	// 1위를 건너뛰고 그 아래 대상 항목으로 후보 1건이 채워진다.
	wantIDs(t, r, ranked[1].ID)
	if len(r.Judges) != 1 {
		t.Errorf("판정은 1건이어야 함: %+v", r.Judges)
	}
	noFO(t, r)
}

func TestFindOnlySubagentStart(t *testing.T) {
	ix, cfg := pushEnv(t, map[string]string{
		"sub-ok":   u + "text: 리뷰어 안내.\nmust: true\nwhen: {agent: reviewer}\nabout: [리뷰어]\nscope: [personal]",
		"fo-sub-a": u + "text: 조회 전용 리뷰어 안내.\nmust: true\nwhen: {agent: reviewer}\nabout: [리뷰어]\nscope: [personal]",
		"fo-sub-m": u + "text: 조회 전용 위임 안내.\nmust: true\nwhen: {moment: subagent_start}\nabout: [위임]\nscope: [personal]",
	}, "fo-sub-a", "fo-sub-m")
	cfg.Judge.Command = script(t, yes)
	r := sel1(t, ix, cfg, Moment{Kind: "subagent_start", Agent: "reviewer", Text: "리뷰어 안내가 필요함"})
	wantIDs(t, r, "sub-ok")
	noFO(t, r)
}

func TestFindOnlyPickList(t *testing.T) {
	items := map[string]string{
		"p-alpha": u + "text: 배포는 금요일에 하지 않는다.\nabout: [배포 절차]\nscope: [personal]",
		"fo-pick": u + "text: 조회 전용 배포 메모.\nabout: [배포 메모]\nscope: [personal]",
	}
	ix, cfg := pushEnv(t, items, "fo-pick")
	cmd, _ := pickScript(t, echoJSON(`{"inject": ["fo-pick", "p-alpha"]}`), "p-alpha", "fo-pick")
	cfg.Judge.Command = cmd
	cfg.Judge.Pick = config.Pick{Mode: "record", Instruction: "목록에서 고른다", Max: 8}
	r, err := Select(context.Background(), ix, cfg, Moment{Kind: "prompt", Text: "배포 절차가 뭐야", Today: "2026-11-15"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Pick == nil || r.Pick.Listed != 1 {
		t.Fatalf("목록에는 push 항목 1건만 올라야 함: %+v", r.Pick)
	}
	noFO(t, r)
}
