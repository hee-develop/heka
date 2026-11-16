package claudecode

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/hee-develop/heka/internal/session"
)

// 결과물 검사 시험용 항목이다.
func checkItems() map[string]string {
	return map[string]string{
		"commit-fmt": "text: 커밋 메시지 첫 줄은 50자 이내로 쓴다.\nmust: true\nwhen: {action: git.commit}\ncheck: {target: commit_message, pattern: '^.{1,50}(\\n|$)', mode: block}",
		"commit-log": "text: 커밋 명령에 우회 옵션을 쓰지 않는다.\nwhen: {action: git.commit}\ncheck: {target: command, not_pattern: '--no-verify', mode: log}",
		"commit-ref": "text: 커밋 전에 상태를 본다.\nmust: true\nwhen: {action: git.commit}",
		"pr-fmt":     "text: PR 본문에는 요약 절을 둔다.\nmust: true\nwhen: {action: pr.create}\ncheck: {target: pr_body, pattern: '## 요약', mode: block}",
		"other-fmt":  "text: 다른 저장소의 커밋 규칙.\nmust: true\nwhen: {action: git.commit, repo: 'other-*'}\nscope: [repo/other]\ncheck: {target: commit_message, pattern: '^zzz', mode: block}",
		"stop-a":     "text: 응답에 aaa 를 쓰지 않는다.\ncheck: {target: last_message, not_pattern: 'aaa', mode: block}",
		"stop-b":     "text: 응답에 bbb 를 쓰지 않는다.\ncheck: {target: last_message, not_pattern: 'bbb', mode: block}",
		"stop-log":   "text: 응답에 ccc 를 쓰지 않는다.\ncheck: {target: last_message, not_pattern: 'ccc', mode: log}",
	}
}

func (e *testEnv) checkEnv() {
	e.onlyItems(checkItems(), "")
}

func commitCmd(first string) string { return `git commit -m "` + first + `"` }

var longTitle = strings.Repeat("가", 51)

// lastRow 는 원장의 마지막 줄이다.
func (e *testEnv) lastRow() map[string]any {
	e.t.Helper()
	rows := e.ledgerRows()
	if len(rows) == 0 {
		e.t.Fatal("원장이 비어 있음")
	}
	return rows[len(rows)-1]
}

// checkOf 는 원장 줄의 검사 결과에서 그 항목의 것을 찾는다. 없으면 nil 이다.
func checkOf(row map[string]any, id string) map[string]any {
	cs, _ := row["checks"].([]any)
	for _, c := range cs {
		if m := c.(map[string]any); m["id"] == id {
			return m
		}
	}
	return nil
}

func TestCheckBlocksWithReason(t *testing.T) {
	e := newEnv(t)
	e.checkEnv()
	h := e.bash("c1", commitCmd(longTitle))
	if h == nil || h.Specific.Perm != "deny" || h.Specific.Event != "PreToolUse" {
		t.Fatalf("deny 여야 함: %+v", h)
	}
	r := h.Specific.Reason
	for _, w := range []string{"커밋 메시지 첫 줄은 50자 이내로 쓴다.", "커밋 메시지", "pattern", "commit-fmt"} {
		if !strings.Contains(r, w) {
			t.Errorf("사유에 %q 가 없음: %q", w, r)
		}
	}
	if strings.Contains(r, "우회 옵션") {
		t.Errorf("log 검사는 사유에 없어야 함: %q", r)
	}
	// 같은 호출에 넣을 다른 항목은 additionalContext 로, 막은 항목의 글은 사유에만 있다.
	if !strings.Contains(h.Specific.Context, "커밋 전에 상태를 본다") || strings.Contains(h.Specific.Context, "50자") {
		t.Errorf("additionalContext = %q", h.Specific.Context)
	}
	row := e.lastRow()
	c := checkOf(row, "commit-fmt")
	if c == nil || c["target"] != "commit_message" || c["result"] != "fail" || c["blocked"] != true {
		t.Errorf("검사 기록: %v", row["checks"])
	}
	if c := checkOf(row, "commit-log"); c == nil || c["result"] != "pass" || c["blocked"] != false {
		t.Errorf("log 검사 기록: %v", row["checks"])
	}
	// 고쳐서 다시 시도하면 통과한다. 사유로 이미 전한 글은 다시 넣지 않는다.
	h = e.bash("c1", commitCmd("짧은 제목"))
	if h != nil {
		t.Errorf("통과해야 함: %+v", h)
	}
	if c := checkOf(e.lastRow(), "commit-fmt"); c == nil || c["result"] != "pass" {
		t.Errorf("통과 기록: %v", e.lastRow()["checks"])
	}
}

