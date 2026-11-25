package claudecode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// 사용자가 친 글이 아닌 줄의 머리다. 도구가 만들어 사용자 글처럼 넣은 것들이다.
// <task-notification> 은 에이전트 결과 알림이다. origin 을 남기지 않는 기록에서도 막으려고 여기에도 둔다.
// ! 로 친 명령 자체(<bash-input>)는 사용자가 친 글이라 넣지 않는다.
var systemPrefixes = []string{"<local-command", "<system-reminder>", "<task-notification"}

// inlineMarkers 는 사용자가 친 글이 아닌 블록의 머리다. 글 머리에 있든 중간에 있든 그 자리부터 끝까지 뺀다.
// <bash-stdout>·<bash-stderr> 는 ! 명령의 출력이고, <artifact-content-authored-by-others> 는 다른 사람이 쓴 글이다.
var inlineMarkers = []string{"<bash-stdout", "<bash-stderr", "<artifact-content-authored-by-others"}

// commandPrefixes 는 슬래시 명령 줄의 머리다. 명령 줄에서는 사용자가 친 인자(<command-args>)만 읽는다.
var commandPrefixes = []string{"<command-name>", "<command-message>"}

// 사용자가 붙여넣은 글 블록은 <pasted_content id="…"> 로 열고 </pasted_content id="…"> 로 닫는다.
const (
	pastedOpen  = `<pasted_content id="`
	pastedClose = `</pasted_content id="`
)

// askTool 은 사용자에게 선택지를 묻는 도구의 이름이다. 그 결과에는 사용자의 답이 들어 있다.
const askTool = "AskUserQuestion"

// transcriptLine 은 세션 기록 파일의 한 줄에서 쓰는 필드다.
type transcriptLine struct {
	Type             string `json:"type"`
	IsMeta           bool   `json:"isMeta"`
	IsSidechain      bool   `json:"isSidechain"`
	IsCompactSummary bool   `json:"isCompactSummary"` // 대화를 압축하며 AI 가 쓴 요약이다
	Origin           *struct {
		Kind string `json:"kind"`
	} `json:"origin"` // user 줄을 누가 넣었는지. 없거나 human 이면 사람이다
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	Attachment    *struct {
		Type        string          `json:"type"`
		Prompt      json.RawMessage `json:"prompt"` // 문자열이거나, 이미지를 붙였으면 content 칸의 배열이다
		CommandMode string          `json:"commandMode"`
		Origin      struct {
			Kind string `json:"kind"`
		} `json:"origin"`
	} `json:"attachment"`
}

// contentPart 는 message.content 가 배열일 때의 한 칸이다.
type contentPart struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
	Input     struct {
		Answers     json.RawMessage `json:"answers"`
		Annotations json.RawMessage `json:"annotations"`
	} `json:"input"`
}

// askResult 는 askTool 결과의 toolUseResult 다. answers 값은 문자열이거나 문자열 배열이다.
type askResult struct {
	Questions []struct {
		Question    string `json:"question"`
		MultiSelect *bool  `json:"multiSelect"` // 없으면 답을 어떻게 이었는지 알 수 없어 답은 읽지 않는다(메모는 읽는다)
		Options     []struct {
			Label string `json:"label"`
		} `json:"options"`
	} `json:"questions"`
	Answers     map[string]json.RawMessage `json:"answers"`
	Annotations map[string]json.RawMessage `json:"annotations"` // 값마다 {notes, preview}. preview 는 AI 가 쓴 글이다
}

