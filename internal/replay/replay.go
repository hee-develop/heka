// Package replay 는 기록해 둔 순간들을 선별기에 다시 넣어 성공 조건을 잰다.
// 실사용 경로(훅)와 무관하고 세션 상태와 원장을 쓰지 않는다. 색인과 설정은 읽기만 한다.
package replay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hee-develop/heka/internal/action"
	"github.com/hee-develop/heka/internal/claudecode"
	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/index"
	"github.com/hee-develop/heka/internal/selector"
)

// list 는 문자열 하나 또는 문자열 배열을 모두 받는다.
type list []string

func (l *list) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*l = list{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*l = many
	return nil
}

// ToolCall 은 도구 호출 하나다.
type ToolCall struct {
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// MomentSpec 은 사례에 적힌 순간이다.
type MomentSpec struct {
	Kind   string    `json:"kind"`
	Text   string    `json:"text"`
	Scope  list      `json:"scope"`
	Scopes list      `json:"scopes"`
	Repo   string    `json:"repo"`
	Agent  string    `json:"agent"`
	Path   string    `json:"path"`
	Cwd    string    `json:"cwd"`
	Action list      `json:"action"`
	Tool   *ToolCall `json:"tool"`
}

// Expect 는 사례의 정답이다. 모르는 키는 무시한다.
type Expect struct {
	Inject        []string `json:"inject"`
	NotInject     []string `json:"not_inject"`
	Silent        bool     `json:"silent"`
	AlsoInject    []string `json:"also_inject"`
	AlsoNotInject []string `json:"also_not_inject"`
	Unsure        []string `json:"unsure"`
}

// Case 는 재현 사례 하나다.
type Case struct {
	ID        string     `json:"id"`
	Moment    MomentSpec `json:"moment"`
	Expect    Expect     `json:"expect"`
	Confirmed *bool      `json:"confirmed"`
	Source    string     `json:"source"`
}

// Skip 은 읽지 못해 건너뛴 줄이다.
type Skip struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

var kinds = []string{"prompt", "tool", "session_start", "subagent_start"}

// Load 는 JSONL 에서 사례를 읽는다. 깨진 줄, id 가 없는 줄, 모르는 순간 종류의 줄은 건너뛰고 Skip 으로 알린다.
// 빈 줄은 세지 않는다.
func Load(r io.Reader) ([]Case, []Skip, error) {
	br := bufio.NewReaderSize(r, 1<<16)
	var cases []Case
	var skips []Skip
	seen := map[string]bool{}
	for n := 1; ; n++ {
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var c Case
			switch jerr := json.Unmarshal(line, &c); {
			case jerr != nil:
				skips = append(skips, Skip{n, "JSON 을 읽지 못함"})
			case c.ID == "":
				skips = append(skips, Skip{n, "id 가 없음"})
			case !slices.Contains(kinds, c.Moment.Kind):
				skips = append(skips, Skip{n, fmt.Sprintf("moment.kind %q 를 모름", c.Moment.Kind)})
			case seen[c.ID]:
				skips = append(skips, Skip{n, fmt.Sprintf("id %q 가 겹침", c.ID)})
			default:
				seen[c.ID] = true
				cases = append(cases, c)
			}
		}
		if err != nil {
			if err != io.EOF {
				return cases, skips, err
			}
			return cases, skips, nil
		}
	}
}

// Options 는 돌리는 방법이다.
type Options struct {
	Runs     int    // 전체를 몇 번 돌리는가(1 이상)
	Parallel int    // 사례를 동시에 몇 개 돌리는가(1 이상)
	NoJudge  bool   // 판정 명령을 쓰지 않는다
	Today    string // YYYY-MM-DD. 비면 오늘
}

// InjectedItem 은 넣은 항목과 이유다.
type InjectedItem struct {
	ID   string `json:"id"`
	Why  string `json:"why"`
	Must bool   `json:"must"`
}

