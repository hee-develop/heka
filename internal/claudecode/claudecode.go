// Package claudecode 는 Claude Code 훅 이벤트를 선별기 순간으로 바꾸고 결과를 훅 출력 JSON 으로 만든다.
// 어떤 오류든 작업을 막지 않는다: 출력 없이 종료 코드 0 으로 끝내고 오류는 errors.log 에 남긴다.
package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hee-develop/heka/internal/action"
	"github.com/hee-develop/heka/internal/check"
	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/index"
	"github.com/hee-develop/heka/internal/ledger"
	"github.com/hee-develop/heka/internal/selector"
	"github.com/hee-develop/heka/internal/session"
	"github.com/hee-develop/heka/internal/shell"
)

// 처리 전체의 시간 상한이다. 프롬프트와 SubagentStart 는 판정 명령을 부를 수 있어 길다.
// 그림자 방식은 훅을 백그라운드로 돌리므로 상한을 넉넉히 둔다.
var (
	limitLong        = 25 * time.Second
	limitShort       = 5 * time.Second
	limitLongShadow  = 60 * time.Second
	limitShortShadow = 10 * time.Second
)

const (
	maxInput     = 16 << 20
	maxContext   = 10000 // additionalContext 문자열당 상한(글자 수)
	maxDelegText = 4000  // 기다리는 위임에 적어 두는 글의 상한(글자 수)
	defaultAgent = "general-purpose"
)

// Input 은 훅 표준 입력의 JSON 이다. 이벤트마다 쓰는 필드가 다르다.
type Input struct {
	SessionID      string          `json:"session_id"`
	Cwd            string          `json:"cwd"`
	HookEventName  string          `json:"hook_event_name"`
	AgentID        string          `json:"agent_id"`
	AgentType      string          `json:"agent_type"`
	Source         string          `json:"source"`
	TranscriptPath string          `json:"transcript_path"`
	Prompt         string          `json:"prompt"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`

	LastAssistantMessage string `json:"last_assistant_message"`
	StopHookActive       bool   `json:"stop_hook_active"`
}

