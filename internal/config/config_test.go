package config

import (
	"os"
	"path/filepath"
	"reflect"
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
	if c.Budgets["prompt"] != 120 || c.Budgets["tool"] != 300 || c.Budgets["session_start"] != 1000 {
		t.Errorf("budgets = %v", c.Budgets)
	}
}

func TestDefaults(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.NegationWindow != 8 || c.Judge.Timeout != 10*time.Second || c.Judge.Candidates != 3 || c.Judge.Votes != 1 || len(c.Judge.Command) != 0 {
		t.Errorf("기본값이 다름: %+v", c)
	}
	if !reflect.DeepEqual(c.Budgets, DefaultBudgets) {
		t.Errorf("budgets = %v", c.Budgets)
	}
}

func TestInvalid(t *testing.T) {
	for name, body := range map[string]string{
		"phrases 정규식":    "actions:\n  a.b: {phrases: ['(']}",
		"negations 정규식":  "negations: ['[']",
		"votes 범위":       "judge: {votes: 3}",
		"budgets 이름":     "budgets: {nope: 1}",
		"instruction 없음": "judge: {command: [x]}",
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
