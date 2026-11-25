package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hee-develop/heka/internal/item"
)

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const sample = `
other_key: 무시된다
scopes:
  repo/sample-app: [team/sample]
  team/sample: [org/sample]
actions:
  git.write: {}
  git.push: {in: git.write, phrases: ["푸시해", "push"]}
  git.push.force: {phrases: ["강제 푸시", "force push"]}
`

func TestClosure(t *testing.T) {
	c, err := Load(write(t, sample))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		got, want []string
	}{
		{c.ScopeClosure("repo/sample-app"), []string{"org/sample", "repo/sample-app", "team/sample"}},
		{c.ScopeClosure("org/sample"), []string{"org/sample"}},
		{c.ActionClosure("git.push.force"), []string{"git", "git.push", "git.push.force", "git.write"}},
		{c.ActionClosure("git.push"), []string{"git", "git.push", "git.write"}},
		{c.ActionClosure("git.write"), []string{"git", "git.write"}},
		{c.ActionClosure("npm.publish.dry"), []string{"npm", "npm.publish", "npm.publish.dry"}}, // 설정에 없는 이름
	}
	for _, tt := range tests {
		if !reflect.DeepEqual(tt.got, tt.want) {
			t.Errorf("닫힘 = %v, 기대 %v", tt.got, tt.want)
		}
	}
	if got := c.Phrases(); len(got) != 4 || got[0] != [2]string{"git.push", "푸시해"} {
		t.Errorf("Phrases = %v", got)
	}
	if !c.ActionVocab()["git.push.force"] || c.ActionVocab()["git.pull"] {
		t.Errorf("ActionVocab 이 맞지 않음: %v", c.ActionVocab())
	}
}

func TestLoadMissingAndCycle(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil || len(c.Scopes) != 0 || len(c.ActionVocab()) != 0 {
		t.Fatalf("파일 없음은 빈 설정이어야 함: %v %v", c, err)
	}
	_, err = Load(write(t, "scopes:\n  a: [b]\n  b: [c]\n  c: [a]\n"))
	if err == nil || !strings.Contains(err.Error(), "순환") {
		t.Errorf("순환 오류가 나야 함: %v", err)
	}
	if _, err = Load(write(t, "scopes: [깨짐")); err == nil {
		t.Error("YAML 오류가 나야 함")
	}
}

func TestHome(t *testing.T) {
	t.Setenv("HEKA_HOME", "/x/y")
	if h, _ := Home(); h != "/x/y" {
		t.Errorf("Home = %q", h)
	}
	t.Setenv("HEKA_HOME", "")
	t.Setenv("HOME", "/h")
	if h, _ := Home(); h != filepath.Join("/h", defaultHomeDir) {
		t.Errorf("Home = %q", h)
	}
}

const extended = `
global_scopes: personal
actions:
  git.push:
    phrases: ['(?:푸[시쉬]|push)\s*(?:해|하고)']
negations: ['하지\s*마']
negation_window: 5
budgets: {prompt: 120}
judge:
  command: [my-model-cli, --model, small]
  timeout: 3s
  votes: 2
  instruction: |
    지시문
`

func TestExtended(t *testing.T) {
	c, err := Load(write(t, extended))
	if err != nil {
		t.Fatal(err)
	}
	j := c.Judge
	if !reflect.DeepEqual(c.GlobalScopes, item.List{"personal"}) || c.NegationWindow != 5 || len(c.Negations) != 1 {
		t.Errorf("읽은 값이 다름: %+v", c)
	}
	if j.Timeout != 3*time.Second || j.Votes != 2 || j.Candidates != 3 || j.Instruction != "지시문\n" || !reflect.DeepEqual(j.Command, []string{"my-model-cli", "--model", "small"}) {
		t.Errorf("judge = %+v", j)
	}
	if c.Budgets["prompt"] != 120 || c.Budgets["tool"] != 0 || c.Budgets["session_start"] != 0 || c.Budgets["subagent_start"] != 0 || c.Budgets["find"] != 1500 {
		t.Errorf("budgets = %v", c.Budgets)
	}
}

func TestDefaults(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.NegationWindow != 8 || c.Judge.Timeout != 15*time.Second || c.Judge.Candidates != 3 || c.Judge.Votes != 1 || len(c.Judge.Command) != 0 {
		t.Errorf("기본값이 다름: %+v", c)
	}
	if !reflect.DeepEqual(c.Budgets, DefaultBudgets) {
		t.Errorf("budgets = %v", c.Budgets)
	}
}

