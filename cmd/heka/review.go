package main

import (
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/hee-develop/heka/internal/regjudge"
	"github.com/hee-develop/heka/internal/review"
)

// isTerminal 은 표준 입력이 터미널인가. 시험에서 바꾼다.
var isTerminal = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	null, err := os.Stat(os.DevNull) // /dev/null 도 문자 장치이지만 터미널은 아니다
	return err != nil || !os.SameFile(st, null)
}

// newRand 는 표본을 뽑는 무작위 원천이다. 시험에서 시드를 고정한다.
var newRand = func() *rand.Rand { return rand.New(rand.NewSource(time.Now().UnixNano())) }

// runJudgeAdd 는 `heka judge-add <id>` 를 실행한다. 등록이 떨어진 프로세스로 띄운다.
func runJudgeAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("judge-add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pos, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "사용법: heka judge-add <id>")
		return 2
	}
	e, err := registerEnv()
	if err != nil {
		fmt.Fprintln(stderr, "heka judge-add:", err)
		return 1
	}
	if err := regjudge.Run(e, stdout, pos[0]); err != nil {
		fmt.Fprintln(stderr, "heka judge-add:", err)
		return 1
	}
	return 0
}

type reviewEntryJSON struct {
	review.Entry
	HeaderDiff []review.DiffField `json:"header_diff"`
	New        *review.ItemView   `json:"new"`
	Target     *review.ItemView   `json:"target,omitempty"`
}

// runReview 는 `heka review` 를 실행한다.
func runReview(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "apply" {
		return runReviewApply(args[1:], stdout, stderr)
	}
	var asJSON, all, notify bool
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&asJSON, "json", false, "검토 목록을 JSON 으로 출력")
	fs.BoolVar(&all, "all", false, "--json 에서 한 번에 보일 수 제한 없이 전부 출력")
	fs.BoolVar(&notify, "notify", false, "표본을 올리고, 기준을 넘으면 알림 명령을 부름")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) > 0 {
		fmt.Fprintln(stderr, "사용법: heka review [--json [--all]] | heka review apply <id> <merge|keep|drop-new|drop-old> | heka review --notify")
		return 2
	}
	e, err := registerEnv()
	if err != nil {
		fmt.Fprintln(stderr, "heka review:", err)
		return 1
	}
	switch {
	case notify:
		if _, err := review.CheckAndNotify(e, stdout, newRand()); err != nil {
			fmt.Fprintln(stderr, "heka review:", err)
			return 1
		}
		return 0
	case asJSON:
		entries := review.List(e)
		out := struct {
			Total      int               `json:"total"`
			OldestDays int               `json:"oldest_days"`
			Shown      int               `json:"shown"`
			Entries    []reviewEntryJSON `json:"entries"`
		}{Total: len(entries), OldestDays: review.OldestDays(entries, e.Now), Entries: []reviewEntryJSON{}}
		if !all && len(entries) > e.Cfg.Review.Batch {
			entries = entries[:e.Cfg.Review.Batch]
		}
		for _, en := range entries {
			j := reviewEntryJSON{Entry: en, New: review.View(e.Home, en.NewID), HeaderDiff: review.HeaderDiff(e.Home, en)}
			if j.HeaderDiff == nil {
				j.HeaderDiff = []review.DiffField{}
			}
			if en.TargetID != "" {
				j.Target = review.View(e.Home, en.TargetID)
			}
			out.Entries = append(out.Entries, j)
		}
		out.Shown = len(out.Entries)
		_ = printJSON(stdout, out)
		return 0
	}
	if !isTerminal(stdin) {
		fmt.Fprintln(stderr, "heka review: 표준 입력이 터미널이 아닙니다. 'heka review --json' 으로 목록을 읽고 'heka review apply' 로 적용하세요.")
		return 1
	}
	rejudge := func(id string) error { return regjudge.Run(e, io.Discard, id) }
	if err := review.Interactive(e, stdin, stdout, e.Cfg.Review.Batch, rejudge); err != nil {
		fmt.Fprintln(stderr, "heka review:", err)
		return 1
	}
	return 0
}

// runReviewApply 는 `heka review apply <entry-id> <결정>` 을 실행한다.
func runReviewApply(args []string, stdout, stderr io.Writer) int {
	var o review.Options
	var bodyFile, about string
	fs := flag.NewFlagSet("review apply", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.Reason, "reason", "", "결정의 사유(원장에 남음)")
	fs.StringVar(&o.Text, "text", "", "merge: 초안 대신 쓸 주입문")
	fs.StringVar(&bodyFile, "body-file", "", "merge: 초안 대신 쓸 본문이 든 파일")
	fs.StringVar(&about, "about", "", "merge: 초안 대신 쓸 주제어(쉼표로 이음)")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 2 {
		fmt.Fprintln(stderr, "사용법: heka review apply <entry-id> <merge|keep|drop-new|drop-old> [--reason 글] [--text 글] [--body-file 파일] [--about a,b]")
		return 2
	}
	if bodyFile != "" {
		b, err := os.ReadFile(bodyFile)
		if err != nil {
			fmt.Fprintln(stderr, "heka review apply:", err)
			return 1
		}
		o.Body, o.BodySet = string(b), true
	}
	if about != "" {
		for _, a := range strings.Split(about, ",") {
			if a = strings.TrimSpace(a); a != "" {
				o.About = append(o.About, a)
			}
		}
	}
	e, err := registerEnv()
	if err != nil {
		fmt.Fprintln(stderr, "heka review apply:", err)
		return 1
	}
	msg, err := review.Apply(e, pos[0], pos[1], o)
	if err != nil {
		fmt.Fprintln(stderr, "heka review apply:", err)
		return 1
	}
	fmt.Fprintln(stdout, msg)
	return 0
}
