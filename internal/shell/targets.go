package shell

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

// 이 파일은 결과물 검사의 대상 글을 Bash 명령에서 뽑는다: 커밋 메시지와 PR 본문.
// 파일로 넘긴 글(-F <파일>, --body-file <파일>)은 files.go 의 규칙대로 풀어서 읽고,
// 풀 수 없거나 글이 없는 경우는 대상에서 뺀다.

var hdOpenRe = regexp.MustCompile(`^\$\(\s*cat\s+<<(-?)[ \t]*(['"]?)([A-Za-z_][A-Za-z0-9_]*)['"]?[ \t]*\n`)

const phMark = "\x01HD"

// maskCatHeredocs 는 `$(cat <<'EOF' … EOF )` 덩어리를 자리표시자로 바꾸고 본문 목록을 돌려준다.
// 본문에 따옴표나 괄호가 있어도 토크나이저가 흐트러지지 않게 하려는 것이다.
func maskCatHeredocs(cmd string) (string, []string) {
	var bodies []string
	var b strings.Builder
	for i := 0; i < len(cmd); {
		if cmd[i] == '$' && i+1 < len(cmd) && cmd[i+1] == '(' {
			if m := hdOpenRe.FindStringSubmatch(cmd[i:]); m != nil {
				h := heredoc{delim: m[3], strip: m[1] == "-"}
				bodyStart := i + len(m[0])
				body, next := heredocBody(cmd, bodyStart, h)
				// 구분자 줄 뒤에 닫는 괄호가 와야 한다.
				j := next
				for j < len(cmd) && (cmd[j] == ' ' || cmd[j] == '\t' || cmd[j] == '\n' || cmd[j] == '\r') {
					j++
				}
				if j < len(cmd) && cmd[j] == ')' && next > bodyStart {
					bodies = append(bodies, body)
					b.WriteString(phMark + strconv.Itoa(len(bodies)-1) + "\x01")
					i = j + 1
					continue
				}
			}
		}
		b.WriteByte(cmd[i])
		i++
	}
	return b.String(), bodies
}

var phRe = regexp.MustCompile("\x01HD([0-9]+)\x01")

func unmask(s string, bodies []string) string {
	if len(bodies) == 0 || !strings.Contains(s, phMark) {
		return s
	}
	return phRe.ReplaceAllStringFunc(s, func(m string) string {
		n, err := strconv.Atoi(phRe.FindStringSubmatch(m)[1])
		if err != nil || n >= len(bodies) {
			return m
		}
		return bodies[n]
	})
}

// maskedSegments 는 마스킹한 명령을 구간으로 나누고 단어에서 자리표시자를 본문으로 되돌린다.
func maskedSegments(cmd string) []Segment {
	masked, bodies := maskCatHeredocs(cmd)
	segs := SplitCommand(masked)
	for i := range segs {
		for j, w := range segs[i].Words {
			segs[i].Words[j] = unmask(w, bodies)
		}
	}
	return segs
}

func isLetters(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isAlpha(s[i]) {
			return false
		}
	}
	return s != ""
}

// 인자가 글 자체인가, 표준 입력(heredoc)인가, 파일 경로인가.
type partKind int

const (
	partText partKind = iota
	partStdin
	partFile
)

// part 는 명령 인자 하나가 가리키는 글이다. at 은 그 인자가 있는 단어의 위치(분석한 슬라이스 안)다.
type part struct {
	kind partKind
	val  string
	at   int
}

// fileOrStdin 은 -F·--body-file 의 값이 `-` 면 표준 입력, 아니면 파일이다.
func fileOrStdin(v string, at int) part {
	if v == "-" {
		return part{kind: partStdin, at: at}
	}
	return part{kind: partFile, val: v, at: at}
}

