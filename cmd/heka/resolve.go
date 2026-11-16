package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hee-develop/heka/internal/action"
	"github.com/hee-develop/heka/internal/config"
)

// runResolve 는 `heka resolve` 를 실행한다. 도구 호출 하나를 동작 이름으로 해석해 JSON 으로 출력한다(확인용).
func runResolve(args []string, stdout, stderr io.Writer) int {
	var c action.Call
	var command, input string
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.Tool, "tool", "", "도구 이름: Bash, Edit, Task, Skill, mcp__... (필수)")
	fs.StringVar(&command, "command", "", "Bash 명령(--tool Bash 일 때)")
	fs.StringVar(&input, "input", "", "도구 입력 JSON")
	fs.StringVar(&c.Cwd, "cwd", "", "작업 폴더(없으면 현재 폴더)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 || c.Tool == "" || (command != "" && input != "") {
		fmt.Fprintln(stderr, "heka resolve: --tool 이 필요하고, --command 와 --input 은 함께 쓸 수 없으며, 위치 인자는 받지 않습니다")
		return 2
	}
	c.Input = map[string]any{}
	if input != "" {
		if err := json.Unmarshal([]byte(input), &c.Input); err != nil {
			fmt.Fprintln(stderr, "heka resolve: --input 이 JSON 객체가 아닙니다:", err)
			return 2
		}
	}
	if command != "" {
		c.Input["command"] = command
	}
	if c.Cwd == "" {
		c.Cwd, _ = os.Getwd()
	}
	home, err := config.Home()
	if err != nil {
		fmt.Fprintln(stderr, "heka resolve:", err)
		return 1
	}
	cfg, err := config.Load(home)
	if err != nil {
		fmt.Fprintln(stderr, "heka resolve:", err)
		return 1
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(action.Resolve(c, cfg)); err != nil {
		fmt.Fprintln(stderr, "heka resolve:", err)
		return 1
	}
	return 0
}
