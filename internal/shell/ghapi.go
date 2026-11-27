package shell

import (
	"strings"
)

// gh api 호출이 PR 리뷰·코멘트를 올리거나 고치는 호출인지 나타내는 종류다.
const (
	GHAPIReview  = "review"  // 리뷰 만들기·제출, 리뷰 본문 수정
	GHAPIComment = "comment" // 인라인 코멘트·답글, PR(이슈) 대화 코멘트와 그 수정
)

// GHAPIRoute 는 gh api 호출의 엔드포인트 경로 조각, 메서드, 리뷰·코멘트 종류(GHAPIReview, GHAPIComment, 아니면 "")다.
// Method 는 대문자이고, 명시하지 않았으면 필드나 --input 이 있을 때 POST, 없으면 "" 이다.
type GHAPIRoute struct {
	Segs   []string
	Method string
	Kind   string
}

// ghField 는 -f·-F 로 넘긴 필드 하나다.
type ghField struct {
	key string
	val part
}

// ghCall 은 gh api 호출을 읽은 것이다.
type ghCall struct {
	GHAPIRoute
	fields     []ghField
	input      *part // --input 의 값
	approvable bool  // event=APPROVE 로 승인할 수 있는 리뷰 호출인가
}

// ghRoute 는 리뷰·코멘트 엔드포인트다. tail 은 경로의 끝부분이고 "#" 는 번호 자리다.
type ghRoute struct {
	method, kind string
	approvable   bool
	tail         []string
}

var ghRoutes = []ghRoute{
	{"POST", GHAPIReview, true, []string{"pulls", "#", "reviews"}},
	{"POST", GHAPIReview, true, []string{"pulls", "#", "reviews", "#", "events"}},
	{"PUT", GHAPIReview, false, []string{"pulls", "#", "reviews", "#"}},
	{"POST", GHAPIComment, false, []string{"pulls", "#", "comments"}},
	{"POST", GHAPIComment, false, []string{"pulls", "#", "comments", "#", "replies"}},
	{"PATCH", GHAPIComment, false, []string{"pulls", "comments", "#"}},
	{"POST", GHAPIComment, false, []string{"issues", "#", "comments"}},
	{"PATCH", GHAPIComment, false, []string{"issues", "comments", "#"}},
}

func (r ghRoute) match(segs []string, method string) bool {
	if method != r.method || len(segs) < len(r.tail) {
		return false
	}
	segs = segs[len(segs)-len(r.tail):]
	for i, t := range r.tail {
		if t == "#" && !isDigits(segs[i]) || t != "#" && t != segs[i] {
			return false
		}
	}
	return true
}

// GHAPI 는 gh api 호출의 엔드포인트와 메서드, 리뷰·코멘트 종류를 돌려준다. args 는 "api" 뒤의 단어다.
func GHAPI(args []string) GHAPIRoute {
	return parseGHAPI(args).GHAPIRoute
}

// parseGHAPI 는 gh api 의 인자를 읽는다. args 는 "api" 뒤의 단어다.
// 필드 값이 `@` 로 시작하는 것은 -F·--field(typed field)일 때만 파일(`@-` 는 표준 입력)이고,
// -f·--raw-field 는 글자 그대로의 문자열이다(gh 의 규칙).
func parseGHAPI(args []string) ghCall {
	valueOpts := map[string]bool{"-H": true, "--header": true, "-q": true, "--jq": true, "-t": true, "--template": true, "--hostname": true, "--cache": true, "-p": true, "--preview": true}
	fieldOpts := map[string]bool{"-f": true, "-F": true, "--field": true, "--raw-field": true}
	var c ghCall
	var endpoint, method string
	hasField := false
	field := func(kv string, typed bool, at int) {
		hasField = true
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return
		}
		var p part
		switch {
		case typed && v == "@-":
			p = part{kind: partStdin, at: at}
		case typed && strings.HasPrefix(v, "@"):
			p = part{kind: partFile, val: v[1:], at: at}
		default:
			p = part{kind: partText, val: v, at: at}
		}
		c.fields = append(c.fields, ghField{key: k, val: p})
	}
	input := func(v string, at int) {
		hasField = true
		p := fileOrStdin(v, at)
		c.input = &p
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
			at := i + 1
			input(next(), at)
		case strings.HasPrefix(a, "--input="):
			input(strings.TrimPrefix(a, "--input="), i)
		case valueOpts[a]:
			next()
		case strings.HasPrefix(a, "-"):
		case endpoint == "":
			endpoint = a
		}
	}
	if i := strings.IndexAny(endpoint, "?#"); i >= 0 {
		endpoint = endpoint[:i]
	}
	c.Segs = strings.FieldsFunc(endpoint, func(r rune) bool { return r == '/' })
	c.Method = strings.ToUpper(method)
	if c.Method == "" && hasField {
		c.Method = "POST"
	}
	for _, r := range ghRoutes {
		if r.match(c.Segs, c.Method) {
			c.Kind, c.approvable = r.kind, r.approvable
			break
		}
	}
	return c
}

// prWrite 는 PR 을 만들거나 고치는 호출인가(pulls 로 끝나는 경로의 POST, pulls/<번호> 가 든 경로의 PATCH).
func (c ghCall) prWrite() bool {
	if c.Kind != "" {
		return false
	}
	if len(c.Segs) > 0 && c.Segs[len(c.Segs)-1] == "pulls" && c.Method == "POST" {
		return true
	}
	for i := 0; i+1 < len(c.Segs); i++ {
		if c.Segs[i] == "pulls" && isDigits(c.Segs[i+1]) && c.Method == "PATCH" {
			return true
		}
	}
	return false
}

// reviewParts 는 리뷰·코멘트 호출에서 본문이 되는 필드(body, comments[][body])다. 그런 호출이 아니면 nil 이다.
func (c ghCall) reviewParts() []part {
	if c.Kind == "" {
		return nil
	}
	var out []part
	for _, f := range c.fields {
		if f.key == "body" || f.key == "comments[][body]" {
			out = append(out, f.val)
		}
	}
	return out
}

// approves 는 필드로 event=APPROVE 를 넘긴 승인 호출인가.
func (c ghCall) approves() bool {
	if !c.approvable {
		return false
	}
	for _, f := range c.fields {
		if f.key == "event" && f.val.kind == partText && strings.EqualFold(f.val.val, "APPROVE") {
			return true
		}
	}
	return false
}