func TestCheckPassPatternAndMultipleReasons(t *testing.T) {
	e := newEnv(t)
	e.checkEnv()
	e.onlyItems(map[string]string{
		"c-a": "text: 규칙 가.\nwhen: {action: git.commit}\ncheck: {target: commit_message, pattern: '^feat', mode: block}",
		"c-b": "text: 규칙 나.\nwhen: {action: git.commit}\ncheck: {target: command, not_pattern: 'wip', mode: block}",
	}, "")
	h := e.bash("c2", `git commit -m "wip 작업"`)
	if h == nil || h.Specific.Perm != "deny" {
		t.Fatalf("deny 여야 함: %+v", h)
	}
	lines := strings.Split(h.Specific.Reason, "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "규칙 가.") || !strings.Contains(lines[1], "규칙 나.") {
		t.Errorf("사유는 항목마다 한 줄: %q", h.Specific.Reason)
	}
	if h.Specific.Context != "" {
		t.Errorf("넣을 다른 항목이 없으면 additionalContext 도 없음: %q", h.Specific.Context)
	}
	if h := e.bash("c2", `git commit -m "feat: 새 기능"`); h != nil {
		t.Errorf("둘 다 통과해야 함: %+v", h)
	}
}

func TestCheckRunsEvenIfAlreadyInjected(t *testing.T) {
	e := newEnv(t)
	e.checkEnv()
	h := e.bash("c3", commitCmd("짧은 제목"))
	if h == nil || !strings.Contains(h.Specific.Context, "50자") {
		t.Fatalf("처음에는 글을 넣어야 함: %+v", h)
	}
	if h := e.bash("c3", commitCmd("짧은 제목 둘")); h != nil {
		t.Errorf("이미 넣었으니 출력이 없어야 함: %+v", h)
	}
	// 이미 넣은 항목이어도 검사는 매번 한다.
	h = e.bash("c3", commitCmd(longTitle))
	if h == nil || h.Specific.Perm != "deny" || !strings.Contains(h.Specific.Reason, "50자") {
		t.Errorf("이미 넣은 항목도 검사해야 함: %+v", h)
	}
}

func TestCheckLogModeOnlyRecords(t *testing.T) {
	e := newEnv(t)
	e.onlyItems(map[string]string{"only-log": checkItems()["commit-log"]}, "")
	// 첫 호출은 글을 넣을 수 있다. 막지는 않는다.
	if h := e.bash("c4", `git commit --no-verify -m x`); h != nil && h.Specific.Perm != "" {
		t.Errorf("log 는 막지 않음: %+v", h)
	}
	row := e.lastRow()
	if c := checkOf(row, "only-log"); c == nil || c["result"] != "fail" || c["blocked"] != false || c["target"] != "command" {
		t.Errorf("log 기록: %v", row["checks"])
	}
	// 두 번째 호출은 넣을 글도 없으므로 출력이 아예 없다.
	code, out := e.hook(map[string]any{"hook_event_name": "PreToolUse", "session_id": "c4", "cwd": e.app(), "tool_name": "Bash", "tool_input": map[string]any{"command": "git commit --no-verify -m y"}})
	if code != 0 || out != "" {
		t.Errorf("log 는 출력 없음: %d %q", code, out)
	}
	if c := checkOf(e.lastRow(), "only-log"); c == nil || c["result"] != "fail" {
		t.Errorf("두 번째도 원장에 남아야 함: %v", e.lastRow())
	}
}

func TestCheckSkipsWhenNothingExtracted(t *testing.T) {
	e := newEnv(t)
	e.checkEnv()
	h := e.bash("c5", `git commit -F msg.txt`)
	if h != nil && h.Specific.Perm != "" {
		t.Errorf("뽑을 수 없으면 통과: %+v", h)
	}
	row := e.lastRow()
	if c := checkOf(row, "commit-fmt"); c == nil || c["result"] != "skip" || c["blocked"] != false {
		t.Errorf("skip 기록: %v", row["checks"])
	}
	// 도구가 Bash 가 아니면 검사하지 않는다.
	e.tool("c5", "Write", map[string]any{"file_path": e.app() + "/a.txt"})
}

