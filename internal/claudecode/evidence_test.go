package claudecode

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/hee-develop/heka/internal/session"
)

// 증거 조건 시험용 항목이다. 단정 표현은 "할 수 없"이고 증거는 --help 를 본 기록이다.
func evidenceItems(mode string) map[string]string {
	ev := "evidence: '(?i)(--help|^WebFetch:|^Agent: claude-code-guide)'"
	return map[string]string{
		"ev-last":  "text: 확인 없이 단정하지 않는다.\ncheck: {target: last_message, not_pattern: '할 수 없', " + ev + ", mode: " + mode + "}",
		"ev-ask":   "text: 질문에도 확인 없이 단정하지 않는다.\ncheck: {target: question, not_pattern: '할 수 없', " + ev + ", mode: " + mode + "}",
		"ev-noevi": "text: 증거 없는 검사.\ncheck: {target: last_message, not_pattern: 'zzz단정', mode: log}",
		"ev-cmd":   "text: 커밋에 우회 옵션을 쓰지 않는다.\nwhen: {action: git.commit}\ncheck: {target: command, not_pattern: 'no-verify', evidence: 'no-verify|^Read: /ok', mode: " + mode + "}",
	}
}

func (e *testEnv) evEnv(mode string) { e.onlyItems(evidenceItems(mode), "") }

const claim = "gh CLI 로는 이미지를 업로드할 수 없습니다."

func (e *testEnv) askQuestion(sid, q string, extra ...any) *hookOut {
	in := map[string]any{"questions": []any{map[string]any{"question": q, "options": []any{map[string]any{"label": "가", "description": "설명"}}}}}
	return e.tool(sid, "AskUserQuestion", in, extra...)
}

// stopRow 는 턴 종료를 처리하고 그 항목의 검사 기록을 돌려준다.
func (e *testEnv) stopCheck(sid, msg, id string) map[string]any {
	e.t.Helper()
	e.stop(sid, msg)
	c := checkOf(e.lastRow(), id)
	if c == nil {
		e.t.Fatalf("%s 검사 기록이 없음: %v", id, e.lastRow())
	}
	return c
}

func TestEvidenceTurnsFailIntoPass(t *testing.T) {
	e := newEnv(t)
	e.evEnv("log")
	// (a) 증거 없이 단정: Fail. log 라 출력은 없다.
	e.prompt("s1", "이미지를 올려 줘")
	if c := e.stopCheck("s1", claim, "ev-last"); c["result"] != "fail" || c["blocked"] != false {
		t.Errorf("증거가 없으면 fail(log): %v", c)
	}
	// (b) 같은 턴에 --help 를 봤으면 Pass 와 증거 기록.
	e.prompt("s2", "이미지를 올려 줘")
	e.bash("s2", "gh pr edit --help")
	c := e.stopCheck("s2", claim, "ev-last")
	if c["result"] != "pass" || c["note"] != "증거: Bash: gh pr edit --help" {
		t.Errorf("증거가 있으면 pass: %v", c)
	}
	// (c) 단정 표현이 없으면 Pass, 증거 기록 없음.
	if c := e.stopCheck("s1", "아래처럼 하면 됩니다.", "ev-last"); c["result"] != "pass" || c["note"] != nil {
		t.Errorf("단정이 없으면 그냥 pass: %v", c)
	}
	// evidence 가 없는 항목은 기록과 상관없이 그대로 판정한다.
	if c := e.stopCheck("s2", "zzz단정", "ev-noevi"); c["result"] != "fail" {
		t.Errorf("evidence 없는 항목: %v", c)
	}
}

func TestEvidenceOtherToolsAndBlockMode(t *testing.T) {
	e := newEnv(t)
	e.evEnv("block")
	e.prompt("s1", "x")
	e.wantBlock("s1", claim)
	e.tool("s1", "WebFetch", map[string]any{"url": "https://cli.github.com/manual/gh_pr_edit"})
	e.wantQuiet("s1", claim)
	e.prompt("s2", "x")
	e.tool("s2", "Agent", map[string]any{"subagent_type": "claude-code-guide", "prompt": "gh 가 이미지를 올릴 수 있는지"})
	e.wantQuiet("s2", claim)
	e.prompt("s3", "x")
	e.tool("s3", "Agent", map[string]any{"subagent_type": "Explore", "prompt": "찾아 줘"})
	e.wantBlock("s3", claim)
}