// UserTexts 는 Claude Code 세션 기록 파일(한 줄에 JSON 하나)에서 사용자가 친 글만 모아 돌려준다.
// 읽는 곳은 세 군데다.
//   - "type":"user" 줄의 message.content 에 든 글.
//   - askTool 결과의 답과 메모. 사용자가 고른 선택지 라벨은 AI 가 쓴 글이라 빼고, 직접 친 글만 읽는다.
//     답을 사람이 아닌 쪽(훅이나 SDK 의 권한 콜백)이 채웠는지는 기록으로 가릴 수 없다. 그런 설정은 사용자가 직접 둔 것이라 받아들인다.
//   - 작업 중에 보낸 메시지("type":"attachment", attachment.type queued_command). origin.kind 가 human 이고
//     commandMode 가 prompt 인 것만 읽는다. peer·coordinator 는 다른 세션이나 에이전트가 보낸 글이다.
//
// askTool 을 부를 때 AI 가 input 에 answers·annotations 를 미리 채웠다면 그 호출의 결과는 읽지 않는다. 답이 AI 의 글일 수 있다.
// 질문을 거절하며 친 글은 도구 결과의 본문에만 남아 읽지 않는다.
//
// 다음은 사용자가 친 글로 보지 않는다. askTool 이 아닌 도구의 tool_result 가 든 줄(AI 가 만든 출력이 들어갈 수 있다),
// isMeta·isSidechain·isCompactSummary 가 참인 줄, origin.kind 가 human 이 아닌 user 줄(에이전트 결과 알림 등),
// toolPrefixes 나 systemPrefixes 로 시작하는 글, inlineMarkers 부터 끝, 붙여넣은 글 블록(사용자가 쓴 글이 아닐 수 있다),
// 슬래시 명령 줄에서 <command-args> 밖의 글. 자세한 것은 userFragments 에 있다.
// AI 의 글("type":"assistant")은 askTool 호출의 id 를 기억하는 데만 쓴다. 깨진 줄은 건너뛰고, 파일을 못 열면 오류를 돌려준다.
// 기록 형식은 Claude Code 가 문서로 내지 않은 내부 형식이라, 형식이 바뀌면 여기서 읽지 못하는 글이 생긴다.
func UserTexts(path string, toolPrefixes []string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	asks := map[string]bool{} // askTool 호출의 id
	var out []string
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 && mayHoldUserText(line) {
			for _, t := range lineTexts(line, asks) {
				out = append(out, userFragments(t, toolPrefixes)...)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
	}
}

// userFragments 는 한 글에서 사용자가 친 조각들을 돌려준다.
// 슬래시 명령 줄이면 <command-args> 안의 글만 읽는다. systemPrefixes·toolPrefixes 로 시작하는 글은 버린다.
// 나머지는 inlineMarkers 부터 끝을 자르고 붙여넣은 글 블록을 뺀다. 블록 앞뒤는 이어 붙이지 않고 따로 돌려준다.
// 이어 붙이면 사용자가 이어 친 적 없는 문장이 인용 대조에서 맞을 수 있다.
func userFragments(t string, toolPrefixes []string) []string {
	head := strings.TrimSpace(t)
	if hasAnyPrefix(head, commandPrefixes) {
		var out []string
		for _, a := range tagContents(t, "<command-args>", "</command-args>") {
			out = append(out, plainFragments(a)...)
		}
		return out
	}
	if hasAnyPrefix(head, systemPrefixes) || hasAnyPrefix(head, toolPrefixes) {
		return nil
	}
	return plainFragments(t)
}

// plainFragments 는 inlineMarkers 부터 끝을 자르고 붙여넣은 글 블록을 뺀 조각들이다. 공백뿐인 조각은 버린다.
func plainFragments(t string) []string {
	for _, m := range inlineMarkers {
		if i := strings.Index(t, m); i >= 0 {
			t = t[:i]
		}
	}
	var out []string
	for _, f := range pastedFragments(t) {
		if strings.TrimSpace(f) != "" {
			out = append(out, f)
		}
	}
	return out
}

// pastedFragments 는 붙여넣은 글 블록을 빼고 남은 조각들을 차례로 돌려준다.
// 닫는 머리가 없으면 여는 머리부터 끝까지 뺀다. 여는 머리 없이 닫는 머리가 먼저 나오면 그 앞은
// 다른 칸에서 시작한 블록의 뒷부분이라 닫는 머리까지 뺀다.
func pastedFragments(t string) []string {
	var out []string
	for t != "" {
		open, close := strings.Index(t, pastedOpen), strings.Index(t, pastedClose)
		if close >= 0 && (open < 0 || close < open) {
			j := strings.Index(t[close:], `">`)
			if j < 0 {
				return out
			}
			t = t[close+j+2:]
			continue
		}
		if open < 0 {
			return append(out, t)
		}
		out = append(out, t[:open])
		rest := t[open+len(pastedOpen):]
		j := strings.Index(rest, `">`)
		if j < 0 {
			return out
		}
		closing := pastedClose + rest[:j] + `">`
		k := strings.Index(rest, closing)
		if k < 0 {
			return out
		}
		t = rest[k+len(closing):]
	}
	return out
}

// tagContents 는 t 에서 open 과 close 사이의 글을 모두 돌려준다. 닫히지 않은 것은 버린다.
func tagContents(t, open, close string) []string {
	var out []string
	for {
		i := strings.Index(t, open)
		if i < 0 {
			return out
		}
		t = t[i+len(open):]
		j := strings.Index(t, close)
		if j < 0 {
			return out
		}
		out = append(out, t[:j])
		t = t[j+len(close):]
	}
}

// mayHoldUserText 는 JSON 을 풀기 전에 볼 필요가 없는 줄을 거른다.
func mayHoldUserText(line []byte) bool {
	return bytes.Contains(line, []byte(`"user"`)) || bytes.Contains(line, []byte(`"queued_command"`)) ||
		bytes.Contains(line, []byte(`"`+askTool+`"`))
}

// lineTexts 는 한 줄에서 사용자가 친 글을 꺼낸다. assistant 줄이면 askTool 호출의 id 를 asks 에 더한다.
func lineTexts(line []byte, asks map[string]bool) []string {
	var tl transcriptLine
	if json.Unmarshal(line, &tl) != nil || tl.IsMeta || tl.IsSidechain || tl.IsCompactSummary {
		return nil
	}
	switch tl.Type {
	case "assistant":
		var parts []contentPart
		if json.Unmarshal(tl.Message.Content, &parts) == nil {
			for _, p := range parts {
				if p.Type == "tool_use" && p.Name == askTool && p.ID != "" && empty(p.Input.Answers) && empty(p.Input.Annotations) {
					asks[p.ID] = true
				}
			}
		}
		return nil
	case "attachment":
		if a := tl.Attachment; a != nil && a.Type == "queued_command" && a.Origin.Kind == "human" && a.CommandMode == "prompt" {
			return contentTexts(a.Prompt)
		}
		return nil
	case "user":
		if o := tl.Origin; o != nil && o.Kind != "" && o.Kind != "human" {
			return nil
		}
	default:
		return nil
	}
	var parts []contentPart
	if json.Unmarshal(tl.Message.Content, &parts) != nil {
		return contentTexts(tl.Message.Content)
	}
	// tool_result 가 든 줄은 askTool 의 결과만 있을 때 그 답만 읽는다. 같은 줄의 다른 글은 Claude Code 가
	// 붙인 안내일 수 있고, toolUseResult 는 줄마다 하나라 다른 도구의 결과와 섞이면 누구의 것인지 가릴 수 없다.
	var ask, other bool
	var texts []string
	for _, p := range parts {
		switch p.Type {
		case "tool_result":
			if asks[p.ToolUseID] {
				ask = true
			} else {
				other = true
			}
		case "text":
			texts = append(texts, p.Text)
		}
	}
	switch {
	case other:
		return nil
	case ask:
		return askAnswers(tl.ToolUseResult)
	}
	return texts
}

// empty 는 JSON 값이 없거나 null·빈 객체·빈 배열·빈 문자열인지 본다.
func empty(raw json.RawMessage) bool {
	switch string(bytes.Join(bytes.Fields(raw), nil)) {
	case "", "null", "{}", "[]", `""`:
		return true
	}
	return false
}

// contentTexts 는 문자열이거나 content 칸의 배열인 값에서 글을 꺼낸다. 배열이면 text 칸만 본다.
func contentTexts(raw json.RawMessage) []string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}
	}
	var parts []contentPart
	if json.Unmarshal(raw, &parts) != nil {
		return nil
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return texts
}

