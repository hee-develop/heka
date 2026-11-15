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
  version   버전을 출력한다
  help      이 도움말을 출력한다
`

// run 은 명령을 실행하고 종료 코드를 돌려준다.
func run(args []string, stdout, stderr io.Writer) int {
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
