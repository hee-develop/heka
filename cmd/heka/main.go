// heka 명령줄 진입점. 지금은 version 하위 명령만 있다.
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
