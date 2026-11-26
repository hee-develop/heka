// Package item 은 항목 파일(머리말 YAML + 본문 마크다운)을 읽고 검증한다.
package item

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// 수준은 문제의 무게다. error 가 있는 항목은 색인에 넣지 않는다.
const (
	Error = "error"
	Warn  = "warn"
)

// Problem 은 검증에서 나온 문제 하나다.
type Problem struct {
	File  string // 파일 이름(폴더 제외)
	Level string
	Msg   string
}

func (p Problem) String() string { return p.File + ": " + p.Level + ": " + p.Msg }

// List 는 문자열 하나로도 목록으로도 쓸 수 있는 YAML 값이다. 읽으면 항상 목록이 된다.
type List []string

func (l *List) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value != "" {
			*l = List{n.Value}
		}
		return nil
	case yaml.SequenceNode:
		var s []string
		if err := n.Decode(&s); err != nil {
			return err
		}
		*l = s
		return nil
	}
	return fmt.Errorf("문자열 또는 목록이어야 함 (줄 %d)", n.Line)
}

// text 는 YAML 에 쓴 모양 그대로의 글을 받는 값이다. 날짜와 confirm(bool 또는 문자열)에 쓴다.
type text string

func (t *text) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("값 하나여야 함 (줄 %d)", n.Line)
	}
	*t = text(n.Value)
	return nil
}

// When 은 구조로 판정하는 조건이다.
type When struct {
	Moment List `yaml:"moment" json:"moment,omitempty"`
	Action List `yaml:"action" json:"action,omitempty"`
	Path   List `yaml:"path" json:"path,omitempty"`
	Repo   List `yaml:"repo" json:"repo,omitempty"`
	Agent  List `yaml:"agent" json:"agent,omitempty"`
}

// Kinds 는 item_when 표에 넣는 (종류, 값) 쌍이다.
func (w When) Kinds() [][2]string {
	var out [][2]string
	for _, k := range []struct {
		kind string
		vs   List
	}{{"moment", w.Moment}, {"action", w.Action}, {"path", w.Path}, {"repo", w.Repo}, {"agent", w.Agent}} {
		for _, v := range k.vs {
			out = append(out, [2]string{k.kind, v})
		}
	}
	return out
}

// Check 는 결과물 검사다.
type Check struct {
	Target     string `yaml:"target" json:"target"`
	Pattern    string `yaml:"pattern" json:"pattern,omitempty"`
	NotPattern string `yaml:"not_pattern" json:"not_pattern,omitempty"`
	Mode       string `yaml:"mode" json:"mode,omitempty"`
	// Evidence 는 있으면, 검사가 Fail 일 때 이번 턴에 앞서 한 도구 호출 기록 중 하나라도 이 식에 맞으면 Pass 로 바꾸는 식이다.
	Evidence string `yaml:"evidence" json:"evidence,omitempty"`
}

// Item 은 항목 파일 하나다.
type Item struct {
	ID      string `yaml:"id"`
	Text    string `yaml:"text"`
	Must    bool   `yaml:"must"`
	When    When   `yaml:"when"`
	About   List   `yaml:"about"`
	Scope   List   `yaml:"scope"`
	Source  string `yaml:"source"`
	Quote   string `yaml:"quote"`
	Checked text   `yaml:"checked"`
	Expires text   `yaml:"expires"`
	Confirm text   `yaml:"confirm"` // false(기본)|true|always
	Check   *Check `yaml:"check"`
	Push    *bool  `yaml:"push"` // 없으면 true. false 면 조회 전용(heka 가 스스로 넣지 않음)

	Body     string    `yaml:"-"`
	Hash     string    `yaml:"-"` // 파일 전체 sha256 의 앞 16자
	File     string    `yaml:"-"` // 파일 경로
	Problems []Problem `yaml:"-"`
}

// Valid 는 error 문제가 없는 항목인가.
func (it *Item) Valid() bool {
	for _, p := range it.Problems {
		if p.Level == Error {
			return false
		}
	}
	return true
}

// Pushes 는 heka 가 스스로 넣을 수 있는 항목인가. push 가 없으면 true 다.
func (it *Item) Pushes() bool { return it.Push == nil || *it.Push }

