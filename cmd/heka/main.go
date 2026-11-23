// heka 명령줄 진입점.
package main

import (
	"fmt"
	"io"
	"os"
)

// version 은 빌드 때 -ldflags "-X main.version=..." 로 주입한다.
var version = "dev"

const usage = `사용법: heka <명령>

명령:
  index     항목과 설정을 검증하고 색인을 만든다 (--check 는 검증만)
  select    순간 하나에 넣을 항목을 골라 JSON 으로 출력한다
  resolve   도구 호출 하나를 동작 이름으로 해석해 JSON 으로 출력한다
  find      항목을 찾는다 (낱말 또는 작업을 설명한 문장, --json 지원)
  add       항목을 등록한다 (비슷한 항목이 있으면 대기함)
  pending   대기함을 보고 결정한다 (fix|replace|keep|drop)
  why       원장의 최근 주입을 사람이 읽는 꼴로 보인다 (--session, --last, --id, --json)
  list      항목 목록 (--json, --agent, --must, --flagged, --find-only, --violated)
  stats     항목 하나의 주입·탈락·검사에 걸림·flag 횟수 (--json)
  judge-stats  원장의 판정 시간 분포와 상한별로 잘릴 판정 수 (--json, --since YYYY-MM-DD)
  gui       원장과 항목을 보는 읽기 전용 화면을 로컬 서버로 연다 (--port, --open)
  replay    기록해 둔 순간들을 선별기에 다시 넣어 전달·정밀도·침묵을 잰다 (--runs, --no-judge, --parallel, --json, --out, --min-*)
  retire    항목을 지우고 색인과 원장을 갱신한다 (--reason 필수)
  mcp       MCP 서버를 표준 입출력으로 실행한다
  hook      Claude Code 훅 이벤트를 처리한다 (--print-settings [--shadow] 는 등록할 hooks JSON 을 출력)
  version   버전을 출력한다
  help      이 도움말을 출력한다
`

// run 은 명령을 실행하고 종료 코드를 돌려준다.
func run(args []string, stdout, stderr io.Writer) int {
	return runIn(args, os.Stdin, stdout, stderr)
}

func runIn(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "index":
		return runIndex(args[1:], stdout, stderr)
	case "select":
		return runSelect(args[1:], stdout, stderr)
	case "resolve":
		return runResolve(args[1:], stdout, stderr)
	case "find":
		return runFind(args[1:], stdout, stderr)
	case "add":
		return runAdd(args[1:], stdout, stderr)
	case "pending":
		return runPending(args[1:], stdout, stderr)
	case "why":
		return runWhy(args[1:], stdout, stderr)
	case "list":
		return runList(args[1:], stdout, stderr)
	case "stats":
		return runStats(args[1:], stdout, stderr)
	case "judge-stats":
		return runJudgeStats(args[1:], stdout, stderr)
	case "gui":
		return runGUI(args[1:], stdout, stderr)
	case "replay":
		return runReplay(args[1:], stdout, stderr)
	case "retire":
		return runRetire(args[1:], stdout, stderr)
	case "mcp":
		return runMCP(args[1:], stdin, stdout, stderr)
	case "hook":
		return runHook(args[1:], stdin, stdout, stderr)
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	default:
		fmt.Fprintf(stderr, "heka: 알 수 없는 명령 %q\n'heka help' 로 사용법을 확인하세요.\n", args[0])
		return 2
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
