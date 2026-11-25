package review

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/hee-develop/heka/internal/register"
)

// Interactive 는 터미널에서 검토 항목을 한 건씩 batch 건까지 보여 주고 키로 결정을 받는다.
// rejudge 는 미판정 항목의 새 항목 id 를 그 자리에서 다시 판정한다.
func Interactive(e register.Env, in io.Reader, out io.Writer, batch int, rejudge func(newID string) error) error {
	entries := List(e)
	if len(entries) == 0 {
		fmt.Fprintln(out, "검토할 항목이 없습니다.")
		return nil
	}
	shown := entries
	if len(shown) > batch {
		shown = shown[:batch]
	}
	rd := bufio.NewReader(in)
	for i, en := range shown {
		fmt.Fprintf(out, "\n[%d/%d] %s\n", i+1, len(shown), header(en))
		printEntry(e.Home, out, en)
		keys := keysFor(en)
	prompt:
		for {
			fmt.Fprintf(out, "선택 %s > ", keys.help)
			line, err := rd.ReadString('\n')
			k := strings.ToLower(strings.TrimSpace(line))
			if k == "" && err != nil {
				return nil // 입력이 끝남
			}
			if k == "q" {
				return remaining(e, out)
			}
			if k == "s" {
				break prompt
			}
			if k == "j" && en.Kind == KindUnjudged {
				if rejudge == nil {
					fmt.Fprintln(out, "다시 판정할 수 없습니다.")
					continue
				}
				if err := rejudge(en.NewID); err != nil {
					fmt.Fprintln(out, "다시 판정하지 못했습니다:", err)
					continue
				}
				fmt.Fprintln(out, "다시 판정했습니다. 결과는 다음 검토에서 보입니다.")
				break prompt
			}
			dec, ok := keys.dec[k]
			if !ok || k == "j" {
				fmt.Fprintln(out, "알 수 없는 키입니다.")
				continue
			}
			msg, err := Apply(e, en.ID, dec, Options{})
			if err != nil {
				fmt.Fprintln(out, "적용하지 못했습니다:", err)
				continue
			}
			fmt.Fprintln(out, msg)
			break prompt
		}
	}
	return remaining(e, out)
}

func remaining(e register.Env, out io.Writer) error {
	fmt.Fprintf(out, "\n남은 검토 %d건.\n", len(List(e)))
	return nil
}

type keySet struct {
	help string
	dec  map[string]string
}

func keysFor(en Entry) keySet {
	if en.Kind == KindUnjudged {
		return keySet{"[j]다시 판정 [k]따로 두기 [n]새 것 버리기 [s]건너뛰기 [q]끝", map[string]string{"j": "", "k": DecKeep, "n": DecDropNew}}
	}
	dec := map[string]string{"k": DecKeep, "n": DecDropNew, "o": DecDropOld}
	help := "[k]따로 두기 [n]새 것 버리기 [o]옛 것 버리기 [s]건너뛰기 [q]끝"
	if en.Merged != nil && strings.TrimSpace(en.Merged.Text) != "" {
		dec["m"] = DecMerge
		help = "[m]합치기 " + help
	}
	return keySet{help, dec}
}

func header(en Entry) string {
	switch en.Kind {
	case KindConflict:
		return fmt.Sprintf("모순: 새 %s <-> 옛 %s", en.NewID, en.TargetID)
	case KindMerge:
		return fmt.Sprintf("합치기: 새 %s <-> 옛 %s", en.NewID, en.TargetID)
	case KindSample:
		return fmt.Sprintf("표본(따로로 판정함): 새 %s <-> 옛 %s", en.NewID, en.TargetID)
	}
	return fmt.Sprintf("미판정: 새 %s (후보 %s)", en.NewID, strings.Join(en.Candidates, ", "))
}

func printEntry(home string, out io.Writer, en Entry) {
	if en.Reason != "" {
		fmt.Fprintf(out, "이유: %s\n", en.Reason)
	}
	for _, p := range []struct{ label, id string }{{"새", en.NewID}, {"옛", en.TargetID}} {
		if p.id == "" {
			continue
		}
		v := View(home, p.id)
		if v == nil {
			continue
		}
		fmt.Fprintf(out, "%s 항목 %s (출처 %s, 범위 %s, 주제어 %s)\n  주입문: %s\n", p.label, v.ID, v.Source, strings.Join(v.Scope, ", "), strings.Join(v.About, ", "), v.Text)
		if v.Body != "" {
			fmt.Fprintf(out, "  본문: %s\n", strings.ReplaceAll(v.Body, "\n", "\n    "))
		}
	}
	if m := en.Merged; m != nil && strings.TrimSpace(m.Text) != "" {
		fmt.Fprintf(out, "합친 초안:\n  주입문: %s\n  주제어: %s\n", m.Text, strings.Join(m.About, ", "))
		if strings.TrimSpace(m.Body) != "" {
			fmt.Fprintf(out, "  본문: %s\n", strings.ReplaceAll(strings.TrimSpace(m.Body), "\n", "\n    "))
		}
	}
}