func TestCheckPRBody(t *testing.T) {
	e := newEnv(t)
	e.checkEnv()
	h := e.bash("c6", `gh pr create --title t --body "요약 없음"`)
	if h == nil || h.Specific.Perm != "deny" || !strings.Contains(h.Specific.Reason, "PR 본문") {
		t.Fatalf("PR 본문 검사에 걸려야 함: %+v", h)
	}
	if h := e.bash("c6", "gh pr create --title t --body-file - <<'EOF'\n## 요약\n- a\nEOF"); h != nil && h.Specific.Perm != "" {
		t.Errorf("heredoc 본문이 통과해야 함: %+v", h)
	}
}

func TestCheckSkipsItemsWhoseConditionsDoNotMatch(t *testing.T) {
	e := newEnv(t)
	e.checkEnv()
	h := e.bash("c7", commitCmd("zzz 아님"))
	if h != nil && strings.Contains(h.Specific.Reason, "다른 저장소") {
		t.Errorf("repo 조건이 안 맞는 항목은 검사하지 않음: %+v", h)
	}
	if checkOf(e.lastRow(), "other-fmt") != nil {
		t.Errorf("검사 기록이 없어야 함: %v", e.lastRow()["checks"])
	}
	// 동작이 다르면(push) 커밋 검사 대상이 아니다.
	e.bash("c7", "git push")
	for _, row := range e.ledgerRows()[1:] {
		if row["actions"].([]any)[0] == "git.push" && row["checks"] != nil {
			t.Errorf("push 에 검사 기록이 있음: %v", row)
		}
	}
}

// 턴 종료 시험용 도우미다. 출력 원문을 돌려준다.
func (e *testEnv) stop(sid, msg string, active ...bool) (int, string) {
	in := map[string]any{"hook_event_name": "Stop", "session_id": sid, "cwd": e.app(), "last_assistant_message": msg}
	if len(active) > 0 {
		in["stop_hook_active"] = active[0]
	}
	return e.hook(in)
}

func (e *testEnv) wantBlock(sid, msg string, active ...bool) string {
	e.t.Helper()
	code, out := e.stop(sid, msg, active...)
	var o struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &o) != nil || o.Decision != "block" || o.Reason == "" || !strings.HasSuffix(out, "\n") || strings.Contains(out, `\u`) {
		e.t.Fatalf("block 이어야 함(%q): %d %q", msg, code, out)
	}
	return o.Reason
}

func (e *testEnv) wantQuiet(sid, msg string, active ...bool) {
	e.t.Helper()
	if code, out := e.stop(sid, msg, active...); code != 0 || out != "" {
		e.t.Fatalf("출력이 없어야 함(%q): %d %q", msg, code, out)
	}
}

func TestStopBlocksWithReason(t *testing.T) {
	e := newEnv(t)
	e.checkEnv()
	e.wantQuiet("s1", "문제 없는 응답")
	r := e.wantBlock("s1", "여기에 aaa 가 있다")
	for _, w := range []string{"응답에 aaa 를 쓰지 않는다.", "마지막 응답", "not_pattern", "stop-a"} {
		if !strings.Contains(r, w) {
			t.Errorf("사유에 %q 가 없음: %q", w, r)
		}
	}
	if strings.Contains(r, "bbb") {
		t.Errorf("걸리지 않은 항목은 사유에 없어야 함: %q", r)
	}
	row := e.lastRow()
	if row["event"] != "Stop" || row["moment"] != "stop" {
		t.Errorf("줄: %v", row)
	}
	if c := checkOf(row, "stop-a"); c == nil || c["target"] != "last_message" || c["result"] != "fail" || c["blocked"] != true {
		t.Errorf("검사 기록: %v", row["checks"])
	}
	if c := checkOf(row, "stop-b"); c == nil || c["result"] != "pass" {
		t.Errorf("검사 기록: %v", row["checks"])
	}
	// 둘이 함께 걸리면 사유가 한 번에 나온다.
	r = e.wantBlock("s2", "aaa 와 bbb")
	if len(strings.Split(r, "\n")) != 2 {
		t.Errorf("항목마다 한 줄: %q", r)
	}
	// 응답이 비었으면 skip 이다.
	e.wantQuiet("s3", "")
	if c := checkOf(e.lastRow(), "stop-a"); c == nil || c["result"] != "skip" {
		t.Errorf("빈 응답: %v", e.lastRow()["checks"])
	}
}

