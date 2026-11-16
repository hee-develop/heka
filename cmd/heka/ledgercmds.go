package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/hee-develop/heka/internal/action"
	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/item"
	"github.com/hee-develop/heka/internal/ledger"
	"github.com/hee-develop/heka/internal/register"
)

// oneLine 은 줄바꿈과 겹친 공백을 한 칸으로 줄인다.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// shortTS 는 원장의 시각에서 밀리초를 뺀다(UTC).
func shortTS(ts string) string {
	if t, err := time.Parse("2006-01-02T15:04:05.000Z", ts); err == nil {
		return t.UTC().Format("2006-01-02T15:04:05Z")
	}
	return ts
}

// mentions 는 줄이 그 항목을 넣었거나 뺀 줄인가.
func mentions(r ledger.Row, id string) bool {
	for _, i := range r.Injected {
		if i.ID == id {
			return true
		}
	}
	for _, d := range r.Dropped {
		if d.ID == id {
			return true
		}
	}
	return false
}

// whyLine 은 원장 줄 하나를 사람이 읽는 한 줄로 만든다.
func whyLine(r ledger.Row) string {
	what := strings.Join(r.Actions, ",")
	if what == "" {
		what = r.Moment
	}
	parts := []string{shortTS(r.TS), r.Event, what}
	line := strings.Join(parts, " ")
	var in []string
	for _, i := range r.Injected {
		s := i.ID
		if w := oneLine(i.Why); w != "" {
			s += "(" + w + ")"
		}
		in = append(in, s)
	}
	switch {
	case r.IsHook() && len(in) == 0:
		line += " | 넣은 항목 없음"
	case r.IsHook():
		line += " | 넣음: " + strings.Join(in, "; ")
	case len(in) > 0:
		line += " | 대상: " + strings.Join(in, "; ")
	}
	if len(r.Dropped) > 0 {
		var out []string
		for _, d := range r.Dropped {
			out = append(out, fmt.Sprintf("%s[%s: %s]", d.ID, d.Stage, oneLine(d.Reason)))
		}
		line += " | 뺌: " + strings.Join(out, "; ")
	}
	if len(r.Judges) > 0 {
		var js []string
		for _, j := range r.Judges {
			js = append(js, fmt.Sprintf("%s %s %dms", j.ID, judgeWord(j), j.MS))
		}
		line += " | 판정: " + strings.Join(js, "; ")
	}
	if len(r.Checks) > 0 {
		var cs []string
		for _, c := range r.Checks {
			t := c.ID + " " + c.Target + " " + c.Result
			if c.Note != "" {
				t += "(" + oneLine(c.Note) + ")"
			}
			if c.Blocked {
				t += "(막음)"
			}
			cs = append(cs, t)
		}
		line += " | 검사: " + strings.Join(cs, "; ")
	}
	if r.Reason != "" {
		line += " | 사유: " + oneLine(r.Reason)
	}
	if r.Tokens > 0 {
		line += fmt.Sprintf(" | 토큰 %d", r.Tokens)
	}
	if r.Shadow {
		line += " (그림자)"
	}
	return line
}

// judgeWord 는 판정 기록 하나의 결과를 한 마디로 적는다.
func judgeWord(j ledger.Judged) string {
	switch {
	case j.Timeout:
		return "시간 초과"
	case j.Error:
		return "오류"
	case j.Inject:
		return "넣음"
	}
	return "안 넣음"
}

// runWhy 는 `heka why` 를 실행한다. 원장의 최근 줄을 사람이 읽는 꼴로 보인다.
func runWhy(args []string, stdout, stderr io.Writer) int {
	var session, id string
	var last int
	var asJSON bool
	fs := flag.NewFlagSet("why", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&session, "session", "", "이 세션의 줄만")
	fs.IntVar(&last, "last", 20, "최근 몇 줄")
	fs.StringVar(&id, "id", "", "이 항목이 넣어졌거나 빠진 줄만")
	fs.BoolVar(&asJSON, "json", false, "원장 줄을 JSON 배열로 출력")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) > 0 || last <= 0 {
		fmt.Fprintln(stderr, "heka why: 사용법: heka why [--session <id>] [--last N] [--id <항목 id>] [--json] (N 은 1 이상)")
		return 2
	}
	home, err := config.Home()
	if err != nil {
		fmt.Fprintln(stderr, "heka why:", err)
		return 1
	}
	rows := []ledger.Row{}
	for _, r := range ledger.ReadAll(home) {
		if (session == "" || r.Session == session) && (id == "" || mentions(r, id)) {
			rows = append(rows, r)
		}
	}
	if len(rows) > last {
		rows = rows[len(rows)-last:]
	}
	if asJSON {
		_ = printJSON(stdout, rows)
		return 0
	}
	if len(rows) == 0 {
		fmt.Fprintln(stdout, "해당하는 원장 줄이 없습니다.")
	}
	for _, r := range rows {
		fmt.Fprintln(stdout, whyLine(r))
	}
	return 0
}

