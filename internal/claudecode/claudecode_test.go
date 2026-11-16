package claudecode

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hee-develop/heka/internal/session"
)

// testEnv 는 지어낸 데이터 폴더와 작업 폴더다.
type testEnv struct {
	t          *testing.T
	home, work string
}

const cfgTmpl = `
scopes:
  repo/sample-app: [team/sample]
locations:
  - path: WORK/sample-app
    scopes: [repo/sample-app]
  - path: WORK/other-*
    scopes: [repo/other]
claude_code:
  scopes: [tool/claude-code]
actions:
  git.write: {}
  git.commit:
    in: git.write
    phrases: ['(?:커밋|commit)\s*(?:을|를)?\s*(?:해|하지\s*(?:마|말)|하고)']
  git.push:
    in: git.write
    phrases: ['(?:푸[시쉬]|push)\s*(?:을|를)?\s*(?:해|하지\s*(?:마|말)|하고)']
  pr.create:
    phrases: ['(?:pr|풀\s*리퀘)\s*(?:을|를)?\s*(?:올려|만들)']
negations: ['하지\s*(?:마|말)', '할까', '해도\s*(?:돼|될까)']
tool_actions:
  "Bash:codegen": [code.generate]
judge: {command: [JUDGE], instruction: 지시문, timeout: 5s}
`

