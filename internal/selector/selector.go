// Package selector 는 "순간" 하나를 받아 넣을 항목과 탈락 사유를 돌려준다.
// 어떤 AI 도구인지 모르고, 파일을 쓰지 않는다. 단계는 범위, 자격, 일치, 중복 제거, 예산, 형태 순이다.
package selector

import (
	"context"
	"fmt"
	"maps"
	"math"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/index"
	"github.com/hee-develop/heka/internal/judge"
)

// topicMax 는 주제 갈래가 한 번에 넣는 참고 항목의 상한이다.
const topicMax = 3

// Moment 는 선별기에 들어오는 순간 하나다.
type Moment struct {
	Kind     string          // session_start | prompt | tool | subagent_start
	Scopes   []string        // 연결부가 작업 위치로 정한 범위
	Text     string          // prompt: 프롬프트 글, subagent_start: 위임 글
	ToolMade bool            // 도구가 만들어 넣은 프롬프트
	Action   string          // tool: 동작 이름
	Path     string          // tool: 대상 경로
	Repo     string          // 저장소 이름
	Agent    string          // 에이전트 종류(메인이면 빈 값)
	Seen     map[string]bool // 이 세션에 이미 넣은 항목 id
	Today    string          // YYYY-MM-DD
}

// Injection 은 넣을 항목이다. Why 는 무엇이 맞아서 넣었는지 한 줄이다.
type Injection struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Must bool   `json:"must"`
	Why  string `json:"why"`
}

// Drop 은 탈락한 항목이다. Stage 는 범위|자격|일치|중복|예산|판정 이다.
type Drop struct {
	ID     string `json:"id"`
	Stage  string `json:"stage"`
	Reason string `json:"reason"`
}

// Result 는 선별 결과다. Asked 는 프롬프트가 시킨 동작이다.
type Result struct {
	Injections []Injection `json:"injections"`
	Drops      []Drop      `json:"drops"`
	Asked      []string    `json:"asked"`
}

type cand struct {
	r    *index.Record
	must bool
	why  string
}

type sel struct {
	ctx    context.Context
	ix     *index.Index
	cfg    *config.Config
	m      Moment
	scopes map[string]bool
	got    map[string]bool // 이미 후보로 다룬 id
	cands  []*cand
	drops  []Drop
	asked  []string
}

// Select 는 순간 하나에 넣을 항목을 고른다.
func Select(ctx context.Context, ix *index.Index, cfg *config.Config, m Moment) (Result, error) {
	s := &sel{ctx: ctx, ix: ix, cfg: cfg, m: m, scopes: map[string]bool{}, got: map[string]bool{}}
	for _, sc := range m.Scopes {
		for _, a := range cfg.ScopeClosure(sc) {
			s.scopes[a] = true
		}
	}
	for _, g := range cfg.GlobalScopes {
		s.scopes[g] = true
	}
	var err error
	switch m.Kind {
	case "tool":
		err = s.tool()
	case "session_start":
		err = s.session()
	case "subagent_start":
		err = s.subagent()
	case "prompt":
		if !m.ToolMade {
			err = s.prompt()
		}
	default:
		err = fmt.Errorf("모르는 순간: %q", m.Kind)
	}
	if err != nil {
		return Result{}, err
	}
	res := Result{Injections: []Injection{}, Drops: s.drops, Asked: s.asked}
	s.fit(&res)
	if res.Drops == nil {
		res.Drops = []Drop{}
	}
	if res.Asked == nil {
		res.Asked = []string{}
	}
	return res, nil
}

func (s *sel) tool() error {
	if s.m.Action == "" {
		return nil
	}
	ids, err := s.ix.ByAction(s.m.Action)
	if err != nil {
		return err
	}
	return s.admitAll(ids, "도구 동작 "+s.m.Action+" 에 조건이 맞음", func(r *index.Record) string {
		return s.matchWhen(r, true, "tool")
	})
}