// listEntry 는 `heka list` 가 보이는 항목 하나다.
type listEntry struct {
	ID      string    `json:"id"`
	Must    bool      `json:"must"`
	When    item.When `json:"when"`
	Scope   []string  `json:"scope"`
	Checked string    `json:"checked"`
	Expires string    `json:"expires"`
	Flagged bool      `json:"flagged"`
	// Violations 는 검사에 걸린 횟수와 flag 횟수의 합이다.
	Violations int `json:"violations"`
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func whenText(w item.When) string {
	var parts []string
	for _, k := range w.Kinds() {
		parts = append(parts, k[0]+"="+k[1])
	}
	return dash(strings.Join(parts, " "))
}

func listText(e listEntry) string {
	must := "참고"
	if e.Must {
		must = "필수"
	}
	flag := ""
	if e.Flagged {
		flag = "  표시됨"
	}
	return fmt.Sprintf("%s  %s  조건: %s  범위: %s  확인: %s  기한: %s%s",
		e.ID, must, whenText(e.When), dash(strings.Join(e.Scope, ",")), dash(e.Checked), dash(e.Expires), flag)
}

// flagStats 는 원장에 flag 가 남은 항목 id 와 그 횟수·마지막 사유다.
func flagStats(rows []ledger.Row) (count map[string]int, lastReason map[string]string) {
	count, lastReason = map[string]int{}, map[string]string{}
	for _, r := range rows {
		if r.Event != "flag" {
			continue
		}
		for _, i := range r.Injected {
			count[i.ID]++
			lastReason[i.ID] = i.Why
		}
	}
	return
}

// checkFailCounts 는 원장에서 결과물 검사에 걸린(fail) 횟수를 항목 id 별로 센다.
func checkFailCounts(rows []ledger.Row) map[string]int {
	n := map[string]int{}
	for _, r := range rows {
		if !r.IsHook() {
			continue
		}
		for _, c := range r.Checks {
			if c.Result == "fail" {
				n[c.ID]++
			}
		}
	}
	return n
}

// runList 는 `heka list` 를 실행한다. 항목 파일을 훑어 목록으로 보인다.
func runList(args []string, stdout, stderr io.Writer) int {
	var agent string
	var must, flagged, asJSON bool
	var violated int
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&asJSON, "json", false, "JSON 으로 출력")
	fs.StringVar(&agent, "agent", "", "when.agent 에 이 종류가 걸린 항목만")
	fs.BoolVar(&must, "must", false, "필수 항목만")
	fs.BoolVar(&flagged, "flagged", false, "원장에 flag 가 남은 항목만")
	fs.IntVar(&violated, "violated", 0, "검사에 걸린 횟수와 flag 횟수의 합이 N 이상인 항목만")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) > 0 || violated < 0 {
		fmt.Fprintln(stderr, "heka list: 사용법: heka list [--json] [--agent <종류>] [--must] [--flagged] [--violated N]")
		return 2
	}
	home, err := config.Home()
	if err != nil {
		fmt.Fprintln(stderr, "heka list:", err)
		return 1
	}
	cfg, err := config.Load(home)
	if err != nil {
		fmt.Fprintln(stderr, "heka list:", err)
		return 1
	}
	items, err := item.LoadDir(filepath.Join(home, "items"), action.Vocab(cfg))
	if err != nil {
		fmt.Fprintln(stderr, "heka list:", err)
		return 1
	}
	rows := ledger.ReadAll(home)
	flags, _ := flagStats(rows)
	fails := checkFailCounts(rows)
	out, skipped := []listEntry{}, 0
	for _, it := range items {
		if !it.Valid() {
			skipped++
			continue
		}
		e := listEntry{ID: it.ID, Must: it.Must, When: it.When, Scope: it.Scope, Checked: string(it.Checked), Expires: string(it.Expires), Flagged: flags[it.ID] > 0, Violations: fails[it.ID] + flags[it.ID]}
		if (must && !e.Must) || (flagged && !e.Flagged) || (violated > 0 && e.Violations < violated) || (agent != "" && !contains(it.When.Agent, agent)) {
			continue
		}
		out = append(out, e)
	}
	if skipped > 0 {
		fmt.Fprintf(stderr, "heka list: 검증에 실패한 항목 %d건은 뺐습니다(heka index --check 로 확인)\n", skipped)
	}
	if asJSON {
		_ = printJSON(stdout, out)
		return 0
	}
	if len(out) == 0 {
		fmt.Fprintln(stdout, "해당하는 항목이 없습니다.")
	}
	for _, e := range out {
		fmt.Fprintln(stdout, listText(e))
	}
	return 0
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// statsResult 는 `heka stats` 의 결과다.
type statsResult struct {
	ID           string         `json:"id"`
	Injected     int            `json:"injected"`
	LastInjected string         `json:"last_injected,omitempty"`
	Dropped      map[string]int `json:"dropped"`
	Flags        int            `json:"flags"`
	CheckFails   int            `json:"check_fails"` // 결과물 검사에 걸린 횟수
	LastFlag     string         `json:"last_flag_reason,omitempty"`
}