// askAnswers 는 askTool 결과에서 사용자가 직접 친 답과 메모를 돌려준다. 질문 순서를 따른다.
// 라벨은 결과에 든 모든 질문의 것을 함께 본다. answers 는 질문 글이 키라, 질문 글이 겹치면 다른 질문의 라벨이 답으로 올 수 있다.
func askAnswers(raw json.RawMessage) []string {
	var res askResult
	if json.Unmarshal(raw, &res) != nil {
		return nil
	}
	labels := map[string]bool{}
	for _, q := range res.Questions {
		for _, o := range q.Options {
			labels[o.Label] = true
		}
	}
	var out []string
	done := map[string]bool{} // answers 는 질문 글이 키라 같은 질문 글은 한 번만 읽는다
	for _, q := range res.Questions {
		if done[q.Question] {
			continue
		}
		done[q.Question] = true
		var one string
		var many []string
		a := res.Answers[q.Question]
		switch {
		case q.MultiSelect == nil:
		case json.Unmarshal(a, &one) == nil:
			if t := typedAnswer(one, labels, *q.MultiSelect); t != "" {
				out = append(out, t)
			}
		case json.Unmarshal(a, &many) == nil:
			for _, m := range many {
				if m != "" && !labels[m] {
					out = append(out, m)
				}
			}
		}
		var note struct {
			Notes string `json:"notes"`
		}
		if json.Unmarshal(res.Annotations[q.Question], &note) == nil && note.Notes != "" {
			out = append(out, note.Notes)
		}
	}
	return out
}