func TestRegisterJudgeAndReviewDefaults(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rj, rv := c.RegisterJudge, c.Review
	if len(rj.Command) != 0 || rj.Timeout != 120*time.Second || rj.Candidates != 5 || rj.Instruction != DefaultRegisterJudgeInstruction {
		t.Errorf("register_judge 기본값: %+v", rj)
	}
	if len(rv.NotifyCommand) != 0 || rv.NotifyMin != 5 || rv.NotifyDays != 7 || rv.Sample != 3 || rv.Batch != 5 {
		t.Errorf("review 기본값: %+v", rv)
	}
	c, err = Load(write(t, "register_judge: {command: [x], timeout: 5s, candidates: 2, instruction: 지시}\nreview: {notify_command: [n, a], notify_min: 1, notify_days: 2, sample: 4, batch: 6}"))
	if err != nil {
		t.Fatal(err)
	}
	if c.RegisterJudge.Timeout != 5*time.Second || c.RegisterJudge.Candidates != 2 || c.RegisterJudge.Instruction != "지시" ||
		!reflect.DeepEqual(c.Review.NotifyCommand, []string{"n", "a"}) || c.Review.Batch != 6 || c.Review.Sample != 4 {
		t.Errorf("설정값: %+v %+v", c.RegisterJudge, c.Review)
	}
}

// 예시 설정의 등록 판정 지시문은 코드의 기본 지시문과 같은 글이다.
func TestExampleRegisterJudgeInstructionIsDefault(t *testing.T) {
	b, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(write(t, string(b)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimRight(c.RegisterJudge.Instruction, "\n") != DefaultRegisterJudgeInstruction || c.Review.NotifyMin != 5 {
		t.Error("예시의 register_judge.instruction 이 기본 지시문과 다름")
	}
}

func TestInvalid(t *testing.T) {
	for name, body := range map[string]string{
		"phrases 정규식":     "actions:\n  a.b: {phrases: ['(']}",
		"negations 정규식":   "negations: ['[']",
		"votes 범위":        "judge: {votes: 3}",
		"budgets 이름":      "budgets: {nope: 1}",
		"instruction 없음":  "judge: {command: [x]}",
		"pick mode 값":     "judge: {pick: {mode: always}}",
		"pick 지시문 없음":     "judge: {command: [x], instruction: i, pick: {mode: record}}",
		"pick 명령 없음":      "judge: {pick: {mode: record, instruction: i}}",
		"pick timeout 음수": "judge: {pick: {timeout: -1s}}",
		"pick max 음수":     "judge: {pick: {max: -1}}",
		"등록 판정 시간 음수":     "register_judge: {timeout: -1s}",
		"검토 batch 음수":     "review: {batch: -1}",
	} {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: 오류여야 함", name)
		}
	}
}

// 저장소의 예시 설정이 설정으로 읽히는지 확인한다.
func TestExampleFile(t *testing.T) {
	b, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(write(t, string(b)))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"git.commit", "git.push", "git.push.force", "git.rebase", "pr.create"} {
		if len(c.Actions[a].Phrases) == 0 {
			t.Errorf("예시 설정에 %s 의 표현이 없음", a)
		}
	}
	// PR 본문 규칙은 pr.write 에 건다: pr.create 와 pr.edit 의 상위에 pr.write 가 들어가야 한다.
	for _, a := range []string{"pr.create", "pr.edit"} {
		if !slices.Contains(c.ActionClosure(a), "pr.write") || !reflect.DeepEqual(c.Actions[a].In, item.List{"pr.write"}) {
			t.Errorf("예시 설정에서 %s 가 pr.write 에 들어가야 함: %v", a, c.ActionClosure(a))
		}
	}
	if len(c.Negations) == 0 || len(c.Judge.Command) != 0 || !strings.Contains(c.Judge.Instruction, "선별기") || !reflect.DeepEqual(c.GlobalScopes, item.List{"personal"}) {
		t.Errorf("예시 설정의 내용이 다름: %+v", c)
	}
}

func TestToolActionsFor(t *testing.T) {
	c, err := Load(write(t, "tool_actions:\n  \"Skill:make-*\": [a.one]\n  \"mcp__*__get\": [b.two, a.one]\n  Exact: [c.three]\n"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		keys []string
		want []string
	}{
		{[]string{"Skill:make-flag"}, []string{"a.one"}},
		{[]string{"Skill:make-"}, []string{"a.one"}},
		{[]string{"Skill:other"}, nil},
		{[]string{"mcp__srv__get", "Exact"}, []string{"c.three", "b.two", "a.one"}},
		{[]string{"mcp__srv__get2"}, nil},
		{[]string{"Exact2"}, nil},
	}
	for _, tt := range tests {
		if got := c.ToolActionsFor(tt.keys...); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%v: %v, 기대 %v", tt.keys, got, tt.want)
		}
	}
	if !c.ActionVocab()["b.two"] || !c.ActionVocab()["a"] {
		t.Errorf("tool_actions 의 동작 이름도 어휘에 있어야 함: %v", c.ActionVocab())
	}
}