// 세는 탈락 단계다. 나머지 단계(범위·자격·일치)는 "후보가 못 된" 것이라 세지 않는다.
var statsStages = []string{"예산", "중복", "판정"}

func computeStats(rows []ledger.Row, id string) statsResult {
	st := statsResult{ID: id, Dropped: map[string]int{}}
	for _, s := range statsStages {
		st.Dropped[s] = 0
	}
	for _, r := range rows {
		if r.IsHook() {
			for _, i := range r.Injected {
				if i.ID == id {
					st.Injected++
					st.LastInjected = shortTS(r.TS)
				}
			}
			for _, d := range r.Dropped {
				if d.ID == id {
					if _, ok := st.Dropped[d.Stage]; ok {
						st.Dropped[d.Stage]++
					}
				}
			}
		}
		if r.IsHook() {
			for _, c := range r.Checks {
				if c.ID == id && c.Result == "fail" {
					st.CheckFails++
				}
			}
		}
		if r.Event == "flag" {
			for _, i := range r.Injected {
				if i.ID == id {
					st.Flags++
					st.LastFlag = i.Why
				}
			}
		}
	}
	return st
}

// runStats 는 `heka stats <id>` 를 실행한다.
func runStats(args []string, stdout, stderr io.Writer) int {
	var asJSON bool
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&asJSON, "json", false, "JSON 으로 출력")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "heka stats: 사용법: heka stats <id> [--json]")
		return 2
	}
	home, err := config.Home()
	if err != nil {
		fmt.Fprintln(stderr, "heka stats:", err)
		return 1
	}
	rows := ledger.ReadAll(home)
	st := computeStats(rows, pos[0])
	if _, statErr := item.Parse(filepath.Join(home, "items", pos[0]+".md")); statErr != nil && !anyMention(rows, pos[0]) {
		fmt.Fprintf(stderr, "heka stats: 항목 %q 도 원장 기록도 없습니다\n", pos[0])
		return 1
	}
	if asJSON {
		_ = printJSON(stdout, st)
		return 0
	}
	last := "없음"
	if st.LastInjected != "" {
		last = st.LastInjected
	}
	fmt.Fprintf(stdout, "%s\n  주입 %d회, 마지막 주입 %s\n  빠진 횟수: 예산 %d, 중복 %d, 판정 %d\n  검사에 걸린 횟수 %d회\n  flag %d회", st.ID, st.Injected, last, st.Dropped["예산"], st.Dropped["중복"], st.Dropped["판정"], st.CheckFails, st.Flags)
	if st.LastFlag != "" {
		fmt.Fprintf(stdout, ", 마지막 사유: %s", oneLine(st.LastFlag))
	}
	fmt.Fprintln(stdout)
	return 0
}

func anyMention(rows []ledger.Row, id string) bool {
	for _, r := range rows {
		if mentions(r, id) {
			return true
		}
	}
	return false
}

// runRetire 는 `heka retire <id> --reason <글>` 를 실행한다. 항목 파일을 지우고 색인과 원장을 갱신한다.
func runRetire(args []string, stdout, stderr io.Writer) int {
	var reason string
	fs := flag.NewFlagSet("retire", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&reason, "reason", "", "은퇴시키는 사유(필수)")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 || strings.TrimSpace(reason) == "" {
		fmt.Fprintln(stderr, "heka retire: 사용법: heka retire <id> --reason <글>")
		return 2
	}
	e, err := registerEnv()
	if err != nil {
		fmt.Fprintln(stderr, "heka retire:", err)
		return 1
	}
	msg, err := register.Retire(e, pos[0], reason)
	if err != nil {
		fmt.Fprintln(stderr, "heka retire:", err)
		return 1
	}
	fmt.Fprintln(stdout, msg)
	return 0
}
