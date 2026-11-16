package main

import (
	"fmt"
	"io"

	"github.com/hee-develop/heka/internal/claudecode"
	"github.com/hee-develop/heka/internal/config"
)

// runHook 는 `heka hook` 을 실행한다. 표준 입력의 Claude Code 훅 JSON 을 처리해 표준 출력에 JSON 을 낸다.
// 작업을 막지 않으려고 처리 중 오류는 모두 삼키고 종료 코드 0 으로 끝낸다.
// --print-settings 는 설정 파일의 hooks 에 넣을 JSON 조각만 출력하고, 여기에 --shadow 를 더하면 그림자 방식용 조각을 낸다.
// 그림자 방식으로 훅을 돌리는 것은 환경 변수 HEKA_SHADOW=1 이나 설정 claude_code.shadow 다.
func runHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var printSettings, shadow bool
	for _, a := range args {
		switch a {
		case "--print-settings":
			printSettings = true
		case "--shadow":
			shadow = true
		default:
			fmt.Fprintf(stderr, "heka hook: 알 수 없는 인자 %q\n", a)
			return 2
		}
	}
	if shadow && !printSettings {
		fmt.Fprintln(stderr, "heka hook: --shadow 는 --print-settings 와 함께 씁니다(훅을 그림자 방식으로 돌리려면 환경 변수 HEKA_SHADOW=1)")
		return 2
	}
	if printSettings {
		b, err := claudecode.Settings(shadow)
		if err != nil {
			fmt.Fprintln(stderr, "heka hook:", err)
			return 1
		}
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	home, err := config.Home()
	if err != nil {
		return 0 // 오류 기록을 둘 곳이 없다
	}
	return claudecode.Run(stdin, stdout, home)
}
