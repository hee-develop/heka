package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/gui"
)

const defaultGUIPort = 7788

// openBrowser 는 주소를 브라우저로 연다. 시험에서 바꿔 끼운다.
var openBrowser = func(url string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	cmd := exec.Command(name, url)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// runGUI 는 `heka gui` 를 실행한다. SIGINT·SIGTERM 에 정상 종료한다.
func runGUI(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runGUIContext(ctx, args, stdout, stderr)
}

// runGUIContext 는 ctx 가 끝나면 서버를 내리고 0 을 돌려준다.
func runGUIContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	port, open := defaultGUIPort, false
	fs := flag.NewFlagSet("gui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.IntVar(&port, "port", defaultGUIPort, "듣는 포트(0 이면 빈 포트)")
	fs.BoolVar(&open, "open", false, "브라우저로 연다")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) > 0 || port < 0 || port > 65535 {
		fmt.Fprintln(stderr, "heka gui: 사용법: heka gui [--port N] [--open] (N 은 0~65535)")
		return 2
	}
	home, err := config.Home()
	if err != nil {
		fmt.Fprintln(stderr, "heka gui:", err)
		return 1
	}
	// 인증이 없는 읽기 화면이라 주소는 늘 127.0.0.1 이다.
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		fmt.Fprintln(stderr, "heka gui: 포트를 잡지 못했습니다:", err)
		return 1
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/", ln.Addr().(*net.TCPAddr).Port)
	fmt.Fprintln(stdout, url)
	if open {
		if err := openBrowser(url); err != nil {
			fmt.Fprintln(stderr, "heka gui: 브라우저를 열지 못했습니다:", err)
		}
	}
	srv := &http.Server{Handler: gui.NewHandler(home, version), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		fmt.Fprintln(stderr, "heka gui:", err)
		return 1
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		_ = srv.Close()
	}
	return 0
}
