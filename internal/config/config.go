// Package config 는 $HEKA_HOME/config.yaml 을 읽는다. 모르는 최상위 키는 무시한다.
package config

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hee-develop/heka/internal/item"
	"go.yaml.in/yaml/v3"
)

// defaultHomeDir 는 $HEKA_HOME 이 없을 때 $HOME 아래에 쓰는 폴더 이름이다.
// 잠정 기본값이며 확정 전이다.
const defaultHomeDir = ".heka"

// Home 은 데이터 폴더를 돌려준다. 환경 변수 HEKA_HOME 이 우선이다.
func Home() (string, error) {
	if h := os.Getenv("HEKA_HOME"); h != "" {
		return h, nil
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("데이터 폴더를 정할 수 없음: %w", err)
	}
	return filepath.Join(u, defaultHomeDir), nil
}

// Action 은 동작 하나의 설정이다.
type Action struct {
	In      item.List `yaml:"in"`
	Phrases []string  `yaml:"phrases"`
}

// Judge 는 판정 명령의 설정이다. Command 가 비어 있으면 판정 명령이 없는 것이다.
type Judge struct {
	Command     []string      `yaml:"command"`
	Timeout     time.Duration `yaml:"timeout"`
	Candidates  int           `yaml:"candidates"`
	Votes       int           `yaml:"votes"`
	Instruction string        `yaml:"instruction"`
	Pick        Pick          `yaml:"pick"`
}

// Pick 은 모델이 항목 목록에서 후보를 고르는 갈래의 설정이다. Mode 는 off(빈 값 포함) 또는 record 다.
// record 는 고른 결과를 기록만 하고 무엇을 넣을지에는 쓰지 않는다.
// Command 가 비면 Judge.Command 를, Timeout 이 0 이면 Judge.Timeout 을 쓴다. Max 는 한 건씩 판정에 올릴 최대 수다.
type Pick struct {
	Mode        string        `yaml:"mode"`
	Command     []string      `yaml:"command"`
	Timeout     time.Duration `yaml:"timeout"`
	Max         int           `yaml:"max"`
	Instruction string        `yaml:"instruction"`
}

// PickDefaultMax 는 pick.max 가 0 일 때 쓰는 값이다.
const PickDefaultMax = 8

// PickCommand 는 고르는 호출에 쓸 명령이다. pick.command 가 비면 판정 명령이다.
func (j Judge) PickCommand() []string {
	if len(j.Pick.Command) > 0 {
		return j.Pick.Command
	}
	return j.Command
}

// PickTimeout 은 고르는 호출의 시간 상한이다. pick.timeout 이 0 이면 judge.timeout 이다.
func (j Judge) PickTimeout() time.Duration {
	if j.Pick.Timeout > 0 {
		return j.Pick.Timeout
	}
	return j.Timeout
}

// PickRecording 은 기록 갈래를 돌릴 설정인가. mode 가 record 이고 쓸 명령이 있어야 한다.
func (j Judge) PickRecording() bool {
	return j.Pick.Mode == "record" && len(j.PickCommand()) > 0
}

// RegisterJudge 는 등록 뒤의 중복·모순 판정 명령의 설정이다. Command 가 비어 있으면 판정 명령이 없는 것이다.
// Instruction 이 비면 DefaultRegisterJudgeInstruction 을 쓴다.
type RegisterJudge struct {
	Command     []string      `yaml:"command"`
	Timeout     time.Duration `yaml:"timeout"`
	Candidates  int           `yaml:"candidates"`
	Instruction string        `yaml:"instruction"`
}

// Review 는 검토 목록과 알림의 설정이다. NotifyCommand 는 알림 글을 마지막 인자로 덧붙여 부르는 명령이다.
type Review struct {
	NotifyCommand []string `yaml:"notify_command"`
	NotifyMin     int      `yaml:"notify_min"`
	NotifyDays    int      `yaml:"notify_days"`
	Sample        int      `yaml:"sample"`
	Batch         int      `yaml:"batch"`
}

