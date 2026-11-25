package main

import (
	"flag"
	"fmt"
	"io"
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
