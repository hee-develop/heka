package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/item"
	"github.com/hee-develop/heka/internal/register"
)

func registerEnv() (register.Env, error) {
	home, err := config.Home()
	if err != nil {
		return register.Env{}, err
	}
	cfg, err := config.Load(home)
	return register.Env{Home: home, Cfg: cfg, Now: time.Now()}, err
}

// runAdd 는 `heka add` 를 실행한다. 필드를 플래그로 받거나 완성된 항목 파일(--file)을 받는다.
func runAdd(args []string, stdout, stderr io.Writer) int {
	var in register.Input
	var file string
	var checked, asJSON bool
	var about, scope, moment, action, path, repo, agent multi
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&file, "file", "", "완성된 항목 파일(있으면 다른 필드 플래그는 무시함)")
	fs.StringVar(&in.ID, "id", "", "id")
	fs.StringVar(&in.Text, "text", "", "주입문")
	fs.BoolVar(&in.Must, "must", false, "필수 항목")
	fs.Var(&moment, "moment", "when.moment(여러 번)")
	fs.Var(&action, "action", "when.action(여러 번)")
	fs.Var(&path, "path", "when.path(여러 번)")
	fs.Var(&repo, "repo", "when.repo(여러 번)")
	fs.Var(&agent, "agent", "when.agent(여러 번)")
	fs.Var(&about, "about", "주제어(여러 번)")
	fs.Var(&scope, "scope", "범위(여러 번)")
	fs.StringVar(&in.Source, "source", "", "user|doc|ai")
	fs.StringVar(&in.Quote, "quote", "", "source 가 user 일 때 사용자의 말")
	fs.StringVar(&in.Expires, "expires", "", "유효기한 YYYY-MM-DD")
	fs.StringVar(&in.Confirm, "confirm", "", "false|true|always")
	fs.StringVar(&in.Body, "body", "", "본문")
	fs.BoolVar(&checked, "checked", false, "사람이 확인했다는 뜻으로 오늘 날짜를 checked 에 넣음")
	fs.BoolVar(&asJSON, "json", false, "결과를 JSON 으로 출력")
	if _, err := parseMixed(fs, args); err != nil {
		return 2
	}
	if file != "" {
		it, err := item.Parse(file)
		if err != nil {
			fmt.Fprintln(stderr, "heka add:", err)
			return 1
		}
		in = register.FromItem(it)
		in.Checked = "" // 파일에 적힌 checked 는 등록으로 넣지 않는다
	} else {
		in.When = item.When{Moment: item.List(moment), Action: item.List(action), Path: item.List(path), Repo: item.List(repo), Agent: item.List(agent)}
		in.About, in.Scope = about, scope
	}
	e, err := registerEnv()
	if err != nil {
		fmt.Fprintln(stderr, "heka add:", err)
		return 1
	}
	if checked {
		in.Checked = e.Now.Format("2006-01-02")
	}
	res, err := register.Add(e, in)
	if err != nil {
		fmt.Fprintln(stderr, "heka add:", err)
		return 1
	}
	if asJSON {
		_ = printJSON(stdout, res)
	} else {
		fmt.Fprint(stdout, res.Text())
	}
	if res.Status == "invalid" {
		return 1
	}
	return 0
}

// runPending 은 `heka pending [fix|replace|keep|drop ...]` 를 실행한다.
func runPending(args []string, stdout, stderr io.Writer) int {
	var reason string
	fs := flag.NewFlagSet("pending", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&reason, "reason", "", "keep: 다른 항목이라는 이유")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	e, err := registerEnv()
	if err != nil {
		fmt.Fprintln(stderr, "heka pending:", err)
		return 1
	}
	if len(pos) == 0 {
		list, err := register.Pending(e)
		if err != nil {
			fmt.Fprintln(stderr, "heka pending:", err)
			return 1
		}
		if len(list) == 0 {
			fmt.Fprintln(stdout, "대기함이 비어 있습니다.")
		}
		for _, p := range list {
			fmt.Fprintf(stdout, "[%s] %s\n", p.ID, p.Text)
			for _, c := range p.Candidates {
				fmt.Fprintf(stdout, "  - %s: %s (%s)\n", c.ID, c.Text, c.Reason)
			}
		}
		return 0
	}
	need := map[string]int{"fix": 2, "replace": 2, "keep": 1, "drop": 1}[pos[0]]
	if need == 0 || len(pos)-1 != need {
		fmt.Fprintln(stderr, strings.TrimSpace(`
사용법: heka pending                                  대기함 목록
        heka pending fix <대기 id> <기존 id>          기존 항목의 내용을 새 내용으로 고침
        heka pending replace <대기 id> <기존 id>      기존 항목을 지우고 새 항목을 등록
        heka pending keep <대기 id> --reason <글>     다른 항목이라는 이유를 적고 등록
        heka pending drop <대기 id>                   대기함에서 지움`))
		return 2
	}
	target := ""
	if need == 2 {
		target = pos[2]
	}
	msg, err := register.Decide(e, pos[0], pos[1], target, reason)
	if err != nil {
		fmt.Fprintln(stderr, "heka pending:", err)
		return 1
	}
	fmt.Fprintln(stdout, msg)
	return 0
}