// CaseResult 는 사례 하나를 한 회차 돌린 결과다(--out 의 한 줄).
type CaseResult struct {
	Run        int             `json:"run"`
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Pass       bool            `json:"pass"` // 정답과 어긋남이 없는가
	Injected   []InjectedItem  `json:"injected"`
	Dropped    []selector.Drop `json:"dropped"`
	Missing    []string        `json:"missing"`   // 넣어야 했는데 빠진 항목
	Wrong      []string        `json:"wrong"`     // 넣으면 안 되는데 넣은 항목
	Unsure     []string        `json:"unsure"`    // 모르는 것으로 센 항목
	Unlabeled  []string        `json:"unlabeled"` // 정답이 없는 항목
	Judged     bool            `json:"judged"`    // 판정 명령을 불렀는가
	DurationMS int64           `json:"duration_ms"`
	Tokens     int             `json:"tokens"`
	Error      string          `json:"error,omitempty"`
	Confirmed  *bool           `json:"confirmed,omitempty"`
	Source     string          `json:"source,omitempty"`

	delivery bool // inject 가 있는 사례인가
	must     bool // 그중 필수 항목이 대상인가
	silent   bool
}

func (c *CaseResult) injectedIDs() []string {
	ids := make([]string, len(c.Injected))
	for i, x := range c.Injected {
		ids[i] = x.ID
	}
	return ids
}

// Run 은 사례 전체를 opt.Runs 번 돌려 회차마다의 사례별 결과를 돌려준다. 사례는 opt.Parallel 개씩 동시에 돌린다.
// 사례마다 세션은 새로 시작한 것이다(이미 넣은 항목 없음).
func Run(ctx context.Context, ix *index.Index, cfg *config.Config, cases []Case, opt Options) [][]CaseResult {
	opt.Runs, opt.Parallel = max(opt.Runs, 1), max(opt.Parallel, 1)
	if opt.Today == "" {
		opt.Today = time.Now().Format("2006-01-02")
	}
	c := *cfg
	if opt.NoJudge {
		c.Judge.Command = nil
	}
	runs := make([][]CaseResult, opt.Runs)
	for r := range runs {
		out := make([]CaseResult, len(cases))
		sem := make(chan struct{}, opt.Parallel)
		var wg sync.WaitGroup
		for i := range cases {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				out[i] = runCase(ctx, ix, &c, cases[i], opt.Today)
				out[i].Run = r + 1
			}()
		}
		wg.Wait()
		runs[r] = out
	}
	return runs
}

// moment 는 사례의 순간을 선별기의 순간으로 바꾼다. 실사용 경로와 같이 설정의 연결부 범위를 더한다.
func moment(cfg *config.Config, s MomentSpec, today string) selector.Moment {
	scopes := append(append(append([]string{}, s.Scope...), s.Scopes...), cfg.ClaudeCode.Scopes...)
	m := selector.Moment{Kind: s.Kind, Scopes: scopes, Text: s.Text, Repo: s.Repo, Agent: s.Agent, Path: s.Path, Seen: map[string]bool{}, Today: today}
	if s.Kind == "prompt" {
		text := strings.TrimSpace(s.Text)
		for _, p := range cfg.ClaudeCode.ToolPromptPrefixes {
			if p != "" && strings.HasPrefix(text, p) {
				m.ToolMade = true
			}
		}
	}
	return m
}

