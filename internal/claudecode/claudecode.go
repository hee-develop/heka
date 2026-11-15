// Package claudecode 는 Claude Code 훅 이벤트를 선별기 순간으로 바꾸고 결과를 훅 출력 JSON 으로 만든다.
// 어떤 오류든 작업을 막지 않는다: 출력 없이 종료 코드 0 으로 끝내고 오류는 errors.log 에 남긴다.
package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/hee-develop/heka/internal/action"
	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/index"
	"github.com/hee-develop/heka/internal/ledger"
	"github.com/hee-develop/heka/internal/selector"
	"github.com/hee-develop/heka/internal/session"
)

// 처리 전체의 시간 상한이다. 프롬프트와 SubagentStart 는 판정 명령을 부를 수 있어 길다.
var (
	limitLong  = 25 * time.Second
	limitShort = 5 * time.Second
)

const (
	maxInput     = 16 << 20
	maxContext   = 10000 // additionalContext 문자열당 상한(글자 수)
	maxDelegText = 4000  // 기다리는 위임에 적어 두는 글의 상한(글자 수)
	defaultAgent = "general-purpose"
)

// Input 은 훅 표준 입력의 JSON 이다. 이벤트마다 쓰는 필드가 다르다.
type Input struct {
	SessionID     string          `json:"session_id"`
	Cwd           string          `json:"cwd"`
	HookEventName string          `json:"hook_event_name"`
	AgentID       string          `json:"agent_id"`
	AgentType     string          `json:"agent_type"`
	Source        string          `json:"source"`
	Prompt        string          `json:"prompt"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
}

// specific 은 hookSpecificOutput 이다.
type specific struct {
	HookEventName            string `json:"hookEventName"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
}

type handler struct {
	home string
	in   Input
	cfg  *config.Config
	ix   *index.Index
	now  time.Time
	row  ledger.Row
}

// Run 은 표준 입력의 훅 JSON 하나를 처리하고 표준 출력에 결과를 쓴다. 항상 0 을 돌려준다.
func Run(stdin io.Reader, stdout io.Writer, home string) int {
	start := time.Now()
	data, err := io.ReadAll(io.LimitReader(stdin, maxInput))
	var in Input
	if err == nil {
		err = json.Unmarshal(data, &in)
	}
	if err != nil {
		ledger.LogError(home, "", "입력을 읽지 못함: "+err.Error(), start)
		return 0
	}
	switch in.HookEventName {
	case "SessionStart", "UserPromptSubmit", "PreToolUse", "SubagentStart":
	default:
		return 0
	}
	limit := limitShort
	if in.HookEventName == "UserPromptSubmit" || in.HookEventName == "SubagentStart" {
		limit = limitLong
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()

	h := &handler{home: home, in: in, now: start}
	type result struct {
		out *specific
		err error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{err: fmt.Errorf("패닉: %v", r)}
			}
		}()
		out, err := h.handle(ctx)
		ch <- result{out, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			ledger.LogError(home, in.HookEventName, r.err.Error(), start)
			return 0
		}
		h.row.DurationMS = time.Since(start).Milliseconds()
		if h.worthLogging() {
			if err := ledger.Append(home, h.row, start); err != nil {
				ledger.LogError(home, in.HookEventName, "원장 쓰기 실패: "+err.Error(), start)
			}
		}
		if r.out != nil {
			enc := json.NewEncoder(stdout)
			enc.SetEscapeHTML(false)
			_ = enc.Encode(map[string]any{"hookSpecificOutput": r.out})
		}
	case <-ctx.Done():
		ledger.LogError(home, in.HookEventName, "시간 상한을 넘김", start)
	}
	return 0
}

// worthLogging 은 원장에 줄을 남길지다. 도구 호출은 걸린 것이 있을 때만 남긴다.
func (h *handler) worthLogging() bool {
	r := h.row
	return h.in.HookEventName != "PreToolUse" || len(r.Injected)+len(r.Dropped)+len(r.Confirm)+len(r.GrantsUsed) > 0
}

func (h *handler) handle(ctx context.Context) (*specific, error) {
	defer func() {
		if h.ix != nil {
			h.ix.Close()
		}
	}()
	var err error
	if h.cfg, err = config.Load(h.home); err != nil {
		return nil, err
	}
	in := h.in
	h.row = ledger.Row{Session: in.SessionID, Agent: in.AgentID, Event: in.HookEventName}
	switch in.HookEventName {
	case "SessionStart":
		h.row.Moment = "session_start"
		return h.sessionStart(ctx)
	case "UserPromptSubmit":
		h.row.Moment = "prompt"
		return h.prompt(ctx)
	case "PreToolUse":
		h.row.Moment = "tool"
		return h.preTool(ctx)
	}
	h.row.Moment = "subagent_start"
	return h.subagentStart(ctx)
}

// moment 는 연결부가 채우는 공통 값(범위, 저장소 이름, 오늘)을 넣은 순간이다.
func (h *handler) moment(kind string) selector.Moment {
	scopes, repo := h.cfg.Locate(h.in.Cwd)
	scopes = append(append([]string{}, scopes...), h.cfg.ClaudeCode.Scopes...)
	seen := session.Load(h.home, h.in.SessionID).Seen(h.in.AgentID)
	return selector.Moment{Kind: kind, Scopes: scopes, Repo: repo, Seen: seen, Today: h.now.Format("2006-01-02")}
}