func TestEvidenceDoesNotCarryOverToNextTurn(t *testing.T) {
	e := newEnv(t)
	e.evEnv("log")
	e.prompt("s1", "첫째")
	e.bash("s1", "gh pr edit --help")
	if c := e.stopCheck("s1", claim, "ev-last"); c["result"] != "pass" {
		t.Fatalf("같은 턴: %v", c)
	}
	// (e) 새 사용자 턴이 시작되면 이전 턴의 --help 는 증거가 아니다.
	e.prompt("s1", "둘째")
	if got := session.Load(e.home, "s1").Calls; len(got) != 0 {
		t.Errorf("새 턴에 기록이 비어야 함: %v", got)
	}
	if c := e.stopCheck("s1", claim, "ev-last"); c["result"] != "fail" {
		t.Errorf("이전 턴의 증거는 쓰지 못함: %v", c)
	}
}

func TestEvidenceToolMadePromptKeepsTurn(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(strings.Replace(cfgTmpl, "claude_code:\n", "claude_code:\n  tool_prompt_prefixes: ['<task-notification>']\n", 1))
	e.onlyItems(evidenceItems("log"), "")
	e.prompt("s1", "사용자 턴")
	e.bash("s1", "gh pr edit --help")
	e.prompt("s1", "<task-notification> 백그라운드 작업이 끝났습니다")
	if got := session.Load(e.home, "s1").Calls[""]; len(got) != 1 {
		t.Errorf("도구가 만든 프롬프트는 같은 턴이라 기록이 남아야 함: %v", got)
	}
	if c := e.stopCheck("s1", claim, "ev-last"); c["result"] != "pass" {
		t.Errorf("%v", c)
	}
}

func TestEvidenceCallIsNotItsOwnEvidence(t *testing.T) {
	e := newEnv(t)
	// log 모드: 첫 호출은 자기 자신에 맞는 증거가 없어 fail, 기록된 뒤의 둘째 호출은 첫 호출을 증거로 pass.
	e.evEnv("log")
	e.prompt("s1", "x")
	cmd := commitCmd("a") + " --no-verify"
	e.bash("s1", cmd)
	if c := checkOf(e.lastRow(), "ev-cmd"); c == nil || c["result"] != "fail" {
		t.Fatalf("첫 호출은 자기 자신이 증거가 될 수 없음: %v", e.lastRow()["checks"])
	}
	e.bash("s1", cmd)
	if c := checkOf(e.lastRow(), "ev-cmd"); c == nil || c["result"] != "pass" || !strings.Contains(c["note"].(string), "no-verify") {
		t.Errorf("앞선 호출이 증거: %v", e.lastRow()["checks"])
	}
	// block 모드: 막힌 호출은 실행되지 않았으므로 기록하지 않는다. 몇 번을 시도해도 막힌다.
	e2 := newEnv(t)
	e2.evEnv("block")
	e2.prompt("s1", "x")
	for i := range 2 {
		if h := e2.bash("s1", cmd); h == nil || h.Specific.Perm != "deny" {
			t.Fatalf("%d번째도 막혀야 함: %+v", i, h)
		}
	}
	if got := session.Load(e2.home, "s1").Calls[""]; len(got) != 0 {
		t.Errorf("막힌 호출은 기록하지 않음: %v", got)
	}
	e2.tool("s1", "Read", map[string]any{"file_path": "/ok/x.md"})
	if h := e2.bash("s1", cmd); h != nil && h.Specific.Perm == "deny" {
		t.Errorf("증거(Read: /ok)가 있으면 통과: %+v", h)
	}
}

func TestEvidenceQuestionTarget(t *testing.T) {
	e := newEnv(t)
	e.evEnv("log")
	e.prompt("s1", "x")
	// (d) 질문 글에 단정이 있고 증거가 없음: fail(log). 호출은 막지 않는다.
	if h := e.askQuestion("s1", "이 도구로는 할 수 없는데 어떻게 할까요?"); h != nil {
		t.Errorf("log 는 출력이 없어야 함: %+v", h)
	}
	if c := checkOf(e.lastRow(), "ev-ask"); c == nil || c["target"] != "question" || c["result"] != "fail" || c["blocked"] != false {
		t.Errorf("question fail(log): %v", e.lastRow()["checks"])
	}
	// 단정이 없으면 pass.
	e.askQuestion("s1", "어느 쪽으로 할까요?")
	if c := checkOf(e.lastRow(), "ev-ask"); c == nil || c["result"] != "pass" {
		t.Errorf("%v", e.lastRow()["checks"])
	}
	// 옵션 글의 단정도 잡는다.
	in := map[string]any{"questions": []any{map[string]any{"question": "어떻게 할까요?", "options": []any{map[string]any{"label": "포기", "description": "방법이 있어도 할 수 없다고 답한다"}}}}}
	e.tool("s1", "AskUserQuestion", in)
	if c := checkOf(e.lastRow(), "ev-ask"); c == nil || c["result"] != "fail" {
		t.Errorf("옵션 설명: %v", e.lastRow()["checks"])
	}
	// 증거가 생기면 pass.
	e.bash("s1", "gh --help")
	e.askQuestion("s1", "이 도구로는 할 수 없는데 어떻게 할까요?")
	if c := checkOf(e.lastRow(), "ev-ask"); c == nil || c["result"] != "pass" || c["note"] != "증거: Bash: gh --help" {
		t.Errorf("증거가 있으면 pass: %v", e.lastRow()["checks"])
	}
	// question 항목은 last_message 검사에 섞이지 않는다.
	e.stop("s1", "아무 말")
	if checkOf(e.lastRow(), "ev-ask") != nil {
		t.Error("question 항목은 턴 종료에서 검사하지 않음")
	}
}

