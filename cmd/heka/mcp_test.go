package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mcpProc 는 실제 `heka mcp` 프로세스다. 한 줄 넣고 한 줄 받는다.
type mcpProc struct {
	t   *testing.T
	in  interface{ Write([]byte) (int, error) }
	out *bufio.Reader
}

func startMCP(t *testing.T, home string) *mcpProc {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "heka")
	if b, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("빌드 실패: %v\n%s", err, b)
	}
	cmd := exec.Command(bin, "mcp")
	cmd.Env = append(os.Environ(), "HEKA_HOME="+home)
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close(); cmd.Wait() })
	return &mcpProc{t, in, bufio.NewReader(out)}
}

func (p *mcpProc) send(line string) { p.t.Helper(); p.in.Write([]byte(line + "\n")) }

func (p *mcpProc) recv() map[string]any {
	p.t.Helper()
	line, err := p.out.ReadString('\n')
	if err != nil {
		p.t.Fatalf("응답을 읽지 못함: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		p.t.Fatalf("응답이 JSON 이 아님: %q", line)
	}
	return m
}

func (p *mcpProc) call(id int, name, args string) map[string]any {
	p.t.Helper()
	p.send(`{"jsonrpc":"2.0","id":` + string(rune('0'+id)) + `,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`)
	return p.recv()
}

func result(m map[string]any) (string, bool) {
	r := m["result"].(map[string]any)
	return r["content"].([]any)[0].(map[string]any)["text"].(string), r["isError"].(bool)
}

func TestMCPProcess(t *testing.T) {
	home := newHome(t)
	p := startMCP(t, home)

	// 협상: 클라이언트가 쓰는 판이 지원 목록에 있으면 그 판으로, 없으면 서버의 최신 판으로 답한다.
	p.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	r := p.recv()["result"].(map[string]any)
	if r["protocolVersion"] != "2025-06-18" || r["serverInfo"].(map[string]any)["name"] != "heka" || r["capabilities"].(map[string]any)["tools"] == nil {
		t.Errorf("initialize = %v", r)
	}
	p.send(`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	if v := p.recv()["result"].(map[string]any)["protocolVersion"]; v != "2025-11-25" {
		t.Errorf("모르는 판이면 서버의 최신 판: %v", v)
	}
	// 알림에는 응답이 없다. 바로 다음 요청의 응답이 그 요청의 것이어야 한다.
	p.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	p.send(`{"jsonrpc":"2.0","method":"notifications/unknown_thing"}`)
	p.send(`{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	if m := p.recv(); m["id"].(float64) != 3 {
		t.Errorf("알림에 응답이 나옴: %v", m)
	}
	p.send(`{"jsonrpc":"2.0","id":4,"method":"tools/list"}`)
	var names []string
	for _, x := range p.recv()["result"].(map[string]any)["tools"].([]any) {
		tl := x.(map[string]any)
		if tl["description"] == "" || tl["inputSchema"].(map[string]any)["type"] != "object" {
			t.Errorf("도구 정의 = %v", tl)
		}
		names = append(names, tl["name"].(string))
	}
	if strings.Join(names, ",") != "heka_find,heka_add,heka_flag" {
		t.Errorf("도구 = %v", names)
	}

	// 오류: 알 수 없는 메서드, 깨진 JSON(서버가 죽지 않음), 알 수 없는 도구.
	p.send(`{"jsonrpc":"2.0","id":5,"method":"nope"}`)
	if e := p.recv()["error"].(map[string]any); e["code"].(float64) != -32601 {
		t.Errorf("알 수 없는 메서드 = %v", e)
	}
	p.send(`{"jsonrpc":"2.0","id":6,"method":`)
	if m := p.recv(); m["error"].(map[string]any)["code"].(float64) != -32700 || m["id"] != nil {
		t.Errorf("깨진 JSON = %v", m)
	}
	p.send(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	if e := p.recv()["error"].(map[string]any); e["code"].(float64) != -32602 {
		t.Errorf("알 수 없는 도구 = %v", e)
	}

	// 도구: 등록 → 조회 → 같은 내용 재등록(바로 등록, 후보 알림) → 표시. 실패는 isError.
	txt, isErr := result(p.call(8, "heka_add", `{"id":"release-check","text":"배포 점검표는 릴리스 전에 확인한다.","about":["릴리스 점검"],"scope":["personal"],"source":"doc","body":"위키 참고."}`))
	if isErr || !strings.Contains(txt, "등록했습니다") {
		t.Errorf("heka_add = %q %v", txt, isErr)
	}
	txt, isErr = result(p.call(9, "heka_find", `{"query":"릴리스 전에 뭘 확인하지?"}`))
	if isErr || !strings.Contains(txt, "[release-check]") || !strings.Contains(txt, "위키 참고.") {
		t.Errorf("heka_find = %q %v", txt, isErr)
	}
	txt, isErr = result(p.call(1, "heka_add", `{"id":"release-check-2","text":"배포 점검표는 릴리스 전에 확인한다.","about":["릴리스 점검"],"scope":["personal"],"source":"doc"}`))
	if isErr || !strings.Contains(txt, "등록했습니다: release-check-2") || !strings.Contains(txt, "- release-check:") || strings.Contains(txt, "대기함") {
		t.Errorf("후보가 있어도 등록 = %q %v", txt, isErr)
	}
	if txt, isErr = result(p.call(3, "heka_flag", `{"id":"release-check","reason":"점검표 위치가 바뀜"}`)); isErr || !strings.Contains(txt, "표시를 남겼") {
		t.Errorf("heka_flag = %q %v", txt, isErr)
	}
	rows, _ := os.ReadFile(filepath.Join(home, "ledger", filepathMonth(t, home)))
	if !strings.Contains(string(rows), `"event":"flag"`) || !strings.Contains(string(rows), "점검표 위치가 바뀜") || !strings.Contains(string(rows), `"event":"find"`) {
		t.Errorf("원장: %s", rows)
	}
	if e, ok := p.call(4, "heka_pending", `{"action":"list"}`)["error"].(map[string]any); !ok || e["code"].(float64) != -32602 {
		t.Errorf("heka_pending 은 없어야 함: %v", e)
	}
	for i, c := range []struct{ name, args string }{
		{"heka_find", `{}`}, {"heka_find", `{"query":3}`}, {"heka_flag", `{"id":"없는-항목","reason":"x"}`},
		{"heka_add", `{"id":"Bad"}`},
	} {
		if txt, isErr = result(p.call(5+i, c.name, c.args)); !isErr || txt == "" {
			t.Errorf("%s %s: isError 여야 함: %q", c.name, c.args, txt)
		}
	}
	// 실패가 이어져도 서버는 살아 있다.
	p.send(`{"jsonrpc":"2.0","id":9,"method":"ping"}`)
	if p.recv()["id"].(float64) != 9 {
		t.Error("서버가 응답해야 함")
	}
}