// choose 는 색인을 열고(낡았으면 먼저 다시 만들고) 순간 하나를 선별한다.
func (h *handler) choose(ctx context.Context, m selector.Moment) (selector.Result, error) {
	if h.ix == nil {
		if err := index.Ensure(h.home, h.cfg); err != nil {
			return selector.Result{}, err
		}
		ix, err := index.Open(h.home)
		if err != nil {
			return selector.Result{}, err
		}
		h.ix = ix
	}
	return selector.Select(ctx, h.ix, h.cfg, m)
}

// claim 은 넣을 항목을 "이미 넣은 항목"에 적고 이번에 처음 적은 것만 돌려준다.
// extra 는 같은 잠금 안에서 함께 고칠 것이다. 시간이 이미 지났으면 아무것도 적지 않는다.
func (h *handler) claim(ctx context.Context, injs []selector.Injection, extra func(*session.State)) ([]selector.Injection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []selector.Injection
	err := session.Update(h.home, h.in.SessionID, func(s *session.State) {
		for _, i := range injs {
			if s.Show(h.in.AgentID, i.ID) {
				out = append(out, i)
			}
		}
		if extra != nil {
			extra(s)
		}
	})
	if err != nil {
		return nil, err
	}
	for _, i := range out {
		h.row.Injected = append(h.row.Injected, ledger.Injected{ID: i.ID, Hash: i.Hash, Why: i.Why})
	}
	return out, nil
}

// record 는 선별 결과의 탈락과 동작을 원장 줄에 옮긴다.
func (h *handler) record(res selector.Result) {
	for _, d := range res.Drops {
		h.row.Dropped = append(h.row.Dropped, ledger.Dropped{ID: d.ID, Stage: d.Stage, Reason: d.Reason})
	}
	h.row.Asked, h.row.Refused = res.Asked, res.Refused
}

// contextOut 은 넣을 항목을 "[heka]" 로 시작하는 글로 만든다. 항목이 없으면 nil 이다.
func contextOut(event string, injs []selector.Injection) *specific {
	if len(injs) == 0 {
		return nil
	}
	return &specific{HookEventName: event, AdditionalContext: contextText(injs)}
}

func contextText(injs []selector.Injection) string {
	var b strings.Builder
	b.WriteString("[heka]")
	for _, i := range injs {
		b.WriteString("\n- " + i.Text)
	}
	s := b.String()
	if r := []rune(s); len(r) > maxContext {
		s = string(r[:maxContext])
	}
	return s
}

func (h *handler) sessionStart(ctx context.Context) (*specific, error) {
	if src := h.in.Source; src == "compact" || src == "clear" {
		err := session.Update(h.home, h.in.SessionID, func(s *session.State) {
			s.Shown = map[string][]string{}
			if src == "clear" {
				s.Grants = map[string]string{}
			}
		})
		if err != nil {
			return nil, err
		}
	}
	res, err := h.choose(ctx, h.moment("session_start"))
	if err != nil {
		return nil, err
	}
	h.record(res)
	injs, err := h.claim(ctx, res.Injections, nil)
	if err != nil {
		return nil, err
	}
	return contextOut("SessionStart", injs), nil
}

func (h *handler) prompt(ctx context.Context) (*specific, error) {
	h.row.Prompt = h.in.Prompt
	text := strings.TrimSpace(h.in.Prompt)
	for _, p := range h.cfg.ClaudeCode.ToolPromptPrefixes {
		if p != "" && strings.HasPrefix(text, p) {
			h.row.ToolMade = true // 넣지도, 허용을 만들거나 끝내지도 않는다
			return nil, nil
		}
	}
	if err := session.Update(h.home, h.in.SessionID, (*session.State).EndTurn); err != nil {
		return nil, err
	}
	m := h.moment("prompt")
	m.Text = h.in.Prompt
	res, err := h.choose(ctx, m)
	if err != nil {
		return nil, err
	}
	h.record(res)
	scope := session.Turn
	if h.sessionGrant(h.in.Prompt) {
		scope = session.Session
	}
	injs, err := h.claim(ctx, res.Injections, func(s *session.State) {
		for _, a := range res.Asked {
			s.Grant(a, scope)
		}
		for _, a := range res.Refused {
			delete(s.Grants, a)
		}
	})
	if err != nil {
		return nil, err
	}
	return contextOut("UserPromptSubmit", injs), nil
}

// sessionGrant 는 프롬프트에 "이 세션 동안 허용" 표현이 있는가.
func (h *handler) sessionGrant(prompt string) bool {
	lower := strings.ToLower(prompt)
	for _, p := range h.cfg.SessionGrant {
		if re, err := regexp.Compile(p); err == nil && (re.MatchString(prompt) || re.MatchString(lower)) {
			return true
		}
	}
	return false
}

