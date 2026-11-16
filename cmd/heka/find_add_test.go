package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCmd(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func newHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HEKA_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("global_scopes: [personal]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestAddThenFind(t *testing.T) {
	home := newHome(t)
	code, out, errb := runCmd("add", "--id", "release-check", "--text", "배포 점검표는 릴리스 전에 확인한다.", "--about", "릴리스 점검",
		"--scope", "personal", "--source", "doc", "--body", "점검표는 팀 위키에 있다.")
	if code != 0 || !strings.Contains(out, "등록했습니다: release-check") {
		t.Fatalf("add: %d %q %q", code, out, errb)
	}
	// 낱말 질의, 문장 질의 모두 나오고 --json 은 약속한 모양이다.
	for _, q := range []string{"릴리스 점검", "릴리스 전에 무엇을 확인해야 하지?"} {
		code, out, _ = runCmd("find", q)
		if code != 0 || !strings.Contains(out, "[release-check] 배포 점검표는") || !strings.Contains(out, "\n    점검표는 팀 위키에 있다.") {
			t.Errorf("find %q: %d %q", q, code, out)
		}
	}
	code, out, _ = runCmd("find", "릴리스", "점검", "--json", "--budget", "3000", "--limit", "5")
	var got struct {
		Items []struct {
			ID, Text, Body, Why string
			Must                bool
			When                map[string][]string
		}
		Omitted    int
		OmittedIDs []string `json:"omitted_ids"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || len(got.Items) != 1 || got.Items[0].Body != "점검표는 팀 위키에 있다." || got.OmittedIDs == nil {
		t.Errorf("--json: %d %s", code, out)
	}
	if code, _, _ := runCmd("find"); code != 2 {
		t.Error("질의가 없으면 사용법 오류")
	}
	if code, out, _ := runCmd("find", "존재하지 않는 낱말 zzz"); code != 0 || !strings.Contains(out, "찾은 항목이 없습니다") {
		t.Errorf("빈 결과: %d %q", code, out)
	}
	rows, _ := os.ReadFile(filepath.Join(home, "ledger", filepathMonth(t, home)))
	if strings.Count(string(rows), `"event":"find"`) != 4 {
		t.Errorf("조회마다 원장 한 줄: %s", rows)
	}
}

func filepathMonth(t *testing.T, home string) string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(home, "ledger", "*.jsonl"))
	if len(m) != 1 {
		t.Fatalf("원장 파일 = %v", m)
	}
	return filepath.Base(m[0])
}

func TestAddFileAndChecked(t *testing.T) {
	home := newHome(t)
	f := filepath.Join(t.TempDir(), "x.md")
	src := "---\nid: from-file\ntext: 파일에서 온 규칙.\nsource: user\nquote: 지어낸 말\nchecked: 2020-01-01\nabout: [파일]\nscope: [personal]\n---\n본문\n"
	os.WriteFile(f, []byte(src), 0o644)
	code, out, errb := runCmd("add", "--file", f, "--checked")
	if code != 0 {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	b, _ := os.ReadFile(filepath.Join(home, "items", "from-file.md"))
	today := bytes.Contains(b, []byte("checked: 20")) && !bytes.Contains(b, []byte("checked: 2020-01-01"))
	if !today || !strings.Contains(string(b), "확인되지 않은 인용") {
		t.Errorf("--checked 는 오늘 날짜, 파일의 checked 는 무시:\n%s", b)
	}
	// 사람이 확인(checked)했으면 인용이 확인되지 않아도 주입 대상이다.
	if _, out, _ := runCmd("find", "파일"); !strings.Contains(out, "from-file") {
		t.Errorf("find: %q", out)
	}
	if code, _, _ := runCmd("add", "--id", "x"); code != 1 {
		t.Error("검증 실패는 종료 코드 1")
	}
}

func TestPendingCommands(t *testing.T) {
	home := newHome(t)
	add := func(id, text string) {
		t.Helper()
		if code, out, errb := runCmd("add", "--id", id, "--text", text, "--about", "푸시 규칙", "--scope", "personal", "--source", "doc"); code != 0 {
			t.Fatalf("%d %q %q", code, out, errb)
		}
	}
	add("first", "테스트는 푸시 전에 돌린다.")
	add("second", "린트는 푸시 전에 돌린다.")
	if _, out, _ := runCmd("pending"); !strings.Contains(out, "[second]") || !strings.Contains(out, "- first:") {
		t.Errorf("pending: %q", out)
	}
	if code, _, _ := runCmd("pending", "keep", "second"); code != 1 {
		t.Error("이유 없는 keep 은 실패")
	}
	if code, out, errb := runCmd("pending", "keep", "second", "--reason", "다른 규칙"); code != 0 {
		t.Fatalf("keep: %q %q", out, errb)
	}
	if _, err := os.Stat(filepath.Join(home, "items", "second.md")); err != nil {
		t.Error("keep 은 등록해야 함")
	}
	if code, _, _ := runCmd("pending", "bogus", "x"); code != 2 {
		t.Error("모르는 결정은 사용법 오류")
	}
	if _, out, _ := runCmd("pending"); !strings.Contains(out, "비어") {
		t.Errorf("pending: %q", out)
	}
}