// 예시 설정이 그대로 읽히고, 위치 맞춤과 기본값이 설명대로인지 본다.
func TestExampleAndLocate(t *testing.T) {
	b, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.ToolActionsFor("Bash:codegen"), []string{"code.generate"}) || c.Judge.Timeout != 15*time.Second {
		t.Errorf("예시 설정: %v, %v", c.ToolActions, c.Judge.Timeout)
	}
	if !reflect.DeepEqual(c.SessionGrant, DefaultSessionGrant) || !reflect.DeepEqual(c.ClaudeCode.ToolPromptPrefixes, DefaultToolPromptPrefixes) {
		t.Errorf("예시의 값이 기본값과 다름: %v %v", c.SessionGrant, c.ClaudeCode.ToolPromptPrefixes)
	}

	c.Locations = []Location{
		{Path: "/w/app", Scopes: item.List{"a"}, Repo: "app-repo"},
		{Path: "/w/app/sub", Scopes: item.List{"b"}},
		{Path: "/w/tool-*", Scopes: item.List{"c"}},
		{Path: "~/home-proj", Scopes: item.List{"d"}},
	}
	t.Setenv("HOME", "/h")
	tests := []struct {
		cwd, repo string
		scopes    []string
	}{
		{"/w/app", "app-repo", []string{"a"}},
		{"/w/app/x/y", "app-repo", []string{"a"}},
		{"/w/app/sub/z", "sub", []string{"b"}}, // 가장 길게 맞는 것
		{"/w/application", "", nil},            // 폴더 경계
		{"/w/tool-one/src", "tool-one", []string{"c"}},
		{"/w/tool-", "tool-", []string{"c"}},
		{"/h/home-proj/a", "home-proj", []string{"d"}},
		{"/elsewhere", "", nil},
		{"", "", nil},
	}
	for _, tt := range tests {
		sc, repo := c.Locate(tt.cwd)
		if !reflect.DeepEqual(sc, tt.scopes) || repo != tt.repo {
			t.Errorf("Locate(%q) = %v, %q; 기대 %v, %q", tt.cwd, sc, repo, tt.scopes, tt.repo)
		}
	}
}

func TestLocationErrors(t *testing.T) {
	for _, body := range []string{"locations:\n  - scopes: [a]\n", "session_grant: ['(']\n"} {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%q: 오류가 나야 함", body)
		}
	}
}

func TestJudgePick(t *testing.T) {
	// 기본은 꺼짐이고 max 는 8 이다.
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.Judge.Pick.Mode != "" || c.Judge.Pick.Max != 8 || c.Judge.PickRecording() {
		t.Errorf("기본값 = %+v", c.Judge.Pick)
	}
	for _, mode := range []string{"", "off", "record"} {
		body := "judge: {command: [x], instruction: i, pick: {mode: " + mode + ", instruction: p}}"
		c, err := Load(write(t, body))
		if err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		if got := c.Judge.PickRecording(); got != (mode == "record") {
			t.Errorf("mode %q: PickRecording = %v", mode, got)
		}
	}
	// pick.command 와 pick.timeout 이 없으면 판정 명령과 판정 시간을 쓴다.
	c, err = Load(write(t, "judge: {command: [x], instruction: i, timeout: 4s, pick: {mode: record, instruction: p}}"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Judge.PickCommand(), []string{"x"}) || c.Judge.PickTimeout() != 4*time.Second {
		t.Errorf("대체값: %v %v", c.Judge.PickCommand(), c.Judge.PickTimeout())
	}
	// judge.command 없이 pick.command 만 있어도 된다.
	c, err = Load(write(t, "judge: {pick: {mode: record, command: [y, z], timeout: 2s, max: 3, instruction: p}}"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Judge.PickCommand(), []string{"y", "z"}) || c.Judge.PickTimeout() != 2*time.Second || c.Judge.Pick.Max != 3 || !c.Judge.PickRecording() {
		t.Errorf("pick = %+v", c.Judge.Pick)
	}
}