func (s *sel) session() error {
	ids, err := s.ix.ByWhen("moment", "session_start")
	if err != nil {
		return err
	}
	return s.admitAll(ids, "세션 시작 항목", s.noActionMatch(false, "session_start"))
}

func (s *sel) subagent() error {
	ids, err := s.ix.ByWhen("moment", "subagent_start")
	if err != nil {
		return err
	}
	if s.m.Agent != "" {
		byAgent, err := s.ix.ByWhen("agent", s.m.Agent)
		if err != nil {
			return err
		}
		ids = slices.Compact(slices.Sorted(slices.Values(append(ids, byAgent...))))
	}
	if err := s.admitAll(ids, "서브에이전트 시작: moment 또는 agent 조건이 맞음", s.noActionMatch(true, "subagent_start")); err != nil {
		return err
	}
	return s.topic()
}

func (s *sel) prompt() error {
	asked, err := s.askedActions()
	if err != nil {
		return err
	}
	s.asked = asked
	for _, a := range asked {
		ids, err := s.ix.ByAction(a)
		if err != nil {
			return err
		}
		err = s.admitAll(ids, "프롬프트가 "+a+" 를 시킴", func(r *index.Record) string {
			if len(r.When["path"]) > 0 {
				return "path 조건이 있는 항목은 프롬프트에서 주지 않음"
			}
			return s.matchWhen(r, true, "tool", "prompt")
		})
		if err != nil {
			return err
		}
	}
	return s.topic()
}

// askedActions 는 동작 낱말 사전으로 프롬프트가 시킨 동작을 찾는다.
// 표현이 맞은 자리마다 맞은 글과 그 뒤 negation_window 글자 안에 부정·질문·조건이 있으면 그 자리는 버린다.
func (s *sel) askedActions() ([]string, error) {
	var negs []*regexp.Regexp
	for _, n := range s.cfg.Negations {
		re, err := regexp.Compile(n)
		if err != nil {
			return nil, err
		}
		negs = append(negs, re)
	}
	text := strings.ToLower(s.m.Text)
	var out []string
	for _, a := range slices.Sorted(maps.Keys(s.cfg.Actions)) {
	phrases:
		for _, p := range s.cfg.Actions[a].Phrases {
			re, err := regexp.Compile(p)
			if err != nil {
				return nil, err
			}
			for _, loc := range re.FindAllStringIndex(text, -1) {
				end := loc[1]
				for n := 0; n < s.cfg.NegationWindow && end < len(text); n++ {
					_, w := utf8.DecodeRuneInString(text[end:])
					end += w
				}
				seg := text[loc[0]:end]
				if !slices.ContainsFunc(negs, func(n *regexp.Regexp) bool { return n.MatchString(seg) }) {
					out = append(out, a)
					break phrases
				}
			}
		}
	}
	return out, nil
}

