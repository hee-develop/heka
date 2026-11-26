package claudecode

import (
	"slices"
	"strings"
	"testing"
)

const pauseCfg = "  pause_first_must: true\n"

// pauseEnv 는 pause_first_must 를 켠 환경이다. 필수 항목 둘과 필수가 아닌 항목 하나가 커밋에 걸린다.
func pauseEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newEnv(t)
	e.onlyItems(map[string]string{
		"must-a":   "text: 커밋 전에 상태를 본다.\nmust: true\nwhen: {action: git.commit}",
		"must-b":   "text: 커밋 메시지는 한국어로 쓴다.\nmust: true\nwhen: {action: git.commit}",
		"inform-c": "text: 커밋은 작게 나눈다.\nwhen: {action: git.commit}",
		"fmt":      "text: 커밋 메시지 첫 줄은 50자 이내로 쓴다.\nmust: true\nwhen: {action: git.commit}\ncheck: {target: commit_message, pattern: '^.{1,50}(\\n|$)', mode: block}",
	}, "")
	e.enablePause()
	return e
}

// enablePause 는 설정의 claude_code 절에 pause_first_must 를 더한다.
func (e *testEnv) enablePause() {
	e.t.Helper()
	cfg := strings.ReplaceAll(cfgTmpl, "claude_code:\n  scopes: [tool/claude-code]\n", "claude_code:\n  scopes: [tool/claude-code]\n"+pauseCfg)
	if !strings.Contains(cfg, pauseCfg) {
		e.t.Fatal("설정 틀에 claude_code 절이 없음")
	}
	e.writeConfig(strings.ReplaceAll(cfg, "JUDGE", "true"))
}

func pausedOf(row map[string]any) []string {
	var ids []string
	ps, _ := row["paused"].([]any)
	for _, p := range ps {
		ids = append(ids, p.(string))
	}
	slices.Sort(ids)
	return ids
}

func TestPauseFirstMustDeniesOnceThenPasses(t *testing.T) {
	e := pauseEnv(t)
	h := e.bash("s", commitCmd("짧은 제목"))
	if h == nil || h.Specific.Perm != "deny" {
		t.Fatalf("처음 보인 필수 항목이 있으면 deny 여야 함: %+v", h)
	}
	r := h.Specific.Reason
	for _, w := range []string{"[heka 먼저 읽기]", "커밋 전에 상태를 본다. (항목 must-a)", "커밋 메시지는 한국어로 쓴다. (항목 must-b)", "(항목 fmt)"} {
		if !strings.Contains(r, w) {
			t.Errorf("사유에 %q 가 없음: %q", w, r)
		}
	}
	if strings.Contains(r, "작게 나눈다") {
		t.Errorf("필수가 아닌 항목은 사유에 없어야 함: %q", r)
	}
	if !strings.Contains(h.Specific.Context, "커밋은 작게 나눈다") {
		t.Errorf("필수가 아닌 항목은 additionalContext 로 가야 함: %q", h.Specific.Context)
	}
	if got := pausedOf(e.lastRow()); !slices.Equal(got, []string{"fmt", "must-a", "must-b"}) {
		t.Errorf("원장 paused = %v", got)
	}

	// 같은 호출을 다시 보내면 이미 보인 항목이라 통과한다(출력 없음).
	if h := e.bash("s", commitCmd("짧은 제목")); h != nil {
		t.Errorf("다시 보낸 호출은 통과해야 함: %+v", h)
	}
	if got := pausedOf(e.lastRow()); len(got) != 0 {
		t.Errorf("다시 보낸 호출에는 paused 가 없어야 함: %v", got)
	}

	// 서브에이전트는 따로 센다.
	if h := e.bash("s", commitCmd("짧은 제목"), "agent_id", "a1", "agent_type", "Explore"); h == nil || h.Specific.Perm != "deny" {
		t.Errorf("서브에이전트에서도 처음에는 되돌려야 함: %+v", h)
	}
}

func TestPauseFirstMustYieldsToCheckBlock(t *testing.T) {
	e := pauseEnv(t)
	h := e.bash("s", commitCmd(longTitle))
	if h == nil || h.Specific.Perm != "deny" {
		t.Fatalf("검사에 걸리면 deny 여야 함: %+v", h)
	}
	if strings.Contains(h.Specific.Reason, "[heka 먼저 읽기]") || !strings.Contains(h.Specific.Reason, "[heka 검사]") {
		t.Errorf("검사로 막는 것이 먼저여야 함: %q", h.Specific.Reason)
	}
	if got := pausedOf(e.lastRow()); len(got) != 0 {
		t.Errorf("검사로 막은 호출에는 paused 가 없어야 함: %v", got)
	}
	// 검사에 걸린 호출에서 다른 필수 항목은 additionalContext 로 이미 보였으므로, 고쳐 보낸 호출은 통과한다.
	if !strings.Contains(h.Specific.Context, "커밋 전에 상태를 본다") {
		t.Errorf("다른 필수 항목은 additionalContext 로 보여야 함: %q", h.Specific.Context)
	}
	if h := e.bash("s", commitCmd("짧은 제목")); h != nil && h.Specific.Perm == "deny" {
		t.Errorf("고쳐 보낸 호출은 통과해야 함: %+v", h)
	}
}

func TestPauseFirstMustOffByDefault(t *testing.T) {
	e := newEnv(t)
	h := e.bash("s", "git commit -m x")
	if h == nil || h.Specific.Perm != "" {
		t.Fatalf("설정을 켜지 않으면 알리기만 해야 함: %+v", h)
	}
	wantContext(t, h, "PreToolUse", "쓰기 전에 상태를 본다")
}

func TestPauseFirstMustSilentInShadow(t *testing.T) {
	e := pauseEnv(t)
	t.Setenv("HEKA_SHADOW", "1")
	if code, out := e.hook(map[string]any{"hook_event_name": "PreToolUse", "session_id": "s", "cwd": e.app(),
		"tool_name": "Bash", "tool_input": map[string]any{"command": commitCmd("짧은 제목")}}); code != 0 || out != "" {
		t.Errorf("그림자 방식에서는 아무것도 내보내지 않아야 함: %d %q", code, out)
	}
	if got := pausedOf(e.lastRow()); !slices.Equal(got, []string{"fmt", "must-a", "must-b"}) {
		t.Errorf("그림자 방식에서도 원장에는 남아야 함: %v", got)
	}
}
