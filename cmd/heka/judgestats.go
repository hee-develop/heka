package main

import (
	"flag"
	"fmt"
	"io"
	"regexp"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/ledger"
	"github.com/hee-develop/heka/internal/stat"
)

// judgeCaps 는 판정 시간 상한을 줄인다면 어디까지 줄여 볼지의 후보(초)다.
var judgeCaps = []int{3, 4, 5, 6, 8, 10, 15}

// capRow 는 상한 하나에서 잘렸을 판정의 수다. Cut 은 걸린 시간이 상한을 넘은 판정 수이고,
// CutInject 는 그중 넣으라고 답한 수다(시간 초과·오류로 끝난 호출은 답이 없어 넣으라는 답에 세지 않는다).
type capRow struct {
	CapSeconds int `json:"cap_seconds"`
	Cut        int `json:"cut"`
	CutInject  int `json:"cut_inject"`
}

// judgeStats 는 `heka judge-stats` 의 결과다. 시간은 밀리초다.
type judgeStats struct {
	Since    string   `json:"since,omitempty"`
	Count    int      `json:"count"`
	Errors   int      `json:"errors"`
	Timeouts int      `json:"timeouts"`
	MedianMS int64    `json:"median_ms"`
	P95MS    int64    `json:"p95_ms"`
	MaxMS    int64    `json:"max_ms"`
	Caps     []capRow `json:"caps"`
}

// computeJudgeStats 는 원장 줄에서 since(YYYY-MM-DD, 비면 전부) 이후의 판정 기록을 모은다.
func computeJudgeStats(rows []ledger.Row, since string) judgeStats {
	st := judgeStats{Since: since, Caps: []capRow{}}
	var all []ledger.Judged
	for _, r := range rows {
		if since != "" && (len(r.TS) < 10 || r.TS[:10] < since) {
			continue
		}
		all = append(all, r.Judges...)
	}
	ms := make([]int64, 0, len(all))
	for _, j := range all {
		ms = append(ms, j.MS)
		if j.Error {
			st.Errors++
		}
		if j.Timeout {
			st.Timeouts++
		}
	}
	d := stat.Of(ms)
	st.Count, st.MedianMS, st.P95MS, st.MaxMS = len(all), d.Median, d.P95, d.Max
	for _, c := range judgeCaps {
		row := capRow{CapSeconds: c}
		for _, j := range all {
			if j.MS > int64(c)*1000 {
				row.Cut++
				if j.Inject {
					row.CutInject++
				}
			}
		}
		st.Caps = append(st.Caps, row)
	}
	return st
}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// runJudgeStats 는 `heka judge-stats` 를 실행한다. 원장의 판정 기록을 모아 시간 분포와 상한별 손실을 보인다.
func runJudgeStats(args []string, stdout, stderr io.Writer) int {
	var since string
	var asJSON bool
	fs := flag.NewFlagSet("judge-stats", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&since, "since", "", "이 날짜(YYYY-MM-DD, UTC) 이후의 기록만")
	fs.BoolVar(&asJSON, "json", false, "JSON 으로 출력")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) > 0 || (since != "" && !dateRe.MatchString(since)) {
		fmt.Fprintln(stderr, "heka judge-stats: 사용법: heka judge-stats [--json] [--since YYYY-MM-DD]")
		return 2
	}
	home, err := config.Home()
	if err != nil {
		fmt.Fprintln(stderr, "heka judge-stats:", err)
		return 1
	}
	st := computeJudgeStats(ledger.ReadAll(home), since)
	if asJSON {
		_ = printJSON(stdout, st)
		return 0
	}
	if st.Count == 0 {
		fmt.Fprintln(stdout, "판정 기록이 없습니다.")
		return 0
	}
	fmt.Fprintf(stdout, "판정 %d건 (오류 %d, 시간 초과 %d)\n", st.Count, st.Errors, st.Timeouts)
	fmt.Fprintf(stdout, "걸린 시간: 중앙값 %dms, 95%% %dms, 최댓값 %dms\n\n", st.MedianMS, st.P95MS, st.MaxMS)
	fmt.Fprintln(stdout, "상한을 줄이면 잘릴 판정 (걸린 시간이 상한을 넘은 것)")
	fmt.Fprintf(stdout, "%-6s  %8s  %10s\n", "상한", "잘림", "그중 넣음")
	for _, c := range st.Caps {
		fmt.Fprintf(stdout, "%-6s  %8d  %10d\n", fmt.Sprintf("%d초", c.CapSeconds), c.Cut, c.CutInject)
	}
	return 0
}
