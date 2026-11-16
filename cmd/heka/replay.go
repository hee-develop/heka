package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/index"
	"github.com/hee-develop/heka/internal/replay"
)

// runReplay 는 `heka replay <사례.jsonl>` 를 실행한다. 세션 상태와 원장은 쓰지 않는다(색인이 낡았으면 다시 만든다).
func runReplay(args []string, stdout, stderr io.Writer) int {
	var runs, parallel int
	var noJudge, asJSON bool
	var out string
	minD, minP, minS := -1.0, -1.0, -1.0
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.IntVar(&runs, "runs", 1, "전체를 도는 횟수")
	fs.IntVar(&parallel, "parallel", 4, "사례를 동시에 돌리는 수")
	fs.BoolVar(&noJudge, "no-judge", false, "판정 명령을 쓰지 않음(참고 항목은 프롬프트에서 넣지 않음)")
	fs.BoolVar(&asJSON, "json", false, "JSON 으로 출력")
	fs.StringVar(&out, "out", "", "사례별 결과를 JSONL 로 쓸 파일")
	fs.Float64Var(&minD, "min-delivery", -1, "전달 합격선(0~1). 가장 낮은 회차가 못 미치면 종료 코드 1")
	fs.Float64Var(&minP, "min-precision", -1, "정밀도 합격선(0~1)")
	fs.Float64Var(&minS, "min-silence", -1, "침묵 합격선(0~1)")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	bad := func(v float64) bool { return v != -1 && (v < 0 || v > 1) }
	if len(pos) != 1 || runs < 1 || parallel < 1 || bad(minD) || bad(minP) || bad(minS) {
		fmt.Fprintln(stderr, "heka replay: 사용법: heka replay <사례.jsonl> [--runs N] [--parallel N] [--no-judge] [--json] [--out 파일] [--min-delivery X] [--min-precision X] [--min-silence X] (N 은 1 이상, X 는 0~1)")
		return 2
	}
	f, err := os.Open(pos[0])
	if err != nil {
		fmt.Fprintln(stderr, "heka replay:", err)
		return 1
	}
	cases, skips, err := replay.Load(f)
	f.Close()
	if err != nil {
		fmt.Fprintln(stderr, "heka replay:", err)
		return 1
	}
	if len(skips) > 0 {
		fmt.Fprintf(stderr, "heka replay: 읽지 못해 건너뛴 줄 %d건\n", len(skips))
	}
	if len(cases) == 0 {
		fmt.Fprintln(stderr, "heka replay: 읽을 수 있는 사례가 없습니다")
		return 1
	}
	home, err := config.Home()
	if err != nil {
		fmt.Fprintln(stderr, "heka replay:", err)
		return 1
	}
	cfg, err := config.Load(home)
	if err == nil {
		err = index.Ensure(home, cfg)
	}
	var ix *index.Index
	if err == nil {
		ix, err = index.Open(home)
	}
	if err != nil {
		fmt.Fprintln(stderr, "heka replay:", err)
		return 1
	}
	defer ix.Close()

	results := replay.Run(context.Background(), ix, cfg, cases, replay.Options{Runs: runs, Parallel: parallel, NoJudge: noJudge})
	rep := replay.Summarize(results, skips)

	if out != "" {
		if err := writeReplayOut(out, results); err != nil {
			fmt.Fprintln(stderr, "heka replay:", err)
			return 1
		}
	}
	if asJSON {
		_ = printJSON(stdout, rep)
	} else {
		replay.Text(stdout, rep)
	}
	opt := func(v float64) *float64 {
		if v == -1 {
			return nil
		}
		return &v
	}
	fails, notes := replay.Check(rep, replay.Thresholds{Delivery: opt(minD), Precision: opt(minP), Silence: opt(minS)})
	for _, n := range notes {
		fmt.Fprintln(stderr, "heka replay:", n)
	}
	for _, f := range fails {
		fmt.Fprintln(stderr, "heka replay: 합격선 미달:", f)
	}
	if len(fails) > 0 {
		return 1
	}
	return 0
}

// writeReplayOut 은 사례별 결과를 회차 순서, 사례 순서로 JSONL 에 쓴다.
func writeReplayOut(path string, runs [][]replay.CaseResult) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, rs := range runs {
		for _, r := range rs {
			if err := enc.Encode(r); err != nil {
				f.Close()
				return err
			}
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