// selectTool 은 도구 순간을 고른다. 동작·경로마다 모은 후보를 한데 모아 예산을 한 번만 적용한다(훅과 같다).
func selectTool(ctx context.Context, ix *index.Index, cfg *config.Config, s MomentSpec, base selector.Moment) (selector.Result, error) {
	actions, paths := []string(s.Action), []string{}
	if len(actions) == 0 && s.Tool != nil {
		r := action.Resolve(action.Call{Tool: s.Tool.Name, Input: s.Tool.Input, Cwd: s.Cwd}, cfg)
		actions, paths = r.Actions, r.Paths
	}
	if s.Path != "" {
		paths = []string{s.Path}
	}
	if len(paths) == 0 {
		paths = []string{""}
	}
	res := selector.Result{Injections: []selector.Injection{}, Drops: []selector.Drop{}, Asked: []string{}, Refused: []string{}}
	byID := map[string]bool{}
	var drops []selector.Drop
	for _, a := range actions {
		for _, p := range paths {
			m := base
			m.Action, m.Path = a, p
			g, err := selector.Gather(ctx, ix, cfg, m)
			if err != nil {
				return res, err
			}
			drops = append(drops, g.Drops...)
			for _, inj := range g.Injections {
				if !byID[inj.ID] {
					byID[inj.ID] = true
					res.Injections = append(res.Injections, inj)
				}
			}
		}
	}
	seen := map[selector.Drop]bool{}
	var kept []selector.Drop
	for _, d := range drops {
		if !byID[d.ID] && !seen[d] {
			seen[d] = true
			kept = append(kept, d)
		}
	}
	res.Injections, res.Drops = selector.Fit(cfg, "tool", res.Injections, kept)
	if res.Drops == nil {
		res.Drops = []selector.Drop{}
	}
	return res, nil
}

func has(xs []string, x string) bool { return slices.Contains(xs, x) }

func runCase(ctx context.Context, ix *index.Index, cfg *config.Config, c Case, today string) CaseResult {
	cr := CaseResult{ID: c.ID, Kind: c.Moment.Kind, Injected: []InjectedItem{}, Dropped: []selector.Drop{},
		Missing: []string{}, Wrong: []string{}, Unsure: []string{}, Unlabeled: []string{}, Confirmed: c.Confirmed, Source: c.Source}
	m := moment(cfg, c.Moment, today)
	start := time.Now()
	var res selector.Result
	var err error
	if c.Moment.Kind == "tool" {
		res, err = selectTool(ctx, ix, cfg, c.Moment, m)
	} else {
		res, err = selector.Select(ctx, ix, cfg, m)
	}
	cr.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		cr.Error = err.Error()
	} else {
		cr.Judged = len(res.Judges) > 0
		cr.Dropped = res.Drops
		for _, i := range res.Injections {
			cr.Injected = append(cr.Injected, InjectedItem{ID: i.ID, Why: i.Why, Must: i.Must})
		}
		if len(res.Injections) > 0 {
			cr.Tokens = selector.Tokens(claudecode.ContextText(res.Injections))
		}
	}
	score(&cr, c.Expect, ix)
	return cr
}

// score 는 넣은 항목을 정답과 견준다. 모르는 것(unsure)이 먼저, 다음이 맞음(inject·also_inject),
// 다음이 틀림(not_inject·also_not_inject, 또는 silent 사례의 나머지 전부)이고, 어디에도 없으면 정답 없음이다.
func score(cr *CaseResult, e Expect, ix *index.Index) {
	got := cr.injectedIDs()
	cr.silent = e.Silent
	cr.delivery = len(e.Inject) > 0
	for _, id := range e.Inject {
		if !has(got, id) {
			cr.Missing = append(cr.Missing, id)
		}
		if r, err := ix.Item(id); err == nil && r.Must {
			cr.must = true
		}
	}
	for _, id := range got {
		switch {
		case has(e.Unsure, id):
			cr.Unsure = append(cr.Unsure, id)
		case has(e.Inject, id) || has(e.AlsoInject, id):
		case has(e.NotInject, id) || has(e.AlsoNotInject, id) || e.Silent:
			cr.Wrong = append(cr.Wrong, id)
		default:
			cr.Unlabeled = append(cr.Unlabeled, id)
		}
	}
	cr.Pass = cr.Error == "" && len(cr.Missing) == 0 && len(cr.Wrong) == 0 && !(e.Silent && len(got) > 0)
}
