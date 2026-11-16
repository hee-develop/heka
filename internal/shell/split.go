// Package shell 은 셸 명령 문자열을 구간(Segment)과 단어로 나누는 토크나이저와,
// 접두 제거·git 하위 명령 추출·강제 푸시 판별 같은 순수 함수를 제공한다.
// 표준 라이브러리만 쓰고 결과는 결정적이다.
package shell

import "strings"

// Segment 는 명령 한 구간이다.
// Words 는 따옴표가 벗겨진 단어(리다이렉션 토큰은 제외), Redirects 는 `>`·`>>` 의 대상 파일이다.
type Segment struct {
	Words     []string
	Redirects []string
	Heredocs  []string // 이 구간에서 연 heredoc 본문(구분자 줄 제외, 열린 순서)
	Appends   []string // Redirects 가운데 `>>`(덧붙이기)로 쓴 것의 대상
	// Subst 는 구간의 마지막 단어 바로 뒤에 $( 나 백틱이 붙어 있었다는 뜻이다(`B=$(mktemp)`).
	// 토크나이저는 거기서 구간을 나누므로 그 단어의 값은 글자 그대로가 아니다.
	Subst bool
}

type token struct {
	text   string
	quoted bool // 따옴표·역슬래시가 하나라도 쓰였으면 연산자(리다이렉션)로 보지 않는다
}

type heredoc struct {
	delim string
	strip bool
	seg   int // 이 heredoc 을 연 구간의 인덱스(구간이 만들어지지 않으면 붙일 곳 없음)
}

// SplitCommand 는 명령을 구간으로 나눈다.
// 경계: && || ; | |& & 개행 ( ) 백틱 $(. 따옴표 안은 나누지 않는다.
// heredoc(<<EOF, <<'EOF', <<-EOF) 본문과 `#` 주석은 명령으로 보지 않는다.
// 큰따옴표 안의 $(...)·백틱은 나누지 않는다(알려진 한계).
func SplitCommand(cmd string) []Segment {
	var segs []Segment
	var toks []token
	var w []byte
	inWord, quoted := false, false
	var pending []heredoc

	endWord := func() {
		if inWord {
			toks = append(toks, token{string(w), quoted})
		}
		w, inWord, quoted = w[:0], false, false
	}
	glued := false // 이번 endSeg 가 단어 바로 뒤의 $( 나 백틱 때문인가
	endSeg := func() {
		endWord()
		if seg, ok := buildSegment(toks); ok {
			seg.Subst = glued
			segs = append(segs, seg)
		}
		toks, glued = nil, false
	}

	n := len(cmd)
	for i := 0; i < n; i++ {
		c := cmd[i]
		switch {
		case c == '\'':
			inWord, quoted = true, true
			j := i + 1
			for j < n && cmd[j] != '\'' {
				w = append(w, cmd[j])
				j++
			}
			i = j // 닫는 따옴표(없으면 끝)
		case c == '"':
			inWord, quoted = true, true
			j := i + 1
			for j < n && cmd[j] != '"' {
				if cmd[j] == '\\' && j+1 < n && strings.IndexByte(`"\$`+"`\n", cmd[j+1]) >= 0 {
					if cmd[j+1] != '\n' {
						w = append(w, cmd[j+1])
					}
					j += 2
					continue
				}
				w = append(w, cmd[j])
				j++
			}
			i = j
		case c == '\\':
			if i+1 < n {
				if cmd[i+1] != '\n' { // 줄 이음은 단어를 잇는다
					w = append(w, cmd[i+1])
					inWord, quoted = true, true
				}
				i++
			}
		case c == '#' && !inWord:
			for i+1 < n && cmd[i+1] != '\n' {
				i++
			}
		case c == '<' && i+2 < n && cmd[i+1] == '<' && cmd[i+2] != '<' && (i == 0 || cmd[i-1] != '<'):
			if d, end, ok := parseHeredocDelim(cmd, i+2); ok {
				endWord()
				d.seg = len(segs)
				pending = append(pending, d)
				i = end - 1
			} else {
				w = append(w, c)
				inWord = true
			}
		case c == '\n':
			endSeg()
			for _, h := range pending {
				body, next := heredocBody(cmd, i+1, h)
				if h.seg < len(segs) {
					segs[h.seg].Heredocs = append(segs[h.seg].Heredocs, body)
				}
				i = next - 1
			}
			pending = nil
		case c == '`':
			glued = inWord
			endSeg()
		case c == ';' || c == '(' || c == ')':
			endSeg()
		case c == '$' && i+1 < n && cmd[i+1] == '(':
			glued = inWord
			endSeg()
			i++
		case c == '|':
			if i > 0 && cmd[i-1] == '>' && inWord { // `>|` clobber 리다이렉션
				w = append(w, c)
				continue
			}
			endSeg()
			if i+1 < n && (cmd[i+1] == '|' || cmd[i+1] == '&') {
				i++
			}
		case c == '&':
			if i+1 < n && cmd[i+1] == '&' {
				endSeg()
				i++
			} else if (i > 0 && (cmd[i-1] == '>' || cmd[i-1] == '<')) || (i+1 < n && cmd[i+1] == '>') {
				w = append(w, c) // 2>&1, >&2, &>file
				inWord = true
			} else {
				endSeg()
			}
		case c == ' ' || c == '\t' || c == '\r':
			endWord()
		default:
			w = append(w, c)
			inWord = true
		}
	}
	endSeg()
	return segs
}