// ConfirmValue 는 정규화한 confirm 값이다(빈 값은 false).
func (it *Item) ConfirmValue() string {
	if it.Confirm == "" {
		return "false"
	}
	return string(it.Confirm)
}

var (
	idRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	known     = map[string]bool{"id": true, "text": true, "must": true, "when": true, "about": true, "scope": true, "source": true, "quote": true, "checked": true, "expires": true, "confirm": true, "check": true, "push": true}
	knownWhen = map[string]bool{"moment": true, "action": true, "path": true, "repo": true, "agent": true}
	knownChk  = map[string]bool{"target": true, "pattern": true, "not_pattern": true, "mode": true, "evidence": true}
	moments   = map[string]bool{"session_start": true, "prompt": true, "tool": true, "subagent_start": true}
	targets   = map[string]bool{"command": true, "commit_message": true, "pr_body": true, "last_message": true, "question": true}
)

// Parse 는 항목 파일 하나를 읽는다. 읽을 수는 있으나 틀린 내용은 Problems 에 담긴다.
// 파일을 열 수 없을 때만 오류를 돌려준다. 동작 어휘 검사는 하지 않는다(LoadDir).
func Parse(path string) (*Item, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	it := &Item{File: path, Hash: hex.EncodeToString(sum[:])[:16]}
	add := func(level, format string, a ...any) {
		it.Problems = append(it.Problems, Problem{filepath.Base(path), level, fmt.Sprintf(format, a...)})
	}

	head, body, ok := splitFrontMatter(string(raw))
	if !ok {
		add(Error, "머리말(---)이 없음")
		return it, nil
	}
	it.Body = body
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(head), &root); err != nil {
		add(Error, "머리말 YAML 해석 실패: %v", err)
		return it, nil
	}
	if root.Kind == 0 || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		add(Error, "머리말 YAML 해석 실패: 키-값 목록이어야 함")
		return it, nil
	}
	m := root.Content[0]
	if err := m.Decode(it); err != nil {
		add(Error, "머리말 YAML 해석 실패: %v", err)
		return it, nil
	}
	for _, k := range unknownKeys(m, known, "") {
		add(Warn, "모르는 머리말 키: %s", k)
	}
	it.validate(m, add)
	return it, nil
}

func (it *Item) validate(m *yaml.Node, add func(level, format string, a ...any)) {
	stem := strings.TrimSuffix(filepath.Base(it.File), filepath.Ext(it.File))
	switch {
	case it.ID == "":
		add(Error, "id 가 없음")
	case !idRe.MatchString(it.ID):
		add(Error, "id %q 가 ^[a-z0-9][a-z0-9-]*$ 에 맞지 않음", it.ID)
	case it.ID != stem:
		add(Error, "id %q 가 파일 이름 %q 과 다름", it.ID, stem)
	}
	if strings.TrimSpace(it.Text) == "" {
		add(Error, "text 가 비어 있음")
	}
	if it.Source != "user" && it.Source != "doc" && it.Source != "ai" {
		add(Error, "source 는 user|doc|ai 중 하나여야 함: %q", it.Source)
	}
	if len(it.About) == 0 {
		add(Error, "about 이 0개")
	}
	if len(it.Scope) == 0 {
		add(Error, "scope 가 0개")
	}
	if it.Must && len(it.When.Kinds()) == 0 {
		add(Error, "must: true 인데 when 에 조건이 없음")
	}
	for _, v := range it.When.Moment {
		if !moments[v] {
			add(Error, "when.moment 값이 session_start|prompt|tool|subagent_start 밖: %q", v)
		}
	}
	for _, f := range []struct {
		name string
		v    text
	}{{"checked", it.Checked}, {"expires", it.Expires}} {
		if f.v != "" {
			if _, err := time.Parse("2006-01-02", string(f.v)); err != nil {
				add(Error, "%s 는 YYYY-MM-DD 여야 함: %q", f.name, f.v)
			}
		}
	}
	if c := it.ConfirmValue(); c != "false" && c != "true" && c != "always" {
		add(Error, "confirm 은 false|true|always 중 하나여야 함: %q", c)
	}
	if it.Check != nil {
		it.validateCheck(add)
	}
	if !it.Pushes() {
		if it.Must {
			add(Error, "push: false 인데 must: true 가 있음(조회 전용 항목은 필수일 수 없음)")
		}
		if len(it.When.Kinds()) > 0 {
			add(Error, "push: false 인데 when 에 조건이 있음(조회 전용 항목은 순간에 걸 수 없음)")
		}
		if c := it.ConfirmValue(); c == "true" || c == "always" {
			add(Error, "push: false 인데 confirm 이 %s 임(조회 전용 항목은 확인을 요구할 수 없음)", c)
		}
		if it.Check != nil {
			add(Error, "push: false 인데 check 가 있음(조회 전용 항목은 결과물 검사를 할 수 없음)")
		}
	}
	// 중첩 키는 해석이 끝난 뒤에 훑는다.
	for i := 0; i+1 < len(m.Content); i += 2 {
		switch m.Content[i].Value {
		case "when":
			for _, k := range unknownKeys(m.Content[i+1], knownWhen, "when.") {
				add(Warn, "모르는 머리말 키: %s", k)
			}
		case "check":
			for _, k := range unknownKeys(m.Content[i+1], knownChk, "check.") {
				add(Warn, "모르는 머리말 키: %s", k)
			}
		}
	}
}