// topic 은 주제 갈래다. 동작 조건이 없는 항목 가운데 주제어가 맞은 것과 낱말 순위 상위 후보를 모델에 물어 고른다.
func (s *sel) topic() error {
	text := strings.TrimSpace(s.m.Text)
	if text == "" {
		return nil
	}
	// 대상은 동작 조건이 없고, moment 조건이 있으면 prompt(또는 이 순간)를 포함하는 항목이다.
	// 대상이 아닌 항목은 후보가 아니므로 탈락으로도 남기지 않는다.
	target := func(r *index.Record) bool {
		return len(r.When["action"]) == 0 && momentOK(r, true, "prompt", s.m.Kind)
	}
	match := func(r *index.Record) string { return s.matchWhen(r, true, "prompt", s.m.Kind) }
	var cands []*cand
	about, err := s.ix.AboutMatches(text)
	if err != nil {
		return err
	}
	for _, am := range about {
		r, err := s.ix.Item(am.ID)
		if err != nil {
			return err
		}
		if !target(r) {
			continue
		}
		if c := s.admitRec(r, match); c != nil {
			c.why = fmt.Sprintf("주제어 %q 가 맞음", am.Term)
			cands = append(cands, c)
		}
	}
	// 순위는 색인 전체에서 매기고, 대상이 아닌 항목은 조용히 건너뛰며 위에서부터 채운다.
	ranked, err := s.ix.Lexical(text, math.MaxInt32)
	if err != nil {
		return err
	}
	picked := 0
	for i, sc := range ranked {
		if picked >= s.cfg.Judge.Candidates {
			break
		}
		r, err := s.ix.Item(sc.ID)
		if err != nil {
			return err
		}
		if !target(r) {
			continue
		}
		if stage, _, _ := s.check(r, match); stage != "" {
			continue
		}
		picked++
		if c := s.admitRec(r, match); c != nil {
			c.why = fmt.Sprintf("낱말 점수 %.2f (%d위)", sc.Score, i+1)
			cands = append(cands, c)
		}
	}
	if len(cands) == 0 {
		return nil
	}
	if len(s.cfg.Judge.Command) == 0 {
		for _, c := range cands {
			s.drop(c.r.ID, "판정", "판정 명령 없음")
		}
		return nil
	}
	type answer struct {
		inject bool
		reason string
		err    error
	}
	ans := make([]answer, len(cands))
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		go func() {
			defer wg.Done()
			it := judge.Item{ID: c.r.ID, Text: c.r.Text, About: c.r.About}
			ans[i].inject, ans[i].reason, ans[i].err = judge.Ask(s.ctx, s.cfg.Judge, s.m.Text, it)
		}()
	}
	wg.Wait()
	kept := 0
	for i, c := range cands {
		switch a := ans[i]; {
		case a.err != nil:
			s.drop(c.r.ID, "판정", "판정 오류: "+a.err.Error())
		case !a.inject:
			s.drop(c.r.ID, "판정", "넣지 않음: "+a.reason)
		case kept >= topicMax:
			s.drop(c.r.ID, "예산", fmt.Sprintf("참고 항목은 한 번에 최대 %d건", topicMax))
		default:
			kept++
			c.why += ", 판정: " + a.reason
			s.cands = append(s.cands, c)
		}
	}
	return nil
}

func (s *sel) drop(id, stage, reason string) { s.drops = append(s.drops, Drop{id, stage, reason}) }

func (s *sel) admitAll(ids []string, why string, match func(*index.Record) string) error {
	for _, id := range ids {
		c, err := s.admit(id, match)
		if err != nil {
			return err
		}
		if c != nil {
			c.why = why
			s.cands = append(s.cands, c)
		}
	}
	return nil
}

// admit 은 항목 하나를 범위, 자격, 일치, 중복 제거에 통과시킨다. 탈락하면 사유를 남기고 nil 을 돌려준다.
func (s *sel) admit(id string, match func(*index.Record) string) (*cand, error) {
	if s.got[id] {
		return nil, nil
	}
	r, err := s.ix.Item(id)
	if err != nil {
		return nil, err
	}
	return s.admitRec(r, match), nil
}

func (s *sel) admitRec(r *index.Record, match func(*index.Record) string) *cand {
	if s.got[r.ID] {
		return nil
	}
	s.got[r.ID] = true
	stage, reason, must := s.check(r, match)
	if stage == "" && s.m.Seen[r.ID] {
		stage, reason = "중복", "이 세션에 이미 넣음"
	}
	if stage != "" {
		s.drop(r.ID, stage, reason)
		return nil
	}
	return &cand{r: r, must: must}
}

