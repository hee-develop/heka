package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/index"
	"github.com/hee-develop/heka/internal/selector"
)

// multi 는 여러 번 쓸 수 있는 플래그 값이다.
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// runSelect 는 `heka select` 를 실행한다. 순간 하나를 선별기에 넣고 결과를 JSON 으로 출력한다.
// 색인이 낡았으면 먼저 다시 만든다.
func runSelect(args []string, stdout, stderr io.Writer) int {
	var m selector.Moment
	var scopes, seen multi
	fs := flag.NewFlagSet("select", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&m.Kind, "moment", "", "순간: session_start|prompt|tool|subagent_start (필수)")
	fs.Var(&scopes, "scope", "작업 위치의 범위(여러 번 쓸 수 있음)")
	fs.StringVar(&m.Text, "text", "", "프롬프트 글 또는 위임 글")
	fs.BoolVar(&m.ToolMade, "tool-made", false, "도구가 만들어 넣은 프롬프트")
	fs.StringVar(&m.Action, "action", "", "tool: 동작 이름")
	fs.StringVar(&m.Path, "path", "", "tool: 대상 경로")
	fs.StringVar(&m.Repo, "repo", "", "저장소 이름")
	fs.StringVar(&m.Agent, "agent", "", "에이전트 종류(메인이면 비움)")
	fs.Var(&seen, "seen", "이 세션에 이미 넣은 항목 id(여러 번 쓸 수 있음)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 || m.Kind == "" {
		fmt.Fprintln(stderr, "heka select: --moment 가 필요하고 위치 인자는 받지 않습니다")
		return 2
	}
	m.Scopes, m.Seen, m.Today = scopes, map[string]bool{}, time.Now().Format("2006-01-02")
	for _, id := range seen {
		m.Seen[id] = true
	}

	res, err := selectOnce(m)
	if err != nil {
		fmt.Fprintln(stderr, "heka select:", err)
		return 1
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(res); err != nil {
		fmt.Fprintln(stderr, "heka select:", err)
		return 1
	}
	return 0
}

func selectOnce(m selector.Moment) (selector.Result, error) {
	home, err := config.Home()
	if err != nil {
		return selector.Result{}, err
	}
	cfg, err := config.Load(home)
	if err != nil {
		return selector.Result{}, err
	}
	if err := index.Ensure(home, cfg); err != nil {
		return selector.Result{}, err
	}
	ix, err := index.Open(home)
	if err != nil {
		return selector.Result{}, err
	}
	defer ix.Close()
	return selector.Select(context.Background(), ix, cfg, m)
}
