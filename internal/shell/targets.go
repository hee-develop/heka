package shell

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

// 이 파일은 결과물 검사의 대상 글을 Bash 명령에서 뽑는다: 커밋 메시지와 PR 본문.
// 파일로 넘겨서 뽑을 수 없는 경우(-F <파일>, --body-file <파일>)와 글이 없는 경우는 대상에서 뺀다.

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

// CommitMessages 는 git commit 마다 메시지를 돌려준다.
// -m 과 --message 의 값은 빈 줄로 잇고(git 이 -m 마다 문단을 나누는 것과 같다), `-F -` 는 heredoc 본문이다.
// -F <파일> 과 메시지 없는 commit(편집기, --no-edit, -C)은 뽑을 수 없어 뺀다.
func CommitMessages(cmd string) []string {
	var out []string
	for _, seg := range maskedSegments(cmd) {
		sub, rest, ok := GitSubcommand(StripPrefix(seg.Words))
		if !ok || sub != "commit" {
			continue
		}
		var msgs []string
		skip, stdin := false, false
		for i := 0; i < len(rest); i++ {
			a := rest[i]
			switch {
			case a == "--":
				i = len(rest)
			case a == "-m" || a == "--message":
				if i+1 < len(rest) {
					msgs = append(msgs, rest[i+1])
					i++
				}
			case strings.HasPrefix(a, "--message="):
				msgs = append(msgs, a[len("--message="):])
			case a == "-F" || a == "--file" || strings.HasPrefix(a, "--file="):
				v := ""
				if strings.HasPrefix(a, "--file=") {
					v = a[len("--file="):]
				} else if i+1 < len(rest) {
					v = rest[i+1]
					i++
				}
				if v == "-" {
					stdin = true
				} else {
					skip = true
				}
			case strings.HasPrefix(a, "--"):
			case len(a) > 2 && strings.HasPrefix(a, "-m"):
				msgs = append(msgs, a[2:])
			case len(a) > 1 && a[0] == '-' && isLetters(a[1:]):
				// -am "msg" 같은 묶음: 마지막 글자가 m 이면 다음 단어가 메시지다.
				if a[len(a)-1] == 'm' && i+1 < len(rest) {
					msgs = append(msgs, rest[i+1])
					i++
				} else if a[len(a)-1] == 'F' && i+1 < len(rest) {
					if rest[i+1] == "-" {
						stdin = true
					} else {
						skip = true
					}
					i++
				}
			}
		}
		if skip {
			continue
		}
		if stdin {
			if len(seg.Heredocs) == 0 {
				continue
			}
			msgs = append(msgs, seg.Heredocs[0])
		}
		if len(msgs) == 0 {
			continue
		}
		out = append(out, strings.Join(msgs, "\n\n"))
	}
	return out
}

// PRBodies 는 gh pr create·edit 마다 --body·-b 값을, `--body-file -` 는 heredoc 본문을 돌려준다.
// gh api 로 PR 을 만들거나 고치는 호출(pulls 엔드포인트)은 `-f body=…` 의 값을 돌려준다.
// 파일로 넘긴 본문(--body-file <파일>, body=@파일)과 본문 없음은 뺀다.
func PRBodies(cmd string) []string {
	var out []string
	for _, seg := range maskedSegments(cmd) {
		ws := StripPrefix(seg.Words)
		if len(ws) < 2 || path.Base(ws[0]) != "gh" {
			continue
		}
		var body string
		var ok bool
		switch {
		case len(ws) >= 3 && ws[1] == "pr" && (ws[2] == "create" || ws[2] == "edit"):
			body, ok = prFlagBody(ws[3:], seg.Heredocs)
		case ws[1] == "api":
			body, ok = ghAPIBody(ws[2:], seg.Heredocs)
		}
		if ok {
			out = append(out, body)
		}
	}
	return out
}

func prFlagBody(args, heredocs []string) (string, bool) {
	var bodies []string
	skip, stdin := false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--body" || a == "-b":
			if i+1 < len(args) {
				bodies = append(bodies, args[i+1])
				i++
			}
		case strings.HasPrefix(a, "--body="):
			bodies = append(bodies, a[len("--body="):])
		case a == "--body-file" || a == "-F" || strings.HasPrefix(a, "--body-file="):
			v := ""
			if strings.HasPrefix(a, "--body-file=") {
				v = a[len("--body-file="):]
			} else if i+1 < len(args) {
				v = args[i+1]
				i++
			}
			if v == "-" {
				stdin = true
			} else {
				skip = true
			}
		}
	}
	if skip {
		return "", false
	}
	if stdin {
		if len(heredocs) == 0 {
			return "", false
		}
		bodies = append(bodies, heredocs[0])
	}
	if len(bodies) == 0 {
		return "", false
	}
	return strings.Join(bodies, "\n"), true
}

// ghAPIBody 는 gh api 호출이 PR 을 만들거나 고치면(pulls 로 끝나는 경로의 POST, pulls/<번호> 의 PATCH)
// body 필드의 값을 돌려준다. args 는 "api" 뒤의 단어다.
func ghAPIBody(args, heredocs []string) (string, bool) {
	valueOpts := map[string]bool{"-H": true, "--header": true, "-q": true, "--jq": true, "-t": true, "--template": true, "--hostname": true, "--cache": true, "-p": true, "--preview": true, "--input": true}
	fieldOpts := map[string]bool{"-f": true, "-F": true, "--field": true, "--raw-field": true}
	var endpoint, method, body string
	hasField, hasBody := false, false
	field := func(kv string) {
		hasField = true
		if k, v, ok := strings.Cut(kv, "="); ok && k == "body" {
			body, hasBody = v, true
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
			field(next())
		case strings.HasPrefix(a, "--field=") || strings.HasPrefix(a, "--raw-field="):
			_, v, _ := strings.Cut(a, "=")
			field(v)
		case len(a) > 2 && (strings.HasPrefix(a, "-f") || strings.HasPrefix(a, "-F")) && !strings.HasPrefix(a, "--"):
			field(a[2:])
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
		return "", false
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
	if !isPR {
		return "", false
	}
	switch {
	case body == "@-":
		if len(heredocs) == 0 {
			return "", false
		}
		return heredocs[0], true
	case strings.HasPrefix(body, "@"):
		return "", false // 파일에서 읽는 본문
	}
	return body, true
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
