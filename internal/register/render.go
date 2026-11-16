package register

import (
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// render 는 항목 파일 글을 만든다. 필드 순서와 모양은 설계 문서의 예시를 따른다:
// 문자열은 필요할 때만 따옴표를 치고, 목록은 한 줄 꼴로 쓴다.
func render(in Input) []byte {
	var b strings.Builder
	line := func(k, v string) { b.WriteString(k + ":" + strings.TrimRight(" "+v, " ") + "\n") }
	list := func(k string, vs []string) {
		if len(vs) == 0 {
			return
		}
		if k != "about" && k != "scope" && len(vs) == 1 { // when 의 값 하나는 설계 문서의 예시처럼 그냥 쓴다
			line(k, scalar(vs[0], false))
			return
		}
		q := make([]string, len(vs))
		for i, v := range vs {
			q[i] = scalar(v, true)
		}
		line(k, "["+strings.Join(q, ", ")+"]")
	}
	b.WriteString("---\n")
	line("id", scalar(in.ID, false))
	line("text", scalar(in.Text, false))
	line("must", strconv.FormatBool(in.Must))
	if w := in.When; len(w.Moment)+len(w.Action)+len(w.Path)+len(w.Repo)+len(w.Agent) > 0 {
		b.WriteString("when:\n")
		for _, k := range []struct {
			name string
			vs   []string
		}{{"moment", w.Moment}, {"action", w.Action}, {"path", w.Path}, {"repo", w.Repo}, {"agent", w.Agent}} {
			if len(k.vs) > 0 {
				b.WriteString("  ")
				list(k.name, k.vs)
			}
		}
	}
	list("about", in.About)
	list("scope", in.Scope)
	line("source", scalar(in.Source, false))
	if in.Quote != "" {
		line("quote", scalar(in.Quote, false))
	}
	line("checked", scalar(in.Checked, false))
	line("expires", scalar(in.Expires, false))
	conf := in.Confirm
	if conf == "" {
		conf = "false"
	}
	if conf == "true" || conf == "false" || conf == "always" {
		line("confirm", conf) // 따옴표를 치면 문자열이라 불리언처럼 읽히지 않는다
	} else {
		line("confirm", scalar(conf, false))
	}
	if c := in.Check; c != nil {
		b.WriteString("check:\n")
		for _, kv := range [][2]string{{"target", c.Target}, {"pattern", c.Pattern}, {"not_pattern", c.NotPattern}, {"mode", c.Mode}} {
			if kv[1] != "" {
				b.WriteString("  " + kv[0] + ": " + scalar(kv[1], false) + "\n")
			}
		}
	}
	b.WriteString("---\n")
	if body := strings.TrimLeft(in.Body, "\n"); body != "" {
		b.WriteString(strings.TrimRight(body, "\n") + "\n")
	}
	return []byte(b.String())
}

// scalar 는 YAML 문자열 값이다. 맨 글로 써도 같은 값으로 읽히면 따옴표를 치지 않는다.
// flow 는 한 줄 목록 안이라 쉼표와 괄호도 따옴표가 필요하다.
func scalar(s string, flow bool) string {
	if s == "" {
		return ""
	}
	b, err := yaml.Marshal(s)
	out := strings.TrimSpace(string(b))
	if err != nil || strings.ContainsAny(s, "\n\r") || (flow && strings.ContainsAny(s, ",[]{}")) {
		return strconv.Quote(s)
	}
	// 날짜 모양의 글은 맨 글로 써도 text 로 읽히므로 그대로 쓴다.
	if len(s) == 10 && (out == "'"+s+"'" || out == `"`+s+`"`) {
		return s
	}
	return out
}