// DefaultRegisterJudgeInstruction 은 register_judge.instruction 이 비었을 때 쓰는 지시문이다.
// 측정 도구가 같은 글로 재므로 바꾸면 config.example.yaml 의 글도 함께 바꾼다.
const DefaultRegisterJudgeInstruction = `너는 개인 지식 저장소의 중복·모순 판정기다. 방금 등록된 [새 항목] 하나와, 주제어나 낱말이 겹쳐 후보가 된 기존 항목들([후보 1], [후보 2] …)이 주어진다. 후보는 주제어 하나(프로젝트 이름 같은 것)가 같다는 이유만으로 잡힌 것이 많아서 대부분 새 항목과 다른 내용이다. 각 항목은 AI 에이전트에게 주입되는 지식이며, 주입문(text)이 실제로 주입되는 글이고 본문(body)은 배경이다.

후보마다 아래 셋 중 하나로 판정한다.

- separate: 다루는 대상이나 지시가 다르다. 같은 프로젝트·같은 주제에 속해도 서로 다른 사실·결정·규칙이면 separate 다. 같은 주제의 다른 규칙(형식, 문체, 무엇을 지적할지)은 separate 다. 한쪽을 읽어도 다른 쪽이 알려 주는 것을 알 수 없으면 separate 다.
- merge: 같은 사실·결정·규칙을 말하거나, 한쪽이 다른 쪽을 품거나, 새 항목이 기존 항목의 같은 규칙을 보강한 것이다. 같은 이름의 규칙이나 같은 모듈(예: 같은 'Pn룰', 같은 mod)에 대해 빠진 갈래나 적용 단계를 채우는 것도 merge 다. 둘이 함께 있으면 같은 내용이 두 번 주입되거나 낡은 판이 함께 주입된다.
- conflict: 같은 상황에 대해 서로 반대로 지시하거나 양립할 수 없는 사실을 말한다. 새 항목이 기존 항목과 같은 규칙을 다루면서 그 지시의 일부를 반대로 바꾸면(예: 'LGTM' 을 'from Claude: LGTM' 으로) conflict 다. 새 항목이 기존 결정을 뒤집은 경우도 여기에 든다. 지시를 바꾸지 않고 덧붙이거나 근거만 보태면 merge 다.

판단 기준:
- 주제어나 프로젝트가 같다는 것만으로는 merge 가 아니다. 주입문이 같은 행동이나 같은 사실을 가리키는지를 본다.
- 확신이 없으면 separate 를 고른다. separate 는 아무것도 바꾸지 않고, 일부는 사람이 나중에 표본으로 본다.
- merge 일 때는 두 항목을 하나로 합친 초안을 쓴다. text 는 한두 문장이고 이것만 읽어도 행동할 수 있어야 한다. 두 항목의 지시를 하나도 빠뜨리지 않고, 새 항목이 새로 보탠 지시는 text 에도 넣는다. body 는 두 본문의 '## ' 절을 하나도 빼지 않는다. 한쪽에만 있는 절(검사, 적용, 왜, 예시)은 그대로 옮긴다. about 은 두 주제어를 합치고 겹친 것을 뺀다.

출력은 JSON 하나만 쓴다. 다른 글, 코드 울타리(` + "`" + `` + "`" + `` + "`" + `)는 쓰지 않는다. 후보마다 한 건씩, 주어진 순서대로 쓴다. verdict 가 merge 면 merged 를 반드시 쓴다.
{"verdicts":[{"id":"후보 id","verdict":"separate|merge|conflict","reason":"한 문장 이유","merged":{"text":"합친 주입문","body":"합친 본문","about":["주제어"]}}]}
merged 는 verdict 가 merge 일 때만 쓴다.`

// Config 는 읽어 둔 설정이다. 빈 값이 유효한 설정이다.
type Config struct {
	Scopes         map[string]item.List `yaml:"scopes"`
	GlobalScopes   item.List            `yaml:"global_scopes"`
	Actions        map[string]Action    `yaml:"actions"`
	Negations      []string             `yaml:"negations"`
	NegationWindow int                  `yaml:"negation_window"`
	Budgets        map[string]int       `yaml:"budgets"`
	Judge          Judge                `yaml:"judge"`
	RegisterJudge  RegisterJudge        `yaml:"register_judge"`
	Review         Review               `yaml:"review"`
	ToolActions    map[string][]string  `yaml:"tool_actions"`
	Locations      []Location           `yaml:"locations"`
	ClaudeCode     ClaudeCode           `yaml:"claude_code"`
	SessionGrant   []string             `yaml:"session_grant"`
}