func (it *Item) validateCheck(add func(level, format string, a ...any)) {
	c := it.Check
	if !targets[c.Target] {
		add(Error, "check.target 은 command|commit_message|pr_body|last_message|question 중 하나여야 함: %q", c.Target)
	}
	if c.Mode != "block" && c.Mode != "log" {
		add(Error, "check.mode 는 block|log 중 하나여야 함: %q", c.Mode)
	}
	if (c.Pattern == "") == (c.NotPattern == "") {
		add(Error, "check 에는 pattern 과 not_pattern 중 정확히 하나가 있어야 함")
	}
	for name, p := range map[string]string{"pattern": c.Pattern, "not_pattern": c.NotPattern, "evidence": c.Evidence} {
		if p == "" {
			continue
		}
		if _, err := regexp.Compile(p); err != nil {
			add(Error, "check.%s 정규식이 컴파일되지 않음: %v", name, err)
		}
	}
}

// checkActions 는 어휘(설정의 actions·tool_actions 가 아는 이름과 해석기가 내는 이름)에 없는 when.action 에 경고를 단다.
// 어휘가 비어 있으면 아무것도 하지 않는다. 어휘 만들기는 action.Vocab 이 한다.
func (it *Item) checkActions(vocab map[string]bool) {
	if len(vocab) == 0 {
		return
	}
	for _, a := range it.When.Action {
		if !vocab[a] {
			it.Problems = append(it.Problems, Problem{filepath.Base(it.File), Warn, fmt.Sprintf("어느 것도 이 동작을 내지 않음: 설정 actions·tool_actions 에도, 해석기에도 없는 when.action: %s", a)})
		}
	}
}

func unknownKeys(n *yaml.Node, ok map[string]bool, prefix string) []string {
	var out []string
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i].Value; !ok[k] {
			out = append(out, prefix+k)
		}
	}
	return out
}

// splitFrontMatter 는 맨 위 --- 두 줄 사이를 머리말로, 나머지를 본문으로 나눈다.
func splitFrontMatter(s string) (head, body string, ok bool) {
	s = strings.TrimPrefix(s, string(rune(0xFEFF)))
	lines := strings.SplitAfter(s, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r\n") != "---" {
		return "", "", false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r\n") == "---" {
			return strings.Join(lines[1:i], ""), strings.TrimPrefix(strings.Join(lines[i+1:], ""), "\n"), true
		}
	}
	return "", "", false
}

// LoadDir 는 dir/*.md 만 읽는다(하위 폴더는 읽지 않는다). 폴더가 없으면 빈 목록이다.
// vocab 은 when.action 에 쓸 수 있는 동작 이름 전체(action.Vocab)이며 비어 있으면 어휘 검사를 하지 않는다. 결과는 파일 이름순이다.
func LoadDir(dir string, vocab map[string]bool) ([]*Item, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var items []*Item
	for _, p := range paths {
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			continue
		}
		it, err := Parse(p)
		if err != nil {
			return nil, err
		}
		it.checkActions(vocab)
		items = append(items, it)
	}
	return items, nil
}
