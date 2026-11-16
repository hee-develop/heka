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
}

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
		j.Timeout = 10 * time.Second
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
	return v
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