// commitParts 는 git commit 의 인자(rest)에서 메시지가 되는 글을 나온 순서대로 돌려준다.
func commitParts(rest []string) []part {
	var out []part
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--":
			i = len(rest)
		case a == "-m" || a == "--message":
			if i+1 < len(rest) {
				out = append(out, part{kind: partText, val: rest[i+1], at: i + 1})
				i++
			}
		case strings.HasPrefix(a, "--message="):
			out = append(out, part{kind: partText, val: a[len("--message="):], at: i})
		case a == "-F" || a == "--file":
			v := ""
			if i+1 < len(rest) {
				v = rest[i+1]
				out = append(out, fileOrStdin(v, i+1))
				i++
			} else {
				out = append(out, part{kind: partFile, at: i}) // 값이 없다
			}
		case strings.HasPrefix(a, "--file="):
			out = append(out, fileOrStdin(a[len("--file="):], i))
		case strings.HasPrefix(a, "--"):
		case len(a) > 2 && strings.HasPrefix(a, "-m"):
			out = append(out, part{kind: partText, val: a[2:], at: i})
		case len(a) > 2 && strings.HasPrefix(a, "-F"):
			out = append(out, fileOrStdin(a[2:], i)) // -F<경로>
		case len(a) > 1 && a[0] == '-' && isLetters(a[1:]):
			// -am "msg" 같은 묶음: 마지막 글자가 m 이면 다음 단어가 메시지, F 이면 다음 단어가 파일이다.
			last := a[len(a)-1]
			if last == 'm' && i+1 < len(rest) {
				out = append(out, part{kind: partText, val: rest[i+1], at: i + 1})
				i++
			} else if last == 'F' {
				if i+1 < len(rest) {
					out = append(out, fileOrStdin(rest[i+1], i+1))
					i++
				} else {
					out = append(out, part{kind: partFile, at: i})
				}
			}
		}
	}
	return out
}

// CommitMessages 는 git commit 마다 메시지를 돌려준다. 파일을 읽지 않는 꼴이다(CommitMessagesIn 의 Files 가 비어 있다).
func CommitMessages(cmd string) []string {
	return CommitMessagesIn(cmd, Files{}).Texts
}

// CommitMessagesIn 은 git commit 마다 메시지를 돌려준다.
// -m 과 --message 의 값은 빈 줄로 잇고(git 이 -m 마다 문단을 나누는 것과 같다), `-F -` 는 heredoc 본문이다.
// -F <파일> 은 files.go 의 규칙으로 풀어서 읽는다. 풀 수 없는 것과 메시지 없는 commit(편집기, --no-edit, -C)은 뺀다.
func CommitMessagesIn(cmd string, f Files) Extracted {
	segs := maskedSegments(cmd)
	var res Extracted
	for k, seg := range segs {
		ws := StripPrefix(seg.Words)
		sub, rest, ok := GitSubcommand(ws)
		if !ok || sub != "commit" {
			continue
		}
		var msgs []string
		skip := ""
		for _, p := range commitParts(rest) {
			switch p.kind {
			case partText:
				msgs = append(msgs, p.val)
			case partStdin:
				if len(seg.Heredocs) == 0 {
					skip = firstNote(skip, NoteNoTarget)
					continue
				}
				msgs = append(msgs, seg.Heredocs[0])
			case partFile:
				text, note := fileText(segs, k, p.val, GitDirs(ws), f)
				if note != "" {
					skip = firstNote(skip, note)
					continue
				}
				msgs = append(msgs, text)
			}
		}
		if skip != "" {
			res.skip(skip)
			continue
		}
		if len(msgs) == 0 {
			continue
		}
		res.Texts = append(res.Texts, strings.Join(msgs, "\n\n"))
	}
	return res.done()
}

// PRBodies 는 PRBodiesIn 을 파일을 읽지 않고 부른 결과다.
func PRBodies(cmd string) []string {
	return PRBodiesIn(cmd, Files{}).Texts
}

// PRBodiesIn 은 gh pr create·edit 마다 --body·-b 값을, `--body-file -` 는 heredoc 본문을 돌려준다.
// --body-file <파일> 은 files.go 의 규칙으로 풀어서 읽는다.
// gh api 로 PR 을 만들거나 고치는 호출(pulls 엔드포인트)은 body 필드의 값을 돌려준다.
// 풀 수 없는 파일과 본문 없음은 뺀다.
func PRBodiesIn(cmd string, f Files) Extracted {
	segs := maskedSegments(cmd)
	var res Extracted
	for k, seg := range segs {
		ws := StripPrefix(seg.Words)
		if len(ws) < 2 || path.Base(ws[0]) != "gh" {
			continue
		}
		var parts []part
		switch {
		case len(ws) >= 3 && ws[1] == "pr" && (ws[2] == "create" || ws[2] == "edit"):
			parts = prParts(ws[3:])
		case ws[1] == "api":
			if p, ok := ghAPIBody(ws[2:]); ok {
				parts = []part{p}
			}
		}
		var bodies []string
		skip := ""
		for _, p := range parts {
			switch p.kind {
			case partText:
				bodies = append(bodies, p.val)
			case partStdin:
				if len(seg.Heredocs) == 0 {
					skip = firstNote(skip, NoteNoTarget)
					continue
				}
				bodies = append(bodies, seg.Heredocs[0])
			case partFile:
				text, note := fileText(segs, k, p.val, nil, f)
				if note != "" {
					skip = firstNote(skip, note)
					continue
				}
				bodies = append(bodies, text)
			}
		}
		if skip != "" {
			res.skip(skip)
			continue
		}
		if len(bodies) == 0 {
			continue
		}
		res.Texts = append(res.Texts, strings.Join(bodies, "\n"))
	}
	return res.done()
}

