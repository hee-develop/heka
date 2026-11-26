package review

import (
	"fmt"
	"strings"

	"github.com/hee-develop/heka/internal/item"
)

// DiffField 는 두 항목에서 값이 다른 머리말 필드 하나다. 합치면 새 항목 쪽 값이 사라지는 것을 보이려는 것이다.
type DiffField struct {
	Field  string `json:"field"`
	New    string `json:"new"`
	Target string `json:"target"`
}

func whenString(w item.When) string {
	var parts []string
	for _, k := range []struct {
		name string
		vs   item.List
	}{{"moment", w.Moment}, {"action", w.Action}, {"repo", w.Repo}, {"path", w.Path}, {"agent", w.Agent}} {
		if len(k.vs) > 0 {
			parts = append(parts, k.name+"="+strings.Join(k.vs, "|"))
		}
	}
	return strings.Join(parts, "; ")
}

func checkString(c *item.Check) string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf("target=%s; pattern=%s; not_pattern=%s; mode=%s; evidence=%s", c.Target, c.Pattern, c.NotPattern, c.Mode, c.Evidence)
}

func headerFields(it *item.Item) [][2]string {
	return [][2]string{
		{"must", fmt.Sprint(it.Must)}, {"when", whenString(it.When)}, {"scope", strings.Join(it.Scope, ", ")},
		{"confirm", it.ConfirmValue()}, {"check", checkString(it.Check)}, {"expires", string(it.Expires)},
		{"checked", string(it.Checked)}, {"source", it.Source}, {"quote", it.Quote},
	}
}

// HeaderDiff 는 새 항목과 대상 항목에서 값이 다른 머리말 필드(must, when, scope, confirm, check, expires, checked, source, quote)다.
// 둘 중 하나를 읽지 못하거나 대상이 없으면 nil 이다.
func HeaderDiff(home string, en Entry) []DiffField {
	if en.TargetID == "" {
		return nil
	}
	n, t := parseItem(home, en.NewID), parseItem(home, en.TargetID)
	if n == nil || t == nil {
		return nil
	}
	nf, tf := headerFields(n), headerFields(t)
	var out []DiffField
	for i := range nf {
		if nf[i][1] != tf[i][1] {
			out = append(out, DiffField{nf[i][0], nf[i][1], tf[i][1]})
		}
	}
	return out
}

func parseItem(home, id string) *item.Item {
	it, err := item.Parse(home + "/items/" + id + ".md")
	if err != nil || !it.Valid() {
		return nil
	}
	return it
}

// diffLine 은 머리말 차이를 한 줄로 만든다. 차이가 없으면 빈 글이다.
func diffLine(d []DiffField) string {
	var parts []string
	for _, f := range d {
		n, t := f.New, f.Target
		if n == "" {
			n = "없음"
		}
		if t == "" {
			t = "없음"
		}
		parts = append(parts, fmt.Sprintf("%s(새 %s / 옛 %s)", f.Field, n, t))
	}
	return strings.Join(parts, "; ")
}
