package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run 의 경우(version, 인자 없음, help, 모르는 명령)를 확인한다.
func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // 부분 문자열
		wantStderr string // 부분 문자열
	}{
		{"version", []string{"version"}, 0, version, ""},
		{"인자 없음", nil, 0, "사용법", ""},
		{"help", []string{"help"}, 0, "사용법", ""},
		{"모르는 명령", []string{"nosuch"}, 2, "", "nosuch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run(tt.args, &out, &errb)
			if code != tt.wantCode {
				t.Errorf("종료 코드 = %d, 기대 %d", code, tt.wantCode)
			}
			if !strings.Contains(out.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, %q 를 포함해야 함", out.String(), tt.wantStdout)
			}
			if !strings.Contains(errb.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, %q 를 포함해야 함", errb.String(), tt.wantStderr)
			}
		})
	}
}

// index 명령의 경우(색인 생성, --check, error 가 있을 때)를 확인한다.
func TestIndexCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HEKA_HOME", home)
	items := filepath.Join(home, "items")
	if err := os.MkdirAll(items, 0o755); err != nil {
		t.Fatal(err)
	}
	good := "---\nid: sample-rule\ntext: 커밋 메시지는 한국어로 쓴다.\nsource: user\nabout: [커밋 메시지]\nscope: [repo/sample-app]\nunknown_key: 1\n---\n"
	if err := os.WriteFile(filepath.Join(items, "sample-rule.md"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	run1 := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := run(append([]string{"index"}, args...), &out, &errb)
		return code, out.String(), errb.String()
	}

	code, out, _ := run1("--check")
	if code != 0 || !strings.Contains(out, "항목 1건 유효, 제외 0건") || !strings.Contains(out, "sample-rule.md: warn: 모르는 머리말 키: unknown_key") {
		t.Errorf("--check: 코드 %d, 출력 %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, "index", "index.db")); err == nil {
		t.Error("--check 는 색인을 쓰면 안 됨")
	}

	code, out, _ = run1()
	if code != 0 || !strings.Contains(out, "항목 1건 색인, 제외 0건") {
		t.Errorf("index: 코드 %d, 출력 %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, "index", "index.db")); err != nil {
		t.Errorf("색인 파일이 있어야 함: %v", err)
	}

	bad := "---\nid: bad-rule\ntext: 틀린 항목\nsource: web\nabout: [x]\nscope: [y]\n---\n"
	if err := os.WriteFile(filepath.Join(items, "bad-rule.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ = run1()
	if code != 1 || !strings.Contains(out, "항목 1건 색인, 제외 1건") || !strings.Contains(out, "bad-rule.md: error: source") {
		t.Errorf("error 가 있을 때: 코드 %d, 출력 %q", code, out)
	}
	if code, _, _ = run1("--check"); code != 1 {
		t.Errorf("--check 도 error 가 있으면 1: %d", code)
	}
	if code, _, e := run1("--nosuch"); code != 2 || e == "" {
		t.Errorf("모르는 인자: %d %q", code, e)
	}
}

// select 명령: 낡은(없는) 색인을 다시 만들고, 결과를 JSON 으로 출력하고, 반복 플래그를 받는다.
func TestSelectCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HEKA_HOME", home)
	items := filepath.Join(home, "items")
	if err := os.MkdirAll(items, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "scopes:\n  repo/sample-app: [team/sample]\nactions:\n  git.commit: {in: git.write}\n"
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	for id, scope := range map[string]string{"commit-rule": "team/sample", "other-rule": "repo/other"} {
		body := "---\nid: " + id + "\ntext: 커밋 메시지는 한국어로 쓴다.\nmust: true\nwhen: {action: git.commit}\nsource: user\nquote: 지어낸 말\nabout: [커밋 메시지]\nscope: [" + scope + "]\n---\n"
		if err := os.WriteFile(filepath.Join(items, id+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sel := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := run(append([]string{"select"}, args...), &out, &errb)
		return code, out.String(), errb.String()
	}

	code, out, errs := sel("--moment", "tool", "--action", "git.commit", "--scope", "repo/sample-app", "--scope", "repo/x")
	if code != 0 {
		t.Fatalf("코드 %d, stderr %q", code, errs)
	}
	var res struct {
		Injections []struct{ ID, Text, Why string }
		Drops      []struct{ ID, Stage string }
		Asked      []string
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("JSON 이 아님: %v\n%s", err, out)
	}
	if len(res.Injections) != 1 || res.Injections[0].ID != "commit-rule" || res.Injections[0].Text != "커밋 메시지는 한국어로 쓴다." {
		t.Errorf("injections = %+v", res.Injections)
	}
	if len(res.Drops) != 1 || res.Drops[0].ID != "other-rule" || res.Drops[0].Stage != "범위" {
		t.Errorf("drops = %+v", res.Drops)
	}
	if !strings.Contains(out, `"text": "커밋 메시지는 한국어로 쓴다."`) || strings.Contains(out, `\u`) {
		t.Errorf("한글이 이스케이프됨: %s", out)
	}

	_, out, _ = sel("--moment", "tool", "--action", "git.commit", "--scope", "repo/sample-app", "--seen", "commit-rule")
	if !strings.Contains(out, `"stage": "중복"`) {
		t.Errorf("--seen 이 먹지 않음: %s", out)
	}
	_, out, _ = sel("--moment", "prompt", "--text", "커밋해 줘", "--tool-made")
	if !strings.Contains(out, `"injections": []`) {
		t.Errorf("--tool-made: %s", out)
	}
	if code, _, _ := sel(); code != 2 {
		t.Errorf("--moment 없으면 2: %d", code)
	}
	if code, _, _ := sel("--moment", "nope"); code != 1 {
		t.Errorf("모르는 순간은 1: %d", code)
	}
}
