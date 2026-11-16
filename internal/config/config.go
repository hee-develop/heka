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
}

// Config 는 읽어 둔 설정이다. 빈 값이 유효한 설정이다.
type Config struct {
	Scopes         map[string]item.List `yaml:"scopes"`
	GlobalScopes   item.List            `yaml:"global_scopes"`
	Actions        map[string]Action    `yaml:"actions"`
	Negations      []string             `yaml:"negations"`
	NegationWindow int                  `yaml:"negation_window"`
	Budgets        map[string]int       `yaml:"budgets"`
	Judge          Judge                `yaml:"judge"`
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
}

// 도구가 만든 프롬프트의 머리와 세션 허용 표현의 기본값이다.
var (
	DefaultToolPromptPrefixes = []string{"<task-notification", "<agent-message"}
	DefaultSessionGrant       = []string{`이번\s*세션`, `세션\s*동안`, `앞으로\s*계속`, `for\s+this\s+session`}
)

// DefaultBudgets 는 순간별 토큰 상한의 기본값이다.
var DefaultBudgets = map[string]int{"session_start": 1000, "prompt": 300, "tool": 300, "subagent_start": 300}

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
	for k, v := range c.Budgets {
		if _, ok := DefaultBudgets[k]; !ok || v < 0 {
			return fmt.Errorf("budgets.%s: 순간 이름(session_start|prompt|tool|subagent_start)과 0 이상의 값이어야 함", k)
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