// toolInput 은 도구 입력을 객체로 읽는다. 객체가 아니면 빈 객체다.
func (h *handler) toolInput() map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal(h.in.ToolInput, &m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

// hit 은 도구 호출 하나에서 걸린 항목과 그것이 걸린 동작들이다.
type hit struct {
	inj     selector.Injection
	actions []string
}

func (h *handler) preTool(ctx context.Context) (*specific, error) {
	in := h.in
	ti := h.toolInput()
	r := action.Resolve(action.Call{Tool: in.ToolName, Input: ti, Cwd: in.Cwd}, h.cfg)
	h.row.Actions = r.Actions
	var deleg *session.Delegation
	if in.ToolName == "Agent" || in.ToolName == "Task" {
		if p, _ := ti["prompt"].(string); p != "" {
			agent := r.Agent
			if agent == "" {
				agent = defaultAgent
			}
			if rs := []rune(p); len(rs) > maxDelegText {
				p = string(rs[:maxDelegText])
			}
			deleg = &session.Delegation{Agent: agent, Text: p}
		}
	}
	if len(r.Actions) == 0 && deleg == nil {
		return nil, nil
	}

	var hits []*hit
	byID := map[string]*hit{}
	var drops []selector.Drop
	if len(r.Actions) > 0 {
		base := h.moment("tool")
		for _, a := range r.Actions {
			paths := r.Paths
			if len(paths) == 0 {
				paths = []string{""}
			}
			for _, p := range paths {
				m := base
				m.Action, m.Path, m.Agent = a, p, in.AgentType
				res, err := h.choose(ctx, m)
				if err != nil {
					return nil, err
				}
				drops = append(drops, res.Drops...)
				for _, inj := range res.Injections {
					if byID[inj.ID] == nil {
						byID[inj.ID] = &hit{inj: inj}
						hits = append(hits, byID[inj.ID])
					}
					if x := byID[inj.ID]; !contains(x.actions, a) {
						x.actions = append(x.actions, a)
					}
				}
			}
		}
	}
	h.record(selector.Result{Drops: dedupDrops(drops, byID)})

	// 확인 요구를 가른다. 허용은 그 동작이나 상위 동작에 있어야 하고, 걸린 동작이 여럿이면 모두에 있어야 한다.
	state := session.Load(h.home, in.SessionID)
	var inform []selector.Injection
	var asks []string
	for _, x := range hits {
		granted := ""
		for _, a := range x.actions {
			if g := state.GrantFor(h.cfg.ActionClosure(a)...); g != "" {
				granted = g
			} else {
				granted = ""
				break
			}
		}
		switch {
		case x.inj.Confirm == "always", x.inj.Confirm == "true" && granted == "":
			asks = append(asks, x.inj.Text)
			h.row.Confirm = append(h.row.Confirm, x.inj.ID)
		default:
			if x.inj.Confirm == "true" {
				h.row.GrantsUsed = append(h.row.GrantsUsed, granted)
			}
			inform = append(inform, x.inj)
		}
	}
	injs, err := h.claim(ctx, inform, func(s *session.State) {
		if deleg != nil {
			s.Delegate(*deleg)
		}
	})
	if err != nil {
		return nil, err
	}
	out := contextOut("PreToolUse", injs)
	if len(asks) > 0 {
		if out == nil {
			out = &specific{HookEventName: "PreToolUse"}
		}
		out.PermissionDecision = "ask"
		out.PermissionDecisionReason = strings.Join(asks, "\n")
	}
	return out, nil
}

func (h *handler) subagentStart(ctx context.Context) (*specific, error) {
	var d session.Delegation
	err := session.Update(h.home, h.in.SessionID, func(s *session.State) {
		d, _ = s.TakeDelegation(h.in.AgentType)
	})
	if err != nil {
		return nil, err
	}
	m := h.moment("subagent_start")
	m.Text, m.Agent = d.Text, h.in.AgentType
	res, err := h.choose(ctx, m)
	if err != nil {
		return nil, err
	}
	h.record(res)
	injs, err := h.claim(ctx, res.Injections, nil)
	if err != nil {
		return nil, err
	}
	return contextOut("SubagentStart", injs), nil
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// dedupDrops 는 같은 (항목, 단계, 사유)의 탈락을 하나로 줄이고, 다른 호출에서 넣게 된 항목의 탈락은 뺀다.
func dedupDrops(drops []selector.Drop, injected map[string]*hit) []selector.Drop {
	seen := map[selector.Drop]bool{}
	var out []selector.Drop
	for _, d := range drops {
		if injected[d.ID] == nil && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// Settings 는 Claude Code 설정 파일의 hooks 에 넣을 JSON 조각이다.
func Settings() ([]byte, error) {
	entry := func(timeout int) []map[string]any {
		return []map[string]any{{"hooks": []map[string]any{{"type": "command", "command": "heka hook", "timeout": timeout}}}}
	}
	return json.MarshalIndent(map[string]any{"hooks": map[string]any{
		"SessionStart":     entry(10),
		"UserPromptSubmit": entry(30),
		"PreToolUse":       entry(10),
		"SubagentStart":    entry(30),
	}}, "", "  ")
}