// check 는 범위, 자격, 일치를 차례로 본다. 통과하면 stage 가 빈 값이고, must 는 필수로 다룰지다.
func (s *sel) check(r *index.Record, match func(*index.Record) string) (stage, reason string, must bool) {
	if !slices.ContainsFunc(r.Scope, func(sc string) bool { return s.scopes[sc] }) {
		return "범위", "항목의 범위 " + strings.Join(r.Scope, ", ") + " 가 순간의 범위에 없음", false
	}
	must = r.Must
	switch {
	case r.Expires != "" && r.Expires < s.m.Today:
		return "자격", "기한이 지남 (" + r.Expires + ")", false
	case r.Checked != "":
	case r.Source == "user" && r.Quote != "":
	case r.Source == "doc":
		must = false
	default:
		return "자격", "미확인 " + r.Source + " 항목은 통과하지 못함", false
	}
	if why := match(r); why != "" {
		return "일치", why, false
	}
	return "", "", must
}

// momentOK 는 항목의 when.moment 가 kinds 중 하나를 포함하는가. 비어 있으면 orEmpty 다.
func momentOK(r *index.Record, orEmpty bool, kinds ...string) bool {
	mo := r.When["moment"]
	if len(mo) == 0 {
		return orEmpty
	}
	return slices.ContainsFunc(mo, func(v string) bool { return slices.Contains(kinds, v) })
}

// matchWhen 은 moment 와 repo·path·agent 조건을 본다. 항목에 조건이 있는데 순간에 그 값이 없으면 맞지 않는다.
func (s *sel) matchWhen(r *index.Record, orEmpty bool, kinds ...string) string {
	if !momentOK(r, orEmpty, kinds...) {
		return "when.moment 에 " + strings.Join(kinds, "|") + " 가 없음"
	}
	for _, k := range []string{"repo", "path", "agent"} {
		vs := r.When[k]
		if len(vs) == 0 {
			continue
		}
		val := map[string]string{"repo": s.m.Repo, "path": s.m.Path, "agent": s.m.Agent}[k]
		if val == "" || !slices.ContainsFunc(vs, func(v string) bool {
			if k == "agent" {
				return v == val
			}
			ok, _ := path.Match(v, val)
			return ok
		}) {
			return "when." + k + " 조건이 맞지 않음"
		}
	}
	return ""
}

// noActionMatch 는 동작 조건이 없는 항목만 맞게 하는 일치 함수다(동작이 없는 순간용).
func (s *sel) noActionMatch(orEmpty bool, kinds ...string) func(*index.Record) string {
	return func(r *index.Record) string {
		if len(r.When["action"]) > 0 {
			return "동작 조건이 있는 항목은 이 순간에 주지 않음"
		}
		return s.matchWhen(r, orEmpty, kinds...)
	}
}

// tokens 는 글의 토큰 수를 (UTF-8 바이트 수 + 2) / 3 으로 어림한다.
func tokens(s string) int { return (len(s) + 2) / 3 }

// fit 은 예산과 형태 단계다. 상한을 넘으면 필수 항목을 먼저 남기고 같은 종류 안에서는 원래 순서대로 남긴다.
func (s *sel) fit(res *Result) {
	budget := s.cfg.Budgets[s.m.Kind]
	texts := make([]string, len(s.cands))
	for i, c := range s.cands {
		texts[i] = strings.TrimSpace(c.r.Text)
		if !c.must {
			texts[i] += " (" + c.r.ID + ")"
		}
	}
	keep := make([]bool, len(s.cands))
	used := 0
	for _, must := range []bool{true, false} {
		for i, c := range s.cands {
			if c.must != must {
				continue
			}
			if t := tokens(texts[i]); used+t <= budget {
				keep[i], used = true, used+t
			} else {
				res.Drops = append(res.Drops, Drop{c.r.ID, "예산", fmt.Sprintf("%s 순간의 상한 %d토큰을 넘음", s.m.Kind, budget)})
			}
		}
	}
	for i, c := range s.cands {
		if keep[i] {
			res.Injections = append(res.Injections, Injection{c.r.ID, texts[i], c.must, c.why})
		}
	}
}