// prParts 는 gh pr create·edit 의 인자(args)에서 본문이 되는 글을 나온 순서대로 돌려준다.
func prParts(args []string) []part {
	var out []part
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--body" || a == "-b":
			if i+1 < len(args) {
				out = append(out, part{kind: partText, val: args[i+1], at: i + 1})
				i++
			}
		case strings.HasPrefix(a, "--body="):
			out = append(out, part{kind: partText, val: a[len("--body="):], at: i})
		case a == "--body-file" || a == "-F":
			if i+1 < len(args) {
				out = append(out, fileOrStdin(args[i+1], i+1))
				i++
			} else {
				out = append(out, part{kind: partFile, at: i}) // 값이 없다
			}
		case strings.HasPrefix(a, "--body-file="):
			out = append(out, fileOrStdin(a[len("--body-file="):], i))
		}
	}
	return out
}

// ghAPIBody 는 gh api 호출이 PR 을 만들거나 고치면(pulls 로 끝나는 경로의 POST, pulls/<번호> 의 PATCH)
// body 필드가 가리키는 글을 돌려준다. args 는 "api" 뒤의 단어다.
// 값이 `@` 로 시작하는 것은 -F·--field(typed field)일 때만 파일(`@-` 는 표준 입력)이고,
// -f·--raw-field 는 글자 그대로의 문자열이다(gh 의 규칙).
func ghAPIBody(args []string) (part, bool) {
	valueOpts := map[string]bool{"-H": true, "--header": true, "-q": true, "--jq": true, "-t": true, "--template": true, "--hostname": true, "--cache": true, "-p": true, "--preview": true, "--input": true}
	fieldOpts := map[string]bool{"-f": true, "-F": true, "--field": true, "--raw-field": true}
	var endpoint, method string
	var body part
	hasField, hasBody := false, false
	field := func(kv string, typed bool, at int) {
		hasField = true
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k != "body" {
			return
		}
		hasBody = true
		switch {
		case typed && v == "@-":
			body = part{kind: partStdin, at: at}
		case typed && strings.HasPrefix(v, "@"):
			body = part{kind: partFile, val: v[1:], at: at}
		default:
			body = part{kind: partText, val: v, at: at}
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch {
		case a == "-X" || a == "--method":
			method = next()
		case strings.HasPrefix(a, "--method="):
			method = strings.TrimPrefix(a, "--method=")
		case strings.HasPrefix(a, "-X") && !strings.HasPrefix(a, "--"):
			method = strings.TrimPrefix(a, "-X")
		case fieldOpts[a]:
			typed := a == "-F" || a == "--field"
			at := i + 1
			field(next(), typed, at)
		case strings.HasPrefix(a, "--field=") || strings.HasPrefix(a, "--raw-field="):
			_, v, _ := strings.Cut(a, "=")
			field(v, strings.HasPrefix(a, "--field="), i)
		case len(a) > 2 && (strings.HasPrefix(a, "-f") || strings.HasPrefix(a, "-F")) && !strings.HasPrefix(a, "--"):
			field(a[2:], a[1] == 'F', i)
		case a == "--input":
			hasField = true
			next()
		case valueOpts[a]:
			next()
		case strings.HasPrefix(a, "-"):
		case endpoint == "":
			endpoint = a
		}
	}
	if !hasBody {
		return part{}, false
	}
	if i := strings.IndexAny(endpoint, "?#"); i >= 0 {
		endpoint = endpoint[:i]
	}
	segs := strings.FieldsFunc(endpoint, func(r rune) bool { return r == '/' })
	method = strings.ToUpper(method)
	if method == "" && hasField {
		method = "POST"
	}
	isPR := false
	if len(segs) > 0 && segs[len(segs)-1] == "pulls" && method == "POST" {
		isPR = true
	}
	for i := 0; i+1 < len(segs); i++ {
		if segs[i] == "pulls" && isDigits(segs[i+1]) && method == "PATCH" {
			isPR = true
		}
	}
	return body, isPR
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}