func TestEvidenceQuestionBlock(t *testing.T) {
	e := newEnv(t)
	e.evEnv("block")
	e.prompt("s1", "x")
	h := e.askQuestion("s1", "이 도구로는 할 수 없는데 어떻게 할까요?")
	if h == nil || h.Specific.Perm != "deny" || h.Specific.Event != "PreToolUse" || !strings.Contains(h.Specific.Reason, "질문에도 확인 없이 단정하지 않는다") || !strings.Contains(h.Specific.Reason, "질문 글") {
		t.Fatalf("deny 와 사유: %+v", h)
	}
	if got := session.Load(e.home, "s1").Calls[""]; len(got) != 0 {
		t.Errorf("막힌 질문은 기록하지 않음: %v", got)
	}
	e.tool("s1", "WebSearch", map[string]any{"query": "gh --help image upload"})
	if h := e.askQuestion("s1", "이 도구로는 할 수 없는데 어떻게 할까요?"); h != nil {
		t.Errorf("증거가 있으면 통과: %+v", h)
	}
}

func TestEvidenceQuestionItemIgnoredWithoutAskCall(t *testing.T) {
	e := newEnv(t)
	e.evEnv("block")
	e.prompt("s1", "x")
	// 다른 도구 호출에는 question 항목이 걸리지 않는다.
	if h := e.tool("s1", "Read", map[string]any{"file_path": "/a"}); h != nil {
		t.Errorf("%+v", h)
	}
	for _, r := range e.ledgerRows() {
		if r["event"] == "PreToolUse" && checkOf(r, "ev-ask") != nil {
			t.Errorf("AskUserQuestion 이 아닌 호출에서 question 검사: %v", r)
		}
	}
	// AskUserQuestion 에 항목이 없는 환경이면 조용히 지나간다.
	e.onlyItems(map[string]string{"x": "text: 아무 규칙.\nmust: true\nwhen: {action: git.commit}"}, "")
	if h := e.askQuestion("s1", "할 수 없다"); h != nil {
		t.Errorf("%+v", h)
	}
}

func TestEvidenceWithoutSessionState(t *testing.T) {
	e := newEnv(t)
	e.evEnv("block")
	// 프롬프트도 도구 호출도 없이 곧바로 턴 종료: 상태 파일이 없어 기록을 읽지 못한다. R9 로 통과시키고 이유를 남긴다.
	e.wantQuiet("nostate", claim)
	c := checkOf(e.lastRow(), "ev-last")
	if c == nil || c["result"] != "skip" || c["note"] != "턴 기록을 읽지 못함" || c["blocked"] != false {
		t.Errorf("상태 없음 → skip: %v", e.lastRow()["checks"])
	}
	// session_id 가 비어도 마찬가지다.
	e.wantQuiet("", claim)
	if c := checkOf(e.lastRow(), "ev-last"); c == nil || c["result"] != "skip" {
		t.Errorf("session_id 없음 → skip: %v", e.lastRow()["checks"])
	}
	// 깨진 상태 파일.
	e.write("sessions/broken.json", "{broken")
	e.wantQuiet("broken", claim)
	if c := checkOf(e.lastRow(), "ev-last"); c == nil || c["result"] != "skip" {
		t.Errorf("깨진 상태 → skip: %v", e.lastRow()["checks"])
	}
	// 상태가 있고 기록이 비었으면(이번 턴에 도구를 안 썼으면) 읽은 것이므로 fail 이다.
	e.prompt("fresh", "x")
	e.wantBlock("fresh", claim)
	// session_id 가 없는 도구 호출은 기록하지 않고 오류 없이 지나간다.
	if h := e.bash("", "git status"); h != nil && h.Specific.Perm == "deny" {
		t.Errorf("%+v", h)
	}
	if _, ok := session.LoadExisting(e.home, ""); ok {
		t.Error("빈 session_id 로 상태를 만들지 않음")
	}
}

