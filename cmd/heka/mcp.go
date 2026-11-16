package main

import (
	"fmt"
	"io"
	"time"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/mcpserver"
)

// runMCP 는 `heka mcp` 를 실행한다. 표준 입력이 닫힐 때까지 MCP 메시지를 처리한다.
func runMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "heka mcp: 알 수 없는 인자 %q\n", args[0])
		return 2
	}
	home, err := config.Home()
	if err != nil {
		fmt.Fprintln(stderr, "heka mcp:", err)
		return 1
	}
	s := &mcpserver.Server{Home: home, Version: version, Now: time.Now}
	if err := s.Serve(stdin, stdout); err != nil {
		fmt.Fprintln(stderr, "heka mcp:", err)
		return 1
	}
	return 0
}