// parseHeredocDelim 은 `<<` 뒤(pos)에서 구분자를 읽는다. 구분자는 글자·밑줄로 시작하거나 따옴표로 감싸야 한다
// (`$((1<<3))` 같은 산술 시프트를 heredoc 으로 오인해 뒤를 삼키지 않기 위해서다).
func parseHeredocDelim(cmd string, pos int) (heredoc, int, bool) {
	n := len(cmd)
	h := heredoc{}
	if pos < n && cmd[pos] == '-' {
		h.strip = true
		pos++
	}
	for pos < n && (cmd[pos] == ' ' || cmd[pos] == '\t') {
		pos++
	}
	if pos >= n {
		return h, 0, false
	}
	if q := cmd[pos]; q == '\'' || q == '"' {
		end := pos + 1
		for end < n && cmd[end] != q {
			end++
		}
		if end >= n {
			return h, 0, false
		}
		h.delim = cmd[pos+1 : end]
		return h, end + 1, h.delim != ""
	}
	start := pos
	if pos < n && cmd[pos] == '\\' {
		pos++
		start = pos
	}
	if pos >= n || !(isAlpha(cmd[pos]) || cmd[pos] == '_') {
		return h, 0, false
	}
	for pos < n && strings.IndexByte(" \t\n;&|<>()", cmd[pos]) < 0 {
		pos++
	}
	h.delim = cmd[start:pos]
	return h, pos, true
}

// heredocBody 는 pos(본문 첫 줄 시작)에서 본문과 구분자 줄 다음 위치를 돌려준다. 구분자가 없으면 문자열 끝까지가 본문이다.
func heredocBody(cmd string, pos int, h heredoc) (string, int) {
	n := len(cmd)
	start := pos
	for pos < n {
		nl := pos
		for nl < n && cmd[nl] != '\n' {
			nl++
		}
		line := cmd[pos:nl]
		if h.strip {
			for len(line) > 0 && line[0] == '\t' {
				line = line[1:]
			}
		}
		for len(line) > 0 && (line[len(line)-1] == ' ' || line[len(line)-1] == '\r' || line[len(line)-1] == '\t') {
			line = line[:len(line)-1]
		}
		next := nl + 1
		if nl >= n {
			next = n
		}
		if line == h.delim {
			body := ""
			if pos > start {
				body = cmd[start : pos-1]
			}
			return body, next
		}
		pos = next
	}
	return cmd[min(start, n):], n
}

func isAlpha(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }
func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// buildSegment 는 토큰열에서 리다이렉션을 떼어 Segment 를 만든다.
func buildSegment(toks []token) (Segment, bool) {
	seg := Segment{Words: []string{}, Redirects: []string{}}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if !t.quoted {
			if rest, app, ok := redirectOut(t.text); ok {
				target := rest
				switch {
				case rest == "":
					if i+1 < len(toks) {
						i++
						target = toks[i].text
						seg.Redirects = append(seg.Redirects, target)
					} else {
						target = ""
					}
				case rest[0] == '&': // 2>&1 같은 fd 복제는 파일이 아니다
					target = ""
				default:
					seg.Redirects = append(seg.Redirects, rest)
				}
				if app && target != "" {
					seg.Appends = append(seg.Appends, target)
				}
				continue
			}
		}
		seg.Words = append(seg.Words, t.text)
	}
	return seg, len(seg.Words) > 0 || len(seg.Redirects) > 0
}

// redirectOut 은 `>`, `>>`, `2>`, `&>`, `>|` 로 시작하는 토큰이면 연산자 뒤 나머지를 돌려준다.
// app 은 연산자가 `>>` 인가이다.
func redirectOut(s string) (rest string, app, ok bool) {
	i := 0
	if i < len(s) && s[i] == '&' {
		i++
	} else {
		for i < len(s) && isDigit(s[i]) {
			i++
		}
	}
	if i >= len(s) || s[i] != '>' {
		return "", false, false
	}
	i++
	if i < len(s) && (s[i] == '>' || s[i] == '|') {
		app = s[i] == '>'
		i++
	}
	return s[i:], app, true
}
