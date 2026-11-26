// Package check 는 항목의 결과물 검사(check)를 글에 적용한다.
// 검사할 글은 도구 호출 직전에는 Bash 명령에서 뽑고, 턴 종료에서는 마지막 응답을 그대로 쓴다.
package check

import (
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
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

// NoteNoTurnLog 는 evidence 가 있는 검사인데 이번 턴의 도구 호출 기록을 읽지 못해 판정하지 못했다는 이유다.
const NoteNoTurnLog = "턴 기록을 읽지 못함"

// NoteBadPattern 은 식이 틀렸거나 없어서 판정하지 못했다는 이유다. 글을 뽑지 못한 이유는 shell.Note… 이다.
const NoteBadPattern = "식을 쓸 수 없음"

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
	case "question":
		return "질문 글"
	}
	return target
}

// Targets 는 도구 호출에서 검사할 수 있는 글을 대상 종류별로 돌려준다. Bash 와 AskUserQuestion 이 아니면 비어 있다.
// 파일을 읽지 않으므로 파일로 넘겨서 뽑을 수 없는 대상은 키가 없다.
func Targets(tool string, input map[string]any) map[string][]string {
	texts, _ := Extract(tool, input, shell.Files{})
	return texts
}

// Extract 는 Targets 와 같되, 파일로 넘긴 커밋 메시지·PR 본문을 f 의 규칙으로 풀어서 읽는다.
// 글을 뽑지 못한 대상(키가 없는 대상)이 커밋 메시지나 PR 본문이면 notes 에 그 이유(shell.Note…)가 있다.
func Extract(tool string, input map[string]any, f shell.Files) (texts map[string][]string, notes map[string]string) {
	texts, notes = map[string][]string{}, map[string]string{}
	if tool == "AskUserQuestion" {
		if q := QuestionText(input); q != "" {
			texts["question"] = []string{q}
		}
		return texts, notes
	}
	if tool != "Bash" {
		return texts, notes
	}
	cmd, _ := input["command"].(string)
	if cmd == "" {
		return texts, notes
	}
	texts["command"] = []string{cmd}
	for target, e := range map[string]shell.Extracted{
		"commit_message": shell.CommitMessagesIn(cmd, f),
		"pr_body":        shell.PRBodiesIn(cmd, f),
	} {
		if len(e.Texts) > 0 {
			texts[target] = e.Texts
		} else {
			notes[target] = e.Note
		}
	}
	return texts, notes
}

// QuestionText 는 AskUserQuestion 입력의 질문 글이다. questions[].question 과 그 options[].label·description 을 줄바꿈으로 잇는다.
// 글이 없으면 빈 문자열이다.
func QuestionText(input map[string]any) string {
	var parts []string
	add := func(m map[string]any, keys ...string) {
		for _, k := range keys {
			if s, _ := m[k].(string); s != "" {
				parts = append(parts, s)
			}
		}
	}
	qs, _ := input["questions"].([]any)
	for _, q := range qs {
		qm, _ := q.(map[string]any)
		add(qm, "question")
		opts, _ := qm["options"].([]any)
		for _, o := range opts {
			om, _ := o.(map[string]any)
			add(om, "label", "description")
		}
	}
	return strings.Join(parts, "\n")
}

// ReadFile 은 shell.Files.Read 로 쓰는 읽기 함수다. 보통 파일만, MaxFileSize 이하만 읽는다.
func ReadFile(p string) ([]byte, error) {
	fh, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > shell.MaxFileSize {
		return nil, errors.New("읽지 않는 파일")
	}
	return io.ReadAll(io.LimitReader(fh, shell.MaxFileSize+1))
}

// Eval 은 검사 하나를 글들에 적용한다. 글이 없으면 Skip 이다.
// Skip 일 때 why 는 건너뛴 이유다(글이 없으면 shell.NoteNoTarget, 식을 쓸 수 없으면 NoteBadPattern).
// pattern 은 맞아야 통과이고 not_pattern 은 맞지 않아야 통과이며, 하나라도 어긋난 글이 있으면 Fail 과 그 이유를 돌려준다.
// 정규식이 틀렸으면 통과시키는 뜻으로 Skip 을 돌려준다.
func Eval(c item.Check, texts []string) (result, why string) {
	if len(texts) == 0 {
		return Skip, shell.NoteNoTarget
	}
	label := Label(c.Target)
	var pat, not *regexp.Regexp
	var err error
	if c.Pattern != "" {
		if pat, err = regexp.Compile(c.Pattern); err != nil {
			return Skip, NoteBadPattern
		}
	}
	if c.NotPattern != "" {
		if not, err = regexp.Compile(c.NotPattern); err != nil {
			return Skip, NoteBadPattern
		}
	}
	if pat == nil && not == nil {
		return Skip, NoteBadPattern
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

// Turn 은 이번 턴에 앞서 한 도구 호출 기록이다. OK 가 거짓이면 기록을 읽지 못한 것이다(세션을 모르거나 상태 파일이 없거나 깨짐).
type Turn struct {
	Calls []string
	OK    bool
}

// maxEvidenceNote 는 원장에 남기는 증거 기록의 글자 수 상한이다.
const maxEvidenceNote = 120

// EvalEvidence 는 Eval 과 같되, 검사가 Fail 이고 c.Evidence 가 있으면 이번 턴의 기록(turn) 중 하나라도 그 식에 맞을 때 Pass 로 바꾼다.
// 바뀌었으면 note 에 맞은 기록의 앞부분을 "증거: …" 로 돌려준다. evidence 식이 틀렸거나 기록을 읽지 못했으면
// 판정하지 못했으므로 통과시키는 뜻으로 Skip 과 이유(NoteBadPattern, NoteNoTurnLog)를 돌려준다(R9).
// 기록이 있는데 맞는 것이 없으면 Fail 그대로다.
func EvalEvidence(c item.Check, texts []string, turn Turn) (result, why, note string) {
	result, why = Eval(c, texts)
	if result != Fail || c.Evidence == "" {
		return result, why, ""
	}
	re, err := regexp.Compile(c.Evidence)
	if err != nil {
		return Skip, NoteBadPattern, ""
	}
	if !turn.OK {
		return Skip, NoteNoTurnLog, ""
	}
	for _, call := range turn.Calls {
		if re.MatchString(call) {
			return Pass, "", "증거: " + cut(call, maxEvidenceNote)
		}
	}
	return Fail, why, ""
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