// Location 은 작업 위치 하나와 그 범위다. Path 끝에 *를 쓰면 이름이 그 앞부분으로 시작하는 폴더들이다.
type Location struct {
	Path   string    `yaml:"path"`
	Scopes item.List `yaml:"scopes"`
	Repo   string    `yaml:"repo"` // when.repo 와 견줄 이름. 없으면 path 의 마지막 이름
}

// ClaudeCode 는 Claude Code 연결부의 설정이다.
type ClaudeCode struct {
	Scopes             item.List `yaml:"scopes"`               // 이 연결부로 들어온 순간에 늘 붙는 범위
	ToolPromptPrefixes []string  `yaml:"tool_prompt_prefixes"` // 이걸로 시작하는 프롬프트는 도구가 만든 것
	Transcripts        []string  `yaml:"transcripts"`          // 인용을 대조할 때 더 찾아볼 세션 기록 파일의 glob 목록(** 사용 가능)
	Shadow             bool      `yaml:"shadow"`               // 그림자 방식: 똑같이 고르고 기록하되 아무것도 내보내지 않는다(환경 변수 HEKA_SHADOW=1 과 같다)
}

// 도구가 만든 프롬프트의 머리와 세션 허용 표현의 기본값이다.
var (
	DefaultToolPromptPrefixes = []string{"<task-notification", "<agent-message"}
	DefaultSessionGrant       = []string{`이번\s*세션`, `세션\s*동안`, `앞으로\s*계속`, `for\s+this\s+session`}
)

// DefaultBudgets 는 순간별 토큰 상한의 기본값이다. 0 은 제한 없음이다.
// 훅의 순간(session_start|prompt|tool|subagent_start)은 기본이 제한 없음이고, find 는 조회(heka find, MCP)의 상한이다.
var DefaultBudgets = map[string]int{"session_start": 0, "prompt": 0, "tool": 0, "subagent_start": 0, "find": 1500}