// 시험 항목. 이름이 곧 id 다.
var testItems = map[string]string{
	"session-rule": "text: 세션 시작 규칙.\nmust: true\nwhen: {moment: session_start}\nscope: [tool/claude-code]",
	"push-confirm": "text: 푸시 전에 확인한다.\nmust: true\nconfirm: true\nwhen: {action: git.push}\nscope: [repo/sample-app]",
	"push-force":   "text: 강제 푸시는 막는다.\nmust: true\nconfirm: true\nwhen: {action: git.push.force}\nscope: [repo/sample-app]",
	"write-info":   "text: 쓰기 전에 상태를 본다.\nmust: true\nwhen: {action: git.write}\nscope: [repo/sample-app]",
	"pr-always":    "text: PR 은 항상 확인한다.\nmust: true\nconfirm: always\nwhen: {action: pr.create}\nscope: [repo/sample-app]",
	"gen-app":      "text: 생성 규칙(앱).\nmust: true\nwhen: {action: code.generate, repo: sample-app}\nscope: [repo/sample-app]",
	"gen-other":    "text: 생성 규칙(다른 곳).\nmust: true\nwhen: {action: code.generate, repo: 'other-*'}\nscope: [repo/other]",
	"docs-path":    "text: 문서 경로 규칙.\nmust: true\nwhen: {action: doc.edit, path: 'WORK/sample-app/docs/*.md'}\nscope: [repo/sample-app]",
	"topic-deploy": "text: 배포는 점검표를 따른다.\nabout: [배포 점검]\nscope: [repo/sample-app]",
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	e := &testEnv{t: t, home: filepath.Join(t.TempDir(), "heka-home"), work: t.TempDir()}
	judge := filepath.Join(t.TempDir(), "judge.sh")
	script := "#!/bin/sh\nif grep -q zeta-token; then echo '{\"inject\": true, \"reason\": \"위임 글에 맞음\"}'; else echo '{\"inject\": false, \"reason\": \"x\"}'; fi\n"
	if err := os.WriteFile(judge, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	e.writeConfig(strings.ReplaceAll(cfgTmpl, "JUDGE", judge))
	for id, body := range testItems {
		front := "---\nid: " + id + "\nsource: user\nquote: 지어낸 말\nabout: [" + id + "]\n" + body + "\n---\n"
		if strings.HasPrefix(id, "topic") {
			front = "---\nid: " + id + "\nsource: user\nquote: 지어낸 말\n" + body + "\n---\n"
		}
		e.write(filepath.Join("items", id+".md"), strings.ReplaceAll(front, "WORK", e.work))
	}
	return e
}

func (e *testEnv) write(rel, body string) {
	e.t.Helper()
	p := filepath.Join(e.home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) writeConfig(body string) {
	e.write("config.yaml", strings.ReplaceAll(body, "WORK", e.work))
}

func (e *testEnv) app() string { return filepath.Join(e.work, "sample-app") }

// hook 은 입력 JSON 을 훅에 넣고 종료 코드와 출력을 돌려준다.
func (e *testEnv) hook(in map[string]any) (int, string) {
	e.t.Helper()
	b, _ := json.Marshal(in)
	var out bytes.Buffer
	return Run(bytes.NewReader(b), &out, e.home), out.String()
}

type hookOut struct {
	Specific struct {
		Event   string `json:"hookEventName"`
		Context string `json:"additionalContext"`
		Perm    string `json:"permissionDecision"`
		Reason  string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// call 은 훅을 부르고 종료 코드 0 과 출력 모양을 확인한 뒤 해석해 돌려준다. 출력이 없으면 nil 이다.
func (e *testEnv) call(in map[string]any) *hookOut {
	e.t.Helper()
	code, out := e.hook(in)
	if code != 0 {
		e.t.Fatalf("종료 코드 %d", code)
	}
	if out == "" {
		return nil
	}
	var h hookOut
	if err := json.Unmarshal([]byte(out), &h); err != nil || !strings.HasSuffix(out, "\n") {
		e.t.Fatalf("출력이 JSON 한 줄이 아님: %q (%v)", out, err)
	}
	if strings.Contains(out, `\u`) {
		e.t.Errorf("한글이 이스케이프됨: %s", out)
	}
	return &h
}

func (e *testEnv) start(sid, source string) *hookOut {
	return e.call(map[string]any{"hook_event_name": "SessionStart", "session_id": sid, "cwd": e.app(), "source": source})
}

func (e *testEnv) prompt(sid, text string) *hookOut {
	return e.call(map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": sid, "cwd": e.app(), "prompt": text})
}

func (e *testEnv) bash(sid, cmd string, extra ...any) *hookOut {
	return e.tool(sid, "Bash", map[string]any{"command": cmd}, extra...)
}

// tool 의 extra 는 (키, 값) 쌍으로, 입력에 더한다(agent_id 등). cwd 도 이 방식으로 바꾼다.
func (e *testEnv) tool(sid, name string, input map[string]any, extra ...any) *hookOut {
	in := map[string]any{"hook_event_name": "PreToolUse", "session_id": sid, "cwd": e.app(), "tool_name": name, "tool_input": input}
	for i := 0; i+1 < len(extra); i += 2 {
		in[extra[i].(string)] = extra[i+1]
	}
	return e.call(in)
}

func (e *testEnv) ledgerRows() []map[string]any {
	e.t.Helper()
	files, _ := filepath.Glob(filepath.Join(e.home, "ledger", "*.jsonl"))
	var rows []map[string]any
	for _, f := range files {
		b, _ := os.ReadFile(f)
		for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				e.t.Fatalf("원장 줄이 JSON 이 아님: %q", line)
			}
			rows = append(rows, m)
		}
	}
	return rows
}

func wantContext(t *testing.T, h *hookOut, event string, in ...string) {
	t.Helper()
	if h == nil {
		t.Fatalf("출력이 있어야 함(%v)", in)
	}
	if h.Specific.Event != event || !strings.HasPrefix(h.Specific.Context, "[heka]\n- ") {
		t.Errorf("이벤트 %q, 글 %q", h.Specific.Event, h.Specific.Context)
	}
	for _, s := range in {
		if !strings.Contains(h.Specific.Context, s) {
			t.Errorf("%q 가 글에 없음: %q", s, h.Specific.Context)
		}
	}
}

func TestSessionStartShape(t *testing.T) {
	e := newEnv(t)
	h := e.start("s1", "startup")
	wantContext(t, h, "SessionStart", "- 세션 시작 규칙.")
	if h.Specific.Perm != "" || strings.Count(h.Specific.Context, "\n- ") != 1 {
		t.Errorf("%+v", h.Specific)
	}
	// 범위 밖(위치 없음, cwd 가 다른 곳)이어도 claude_code.scopes 의 항목은 나온다.
	h = e.call(map[string]any{"hook_event_name": "SessionStart", "session_id": "s2", "cwd": "/nowhere", "source": "startup"})
	wantContext(t, h, "SessionStart", "세션 시작 규칙")
	// 넣을 것이 없으면 출력이 없다.
	if h := e.start("s1", "resume"); h != nil {
		t.Errorf("두 번째는 출력이 없어야 함: %+v", h)
	}
	for _, ev := range []string{"Stop", "Notification", "PostToolUse"} {
		if code, out := e.hook(map[string]any{"hook_event_name": ev, "session_id": "s1"}); code != 0 || out != "" {
			t.Errorf("%s: %d %q", ev, code, out)
		}
	}
}

func TestDedupe(t *testing.T) {
	e := newEnv(t)
	wantContext(t, e.start("s", "startup"), "SessionStart", "세션 시작 규칙")
	if h := e.start("s", "startup"); h != nil {
		t.Errorf("같은 세션 두 번째: %+v", h)
	}
	if h := e.start("s", "compact"); h == nil {
		t.Error("compact 뒤에는 다시 나와야 함")
	}
	if h := e.start("s", "startup"); h != nil {
		t.Error("compact 뒤 다시 넣은 것도 한 번뿐")
	}
	if h := e.start("s", "clear"); h == nil {
		t.Error("clear 뒤에는 다시 나와야 함")
	}
	// 다른 세션은 따로 센다.
	if h := e.start("other", "startup"); h == nil {
		t.Error("다른 세션은 따로 셈")
	}

	// 도구 순간의 알리기 항목: 메인에 한 번, 서브에이전트는 따로, 같은 서브에이전트에는 한 번.
	wantContext(t, e.bash("s", "git commit -m x"), "PreToolUse", "쓰기 전에 상태를 본다")
	if h := e.bash("s", "git commit -m y"); h != nil {
		t.Errorf("메인 두 번째: %+v", h)
	}
	wantContext(t, e.bash("s", "git commit -m z", "agent_id", "a1", "agent_type", "Explore"), "PreToolUse", "쓰기 전에")
	if h := e.bash("s", "git commit -m z", "agent_id", "a1", "agent_type", "Explore"); h != nil {
		t.Errorf("같은 서브에이전트 두 번째: %+v", h)
	}
	wantContext(t, e.bash("s", "git commit -m z", "agent_id", "a2", "agent_type", "Explore"), "PreToolUse", "쓰기 전에")
}

func TestPreToolConditions(t *testing.T) {
	e := newEnv(t)
	// Bash 명령 → 동작 이름 → 조건이 걸린 항목. repo 조건은 위치에서 정한 저장소 이름과 견준다.
	h := e.bash("s", "npm run codegen screen")
	wantContext(t, h, "PreToolUse", "생성 규칙(앱)")
	if strings.Contains(h.Specific.Context, "다른 곳") {
		t.Errorf("repo 조건이 안 걸림: %s", h.Specific.Context)
	}
	h = e.call(map[string]any{"hook_event_name": "PreToolUse", "session_id": "s", "cwd": filepath.Join(e.work, "other-x", "src"),
		"tool_name": "Bash", "tool_input": map[string]any{"command": "npx codegen api"}})
	wantContext(t, h, "PreToolUse", "생성 규칙(다른 곳)")
	if strings.Contains(h.Specific.Context, "(앱)") {
		t.Errorf("repo 조건이 안 걸림: %s", h.Specific.Context)
	}
	// path 조건: 편집 대상 경로가 맞아야 한다.
	if h := e.tool("s", "Write", map[string]any{"file_path": filepath.Join(e.app(), "src", "a.md")}); h != nil {
		t.Errorf("경로가 다르면 나오지 않음: %+v", h)
	}
	wantContext(t, e.tool("s", "Write", map[string]any{"file_path": filepath.Join(e.app(), "docs", "a.md")}), "PreToolUse", "문서 경로 규칙")
	// 동작이 둘인 명령: 둘 다의 항목이 나오고 write-info 는 한 번만 나온다.
	h = e.bash("s2", "git commit -m x && git push")
	if h == nil || h.Specific.Perm != "ask" {
		t.Fatalf("push 는 확인을 요구해야 함: %+v", h)
	}
	if got := strings.Count(h.Specific.Context, "쓰기 전에 상태를 본다"); got != 1 {
		t.Errorf("write-info 는 한 번만: %q", h.Specific.Context)
	}
	if h.Specific.Reason != "푸시 전에 확인한다." {
		t.Errorf("사유 = %q", h.Specific.Reason)
	}
}

func TestSpokenGrant(t *testing.T) {
	e := newEnv(t)
	ask := func(sid string) *hookOut { return e.bash(sid, "git push origin main") }

	// 허용이 없으면 확인을 요구한다. 확인을 요구한 항목은 넣은 것으로 치지 않아 다음에도 묻는다.
	for range 2 {
		if h := ask("g1"); h == nil || h.Specific.Perm != "ask" || !strings.Contains(h.Specific.Reason, "푸시 전에 확인한다") {
			t.Fatalf("허용이 없으면 확인: %+v", h)
		}
	}
	if s := session.Load(e.home, "g1"); slices.Contains(s.Shown[""], "push-confirm") {
		t.Errorf("확인을 요구한 항목이 넣은 것으로 적힘: %v", s.Shown)
	}
	// 말로 한 허용: 확인 없이 진행한다.
	e.prompt("g1", "푸시해 줘")
	if h := ask("g1"); h != nil && h.Specific.Perm != "" {
		t.Errorf("허용이 있으면 확인하지 않음: %+v", h)
	}
	// 도구가 만든 프롬프트가 끼어도 허용이 남고, 그 글로는 아무것도 만들지 않는다.
	if h := e.prompt("g1", "  <task-notification>푸시하지 마 커밋해 줘 푸시해 줘</task-notification>"); h != nil {
		t.Errorf("도구가 만든 프롬프트는 출력이 없어야 함: %+v", h)
	}
	if h := ask("g1"); h != nil && h.Specific.Perm != "" {
		t.Errorf("도구가 만든 프롬프트 뒤에도 허용이 남아야 함: %+v", h)
	}
	// 다음 사용자 프롬프트가 오면 turn 허용이 사라진다.
	e.prompt("g1", "상태를 알려 줘")
	if h := ask("g1"); h == nil || h.Specific.Perm != "ask" {
		t.Errorf("다음 턴에는 허용이 없어야 함: %+v", h)
	}

	// session_grant 표현이 있으면 다음 프롬프트 뒤에도 남는다.
	e.prompt("g2", "이번 세션 동안 푸시해 줘")
	e.prompt("g2", "상태를 알려 줘")
	if h := ask("g2"); h != nil && h.Specific.Perm != "" {
		t.Errorf("세션 허용은 남아야 함: %+v", h)
	}
	// 푸시하지 말라고 하면 세션 허용도 사라진다.
	e.prompt("g2", "푸시하지 마")
	if h := ask("g2"); h == nil || h.Specific.Perm != "ask" {
		t.Errorf("금지 뒤에는 허용이 없어야 함: %+v", h)
	}
	// 질문은 시킨 것이 아니라 허용을 만들지 않는다.
	e.prompt("g3", "푸시해도 돼?")
	if h := ask("g3"); h == nil || h.Specific.Perm != "ask" {
		t.Errorf("질문은 허용이 아님: %+v", h)
	}
	// 푸시를 시켰어도 다른 세션에는 통하지 않는다.
	if h := ask("g4"); h == nil || h.Specific.Perm != "ask" {
		t.Errorf("다른 세션: %+v", h)
	}
}

func TestGrantInheritanceAndAlways(t *testing.T) {
	e := newEnv(t)
	// 상위 동작의 허용은 하위 동작에도 통한다: git.push 허용 → git.push.force.
	e.prompt("h1", "푸시해 줘")
	if h := e.bash("h1", "git push --force origin main"); h != nil && h.Specific.Perm != "" {
		t.Errorf("git.push 허용이 git.push.force 에 통해야 함: %+v", h)
	}
	// always 는 허용이 있어도 확인한다.
	e.prompt("h3", "이번 세션 동안 PR 올려 줘")
	h := e.bash("h3", "gh pr create --title x")
	if h == nil || h.Specific.Perm != "ask" || !strings.Contains(h.Specific.Reason, "PR 은 항상 확인한다") {
		t.Errorf("always 는 허용이 있어도 확인: %+v", h)
	}
	// 프롬프트가 이미 글을 줬어도(중복) 도구 순간에는 다시 확인한다.
	if h := e.bash("h3", "gh pr create --title y"); h == nil || h.Specific.Perm != "ask" || h.Specific.Context != "" {
		t.Errorf("다음에도 확인하되 글은 다시 넣지 않음: %+v", h)
	}
}

func TestPromptDelivery(t *testing.T) {
	e := newEnv(t)
	// 프롬프트가 시킨 동작에 조건이 걸린 항목을 미리 준다. 확인 항목도 글은 나간다.
	h := e.prompt("p1", "커밋해 줘")
	wantContext(t, h, "UserPromptSubmit", "쓰기 전에 상태를 본다")
	// 미리 준 것은 도구 순간에 다시 나오지 않는다.
	if h := e.bash("p1", "git commit -m x"); h != nil {
		t.Errorf("미리 준 항목이 다시 나옴: %+v", h)
	}
	// 시키지 않은 프롬프트에는 출력이 없다.
	if h := e.prompt("p2", "안녕하세요"); h != nil {
		t.Errorf("%+v", h)
	}
}

func TestDelegation(t *testing.T) {
	e := newEnv(t)
	sub := func(sid, agentType, agentID string) *hookOut {
		return e.call(map[string]any{"hook_event_name": "SubagentStart", "session_id": sid, "cwd": e.app(), "agent_id": agentID, "agent_type": agentType})
	}
	// 위임 글이 없으면 글 없이 선별한다.
	if h := sub("d0", "Explore", "x1"); h != nil {
		t.Errorf("위임 글이 없으면 항목이 없음: %+v", h)
	}
	// Agent 도구의 PreToolUse 가 위임 글을 적어 두고, SubagentStart 가 꺼내 그 글로 선별한다.
	e.tool("d1", "Agent", map[string]any{"subagent_type": "Explore", "prompt": "배포 점검 zeta-token 을 조사해"})
	s := session.Load(e.home, "d1")
	if len(s.Pending) != 1 || s.Pending[0].Agent != "Explore" {
		t.Fatalf("기다리는 위임: %+v", s.Pending)
	}
	// 종류가 다른 시작은 꺼내지 않는다.
	if h := sub("d1", "Plan", "x2"); h != nil {
		t.Errorf("종류가 다르면 위임 글이 없어 항목이 없음: %+v", h)
	}
	if len(session.Load(e.home, "d1").Pending) != 1 {
		t.Error("종류가 다른 시작이 위임을 꺼냄")
	}
	h := sub("d1", "Explore", "x3")
	wantContext(t, h, "SubagentStart", "배포는 점검표를 따른다")
	if len(session.Load(e.home, "d1").Pending) != 0 {
		t.Error("꺼낸 위임이 남음")
	}
	// 서브에이전트는 agent_id 별로 따로 센다: 같은 항목이 다른 서브에이전트에게는 다시 나온다.
	e.tool("d1", "Task", map[string]any{"subagent_type": "Explore", "prompt": "배포 점검 zeta-token 다시"})
	wantContext(t, sub("d1", "Explore", "x4"), "SubagentStart", "배포는 점검표를 따른다")
	// subagent_type 이 없으면 general-purpose 로 적는다.
	e.tool("d2", "Agent", map[string]any{"prompt": "zeta-token 배포 점검"})
	wantContext(t, sub("d2", "general-purpose", "x5"), "SubagentStart", "배포는 점검표")
}

func TestFailuresStaySilent(t *testing.T) {
	e := newEnv(t)
	check := func(name, in string, home string) {
		t.Helper()
		var out bytes.Buffer
		if code := Run(strings.NewReader(in), &out, home); code != 0 || out.Len() != 0 {
			t.Errorf("%s: 코드 %d, 출력 %q", name, code, out.String())
		}
	}
	check("깨진 입력", "{not json", e.home)
	check("빈 입력", "", e.home)
	check("배열 입력", "[1]", e.home)
	missing := filepath.Join(t.TempDir(), "no", "such", "home")
	check("없는 HEKA_HOME", `{"hook_event_name":"SessionStart","session_id":"x","cwd":"/a","source":"startup"}`, missing)
	check("없는 HEKA_HOME 프롬프트", `{"hook_event_name":"UserPromptSubmit","session_id":"x","prompt":"푸시해 줘"}`, missing)
	// 깨진 설정 파일.
	e.write("config.yaml", "scopes: [")
	check("깨진 설정", `{"hook_event_name":"SessionStart","session_id":"x","cwd":"/a","source":"startup"}`, e.home)
	b, err := os.ReadFile(filepath.Join(e.home, "ledger", "errors.log"))
	if err != nil || !strings.Contains(string(b), "SessionStart") {
		t.Errorf("errors.log 에 한 줄 남아야 함: %q %v", b, err)
	}
	// session_id 가 파일 이름으로 위험한 글자여도 안전하다.
	e.writeConfig(strings.ReplaceAll(cfgTmpl, "JUDGE", "/bin/true"))
	check("경로 글자", `{"hook_event_name":"UserPromptSubmit","session_id":"../../x/y","prompt":"안녕"}`, e.home)
	if _, err := os.Stat(filepath.Join(e.home, "sessions")); err != nil {
		t.Errorf("세션 폴더: %v", err)
	}
	if fs, _ := filepath.Glob(filepath.Join(e.home, "..", "*.json")); len(fs) != 0 {
		t.Errorf("세션 폴더 밖에 파일이 생김: %v", fs)
	}
}

func TestJudgeErrorStaysSilent(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(strings.ReplaceAll(cfgTmpl, "JUDGE", "/nonexistent/judge-cmd"))
	e.tool("j", "Agent", map[string]any{"subagent_type": "Explore", "prompt": "배포 점검 zeta-token"})
	code, out := e.hook(map[string]any{"hook_event_name": "SubagentStart", "session_id": "j", "cwd": e.app(), "agent_id": "x", "agent_type": "Explore"})
	if code != 0 || out != "" {
		t.Errorf("판정 명령 오류: %d %q", code, out)
	}
	found := false
	for _, r := range e.ledgerRows() {
		for _, d := range r["dropped"].([]any) {
			if m := d.(map[string]any); m["stage"] == "판정" && strings.Contains(m["reason"].(string), "판정 오류") {
				found = true
			}
		}
	}
	if !found {
		t.Error("원장에 판정 오류 탈락이 남아야 함")
	}
}

func TestTimeLimit(t *testing.T) {
	e := newEnv(t)
	slow := filepath.Join(t.TempDir(), "slow.sh")
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nsleep 3\necho '{\"inject\": true}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.writeConfig(strings.ReplaceAll(strings.ReplaceAll(cfgTmpl, "JUDGE", slow), "timeout: 5s", "timeout: 20s"))
	e.tool("t", "Agent", map[string]any{"subagent_type": "Explore", "prompt": "배포 점검"})
	old := limitLong
	limitLong = 300 * time.Millisecond
	defer func() { limitLong = old }()
	start := time.Now()
	code, out := e.hook(map[string]any{"hook_event_name": "SubagentStart", "session_id": "t", "cwd": e.app(), "agent_id": "x", "agent_type": "Explore"})
	if code != 0 || out != "" || time.Since(start) > 2*time.Second {
		t.Errorf("시간 상한: %d %q %v", code, out, time.Since(start))
	}
	if s := session.Load(e.home, "t"); len(s.Shown["x"]) != 0 {
		t.Errorf("시간이 지난 처리가 넣은 것으로 적음: %v", s.Shown)
	}
}

func TestConcurrentHooks(t *testing.T) {
	e := newEnv(t)
	// 항목마다 다른 동작을 두고 병렬 도구 호출을 흉내 낸다.
	cmds := []string{"git commit -m a", "npm run codegen x", "git push", "gh pr create"}
	var wg sync.WaitGroup
	for i := range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.bash("c", cmds[i%len(cmds)])
		}()
	}
	wg.Wait()
	b, err := os.ReadFile(session.Path(e.home, "c"))
	if err != nil {
		t.Fatal(err)
	}
	var s session.State
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("세션 파일이 깨짐: %v\n%s", err, b)
	}
	got := map[string]int{}
	for _, id := range s.Shown[""] {
		got[id]++
	}
	// push-confirm 과 pr-always 는 확인을 요구하므로 적히지 않고, 나머지는 정확히 한 번씩 적힌다.
	for _, id := range []string{"write-info", "gen-app"} {
		if got[id] != 1 {
			t.Errorf("%s 가 %d 번 적힘: %v", id, got[id], s.Shown)
		}
	}
	if len(got) != 2 {
		t.Errorf("넣은 항목 = %v", got)
	}
	if fs, _ := filepath.Glob(filepath.Join(e.home, "sessions", "*.tmp")); len(fs) != 0 {
		t.Errorf("임시 파일이 남음: %v", fs)
	}
	if _, err := os.Stat(session.Path(e.home, "c") + ".lock"); err == nil {
		t.Error("잠금이 남음")
	}
}