func TestEvidenceMainAndSubagentAreSeparate(t *testing.T) {
	e := newEnv(t)
	e.evEnv("log")
	e.prompt("s1", "x")
	e.bash("s1", "gh --help", "agent_id", "a1", "agent_type", "Explore")
	st := session.Load(e.home, "s1")
	if len(st.Calls["a1"]) != 1 || len(st.Calls[""]) != 0 {
		t.Errorf("서브에이전트 기록은 따로: %+v", st.Calls)
	}
	// 메인의 턴 종료는 서브에이전트의 --help 를 증거로 보지 않는다.
	if c := e.stopCheck("s1", claim, "ev-last"); c["result"] != "fail" {
		t.Errorf("%v", c)
	}
	// 서브에이전트 안의 질문은 그 서브에이전트의 기록을 본다.
	e.askQuestion("s1", "이 도구로는 할 수 없다", "agent_id", "a1", "agent_type", "Explore")
	if c := checkOf(e.lastRow(), "ev-ask"); c == nil || c["result"] != "pass" {
		t.Errorf("%v", e.lastRow()["checks"])
	}
}

func TestEvidenceRecordLimits(t *testing.T) {
	e := newEnv(t)
	e.evEnv("log")
	e.prompt("s1", "x")
	for i := range session.MaxCallLines + 6 {
		e.bash("s1", "echo "+strconv.Itoa(i))
	}
	calls := session.Load(e.home, "s1").Calls[""]
	if len(calls) != session.MaxCallLines || calls[0] != "Bash: echo 6" || calls[len(calls)-1] != "Bash: echo "+strconv.Itoa(session.MaxCallLines+5) {
		t.Errorf("64줄, 오래된 것부터 버림: %d줄 %q ... %q", len(calls), calls[0], calls[len(calls)-1])
	}
	e.bash("s1", "echo "+strings.Repeat("가", 2000))
	calls = session.Load(e.home, "s1").Calls[""]
	if last := []rune(calls[len(calls)-1]); len(last) != session.MaxCallChars || !strings.HasPrefix(string(last), "Bash: echo 가") {
		t.Errorf("한 줄은 1000자: %d", len(last))
	}
	// 오래 전에 밀려난 증거는 쓰지 못한다.
	e.prompt("s2", "x")
	e.bash("s2", "gh --help")
	for i := range session.MaxCallLines {
		e.bash("s2", "echo "+strconv.Itoa(i))
	}
	if c := e.stopCheck("s2", claim, "ev-last"); c["result"] != "fail" {
		t.Errorf("밀려난 증거: %v", c)
	}
}

func TestCallLine(t *testing.T) {
	long := strings.Repeat("가", 300)
	tests := []struct {
		tool string
		in   map[string]any
		want string
	}{
		{"Bash", map[string]any{"command": "gh pr edit --help"}, "Bash: gh pr edit --help"},
		{"Bash", map[string]any{}, "Bash"},
		{"WebFetch", map[string]any{"url": "https://x.dev/a", "prompt": "요약"}, "WebFetch: https://x.dev/a"},
		{"WebSearch", map[string]any{"query": "gh cli upload image"}, "WebSearch: gh cli upload image"},
		{"Agent", map[string]any{"subagent_type": "claude-code-guide", "prompt": "질문"}, "Agent: claude-code-guide 질문"},
		{"Task", map[string]any{"prompt": "질문"}, "Agent: general-purpose 질문"},
		{"Agent", map[string]any{"subagent_type": "Explore", "prompt": long}, "Agent: Explore " + strings.Repeat("가", 200)},
		{"Skill", map[string]any{"skill": "slack:standup"}, "Skill: slack:standup"},
		{"Read", map[string]any{"file_path": "/a/b.go", "limit": 5}, "Read: /a/b.go"},
		{"mcp__heka__heka_find", map[string]any{"query": "비밀"}, "mcp__heka__heka_find"},
		{"Edit", map[string]any{"file_path": "/a"}, "Edit"},
		{"AskUserQuestion", map[string]any{"questions": []any{}}, "AskUserQuestion"},
		{"", nil, ""},
	}
	for _, tt := range tests {
		if got := callLine(tt.tool, tt.in); got != tt.want {
			t.Errorf("callLine(%s) = %q, 기대 %q", tt.tool, got, tt.want)
		}
	}
}

// 훅 출력에 증거 조건이 새로 만드는 출력은 없다: 도구 호출은 조용히 지나가고 상태 파일만 쌓인다.
func TestEvidenceRecordingIsSilent(t *testing.T) {
	e := newEnv(t)
	e.evEnv("log")
	e.prompt("s1", "x")
	code, out := e.hook(map[string]any{"hook_event_name": "PreToolUse", "session_id": "s1", "cwd": e.app(), "tool_name": "Read", "tool_input": map[string]any{"file_path": "/a"}})
	if code != 0 || out != "" {
		t.Errorf("출력이 없어야 함: %d %q", code, out)
	}
	b, _ := json.Marshal(session.Load(e.home, "s1").Calls)
	if string(b) != `{"":["Read: /a"]}` {
		t.Errorf("calls = %s", b)
	}
}