// typedAnswer 는 답에서 고른 선택지 라벨을 빼고 사용자가 직접 친 글을 돌려준다. 없거나 가릴 수 없으면 빈 글이다.
// 하나를 고르는 질문의 답은 라벨 하나이거나 직접 친 글 하나라, 답 전체가 라벨과 같을 때만 뺀다.
// 여러 개를 고르는 질문의 답은 고른 라벨들 뒤에 직접 친 글 한 칸을 ", " 로 이은 것이고, 쉼표가 든 칸은 큰따옴표로 감싼다.
// 그래서 칸으로 나눈 뒤 앞의 라벨 칸을 떼고, 남은 칸이 꼭 하나일 때만 그 칸을 돌려준다.
// 나눌 수 없거나 남은 칸이 여럿이면 형식을 잘못 읽은 것이라, AI 가 쓴 라벨이 섞이지 않도록 아무것도 돌려주지 않는다.
// 칸 안의 큰따옴표를 어떻게 적는지는 세션 기록에서 확인하지 못했다. 그래서 큰따옴표가 든 라벨이 하나라도 있으면
// 라벨이 칸 여러 개로 잘못 나뉠 수 있어 답을 읽지 않는다.
func typedAnswer(ans string, labels map[string]bool, multi bool) string {
	if labels[ans] {
		return ""
	}
	if !multi {
		return ans
	}
	for l := range labels {
		if strings.Contains(l, `"`) {
			return ""
		}
	}
	cells, ok := splitCells(ans)
	if !ok {
		return ""
	}
	for len(cells) > 0 && labels[cells[0]] {
		cells = cells[1:]
	}
	if len(cells) != 1 {
		return ""
	}
	return cells[0]
}