func TestLedger(t *testing.T) {
	e := newEnv(t)
	e.start("l", "startup")
	e.prompt("l", "커밋해 줘 그리고 푸시하지 마")
	e.bash("l", "git push")
	e.prompt("l", "<agent-message>x</agent-message>")
	rows := e.ledgerRows()
	if len(rows) != 4 {
		t.Fatalf("원장 %d 줄", len(rows))
	}
	for _, r := range rows {
		for _, k := range []string{"ts", "session", "event", "moment", "actions", "injected", "dropped", "asked", "refused", "grants_used", "confirm", "duration_ms"} {
			if _, ok := r[k]; !ok {
				t.Errorf("%v 에 %q 가 없음", r["event"], k)
			}
		}
		if r["session"] != "l" {
			t.Errorf("session = %v", r["session"])
		}
	}
	p := rows[1]
	if p["moment"] != "prompt" || p["prompt"] != "커밋해 줘 그리고 푸시하지 마" || p["asked"].([]any)[0] != "git.commit" || p["refused"].([]any)[0] != "git.push" {
		t.Errorf("프롬프트 줄: %v", p)
	}
	inj := p["injected"].([]any)[0].(map[string]any)
	if inj["id"] != "write-info" || inj["hash"] == "" || inj["why"] == "" {
		t.Errorf("넣은 항목: %v", inj)
	}
	tool := rows[2]
	if tool["moment"] != "tool" || tool["actions"].([]any)[0] != "git.push" || tool["confirm"].([]any)[0] != "push-confirm" {
		t.Errorf("도구 줄: %v", tool)
	}
	if rows[3]["tool_made"] != true || len(rows[3]["injected"].([]any)) != 0 {
		t.Errorf("도구가 만든 프롬프트 줄: %v", rows[3])
	}
	if long := e.prompt("l2", strings.Repeat("가", 300)); long != nil {
		t.Errorf("%v", long)
	}
	rows = e.ledgerRows()
	if got := []rune(rows[len(rows)-1]["prompt"].(string)); len(got) != 200 {
		t.Errorf("프롬프트는 앞 200자만: %d", len(got))
	}
}

func TestSettings(t *testing.T) {
	b, err := Settings()
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"SessionStart": 10, "UserPromptSubmit": 30, "PreToolUse": 10, "SubagentStart": 30}
	if len(s.Hooks) != len(want) {
		t.Errorf("hooks = %s", b)
	}
	for ev, to := range want {
		h := s.Hooks[ev]
		if len(h) != 1 || len(h[0].Hooks) != 1 || h[0].Hooks[0].Command != "heka hook" || h[0].Hooks[0].Type != "command" || h[0].Hooks[0].Timeout != to {
			t.Errorf("%s: %+v", ev, h)
		}
	}
}
