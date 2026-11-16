// Package check 는 항목의 결과물 검사(check)를 글에 적용한다.
// 검사할 글은 도구 호출 직전에는 Bash 명령에서 뽑고, 턴 종료에서는 마지막 응답을 그대로 쓴다.
package check

import (
	"regexp"
	"unicode/utf8"

	"github.com/hee-develop/heka/internal/item"
	"github.com/hee-develop/heka/internal/shell"
)

// 검사 결과다.
const (
	Pass = "pass"
	Fail = "fail"
	Skip = "skip" // 글을 뽑지 못했거나 식을 쓸 수 없어 판정하지 않음
)

// Label 은 검사 대상의 한국어 이름이다.
func Label(target string) string {
	switch target {
	case "command":
		return "명령"
	case "commit_message":
		return "커밋 메시지"
	case "pr_body":
		return "PR 본문"
	case "last_message":
		return "마지막 응답"
	}
	return target
}

// Targets 는 도구 호출에서 검사할 수 있는 글을 대상 종류별로 돌려준다. Bash 가 아니면 비어 있다.
// 파일로 넘겨서 뽑을 수 없는 대상은 키가 없다.
func Targets(tool string, input map[string]any) map[string][]string {
	out := map[string][]string{}
	if tool != "Bash" {
		return out
	}
	cmd, _ := input["command"].(string)
	if cmd == "" {
		return out
	}
	out["command"] = []string{cmd}
	if m := shell.CommitMessages(cmd); len(m) > 0 {
		out["commit_message"] = m
	}
	if b := shell.PRBodies(cmd); len(b) > 0 {
		out["pr_body"] = b
	}
	return out
}

// Eval 은 검사 하나를 글들에 적용한다. 글이 없으면 Skip 이다.
// pattern 은 맞아야 통과이고 not_pattern 은 맞지 않아야 통과이며, 하나라도 어긋난 글이 있으면 Fail 과 그 이유를 돌려준다.
// 정규식이 틀렸으면 통과시키는 뜻으로 Skip 을 돌려준다.
func Eval(c item.Check, texts []string) (result, why string) {
	if len(texts) == 0 {
		return Skip, ""
	}
	label := Label(c.Target)
	var pat, not *regexp.Regexp
	var err error
	if c.Pattern != "" {
		if pat, err = regexp.Compile(c.Pattern); err != nil {
			return Skip, ""
		}
	}
	if c.NotPattern != "" {
		if not, err = regexp.Compile(c.NotPattern); err != nil {
			return Skip, ""
		}
	}
	if pat == nil && not == nil {
		return Skip, ""
	}
	for _, t := range texts {
		if pat != nil && !pat.MatchString(t) {
			return Fail, label + "이(가) 요구 형식에 맞지 않습니다(pattern: " + cut(c.Pattern, 60) + ")"
		}
		if not != nil && not.MatchString(t) {
			return Fail, label + "에 금지된 표현이 있습니다(not_pattern: " + cut(c.NotPattern, 60) + ")"
		}
	}
	return Pass, ""
}

func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// Line 은 모델에게 돌려줄 사유 한 줄이다. 항목의 주입문과 어느 검사에 걸렸는지를 적는다.
func Line(id, text, why string) string {
	return "[heka 검사] " + text + " — " + why + " (항목 " + id + ")"
}
