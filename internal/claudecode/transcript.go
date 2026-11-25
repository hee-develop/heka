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
var systemPrefixes = []string{"<local-command", "<command-name>", "<system-reminder>"}

// askTool 은 사용자에게 선택지를 묻는 도구의 이름이다. 그 결과에는 사용자의 답이 들어 있다.
const askTool = "AskUserQuestion"

// transcriptLine 은 세션 기록 파일의 한 줄에서 쓰는 필드다.
type transcriptLine struct {
	Type        string `json:"type"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	Attachment    *struct {
		Type   string          `json:"type"`
		Prompt json.RawMessage `json:"prompt"` // 문자열이거나, 이미지를 붙였으면 content 칸의 배열이다
		Origin struct {
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
}

// askResult 는 askTool 결과의 toolUseResult 다. answers 값은 문자열이거나 문자열 배열이다.
type askResult struct {
	Questions []struct {
		Question    string `json:"question"`
		MultiSelect bool   `json:"multiSelect"`
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
//   - 작업 중에 보낸 메시지("type":"attachment", attachment.type queued_command). origin.kind 가 human 인 것만 읽는다.
//     peer·coordinator 는 다른 세션이나 에이전트가 보낸 글이다.
//
// 다음은 사용자가 친 글로 보지 않는다. askTool 이 아닌 도구의 tool_result 가 든 줄(AI 가 만든 출력이 들어갈 수 있다),
// isMeta·isSidechain 이 참인 줄, toolPrefixes 나 systemPrefixes 로 시작하는 글.
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
				head := strings.TrimSpace(t)
				if head == "" || hasAnyPrefix(head, systemPrefixes) || hasAnyPrefix(head, toolPrefixes) {
					continue
				}
				out = append(out, t)
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

// mayHoldUserText 는 JSON 을 풀기 전에 볼 필요가 없는 줄을 거른다.
func mayHoldUserText(line []byte) bool {
	return bytes.Contains(line, []byte(`"user"`)) || bytes.Contains(line, []byte(`"queued_command"`)) ||
		bytes.Contains(line, []byte(`"`+askTool+`"`))
}

// lineTexts 는 한 줄에서 사용자가 친 글을 꺼낸다. assistant 줄이면 askTool 호출의 id 를 asks 에 더한다.
func lineTexts(line []byte, asks map[string]bool) []string {
	var tl transcriptLine
	if json.Unmarshal(line, &tl) != nil || tl.IsMeta || tl.IsSidechain {
		return nil
	}
	switch tl.Type {
	case "assistant":
		var parts []contentPart
		if json.Unmarshal(tl.Message.Content, &parts) == nil {
			for _, p := range parts {
				if p.Type == "tool_use" && p.Name == askTool && p.ID != "" {
					asks[p.ID] = true
				}
			}
		}
		return nil
	case "attachment":
		if a := tl.Attachment; a != nil && a.Type == "queued_command" && a.Origin.Kind == "human" {
			return contentTexts(a.Prompt)
		}
		return nil
	case "user":
	default:
		return nil
	}
	var parts []contentPart
	if json.Unmarshal(tl.Message.Content, &parts) != nil {
		return contentTexts(tl.Message.Content)
	}
	var texts []string
	for _, p := range parts {
		switch p.Type {
		case "tool_result":
			if !asks[p.ToolUseID] {
				return nil
			}
			texts = append(texts, askAnswers(tl.ToolUseResult)...)
		case "text":
			texts = append(texts, p.Text)
		}
	}
	return texts
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
func askAnswers(raw json.RawMessage) []string {
	var res askResult
	if json.Unmarshal(raw, &res) != nil {
		return nil
	}
	var out []string
	for _, q := range res.Questions {
		labels := make([]string, len(q.Options))
		for i, o := range q.Options {
			labels[i] = o.Label
		}
		var one string
		var many []string
		if a := res.Answers[q.Question]; json.Unmarshal(a, &one) == nil {
			if t := typedAnswer(one, labels, q.MultiSelect); t != "" {
				out = append(out, t)
			}
		} else if json.Unmarshal(a, &many) == nil {
			for _, m := range many {
				if !slices.Contains(labels, m) && m != "" {
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

// typedAnswer 는 답에서 고른 선택지 라벨을 빼고 남은 글을 원문 그대로 돌려준다.
// 하나를 고르는 질문의 답은 라벨 하나이거나 직접 친 글 하나라, 답 전체가 라벨과 같을 때만 뺀다.
// 여러 개를 고르는 질문의 답은 고른 라벨들 뒤에 직접 친 글을 ", " 로 이은 문자열이고,
// 칸 안에 ", " 가 있으면 그 칸을 큰따옴표로 감싼다. 그래서 앞에서부터 라벨 칸만 떼고 나머지는 자르지 않는다.
// 남은 글이 큰따옴표로 감싸여 있어도 그대로 둔다. 인용 대조는 포함 여부로 보므로 안쪽 글이 그대로 맞는다.
func typedAnswer(ans string, labels []string, multi bool) string {
	if !multi {
		if slices.Contains(labels, ans) {
			return ""
		}
		return ans
	}
	const sep = ", "
	rest := ans
	for rest != "" {
		n := labelCell(rest, labels, sep)
		if n == 0 {
			break
		}
		rest = strings.TrimPrefix(rest[n:], sep)
	}
	return rest
}

// labelCell 은 rest 가 라벨 하나(그대로거나 큰따옴표로 감싼 것)로 된 칸으로 시작하면 그 칸의 길이를, 아니면 0 을 돌려준다.
// 칸은 rest 의 끝이나 sep 앞에서 끝나야 한다. 길이가 같은 라벨이 여럿 맞아도 길이만 쓰므로 상관없다.
func labelCell(rest string, labels []string, sep string) int {
	best := 0
	for _, l := range labels {
		if l == "" {
			continue
		}
		for _, cell := range []string{l, `"` + l + `"`} {
			if len(cell) > best && strings.HasPrefix(rest, cell) && (len(rest) == len(cell) || strings.HasPrefix(rest[len(cell):], sep)) {
				best = len(cell)
			}
		}
	}
	return best
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
