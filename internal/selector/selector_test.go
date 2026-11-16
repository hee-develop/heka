package selector

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/index"
	"github.com/hee-develop/heka/internal/item"
)

const testCfg = `
scopes:
  repo/sample-app: [team/sample]
global_scopes: [personal]
actions:
  git.commit:
    in: git.write
    phrases: ['(?:커밋|commit)\s*(?:을|를)?\s*(?:해|하고)']
  git.push:
    in: git.write
    phrases: ['(?:푸[시쉬]|push)\s*(?:을|를|도|까지|만)?\s*(?:해|하고|한\s*뒤|하자|진행|부탁)']
  git.push.force:
    phrases: ['force[- ]?push|강제\s*푸[시쉬]']
negations: ['하지\s*(?:마|말)', '하면\s*안', '(?:하기\s*)?전에', '할까', '해도\s*(?:돼|될까|되나)']
budgets: {tool: 60, prompt: 300}
judge: {candidates: 3, instruction: 지시문}
`

// u 는 사용자가 말했고 quote 가 있는 항목의 공통 머리말이다.
const u = "source: user\nquote: \"지어낸 말\"\n"

// 시험용 항목. 이름이 곧 id 다.
var testItems = map[string]string{
	// 범위
	"rule-write":       u + "text: 쓰기 전에 상태를 본다.\nmust: true\nwhen: {action: git.write}\nabout: [쓰기]\nscope: [repo/sample-app]",
	"rule-push-team":   u + "text: 푸시는 팀 규칙을 따른다.\nmust: true\nwhen: {action: git.push}\nabout: [푸시 규칙]\nscope: [team/sample]",
	"rule-push-force":  u + "text: 강제 푸시는 하지 않는다.\nmust: true\nwhen: {action: git.push.force}\nabout: [강제 푸시]\nscope: [repo/sample-app]",
	"rule-global":      u + "text: 개인 규칙.\nmust: true\nwhen: {action: git.commit}\nabout: [개인]\nscope: [personal]",
	"rule-elsewhere":   u + "text: 다른 곳 규칙.\nmust: true\nwhen: {action: git.commit}\nabout: [다른 곳]\nscope: [repo/other]",
	"rule-moment-only": u + "text: 도구 순간 전용.\nmust: true\nwhen: {action: git.commit, moment: session_start}\nabout: [전용]\nscope: [personal]",
	// 자격
	"el-expired": u + "text: 기한 지남.\nmust: true\nwhen: {action: x.elig}\nabout: [기한]\nscope: [personal]\nexpires: 2026-11-01",
	"el-live":    u + "text: 기한 안.\nmust: true\nwhen: {action: x.elig}\nabout: [기한]\nscope: [personal]\nexpires: 2026-12-01",
	"el-checked": "source: ai\ntext: 확인된 ai.\nmust: true\nwhen: {action: x.elig}\nabout: [확인]\nscope: [personal]\nchecked: 2026-11-10",
	"el-noquote": "source: user\ntext: quote 없음.\nmust: true\nwhen: {action: x.elig}\nabout: [없음]\nscope: [personal]",
	"el-doc":     "source: doc\ntext: 문서 항목.\nmust: true\nwhen: {action: x.elig}\nabout: [문서]\nscope: [personal]",
	"el-ai":      "source: ai\ntext: ai 항목.\nmust: true\nwhen: {action: x.elig}\nabout: [ai]\nscope: [personal]",
	// 조건
	"cond-repo":  u + "text: 저장소 조건.\nmust: true\nwhen: {action: x.cond, repo: 'sample-*'}\nabout: [저장소]\nscope: [personal]",
	"cond-path":  u + "text: 경로 조건.\nmust: true\nwhen: {action: x.cond, path: 'docs/*'}\nabout: [경로]\nscope: [personal]",
	"cond-agent": u + "text: 에이전트 조건.\nmust: true\nwhen: {action: x.cond, agent: reviewer}\nabout: [에이전트]\nscope: [personal]",
	"cond-none":  u + "text: 조건 없음.\nmust: true\nwhen: {action: x.cond}\nabout: [없음]\nscope: [personal]",
	// 세션 시작, 서브에이전트
	"start-note": u + "text: 세션 시작 안내.\nmust: true\nwhen: {moment: session_start}\nabout: [안내]\nscope: [personal]",
	"sub-agent":  u + "text: 리뷰어 안내.\nmust: true\nwhen: {agent: reviewer}\nabout: [리뷰어]\nscope: [personal]",
	"sub-moment": u + "text: 위임 안내.\nmust: true\nwhen: {moment: subagent_start}\nabout: [위임]\nscope: [personal]",
	// 주제
	"topic-deploy":    u + "text: 배포는 금요일에 하지 않는다.\nabout: [배포 절차]\nscope: [personal]",
	"topic-regex":     u + "text: 정규식은 RE2 문법이다.\nabout: [정규식]\nscope: [personal]",
	"topic-act":       u + "text: 배포 전 점검 규칙.\nmust: true\nwhen: {action: x.deploy}\nabout: [배포 절차]\nscope: [personal]",
	"topic-push-path": u + "text: 경로 있는 푸시 규칙.\nmust: true\nwhen: {action: git.push, path: 'docs/*'}\nabout: [푸시 경로]\nscope: [personal]",
	"topic-moment":    u + "text: 도구에서만.\nwhen: {moment: tool}\nabout: [배포 절차]\nscope: [personal]",
}