// Load 는 home/config.yaml 을 읽는다. 파일이 없으면 빈 설정이다.
// 범위 포함 관계에 순환이 있으면 오류다.
func Load(home string) (*Config, error) {
	c := &Config{}
	b, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err := yaml.Unmarshal(b, c); err != nil {
			return nil, fmt.Errorf("config.yaml 해석 실패: %w", err)
		}
	}
	if err := c.checkScopeCycle(); err != nil {
		return nil, err
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// validate 는 정규식과 숫자를 검사하고 빠진 값에 기본값을 채운다.
func (c *Config) validate() error {
	for _, a := range slices.Sorted(maps.Keys(c.Actions)) {
		for _, p := range c.Actions[a].Phrases {
			if _, err := regexp.Compile(p); err != nil {
				return fmt.Errorf("actions.%s.phrases 정규식이 컴파일되지 않음: %w", a, err)
			}
		}
	}
	for _, n := range c.Negations {
		if _, err := regexp.Compile(n); err != nil {
			return fmt.Errorf("negations 정규식이 컴파일되지 않음: %w", err)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(c.ToolActions)) {
		if strings.TrimSpace(k) == "" || len(c.ToolActions[k]) == 0 || slices.Contains(c.ToolActions[k], "") {
			return fmt.Errorf("tool_actions[%q]: 키와 동작 이름은 비어 있으면 안 되고 동작 이름이 하나 이상 있어야 함", k)
		}
	}
	for i, l := range c.Locations {
		if strings.TrimSpace(l.Path) == "" {
			return fmt.Errorf("locations[%d].path 가 비어 있음", i)
		}
	}
	for _, p := range c.SessionGrant {
		if _, err := regexp.Compile(p); err != nil {
			return fmt.Errorf("session_grant 정규식이 컴파일되지 않음: %w", err)
		}
	}
	for i, g := range c.ClaudeCode.Transcripts {
		if strings.TrimSpace(g) == "" {
			return fmt.Errorf("claude_code.transcripts[%d] 가 비어 있음", i)
		}
	}
	if c.ClaudeCode.ToolPromptPrefixes == nil {
		c.ClaudeCode.ToolPromptPrefixes = DefaultToolPromptPrefixes
	}
	if c.SessionGrant == nil {
		c.SessionGrant = DefaultSessionGrant
	}
	j := &c.Judge
	if c.NegationWindow < 0 || j.Timeout < 0 || j.Candidates < 0 || j.Votes < 0 || j.Votes > 2 {
		return fmt.Errorf("negation_window, judge.timeout, judge.candidates 는 0 이상, judge.votes 는 1 또는 2 여야 함")
	}
	if len(j.Command) > 0 && strings.TrimSpace(j.Instruction) == "" {
		return fmt.Errorf("judge.command 가 있으면 judge.instruction 도 있어야 함")
	}
	switch j.Pick.Mode {
	case "", "off", "record":
	default:
		return fmt.Errorf("judge.pick.mode 는 off 또는 record 여야 함: %q", j.Pick.Mode)
	}
	if j.Pick.Timeout < 0 || j.Pick.Max < 0 {
		return fmt.Errorf("judge.pick.timeout, judge.pick.max 는 0 이상이어야 함")
	}
	if j.Pick.Mode == "record" {
		if strings.TrimSpace(j.Pick.Instruction) == "" {
			return fmt.Errorf("judge.pick.mode 가 record 이면 judge.pick.instruction 도 있어야 함")
		}
		if len(j.PickCommand()) == 0 {
			return fmt.Errorf("judge.pick.mode 가 record 이면 judge.pick.command 또는 judge.command 가 있어야 함")
		}
	}
	rj := &c.RegisterJudge
	if rj.Timeout < 0 || rj.Candidates < 0 {
		return fmt.Errorf("register_judge.timeout, register_judge.candidates 는 0 이상이어야 함")
	}
	rv := &c.Review
	if rv.NotifyMin < 0 || rv.NotifyDays < 0 || rv.Sample < 0 || rv.Batch < 0 {
		return fmt.Errorf("review.notify_min, review.notify_days, review.sample, review.batch 는 0 이상이어야 함")
	}
	for k, v := range c.Budgets {
		if _, ok := DefaultBudgets[k]; !ok || v < 0 {
			return fmt.Errorf("budgets.%s: 순간 이름(session_start|prompt|tool|subagent_start|find)과 0 이상의 값이어야 함", k)
		}
	}
	if c.NegationWindow == 0 {
		c.NegationWindow = 8
	}
	if j.Timeout == 0 {
		j.Timeout = 15 * time.Second
	}
	if j.Candidates == 0 {
		j.Candidates = 3
	}
	if j.Votes == 0 {
		j.Votes = 1
	}
	if j.Pick.Max == 0 {
		j.Pick.Max = PickDefaultMax
	}
	if rj.Timeout == 0 {
		rj.Timeout = 120 * time.Second
	}
	if rj.Candidates == 0 {
		rj.Candidates = 5
	}
	if strings.TrimSpace(rj.Instruction) == "" {
		rj.Instruction = DefaultRegisterJudgeInstruction
	}
	if rv.NotifyMin == 0 {
		rv.NotifyMin = 5
	}
	if rv.NotifyDays == 0 {
		rv.NotifyDays = 7
	}
	if rv.Sample == 0 {
		rv.Sample = 3
	}
	if rv.Batch == 0 {
		rv.Batch = 5
	}
	b := maps.Clone(DefaultBudgets)
	maps.Copy(b, c.Budgets)
	c.Budgets = b
	return nil
}

func (c *Config) checkScopeCycle() error {
	state := map[string]int{} // 1 방문 중, 2 끝
	var visit func(s string, path []string) error
	visit = func(s string, path []string) error {
		switch state[s] {
		case 1:
			return fmt.Errorf("범위 포함 관계에 순환이 있음: %s", strings.Join(append(path, s), " -> "))
		case 2:
			return nil
		}
		state[s] = 1
		for _, p := range c.Scopes[s] {
			if err := visit(p, append(path, s)); err != nil {
				return err
			}
		}
		state[s] = 2
		return nil
	}
	for _, k := range slices.Sorted(maps.Keys(c.Scopes)) {
		if err := visit(k, nil); err != nil {
			return err
		}
	}
	return nil
}

// ScopeClosure 는 범위와 그것을 포함하는 모든 범위(자기 자신 포함)를 정렬해 돌려준다.
func (c *Config) ScopeClosure(scope string) []string {
	return closure(scope, func(s string) []string { return c.Scopes[s] })
}

// ActionClosure 는 동작과 그 상위 동작(자기 자신 포함)을 정렬해 돌려준다.
// 상위는 설정의 in 과 점으로 끊은 앞부분 두 가지다. 설정에 없는 이름도 점 규칙으로 계산한다.
func (c *Config) ActionClosure(action string) []string {
	return closure(action, func(a string) []string {
		up := append([]string{}, c.Actions[a].In...)
		if i := strings.LastIndex(a, "."); i > 0 {
			up = append(up, a[:i])
		}
		return up
	})
}

// ActionVocab 은 설정이 아는 동작 이름 전체다(actions 의 키, in 의 값, 키의 점 앞부분).
func (c *Config) ActionVocab() map[string]bool {
	v := map[string]bool{}
	for k, a := range c.Actions {
		for _, n := range c.ActionClosure(k) {
			v[n] = true
		}
		for _, p := range a.In {
			v[p] = true
		}
	}
	for _, names := range c.ToolActions {
		for _, n := range names {
			for _, x := range c.ActionClosure(n) {
				v[x] = true
			}
		}
	}
	return v
}

// ToolActionsFor 는 키(도구 이름 또는 "Skill:<스킬 이름>") 중 하나라도 tool_actions 의 키 패턴에 맞으면
// 그 동작 이름들을 돌려준다. 패턴의 *는 임의의 글자열이다. 설정 키의 정렬 순서를 따르고 중복은 뺀다.
func (c *Config) ToolActionsFor(keys ...string) []string {
	var out []string
	for _, pat := range slices.Sorted(maps.Keys(c.ToolActions)) {
		if !slices.ContainsFunc(keys, func(k string) bool { return globMatch(pat, k) }) {
			continue
		}
		for _, n := range c.ToolActions[pat] {
			if !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	return out
}

// globMatch 는 pat 의 *를 임의의 글자열로 보고 s 전체가 맞는지 본다.
func globMatch(pat, s string) bool {
	head, rest, found := strings.Cut(pat, "*")
	if !found {
		return pat == s
	}
	if !strings.HasPrefix(s, head) {
		return false
	}
	s = s[len(head):]
	for i := 0; i <= len(s); i++ {
		if globMatch(rest, s[i:]) {
			return true
		}
	}
	return false
}

// Locate 는 작업 폴더에 가장 길게 맞는 위치의 범위와 저장소 이름을 돌려준다. 맞는 위치가 없으면 빈 값이다.
// path 앞의 ~ 는 홈 폴더로 풀고, 끝의 *는 이름이 그 앞부분으로 시작하는 폴더들을 뜻한다.
func (c *Config) Locate(cwd string) (scopes []string, repo string) {
	cwd = filepath.Clean(cwd)
	home, _ := os.UserHomeDir()
	best := -1
	for _, l := range c.Locations {
		p, star := strings.CutSuffix(expandHome(l.Path, home), "*")
		end := 0 // cwd[:end] 가 맞은 폴더다
		if star {
			if !strings.HasPrefix(cwd, p) {
				continue
			}
			end = len(p) + strings.IndexByte(cwd[len(p):]+"/", '/')
		} else {
			p = filepath.Clean(p)
			if cwd != p && !strings.HasPrefix(cwd, p+"/") {
				continue
			}
			end = len(p)
		}
		if end > best {
			best, scopes, repo = end, l.Scopes, l.Repo
			if repo == "" {
				repo = filepath.Base(cwd[:end])
			}
		}
	}
	return scopes, repo
}

func expandHome(p, home string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return home + p[1:]
	}
	return p
}

// ScopeNames 는 설정에 나오는 모든 범위 이름이다.
func (c *Config) ScopeNames() []string {
	set := map[string]bool{}
	for k, vs := range c.Scopes {
		set[k] = true
		for _, v := range vs {
			set[v] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// Phrases 는 동작 낱말 사전을 (동작, 표현) 쌍으로 돌려준다.
func (c *Config) Phrases() [][2]string {
	var out [][2]string
	for _, a := range slices.Sorted(maps.Keys(c.Actions)) {
		for _, p := range c.Actions[a].Phrases {
			out = append(out, [2]string{a, p})
		}
	}
	return out
}

func closure(start string, up func(string) []string) []string {
	seen := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, p := range up(n) {
			if !seen[p] {
				seen[p] = true
				queue = append(queue, p)
			}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}