// splitCells 는 여러 개를 고른 답을 칸으로 나눈다. 칸은 ", " 로 이어지고, 큰따옴표로 시작하는 칸은
// 닫는 큰따옴표까지이며 그 안의 "" 는 " 하나로 읽는다(직접 친 글에 큰따옴표가 있을 때를 위한 가정이다). 감싼 칸이 닫히지 않거나 칸 사이가 ", " 가 아니면 ok 는 거짓이다.
func splitCells(s string) (cells []string, ok bool) {
	const sep = ", "
	for {
		var cell string
		if strings.HasPrefix(s, `"`) {
			var b strings.Builder
			i := 1
			for {
				j := strings.IndexByte(s[i:], '"')
				if j < 0 {
					return nil, false
				}
				b.WriteString(s[i : i+j])
				i += j + 1
				if !strings.HasPrefix(s[i:], `"`) {
					break
				}
				b.WriteByte('"')
				i++
			}
			cell, s = b.String(), s[i:]
		} else if i := strings.Index(s, sep); i >= 0 {
			cell, s = s[:i], s[i:]
		} else {
			cell, s = s, ""
		}
		cells = append(cells, cell)
		if s == "" {
			return cells, true
		}
		if !strings.HasPrefix(s, sep) || len(s) == len(sep) {
			return nil, false
		}
		s = s[len(sep):]
	}
}

func hasAnyPrefix(s string, prefixes []string) bool {
	return slices.ContainsFunc(prefixes, func(p string) bool { return p != "" && strings.HasPrefix(s, p) })
}

// Transcripts 는 glob 목록에 맞는 파일 가운데 since 이후에 고쳐진 것의 경로를 돌려준다.
// 패턴 앞의 ~ 는 홈 폴더로 풀고, ** 는 폴더 여러 층에 맞는다. 맞는 파일이 없거나 폴더를 못 읽으면 건너뛴다.
func Transcripts(patterns []string, since time.Time) []string {
	home, _ := os.UserHomeDir()
	var out []string
	for _, pat := range patterns {
		if pat == "~" || strings.HasPrefix(pat, "~/") {
			pat = home + pat[1:]
		}
		re, err := globRegexp(pat)
		if err != nil {
			continue
		}
		root := globRoot(pat)
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() || !re.MatchString(filepath.ToSlash(p)) {
				return nil
			}
			if st, err := d.Info(); err == nil && !st.ModTime().Before(since) && !slices.Contains(out, p) {
				out = append(out, p)
			}
			return nil
		})
	}
	return out
}

// globRoot 는 패턴에서 글롭 문자가 없는 앞쪽 폴더다. 그 아래만 훑는다.
func globRoot(pat string) string {
	parts := strings.Split(filepath.ToSlash(pat), "/")
	var keep []string
	for _, s := range parts {
		if strings.ContainsAny(s, "*?[") {
			break
		}
		keep = append(keep, s)
	}
	if len(keep) == len(parts) { // 글롭 문자가 없으면 파일 하나다
		keep = keep[:len(keep)-1]
	}
	root := strings.Join(keep, "/")
	if root == "" {
		if strings.HasPrefix(pat, "/") {
			return "/"
		}
		return "."
	}
	return filepath.FromSlash(root)
}

// globRegexp 는 글롭 패턴을 경로 전체에 맞는 정규식으로 바꾼다.
// *·? 는 폴더 경계(/)를 넘지 않고, **/ 는 폴더 0층 이상, ** 는 경계를 넘는 아무 글자다.
func globRegexp(pat string) (*regexp.Regexp, error) {
	pat = filepath.ToSlash(pat)
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pat); i++ {
		switch c := pat[i]; c {
		case '*':
			if strings.HasPrefix(pat[i:], "**/") {
				b.WriteString("(?:.*/)?")
				i += 2
			} else if strings.HasPrefix(pat[i:], "**") {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			j := strings.IndexByte(pat[i:], ']')
			if j < 0 {
				b.WriteString(`\[`)
				break
			}
			set := pat[i+1 : i+j]
			if strings.HasPrefix(set, "!") {
				set = "^" + set[1:]
			}
			b.WriteString("[" + set + "]")
			i += j
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