// specific 은 hookSpecificOutput 이다.
type specific struct {
	HookEventName            string `json:"hookEventName"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
}

type handler struct {
	home   string
	in     Input
	cfg    *config.Config
	cfgErr error
	ix     *index.Index
	now    time.Time
	row    ledger.Row
	shadow bool
	top    map[string]any // hookSpecificOutput 밖의 최상위 출력(턴 종료의 block)
}

// shadowOn 은 그림자 방식인가. 환경 변수 HEKA_SHADOW=1 이나 설정 claude_code.shadow 가 켜 둔다.
func shadowOn(cfg *config.Config) bool {
	return os.Getenv("HEKA_SHADOW") == "1" || (cfg != nil && cfg.ClaudeCode.Shadow)
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
	case "SessionStart", "UserPromptSubmit", "PreToolUse", "SubagentStart", "Stop":
	default:
		return 0
	}
	cfg, cfgErr := config.Load(home)
	shadow := shadowOn(cfg)
	long, short := limitLong, limitShort
	if shadow {
		long, short = limitLongShadow, limitShortShadow
	}
	limit := short
	if in.HookEventName == "UserPromptSubmit" || in.HookEventName == "SubagentStart" {
		limit = long
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()

	h := &handler{home: home, in: in, now: start, cfg: cfg, cfgErr: cfgErr, shadow: shadow}
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
		h.row.Shadow = h.shadow
		if h.worthLogging() {
			if err := ledger.Append(home, h.row, start); err != nil {
				ledger.LogError(home, in.HookEventName, "원장 쓰기 실패: "+err.Error(), start)
			}
		}
		if h.shadow {
			return 0 // 그림자 방식은 판정만 남기고 아무것도 내보내지 않는다
		}
		enc := json.NewEncoder(stdout)
		enc.SetEscapeHTML(false)
		switch {
		case r.out != nil:
			_ = enc.Encode(map[string]any{"hookSpecificOutput": r.out})
		case h.top != nil:
			_ = enc.Encode(h.top)
		}
	case <-ctx.Done():
		ledger.LogError(home, in.HookEventName, "시간 상한을 넘김", start)
	}
	return 0
}

// worthLogging 은 원장에 줄을 남길지다. 도구 호출은 걸린 것이 있을 때만, 턴 종료는 검사한 것이 있을 때만 남긴다.
func (h *handler) worthLogging() bool {
	r := h.row
	if h.in.HookEventName == "Stop" {
		return len(r.Checks) > 0
	}
	return h.in.HookEventName != "PreToolUse" || len(r.Injected)+len(r.Dropped)+len(r.Confirm)+len(r.GrantsUsed)+len(r.Checks) > 0
}

func (h *handler) handle(ctx context.Context) (*specific, error) {
	defer func() {
		if h.ix != nil {
			h.ix.Close()
		}
	}()
	if h.cfgErr != nil {
		return nil, h.cfgErr
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
	case "Stop":
		h.row.Moment = "stop"
		return nil, h.stop(ctx)
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

// openIndex 는 색인을 열고(낡았으면 먼저 다시 만들고) 이 처리 안에서 다시 쓴다.
func (h *handler) openIndex() error {
	if h.ix != nil {
		return nil
	}
	if err := index.Ensure(h.home, h.cfg); err != nil {
		return err
	}
	ix, err := index.Open(h.home)
	if err != nil {
		return err
	}
	h.ix = ix
	return nil
}

// choose 는 순간 하나를 선별한다. 예산까지 적용한 결과다.
func (h *handler) choose(ctx context.Context, m selector.Moment) (selector.Result, error) {
	if err := h.openIndex(); err != nil {
		return selector.Result{}, err
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
	if len(out) > 0 {
		h.row.Tokens = selector.Tokens(contextText(out))
	}
	return out, nil
}

// gather 는 choose 와 같되 예산을 적용하지 않은 후보를 돌려준다. 여러 번 부른 결과를 모아 예산을 한 번 적용할 때 쓴다.
func (h *handler) gather(ctx context.Context, m selector.Moment) (selector.Result, error) {
	if err := h.openIndex(); err != nil {
		return selector.Result{}, err
	}
	return selector.Gather(ctx, h.ix, h.cfg, m)
}

// record 는 선별 결과의 탈락과 동작을 원장 줄에 옮긴다.
func (h *handler) record(res selector.Result) {
	for _, d := range res.Drops {
		h.row.Dropped = append(h.row.Dropped, ledger.Dropped{ID: d.ID, Stage: d.Stage, Reason: d.Reason})
	}
	h.row.Asked, h.row.Refused = res.Asked, res.Refused
	for _, j := range res.Judges {
		h.row.Judges = append(h.row.Judges, ledger.Judged{ID: j.ID, Inject: j.Inject, MS: j.MS, Error: j.Error, Timeout: j.Timeout})
	}
}

// contextOut 은 넣을 항목을 "[heka]" 로 시작하는 글로 만든다. 항목이 없으면 nil 이다.
func contextOut(event string, injs []selector.Injection) *specific {
	if len(injs) == 0 {
		return nil
	}
	return &specific{HookEventName: event, AdditionalContext: contextText(injs)}
}

// ContextText 는 넣을 항목이 모델에게 실제로 전해지는 글이다. 토큰을 잴 때 쓴다.
func ContextText(injs []selector.Injection) string { return contextText(injs) }

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
	injs, err := h.claim(ctx, res.Injections, func(s *session.State) {
		if h.in.TranscriptPath != "" {
			s.Transcript = h.in.TranscriptPath
		}
	})
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
	err := session.Update(h.home, h.in.SessionID, func(s *session.State) {
		s.EndTurn()
		if h.in.TranscriptPath != "" {
			s.Transcript = h.in.TranscriptPath
		}
	})
	if err != nil {
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
				res, err := h.gather(ctx, m)
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
	// 동작·경로마다 모은 후보를 한데 모은 뒤 예산을 한 번만 적용한다. 도구 호출 하나가 넣는 글 전체가 상한을 넘지 않게 한다.
	// 예산으로 빠진 항목은 탈락으로 남고 "이미 넣은 항목"에 들어가지 않으므로 다음 호출에서 다시 후보가 된다.
	drops = dedupDrops(drops, byID)
	cands := make([]selector.Injection, len(hits))
	for i, x := range hits {
		cands[i] = x.inj
	}
	kept, drops := selector.Fit(h.cfg, "tool", cands, drops)
	keptHits := hits[:0:0]
	for _, k := range kept {
		x := byID[k.ID]
		x.inj = k
		keptHits = append(keptHits, x)
	}
	hits = keptHits
	h.record(selector.Result{Drops: drops})

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
	denied, reasons, err := h.toolChecks(ctx, r.Actions, r.Paths)
	if err != nil {
		return nil, err
	}
	injs, err := h.claim(ctx, inform, func(s *session.State) {
		if deleg != nil {
			s.Delegate(*deleg)
		}
	})
	if err != nil {
		return nil, err
	}
	if len(reasons) > 0 {
		// 막은 항목의 주입문은 사유에 들어 있으므로 additionalContext 에서는 뺀다.
		// 사유로 이미 전한 것이라 "이미 넣은 항목"에는 남긴다.
		injs = slices.DeleteFunc(slices.Clone(injs), func(i selector.Injection) bool { return slices.Contains(denied, i.ID) })
		out := contextOut("PreToolUse", injs)
		if out == nil {
			out = &specific{HookEventName: "PreToolUse"}
		}
		out.PermissionDecision = "deny"
		out.PermissionDecisionReason = strings.Join(reasons, "\n")
		return out, nil
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

// toolChecks 는 도구 호출 직전의 결과물 검사다. 검사 결과를 원장 줄에 적고,
// 막아야 하는 항목의 id 와 모델에게 돌려줄 사유(항목마다 한 줄)를 돌려준다.
// 글을 뽑다가 오류가 나도 검사만 건너뛰고 통과시킨다.
func (h *handler) toolChecks(ctx context.Context, actions, paths []string) (denied, reasons []string, err error) {
	defer func() {
		if r := recover(); r != nil {
			denied, reasons, err = nil, nil, nil
			h.row.Checks = nil
		}
	}()
	if h.in.ToolName != "Bash" || len(actions) == 0 {
		return nil, nil, nil
	}
	if len(paths) == 0 {
		paths = []string{""}
	}
	var cands []selector.Checkable
	seen := map[string]bool{}
	base := h.moment("tool")
	for _, a := range actions {
		for _, p := range paths {
			m := base
			m.Action, m.Path, m.Agent = a, p, h.in.AgentType
			if err := h.openIndex(); err != nil {
				return nil, nil, err
			}
			cs, err := selector.ToolChecks(ctx, h.ix, h.cfg, m)
			if err != nil {
				return nil, nil, err
			}
			for _, c := range cs {
				if !seen[c.ID] {
					seen[c.ID] = true
					cands = append(cands, c)
				}
			}
		}
	}
	if len(cands) == 0 {
		return nil, nil, nil
	}
	home, _ := os.UserHomeDir()
	targets, notes := check.Extract(h.in.ToolName, h.toolInput(), shell.Files{Cwd: h.in.Cwd, Home: home, Read: check.ReadFile})
	for _, c := range cands {
		result, why := check.Eval(c.Check, targets[c.Check.Target])
		blocked := result == check.Fail && c.Check.Mode == "block"
		note := ""
		if result == check.Skip {
			note = why
			if n := notes[c.Check.Target]; n != "" && len(targets[c.Check.Target]) == 0 {
				note = n // 글을 뽑지 못한 구체적인 이유
			}
		}
		h.row.Checks = append(h.row.Checks, ledger.Checked{ID: c.ID, Target: c.Check.Target, Result: result, Blocked: blocked, Note: note})
		if blocked {
			denied = append(denied, c.ID)
			reasons = append(reasons, check.Line(c.ID, c.Text, why))
		}
	}
	return denied, reasons, nil
}

// stop 은 턴 종료의 결과물 검사다. 마지막 응답이 block 검사에 걸리면 모델이 한 번 더 돌도록 되돌린다.
// 세션당 3회까지만, 같은 항목이 연속으로 걸리면 두 번까지만 되돌리고 그 뒤에는 원장에만 남긴다.
// 오류는 검사만 건너뛰고 통과시킨다.
func (h *handler) stop(ctx context.Context) error {
	m := h.moment("stop")
	m.Agent = h.in.AgentType
	if err := h.openIndex(); err != nil {
		return err
	}
	cands, err := selector.StopChecks(h.ix, h.cfg, m)
	if err != nil || len(cands) == 0 {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	type failed struct {
		c   selector.Checkable
		why string
	}
	var fails []failed
	var failIDs []string
	for _, c := range cands {
		var texts []string
		if h.in.LastAssistantMessage != "" {
			texts = []string{h.in.LastAssistantMessage}
		}
		result, why := check.Eval(c.Check, texts)
		note := ""
		if result == check.Skip {
			note = why
		}
		h.row.Checks = append(h.row.Checks, ledger.Checked{ID: c.ID, Target: c.Check.Target, Result: result, Note: note})
		if result == check.Fail && c.Check.Mode == "block" {
			fails = append(fails, failed{c, why})
			failIDs = append(failIDs, c.ID)
		}
	}
	var blocked []string
	if len(failIDs) > 0 && h.in.SessionID != "" {
		err := session.Update(h.home, h.in.SessionID, func(s *session.State) {
			blocked, _ = s.StopDecide(failIDs)
		})
		if err != nil {
			return err
		}
	} else if len(failIDs) > 0 {
		blocked = failIDs // 세션을 모르면 횟수를 셀 수 없으니 한 번만 되돌린다
	}
	var reasons []string
	for _, f := range fails {
		if slices.Contains(blocked, f.c.ID) {
			reasons = append(reasons, check.Line(f.c.ID, f.c.Text, f.why))
		}
	}
	for i := range h.row.Checks {
		h.row.Checks[i].Blocked = slices.Contains(blocked, h.row.Checks[i].ID)
	}
	if len(failIDs) > len(blocked) {
		h.row.Reason = "턴 종료 검사의 되돌림 한도로 막지 않음"
	}
	if len(reasons) > 0 {
		h.top = map[string]any{"decision": "block", "reason": strings.Join(reasons, "\n")}
	}
	return nil
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
// shadow 이면 그림자 방식용이다. 명령에 HEKA_SHADOW=1 을 붙이고 각 훅을 async 로 돌려 Claude Code 가 기다리지 않게 한다.
func Settings(shadow bool) ([]byte, error) {
	command := "heka hook"
	if shadow {
		command = "HEKA_SHADOW=1 heka hook"
	}
	entry := func(timeout int) []map[string]any {
		h := map[string]any{"type": "command", "command": command, "timeout": timeout}
		if shadow {
			h["async"] = true
		}
		return []map[string]any{{"hooks": []map[string]any{h}}}
	}
	short, long := 10, 30
	if shadow {
		short, long = 20, 90 // 처리 시간 상한(프롬프트 60초)보다 길게
	}
	return json.MarshalIndent(map[string]any{"hooks": map[string]any{
		"SessionStart":     entry(short),
		"UserPromptSubmit": entry(long),
		"PreToolUse":       entry(short),
		"SubagentStart":    entry(long),
		"Stop":             entry(short),
	}}, "", "  ")
}
