package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf 는 고루틴에서 쓰고 시험에서 읽는 버퍼다.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestGUIServeAndShutdown(t *testing.T) {
	t.Setenv("HEKA_HOME", t.TempDir())
	var opened string
	old := openBrowser
	openBrowser = func(u string) error { opened = u; return nil }
	defer func() { openBrowser = old }()

	ctx, cancel := context.WithCancel(context.Background())
	var out, errb syncBuf
	done := make(chan int, 1)
	go func() { done <- runGUIContext(ctx, []string{"--port", "0", "--open"}, &out, &errb) }()

	var url string
	for i := 0; i < 200 && url == ""; i++ {
		url = strings.TrimSpace(out.String())
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.HasSuffix(url, "/") {
		t.Fatalf("주소 줄 = %q", url)
	}
	resp, err := http.Get(url + "api/meta")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"version"`) {
		t.Errorf("meta: %d %s", resp.StatusCode, b)
	}
	if opened != url {
		t.Errorf("열린 주소 = %q", opened)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("종료 코드 = %d, stderr=%s", code, errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("서버가 내려가지 않음")
	}
}

func TestGUIOpenFailureKeepsServing(t *testing.T) {
	t.Setenv("HEKA_HOME", t.TempDir())
	old := openBrowser
	openBrowser = func(string) error { return io.ErrClosedPipe }
	defer func() { openBrowser = old }()
	ctx, cancel := context.WithCancel(context.Background())
	var out, errb syncBuf
	done := make(chan int, 1)
	go func() { done <- runGUIContext(ctx, []string{"--port", "0", "--open"}, &out, &errb) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	if code := <-done; code != 0 || !strings.Contains(errb.String(), "브라우저") {
		t.Errorf("code=%d stderr=%q", code, errb.String())
	}
}

func TestGUIBadArgs(t *testing.T) {
	t.Setenv("HEKA_HOME", t.TempDir())
	for _, args := range [][]string{{"--nosuch"}, {"--port", "x"}, {"--port", "70000"}, {"--port", "-1"}, {"extra"}} {
		var out, errb bytes.Buffer
		if code := runGUIContext(context.Background(), args, &out, &errb); code != 2 {
			t.Errorf("%v: 종료 코드 = %d, 기대 2", args, code)
		}
	}
}

func TestGUIPortInUse(t *testing.T) {
	t.Setenv("HEKA_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out syncBuf
	done := make(chan int, 1)
	go func() { done <- runGUIContext(ctx, []string{"--port", "0"}, &out, io.Discard) }()
	var url string
	for i := 0; i < 200 && url == ""; i++ {
		url = strings.TrimSpace(out.String())
		time.Sleep(10 * time.Millisecond)
	}
	port := url[strings.LastIndex(strings.TrimSuffix(url, "/"), ":")+1 : len(url)-1]
	var o2, e2 bytes.Buffer
	if code := runGUIContext(context.Background(), []string{"--port", port}, &o2, &e2); code != 1 || !strings.Contains(e2.String(), "포트") {
		t.Errorf("code=%d stderr=%q", code, e2.String())
	}
	cancel()
	<-done
}

func TestGUIListedInUsage(t *testing.T) {
	var out, errb bytes.Buffer
	run([]string{"help"}, &out, &errb)
	if !strings.Contains(out.String(), "gui ") {
		t.Error("usage 에 gui 가 없음")
	}
}
