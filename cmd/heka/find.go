package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/index"
	"github.com/hee-develop/heka/internal/lookup"
)

// parseMixed 는 플래그를 위치 인자 앞뒤 어디에나 쓸 수 있게 읽고 위치 인자들을 돌려준다.
func parseMixed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// printJSON 은 들여쓴 JSON 한 덩어리를 쓴다.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// runFind 는 `heka find` 를 실행한다. 질의는 낱말 나열이거나 작업을 설명하는 문장이다.
func runFind(args []string, stdout, stderr io.Writer) int {
	var scopes multi
	var budget, limit int
	var asJSON bool
	fs := flag.NewFlagSet("find", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Var(&scopes, "scope", "범위(여러 번 쓸 수 있음). 없으면 작업 폴더로 정함")
	fs.IntVar(&budget, "budget", 0, "토큰 상한. 없으면 설정의 budgets.find")
	fs.IntVar(&limit, "limit", lookup.DefaultLimit, "낱말 점수로 잡는 후보 수")
	fs.BoolVar(&asJSON, "json", false, "JSON 으로 출력")
	words, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	query := strings.Join(words, " ")
	if strings.TrimSpace(query) == "" {
		fmt.Fprintln(stderr, "heka find: 질의가 필요합니다. 예: heka find [--scope S] [--budget N] [--limit K] [--json] <질의…>")
		return 2
	}
	home, err := config.Home()
	if err != nil {
		fmt.Fprintln(stderr, "heka find:", err)
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
		fmt.Fprintln(stderr, "heka find:", err)
		return 1
	}
	defer ix.Close()
	cwd, _ := os.Getwd()
	now := time.Now()
	q := lookup.Query{Text: query, Scopes: lookup.Scopes(cfg, scopes, cwd), Budget: budget, Limit: limit, Today: now.Format("2006-01-02")}
	res, err := lookup.Find(ix, cfg, q)
	if err != nil {
		fmt.Fprintln(stderr, "heka find:", err)
		return 1
	}
	lookup.Log(home, q, res, now)
	if asJSON {
		if err := printJSON(stdout, res); err != nil {
			fmt.Fprintln(stderr, "heka find:", err)
			return 1
		}
		return 0
	}
	fmt.Fprint(stdout, res.Text())
	return 0
}