func TestStopLimitsPerSession(t *testing.T) {
	e := newEnv(t)
	e.checkEnv()
	// 항목이 번갈아 걸리면 같은 항목의 연속 한도에는 안 걸리고 세션당 3회에서 멈춘다.
	e.wantBlock("lim", "aaa", true) // stop_hook_active 여도 한도 안이면 막는다
	e.wantBlock("lim", "bbb", true)
	e.wantBlock("lim", "aaa", true)
	e.wantQuiet("lim", "bbb", true)
	e.wantQuiet("lim", "aaa")
	row := e.lastRow()
	if c := checkOf(row, "stop-a"); c["result"] != "fail" || c["blocked"] != false {
		t.Errorf("한도 뒤에도 원장에는 남아야 함: %v", row["checks"])
	}
	if !strings.Contains(row["reason"].(string), "한도") {
		t.Errorf("한도로 넘어갔다는 사유가 없음: %v", row)
	}
	if st := session.Load(e.home, "lim"); st.StopBlocks != 3 {
		t.Errorf("StopBlocks = %d", st.StopBlocks)
	}
	// 다른 세션은 따로 센다.
	e.wantBlock("lim2", "aaa")
}

func TestStopSameItemTwiceInARow(t *testing.T) {
	e := newEnv(t)
	e.checkEnv()
	e.wantBlock("rep", "aaa")
	e.wantBlock("rep", "aaa", true)
	e.wantQuiet("rep", "aaa", true) // 같은 항목이 세 번째 연속이면 통과
	// 연속이 끊겼으니(직전 턴 종료는 되돌리지 않았다) 다시 두 번까지 막을 수 있으나 세션 한도 3회가 먼저 닫는다.
	e.wantBlock("rep", "aaa")
	e.wantQuiet("rep", "aaa")
}

func TestStopLogModeAndConditions(t *testing.T) {
	e := newEnv(t)
	e.checkEnv()
	e.wantQuiet("lg", "여기에 ccc 가 있다")
	row := e.lastRow()
	if c := checkOf(row, "stop-log"); c == nil || c["result"] != "fail" || c["blocked"] != false {
		t.Errorf("log 기록: %v", row["checks"])
	}
	// when 에 repo·agent 조건이 있으면 맞아야 한다. action·path 조건은 보지 않는다.
	e.onlyItems(map[string]string{
		"st-repo":  "text: 저장소 규칙.\nwhen: {repo: 'other-*'}\nscope: [repo/other]\ncheck: {target: last_message, not_pattern: 'x', mode: block}",
		"st-agent": "text: 에이전트 규칙.\nwhen: {agent: reviewer}\ncheck: {target: last_message, not_pattern: 'x', mode: block}",
		"st-act":   "text: 동작 조건은 보지 않음.\nwhen: {action: git.commit, path: 'nowhere/*'}\ncheck: {target: last_message, not_pattern: 'x', mode: block}",
	}, "")
	r := e.wantBlock("lg2", "x")
	if !strings.Contains(r, "동작 조건은 보지 않음") || strings.Contains(r, "저장소 규칙") || strings.Contains(r, "에이전트 규칙") {
		t.Errorf("조건 처리: %q", r)
	}
	code, out := e.hook(map[string]any{"hook_event_name": "Stop", "session_id": "lg3", "cwd": e.app(), "last_assistant_message": "x", "agent_type": "reviewer"})
	if code != 0 || !strings.Contains(out, "에이전트 규칙") {
		t.Errorf("agent 조건이 맞으면 검사: %q", out)
	}
}

func TestStopWithoutChecksIsSilent(t *testing.T) {
	e := newEnv(t)
	e.wantQuiet("none", "아무 말")
	if rows := e.ledgerRows(); len(rows) != 0 {
		t.Errorf("검사할 것이 없으면 원장에도 남기지 않음: %v", rows)
	}
}

// 그림자 방식 시험이다.

