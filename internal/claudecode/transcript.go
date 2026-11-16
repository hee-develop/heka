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

// transcriptLine 은 세션 기록 파일의 한 줄에서 쓰는 필드다.
type transcriptLine struct {
	Type        string `json:"type"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// UserTexts 는 Claude Code 세션 기록 파일(한 줄에 JSON 하나)에서 사용자가 친 글만 모아 돌려준다.
// "type":"user" 줄의 message.content 를 읽되, 다음은 사용자가 친 글로 보지 않는다.
// tool_result 가 든 줄, isMeta·isSidechain 이 참인 줄, toolPrefixes 나 systemPrefixes 로 시작하는 글.
// AI 의 글("type":"assistant")은 읽지 않는다. 깨진 줄은 건너뛰고, 파일을 못 열면 오류를 돌려준다.
func UserTexts(path string, toolPrefixes []string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	var out []string
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 && bytes.Contains(line, []byte(`"user"`)) {
			out = append(out, userLineTexts(line, toolPrefixes)...)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
	}
}

func userLineTexts(line []byte, toolPrefixes []string) []string {
	var tl transcriptLine
	if json.Unmarshal(line, &tl) != nil || tl.Type != "user" || tl.IsMeta || tl.IsSidechain {
		return nil
	}
	var texts []string
	var s string
	if json.Unmarshal(tl.Message.Content, &s) == nil {
		texts = []string{s}
	} else {
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(tl.Message.Content, &parts) != nil {
			return nil
		}
		for _, p := range parts {
			switch p.Type {
			case "tool_result":
				return nil
			case "text":
				texts = append(texts, p.Text)
			}
		}
	}
	var out []string
	for _, t := range texts {
		head := strings.TrimSpace(t)
		if head == "" || hasAnyPrefix(head, systemPrefixes) || hasAnyPrefix(head, toolPrefixes) {
			continue
		}
		out = append(out, t)
	}
	return out
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