// env 는 임시 폴더에 항목과 설정으로 색인을 만들어 열어 준다.
func env(t *testing.T, items map[string]string, cfgYAML string) (*index.Index, *config.Config) {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "items"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, "config.yaml"), cfgYAML)
	for id, body := range items {
		write(filepath.Join(home, "items", id+".md"), "---\nid: "+id+"\n"+body+"\n---\n")
	}
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	its, err := item.LoadDir(filepath.Join(home, "items"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range its {
		if !it.Valid() {
			t.Fatalf("시험 항목이 틀림: %v", it.Problems)
		}
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

// script 는 임시 폴더에 판정 명령으로 쓸 셸 스크립트를 만든다.
func script(t *testing.T, body string) []string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "judge.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{p}
}

const (
	yes = `cat >/dev/null; echo '{"inject": true, "reason": "답이 됨"}'`
	no  = `cat >/dev/null; echo '{"inject": false, "reason": "답이 없음"}'`
)

func sel1(t *testing.T, ix *index.Index, cfg *config.Config, m Moment) Result {
	t.Helper()
	if m.Today == "" {
		m.Today = "2026-11-15"
	}
	res, err := Select(context.Background(), ix, cfg, m)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func ids(r Result) []string {
	out := []string{}
	for _, i := range r.Injections {
		out = append(out, i.ID)
	}
	return out
}

func dropOf(r Result, id string) *Drop {
	for _, d := range r.Drops {
		if d.ID == id {
			return &d
		}
	}
	return nil
}

func wantIDs(t *testing.T, r Result, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if got := ids(r); !reflect.DeepEqual(got, want) {
		t.Errorf("넣은 항목 = %v, 기대 %v (탈락 %v)", got, want, r.Drops)
	}
}

func wantDrop(t *testing.T, r Result, id, stage string) {
	t.Helper()
	if d := dropOf(r, id); d == nil || d.Stage != stage {
		t.Errorf("%s 의 탈락 = %v, 기대 단계 %s", id, d, stage)
	}
}

func TestScope(t *testing.T) {
	ix, cfg := env(t, testItems, testCfg)
	m := Moment{Kind: "tool", Action: "git.commit", Scopes: []string{"repo/sample-app"}}
	r := sel1(t, ix, cfg, m)
	wantIDs(t, r, "rule-global", "rule-write") // git.commit 의 상위 동작은 git.write
	wantDrop(t, r, "rule-elsewhere", "범위")
	wantDrop(t, r, "rule-moment-only", "일치")

	// 닫힘: 상위 범위 team/sample 의 항목과 상위 동작 git.write 의 항목이 하위에서 나온다.
	m.Action = "git.push"
	wantIDs(t, sel1(t, ix, cfg, m), "rule-push-team", "rule-write")

	// 하위 동작에 건 조건은 상위 동작만으로는 걸리지 않는다.
	m.Action = "git.push.force"
	wantIDs(t, sel1(t, ix, cfg, m), "rule-push-force", "rule-push-team", "rule-write")

	// 범위를 모르면 global_scopes 만 본다.
	m = Moment{Kind: "tool", Action: "git.commit"}
	wantIDs(t, sel1(t, ix, cfg, m), "rule-global")
	m.Action = "git.push"
	r = sel1(t, ix, cfg, m)
	wantIDs(t, r)
	wantDrop(t, r, "rule-push-team", "범위")
}

func TestEligibility(t *testing.T) {
	ix, cfg := env(t, testItems, testCfg)
	r := sel1(t, ix, cfg, Moment{Kind: "tool", Action: "x.elig"})
	wantIDs(t, r, "el-checked", "el-doc", "el-live")
	wantDrop(t, r, "el-expired", "자격")
	wantDrop(t, r, "el-noquote", "자격")
	wantDrop(t, r, "el-ai", "자격")
	for _, i := range r.Injections {
		switch i.ID {
		case "el-doc":
			if i.Must || i.Text != "문서 항목. (el-doc)" {
				t.Errorf("doc 은 참고 항목이어야 함: %+v", i)
			}
		default:
			if !i.Must || strings.Contains(i.Text, "(") {
				t.Errorf("필수 항목은 text 그대로여야 함: %+v", i)
			}
		}
	}
}

func TestToolConditions(t *testing.T) {
	ix, cfg := env(t, testItems, testCfg)
	tests := []struct {
		name string
		m    Moment
		want []string
	}{
		{"조건 값 없음", Moment{}, []string{"cond-none"}},
		{"repo 일치", Moment{Repo: "sample-app"}, []string{"cond-none", "cond-repo"}},
		{"repo 불일치", Moment{Repo: "other"}, []string{"cond-none"}},
		{"path 일치", Moment{Path: "docs/a.md"}, []string{"cond-none", "cond-path"}},
		{"path 불일치", Moment{Path: "src/a.go"}, []string{"cond-none"}},
		{"agent 일치", Moment{Agent: "reviewer"}, []string{"cond-agent", "cond-none"}},
		{"agent 불일치", Moment{Agent: "builder"}, []string{"cond-none"}},
		{"모두", Moment{Repo: "sample-x", Path: "docs/b", Agent: "reviewer"}, []string{"cond-agent", "cond-none", "cond-path", "cond-repo"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.m.Kind, tt.m.Action = "tool", "x.cond"
			wantIDs(t, sel1(t, ix, cfg, tt.m), tt.want...)
		})
	}
}

func TestSessionAndSubagent(t *testing.T) {
	ix, cfg := env(t, testItems, testCfg)
	r := sel1(t, ix, cfg, Moment{Kind: "session_start"})
	wantIDs(t, r, "start-note")
	wantDrop(t, r, "rule-moment-only", "일치") // 동작 조건은 동작이 없는 순간에서 맞을 수 없다.

	wantIDs(t, sel1(t, ix, cfg, Moment{Kind: "subagent_start"}), "sub-moment")
	wantIDs(t, sel1(t, ix, cfg, Moment{Kind: "subagent_start", Agent: "reviewer"}), "sub-agent", "sub-moment")

	// 위임 글은 주제 갈래도 돈다.
	cfg.Judge.Command = script(t, yes)
	m := Moment{Kind: "subagent_start", Agent: "reviewer", Text: "배포 절차를 정리해 줘"}
	wantIDs(t, sel1(t, ix, cfg, m), "sub-agent", "sub-moment", "topic-deploy")
}

func TestPromptActions(t *testing.T) {
	ix, cfg := env(t, testItems, testCfg)
	sc := []string{"repo/sample-app"}
	push := []string{"rule-push-team", "rule-write"}
	tests := []struct {
		text  string
		asked []string
		want  []string
	}{
		{"푸시해 줘", []string{"git.push"}, push},
		{"Push 해줘", []string{"git.push"}, push},
		{"푸시하지 마", []string{}, nil},
		{"푸시해도 돼?", []string{}, nil},
		{"푸시하기 전에 테스트부터 돌려", []string{}, nil},
		{"그냥 설명해 줘", []string{}, nil},
		// 표현 뒤의 부정은 그 표현만 버린다.
		{"푸시해 줘. 아, 강제 푸시는 하지 마", []string{"git.push"}, push},
		{"강제 푸시 해 줘", []string{"git.push", "git.push.force"}, []string{"rule-push-team", "rule-write", "rule-push-force"}},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			r := sel1(t, ix, cfg, Moment{Kind: "prompt", Text: tt.text, Scopes: sc})
			if !reflect.DeepEqual(r.Asked, tt.asked) {
				t.Errorf("Asked = %v, 기대 %v", r.Asked, tt.asked)
			}
			wantIDs(t, r, tt.want...)
		})
	}
	// path 조건이 있는 항목은 프롬프트에서 주지 않는다.
	r := sel1(t, ix, cfg, Moment{Kind: "prompt", Text: "푸시해 줘", Path: "docs/a.md"})
	wantDrop(t, r, "topic-push-path", "일치")
	if i := slices.Index(ids(r), "topic-push-path"); i >= 0 {
		t.Error("path 조건이 있는 항목이 나옴")
	}
	// 시킨 동작의 Why.
	r = sel1(t, ix, cfg, Moment{Kind: "prompt", Text: "푸시해 줘", Scopes: sc})
	if r.Injections[0].Why != "프롬프트가 git.push 를 시킴" {
		t.Errorf("Why = %q", r.Injections[0].Why)
	}
}

func TestToolMadeAndSeen(t *testing.T) {
	ix, cfg := env(t, testItems, testCfg)
	cfg.Judge.Command = script(t, yes)
	r := sel1(t, ix, cfg, Moment{Kind: "prompt", Text: "푸시해 줘. 배포 절차도", ToolMade: true})
	if len(r.Injections)+len(r.Drops)+len(r.Asked) != 0 {
		t.Errorf("도구가 만든 프롬프트는 빈 결과여야 함: %+v", r)
	}

	r = sel1(t, ix, cfg, Moment{Kind: "tool", Action: "x.cond", Seen: map[string]bool{"cond-none": true}})
	wantIDs(t, r)
	wantDrop(t, r, "cond-none", "중복")
}

func TestPromptTopic(t *testing.T) {
	const ask = "배포 절차가 뭐야?"
	tests := []struct {
		name    string
		judge   string // 비면 판정 명령 없음
		votes   int
		timeout time.Duration
		want    []string
		stage   string // topic-deploy 의 탈락 단계(없으면 "")
		reason  string
	}{
		{name: "판정 명령 없음", stage: "판정", reason: "판정 명령 없음"},
		{name: "넣으라는 답", judge: yes, want: []string{"topic-deploy"}},
		{name: "넣지 말라는 답", judge: no, stage: "판정", reason: "넣지 않음: 답이 없음"},
		{name: "시간 초과", judge: "sleep 5", timeout: 200 * time.Millisecond, stage: "판정", reason: "판정 오류"},
		{name: "깨진 답", judge: "cat >/dev/null; echo 모르겠음", stage: "판정", reason: "판정 오류"},
		{name: "종료 코드", judge: "cat >/dev/null; echo '{\"inject\": true}'; exit 3", stage: "판정", reason: "판정 오류"},
		// 두 호출 중 먼저 디렉터리를 만든 쪽만 넣으라고 답한다.
		{name: "votes 2 답이 갈림", judge: `cat >/dev/null; if mkdir "$(dirname "$0")/first" 2>/dev/null; then echo '{"inject": true, "reason": "a"}'; else echo '{"inject": false, "reason": "b"}'; fi`, votes: 2, stage: "판정", reason: "넣지 않음"},
		{name: "votes 2 둘 다 넣음", judge: yes, votes: 2, want: []string{"topic-deploy"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ix, cfg := env(t, testItems, testCfg)
			if tt.judge != "" {
				cfg.Judge.Command = script(t, tt.judge)
			}
			if tt.votes > 0 {
				cfg.Judge.Votes = tt.votes
			}
			if tt.timeout > 0 {
				cfg.Judge.Timeout = tt.timeout
			}
			r := sel1(t, ix, cfg, Moment{Kind: "prompt", Text: ask})
			wantIDs(t, r, tt.want...)
			if tt.stage != "" {
				d := dropOf(r, "topic-deploy")
				if d == nil || d.Stage != tt.stage || !strings.Contains(d.Reason, tt.reason) {
					t.Errorf("topic-deploy 탈락 = %+v, 기대 %s/%s", d, tt.stage, tt.reason)
				}
			}
			// 동작 조건이 있는 항목은 후보가 아니다. 판정 단계의 탈락도 없다.
			if d := dropOf(r, "topic-act"); d != nil {
				t.Errorf("동작 조건 항목이 후보가 됨: %+v", d)
			}
			// 주제 갈래는 moment 가 prompt 를 포함하지 않는 항목도 후보로 보지 않는다.
			if d := dropOf(r, "topic-moment"); d != nil {
				t.Errorf("도구 전용 항목은 후보가 아님: %+v", d)
			}
		})
	}
}

func TestTopicDetails(t *testing.T) {
	ix, cfg := env(t, testItems, testCfg)

	// 항목마다 판정한다.
	cfg.Judge.Command = script(t, `in=$(cat); case "$in" in *"id: topic-regex"*) echo '{"inject": true, "reason": "맞음"}';; *) echo '{"inject": false, "reason": "아님"}';; esac`)
	r := sel1(t, ix, cfg, Moment{Kind: "prompt", Text: "정규식과 배포 절차"})
	wantIDs(t, r, "topic-regex")
	wantDrop(t, r, "topic-deploy", "판정")
	if !strings.Contains(r.Injections[0].Why, `주제어 "정규식"`) || !strings.Contains(r.Injections[0].Why, "판정: 맞음") {
		t.Errorf("Why = %q", r.Injections[0].Why)
	}

	// 낱말 순위만으로 걸린 후보. 순위 맨 위의 동작 조건 항목은 건너뛰고 후보 수를 채운다.
	cfg.Judge.Command = script(t, yes)
	cfg.Judge.Candidates = 1
	r = sel1(t, ix, cfg, Moment{Kind: "prompt", Text: "배포 전 푸시 규칙"})
	wantIDs(t, r, "topic-deploy")
	if !strings.Contains(r.Injections[0].Why, "낱말 점수") {
		t.Errorf("Why = %q", r.Injections[0].Why)
	}

	// 판정을 불러도 이미 넣은 항목은 부르기 전에 걸러진다.
	r = sel1(t, ix, cfg, Moment{Kind: "prompt", Text: "배포 전 푸시 규칙", Seen: map[string]bool{"topic-deploy": true}})
	wantIDs(t, r)
	wantDrop(t, r, "topic-deploy", "중복")
}

func TestTopicMax(t *testing.T) {
	items := map[string]string{}
	for _, n := range []string{"1", "2", "3", "4", "5"} {
		items["many-"+n] = u + "text: 공통 안내 " + n + ".\nabout: [공통용어]\nscope: [personal]"
	}
	ix, cfg := env(t, items, testCfg)
	cfg.Judge.Command = script(t, yes)
	r := sel1(t, ix, cfg, Moment{Kind: "prompt", Text: "공통용어가 뭐야"})
	wantIDs(t, r, "many-1", "many-2", "many-3")
	wantDrop(t, r, "many-4", "예산")
	wantDrop(t, r, "many-5", "예산")
}

func TestBudgetAndForm(t *testing.T) {
	items := map[string]string{
		"a-ref":  "source: doc\ntext: bbbbbbbbb\nmust: true\nwhen: {action: x.b}\nabout: [b]\nscope: [personal]",
		"b-must": u + "text: aaaaaaaaa\nmust: true\nwhen: {action: x.b}\nabout: [b]\nscope: [personal]",
		"c-must": u + "text: ccccccccc\nmust: true\nwhen: {action: x.b}\nabout: [b]\nscope: [personal]",
	}
	ix, cfg := env(t, items, testCfg)
	tests := []struct {
		budget int
		want   []string
	}{
		{12, []string{"a-ref", "b-must", "c-must"}}, // 다 들어가면 원래 순서
		{7, []string{"b-must", "c-must"}},           // 필수 먼저
		{4, []string{"b-must"}},                     // 같은 종류 안에서는 원래 순서
		{2, nil},
	}
	for _, tt := range tests {
		cfg.Budgets["tool"] = tt.budget
		r := sel1(t, ix, cfg, Moment{Kind: "tool", Action: "x.b"})
		wantIDs(t, r, tt.want...)
		if len(r.Drops) != 3-len(tt.want) {
			t.Errorf("예산 %d: 탈락 %v", tt.budget, r.Drops)
		}
	}
	cfg.Budgets["tool"] = 12
	r := sel1(t, ix, cfg, Moment{Kind: "tool", Action: "x.b"})
	if r.Injections[0].Text != "bbbbbbbbb (a-ref)" || r.Injections[1].Text != "aaaaaaaaa" {
		t.Errorf("형태: %+v", r.Injections)
	}
	if tokens("") != 0 || tokens("가") != 1 || tokens("abcd") != 2 {
		t.Error("토큰 어림이 (바이트 + 2) / 3 이어야 함")
	}
}

func TestUnknownMoment(t *testing.T) {
	ix, cfg := env(t, testItems, testCfg)
	if _, err := Select(context.Background(), ix, cfg, Moment{Kind: "nope"}); err == nil {
		t.Error("모르는 순간은 오류여야 함")
	}
}

// Refused 는 표현이 있었지만 모든 자리가 버려진 동작이다.
func TestRefused(t *testing.T) {
	ix, cfg := env(t, testItems, testCfg)
	r := sel1(t, ix, cfg, Moment{Kind: "prompt", Text: "푸시해도 돼? 커밋해 줘"})
	if !reflect.DeepEqual(r.Refused, []string{"git.push"}) || !reflect.DeepEqual(r.Asked, []string{"git.commit"}) {
		t.Errorf("asked = %v, refused = %v", r.Asked, r.Refused)
	}
	// 한 자리라도 살아남으면 시킨 것이다.
	r = sel1(t, ix, cfg, Moment{Kind: "prompt", Text: "푸시해도 돼? 좋아, 푸시해 줘"})
	if !reflect.DeepEqual(r.Asked, []string{"git.push"}) || len(r.Refused) != 0 {
		t.Errorf("asked = %v, refused = %v", r.Asked, r.Refused)
	}
	r = sel1(t, ix, cfg, Moment{Kind: "prompt", Text: "안녕"})
	if r.Refused == nil || len(r.Refused) != 0 {
		t.Errorf("refused 는 빈 목록이어야 함: %#v", r.Refused)
	}
}

// Injection 은 confirm 과 해시를 싣고, 도구 순간에는 확인 항목이 이미 넣은 것이어도 남는다.
func TestConfirmFieldsAndSeen(t *testing.T) {
	items := map[string]string{
		"ask-rule":  u + "text: 확인 항목.\nmust: true\nconfirm: always\nwhen: {action: git.push}\nabout: [푸시]\nscope: [personal]",
		"info-rule": u + "text: 알리기 항목.\nmust: true\nwhen: {action: git.push}\nabout: [푸시]\nscope: [personal]",
	}
	ix, cfg := env(t, items, testCfg)
	m := Moment{Kind: "tool", Action: "git.push"}
	r := sel1(t, ix, cfg, m)
	byID := map[string]Injection{}
	for _, i := range r.Injections {
		byID[i.ID] = i
	}
	if byID["ask-rule"].Confirm != "always" || byID["info-rule"].Confirm != "false" || len(byID["ask-rule"].Hash) != 16 {
		t.Errorf("injections = %+v", r.Injections)
	}
	m.Seen = map[string]bool{"ask-rule": true, "info-rule": true}
	r = sel1(t, ix, cfg, m)
	wantIDs(t, r, "ask-rule")
	wantDrop(t, r, "info-rule", "중복")
	m.Kind, m.Text = "prompt", "푸시해 줘"
	r = sel1(t, ix, cfg, m)
	wantIDs(t, r)
}