func shadowEnv(t *testing.T) *testEnv {
	t.Helper()
	t.Setenv("HEKA_SHADOW", "1")
	e := newEnv(t)
	e.onlyItems(checkItems(), "")
	return e
}

func TestShadowPrintsNothingButRecordsEverything(t *testing.T) {
	e := shadowEnv(t)
	// 시작
	if code, out := e.hook(map[string]any{"hook_event_name": "SessionStart", "session_id": "sh", "cwd": e.app(), "source": "startup"}); code != 0 || out != "" {
		t.Errorf("SessionStart: %d %q", code, out)
	}
	// 검사에 걸려도 막지 않는다.
	code, out := e.hook(map[string]any{"hook_event_name": "PreToolUse", "session_id": "sh", "cwd": e.app(), "tool_name": "Bash", "tool_input": map[string]any{"command": commitCmd(longTitle)}})
	if code != 0 || out != "" {
		t.Fatalf("그림자는 출력이 없어야 함: %d %q", code, out)
	}
	row := e.lastRow()
	if row["shadow"] != true {
		t.Errorf("shadow: true 가 아님: %v", row)
	}
	var ids []string
	for _, i := range row["injected"].([]any) {
		ids = append(ids, i.(map[string]any)["id"].(string))
	}
	if len(ids) != 3 {
		t.Errorf("평소처럼 넣은 항목을 적어야 함: %v", ids)
	}
	if c := checkOf(row, "commit-fmt"); c == nil || c["result"] != "fail" || c["blocked"] != true {
		t.Errorf("검사 판정은 평소와 같아야 함: %v", row["checks"])
	}
	// 세션 상태가 평소처럼 갱신되어 두 번째 호출에서는 중복으로 빠진다.
	if seen := session.Load(e.home, "sh").Seen(""); !seen["commit-fmt"] || !seen["commit-ref"] {
		t.Errorf("이미 넣은 항목: %v", seen)
	}
	code, out = e.hook(map[string]any{"hook_event_name": "PreToolUse", "session_id": "sh", "cwd": e.app(), "tool_name": "Bash", "tool_input": map[string]any{"command": commitCmd("짧다")}})
	if code != 0 || out != "" {
		t.Errorf("%d %q", code, out)
	}
	row = e.lastRow()
	if len(row["injected"].([]any)) != 0 || row["shadow"] != true {
		t.Errorf("두 번째 호출은 넣은 것이 없어야 함: %v", row)
	}
	dup := false
	for _, d := range row["dropped"].([]any) {
		if m := d.(map[string]any); m["id"] == "commit-ref" && m["stage"] == "중복" {
			dup = true
		}
	}
	if !dup {
		t.Errorf("중복으로 빠져야 함: %v", row["dropped"])
	}
}

