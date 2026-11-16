package action

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hee-develop/heka/internal/config"
)

type corpusCase struct {
	ID    string         `json:"id"`
	Tool  string         `json:"tool"`
	Input map[string]any `json:"input"`
	Cwd   string         `json:"cwd"`
	Want  Resolved       `json:"want"`
}

// 시험 자료의 모든 호출이 기대한 동작 이름·경로·에이전트로 해석되는지 본다.
func TestResolveCorpus(t *testing.T) {
	t.Setenv("HOME", "/home/dev")
	t.Setenv("TMPDIR", "")
	b, err := os.ReadFile(filepath.Join("testdata", "calls.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []corpusCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 300 {
		t.Fatalf("시험 사례가 %d건뿐임", len(cases))
	}
	for _, c := range cases {
		got := Resolve(Call{Tool: c.Tool, Input: c.Input, Cwd: c.Cwd}, nil)
		if !reflect.DeepEqual(got, c.Want) {
			t.Errorf("%s: %s %v (cwd %s)\n got %+v\nwant %+v", c.ID, c.Tool, c.Input, c.Cwd, got, c.Want)
		}
	}
}

func loadConfig(t *testing.T, body string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// 도구 이름·스킬 이름으로 잇는 tool_actions 를 본다.
func TestResolveToolActions(t *testing.T) {
	cfg := loadConfig(t, `
tool_actions:
  "Skill:create-flag": [feature_flag.create]
  "mcp__tracker__*": [ticket.plan]
  "mcp__*__get_*": [lookup.read]
  "Workflow": [flow.start]
`)
	skill := func(name string) Call { return Call{Tool: "Skill", Input: map[string]any{"skill": name}} }
	tests := []struct {
		name string
		call Call
		want []string
	}{
		{"스킬 이름", skill("create-flag"), []string{"skill.run", "feature_flag.create"}},
		{"플러그인 접두가 붙은 스킬", skill("sample-plugin:create-flag"), []string{"skill.run", "feature_flag.create"}},
		{"다른 스킬", skill("other"), []string{"skill.run"}},
		{"mcp 패턴", Call{Tool: "mcp__tracker__create_issue"}, []string{"mcp.write", "ticket.plan"}},
		{"mcp 패턴 두 개", Call{Tool: "mcp__tracker__get_issue"}, []string{"mcp.read", "lookup.read", "ticket.plan"}},
		{"인증 호출은 잇지 않음", Call{Tool: "mcp__tracker__authenticate"}, []string{}},
		{"정확한 도구 이름", Call{Tool: "Workflow"}, []string{"workflow.run", "flow.start"}},
		{"Bash 에는 걸리지 않음", Call{Tool: "Bash", Input: map[string]any{"command": "git push"}}, []string{"git.push"}},
	}
	for _, tt := range tests {
		if got := Resolve(tt.call, cfg).Actions; !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: %v, 기대 %v", tt.name, got, tt.want)
		}
	}
	if got := Resolve(skill("create-flag"), nil).Actions; !reflect.DeepEqual(got, []string{"skill.run"}) {
		t.Errorf("설정이 없으면 내장 동작만: %v", got)
	}
}

func TestToolActionsConfigErrors(t *testing.T) {
	for _, body := range []string{"tool_actions:\n  x: []\n", "tool_actions:\n  x: ['']\n"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Load(dir); err == nil || !strings.Contains(err.Error(), "tool_actions") {
			t.Errorf("%q: 오류가 나야 함: %v", body, err)
		}
	}
}

// rm 의 대상이 작업 폴더를 품는지는 cwd 에 달려 있다.
func TestRmDependsOnCwd(t *testing.T) {
	t.Setenv("HOME", "/home/dev")
	rm := func(cwd, cmd string) []string {
		return Resolve(Call{Tool: "Bash", Input: map[string]any{"command": cmd}, Cwd: cwd}, nil).Actions
	}
	if got := rm("/home/dev/work/sample-app", "rm -rf /home/dev/work"); !reflect.DeepEqual(got, []string{"fs.destructive"}) {
		t.Errorf("상위 폴더 삭제: %v", got)
	}
	if got := rm("/home/dev/work/sample-app", "rm -rf /home/dev/work/other"); !reflect.DeepEqual(got, []string{"bash.other"}) {
		t.Errorf("다른 폴더 삭제: %v", got)
	}
	if got := rm("", "rm -rf build"); !reflect.DeepEqual(got, []string{"bash.other"}) {
		t.Errorf("cwd 가 없으면 상대 경로는 풀지 않음: %v", got)
	}
}
