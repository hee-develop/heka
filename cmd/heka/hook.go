package main

import (
	"fmt"
	"io"

	"github.com/hee-develop/heka/internal/claudecode"
	"github.com/hee-develop/heka/internal/config"
)

// runHook 는 `heka hook` 을 실행한다. 표준 입력의 Claude Code 훅 JSON 을 처리해 표준 출력에 JSON 을 낸다.
// 작업을 막지 않으려고 처리 중 오류는 모두 삼키고 종료 코드 0 으로 끝낸다.
// --print-settings 는 설정 파일의 hooks 에 넣을 JSON 조각만 출력한다.
func runHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "--print-settings" {
		b, err := claudecode.Settings()
		if err != nil {
			fmt.Fprintln(stderr, "heka hook:", err)
			return 1
		}
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if len(args) > 0 {
		fmt.Fprintf(stderr, "heka hook: 알 수 없는 인자 %q\n", args[0])
		return 2
	}
	home, err := config.Home()
	if err != nil {
		return 0 // 오류 기록을 둘 곳이 없다
	}
	return claudecode.Run(stdin, stdout, home)
}