func TestShadowKeepsGrantsDelegationsAndTranscript(t *testing.T) {
	e := shadowEnv(t)
	e.onlyItems(map[string]string{
		"commit-ask": "text: 커밋은 확인한다.\nmust: true\nconfirm: true\nwhen: {action: git.commit}",
		"deleg-rule": "text: 위임 규칙.\nwhen: {agent: reviewer}\nmust: true",
	}, "")
	tp := e.work + "/transcript.jsonl"
	code, out := e.hook(map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": "sg", "cwd": e.app(), "prompt": "커밋해 줘", "transcript_path": tp})
	if code != 0 || out != "" {
		t.Fatalf("%d %q", code, out)
	}
	st := session.Load(e.home, "sg")
	if st.Grants["git.commit"] != session.Turn || st.Transcript != tp {
		t.Errorf("허용과 기록 파일 경로가 평소처럼 적혀야 함: %+v", st)
	}
	// 확인 요구도 내지 않는다. 허용이 있어 통과한 것은 grants_used 에 남는다.
	code, out = e.hook(map[string]any{"hook_event_name": "PreToolUse", "session_id": "sg", "cwd": e.app(), "tool_name": "Bash", "tool_input": map[string]any{"command": "git commit -m x"}})
	if code != 0 || out != "" {
		t.Errorf("%d %q", code, out)
	}
	if g := e.lastRow()["grants_used"].([]any); len(g) != 1 || g[0] != "git.commit" {
		t.Errorf("grants_used: %v", e.lastRow())
	}
	// 허용이 없으면 확인 요구가 났을 상황이다. 출력은 없고 원장에 남는다.
	code, out = e.hook(map[string]any{"hook_event_name": "PreToolUse", "session_id": "sg2", "cwd": e.app(), "tool_name": "Bash", "tool_input": map[string]any{"command": "git commit -m x"}})
	if code != 0 || out != "" {
		t.Errorf("확인 요구를 내면 안 됨: %d %q", code, out)
	}
	if c := e.lastRow()["confirm"].([]any); len(c) != 1 || c[0] != "commit-ask" {
		t.Errorf("confirm: %v", e.lastRow())
	}
	// 기다리는 위임
	e.hook(map[string]any{"hook_event_name": "PreToolUse", "session_id": "sg3", "cwd": e.app(), "tool_name": "Agent", "tool_input": map[string]any{"prompt": "작업을 해 줘", "subagent_type": "reviewer"}})
	if p := session.Load(e.home, "sg3").Pending; len(p) != 1 || p[0].Agent != "reviewer" {
		t.Errorf("기다리는 위임: %+v", p)
	}
}

func TestShadowStopRecordsButNeverBlocks(t *testing.T) {
	e := shadowEnv(t)
	e.wantQuiet("shs", "aaa")
	row := e.lastRow()
	if row["shadow"] != true || row["event"] != "Stop" {
		t.Errorf("줄: %v", row)
	}
	if c := checkOf(row, "stop-a"); c == nil || c["result"] != "fail" || c["blocked"] != true {
		t.Errorf("판정은 평소와 같아야 함: %v", row["checks"])
	}
	if st := session.Load(e.home, "shs"); st.StopBlocks != 1 {
		t.Errorf("횟수도 평소처럼 적힘: %d", st.StopBlocks)
	}
}

func TestShadowByConfig(t *testing.T) {
	e := newEnv(t)
	e.onlyItems(checkItems(), "")
	cfg, err := os.ReadFile(e.home + "/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	e.write("config.yaml", strings.Replace(string(cfg), "  scopes: [tool/claude-code]", "  scopes: [tool/claude-code]\n  shadow: true", 1))
	e.wantQuiet("cfg", "aaa")
	if e.lastRow()["shadow"] != true {
		t.Errorf("설정으로도 켜져야 함: %v", e.lastRow())
	}
	// 꺼져 있으면 평소처럼 막고 shadow 필드가 없다.
	e.write("config.yaml", string(cfg))
	e.wantBlock("cfg2", "aaa")
	if _, ok := e.lastRow()["shadow"]; ok {
		t.Errorf("shadow 필드가 없어야 함: %v", e.lastRow())
	}
}

func TestShadowStillSilentOnFailure(t *testing.T) {
	e := shadowEnv(t)
	var out bytes.Buffer
	if code := Run(strings.NewReader("{깨진"), &out, e.home); code != 0 || out.Len() != 0 {
		t.Errorf("%d %q", code, out.String())
	}
}

func TestSettingsShape(t *testing.T) {
	type hook struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
		Async   bool   `json:"async"`
	}
	read := func(shadow bool) map[string][]hook {
		b, err := Settings(shadow)
		if err != nil {
			t.Fatal(err)
		}
		var s struct {
			Hooks map[string][]struct {
				Hooks []hook `json:"hooks"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatal(err)
		}
		out := map[string][]hook{}
		for ev, es := range s.Hooks {
			for _, e := range es {
				out[ev] = append(out[ev], e.Hooks...)
			}
		}
		return out
	}
	normal := read(false)
	if len(normal) != 5 || normal["Stop"][0].Timeout != 10 || normal["Stop"][0].Command != "heka hook" || normal["Stop"][0].Async {
		t.Errorf("평소 설정: %+v", normal)
	}
	sh := read(true)
	if len(sh) != 5 {
		t.Errorf("그림자 설정: %+v", sh)
	}
	for ev, hs := range sh {
		if len(hs) != 1 || hs[0].Command != "HEKA_SHADOW=1 heka hook" || !hs[0].Async || hs[0].Type != "command" {
			t.Errorf("%s: %+v", ev, hs)
		}
	}
	if sh["Stop"] == nil || sh["UserPromptSubmit"][0].Timeout < 60 {
		t.Errorf("그림자 설정의 시간 상한: %+v", sh)
	}
}
