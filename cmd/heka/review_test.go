package main

import (
	"bytes"
	"encoding/json"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeCommand(t *testing.T, stdout string) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(out, []byte(stdout), 0o644); err != nil {
		t.Fatal(err)
	}
	return `["sh", "-c", "cat >/dev/null; cat \"$0\"", "` + out + `"]`
}

func addItem(t *testing.T, id, text string) {
	t.Helper()
	if code, out, errb := runCmd("add", "--id", id, "--text", text, "--about", "푸시 규칙", "--scope", "personal", "--source", "doc", "--body", "본문 "+id); code != 0 {
		t.Fatalf("add %s: %d %q %q", id, code, out, errb)
	}
}

// add -> judge-add(가짜 명령) -> review --json -> review apply 흐름.
func TestJudgeAddReviewFlow(t *testing.T) {
	home := newHome(t)
	reply := `{"verdicts":[{"id":"first","verdict":"merge","reason":"같은 규칙","merged":{"text":"푸시 전에 테스트와 린트를 돌린다.","body":"본문 first 본문 second 합친 본문","about":["푸시 규칙"]}}]}`
	cfg := "global_scopes: [personal]\nregister_judge:\n  command: " + fakeCommand(t, reply) + "\nreview:\n  batch: 1\n"
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	addItem(t, "first", "테스트는 푸시 전에 돌린다.")
	addItem(t, "second", "린트는 푸시 전에 돌린다.")
	if code, out, errb := runCmd("judge-add", "second"); code != 0 {
		t.Fatalf("judge-add: %d %q %q", code, out, errb)
	}
	if code, _, _ := runCmd("judge-add"); code != 2 {
		t.Error("id 가 없으면 사용법 오류")
	}
	if code, _, _ := runCmd("judge-add", "nope"); code != 1 {
		t.Error("없는 항목은 실패")
	}

	code, out, _ := runCmd("review", "--json")
	var got struct {
		Total, OldestDays, Shown int
		Entries                  []struct {
			ID, Kind   string
			New        struct{ ID, Text, Body, Source string }
			Target     struct{ ID, Text string }
			Merged     struct{ Text string }
			HeaderDiff []struct{ Field, New, Target string } `json:"header_diff"`
		}
	}
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || got.Total != 1 || got.Shown != 1 || len(got.Entries) != 1 {
		t.Fatalf("review --json: %d %s", code, out)
	}
	en := got.Entries[0]
	if en.ID != "second--first" || en.Kind != "merge" || en.New.ID != "second" || en.New.Body != "본문 second" || en.Target.ID != "first" || en.Merged.Text != "푸시 전에 테스트와 린트를 돌린다." {
		t.Errorf("항목 = %+v", en)
	}
	if en.HeaderDiff == nil || len(en.HeaderDiff) != 0 {
		t.Errorf("머리말이 같으면 header_diff 는 빈 배열: %v", en.HeaderDiff)
	}
	// 터미널이 아니면 --json 을 쓰라고 안내한다.
	if code, _, errb := runCmd("review"); code != 1 || !strings.Contains(errb, "heka review --json") {
		t.Errorf("터미널이 아닐 때: %d %q", code, errb)
	}

	if code, _, _ := runCmd("review", "apply", "second--first"); code != 2 {
		t.Error("결정이 없으면 사용법 오류")
	}
	if code, _, _ := runCmd("review", "apply", "second--first", "bogus"); code != 1 {
		t.Error("모르는 결정은 실패")
	}
	body := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(body, []byte("고친 본문\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errb := runCmd("review", "apply", "second--first", "merge", "--text", "고친 주입문", "--body-file", body, "--about", "푸시 규칙, 린트")
	if code != 0 || !strings.Contains(out, "first 에 합치고 second 를 은퇴시켰습니다") {
		t.Fatalf("apply: %d %q %q", code, out, errb)
	}
	b, _ := os.ReadFile(filepath.Join(home, "items", "first.md"))
	if !strings.Contains(string(b), "text: 고친 주입문") || !strings.Contains(string(b), "about: [푸시 규칙, 린트]") || !strings.Contains(string(b), "고친 본문\n\n합친 항목 second: 출처 doc") {
		t.Errorf("합친 항목:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(home, "items", "second.md")); err == nil {
		t.Error("second 는 은퇴해야 함")
	}
	if _, out, _ := runCmd("review", "--json"); !strings.Contains(out, `"total": 0`) && !strings.Contains(out, `"total":0`) {
		t.Errorf("검토 목록이 비어야 함: %s", out)
	}
	if _, out, _ := runCmd("why", "--id", "second"); !strings.Contains(out, "등록 판정: first merge") {
		t.Errorf("why 에 등록 판정이 보여야 함: %s", out)
	}
}

func TestReviewJSONAllAndNotify(t *testing.T) {
	home := newHome(t)
	notes := filepath.Join(t.TempDir(), "n.txt")
	cfg := "global_scopes: [personal]\nreview:\n  batch: 2\n  notify_min: 3\n  notify_command: [sh, -c, 'echo \"$0\" >> " + notes + "']\n"
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	newRand = func() *rand.Rand { return rand.New(rand.NewSource(1)) }
	addItem(t, "target", "대상 글")
	for _, id := range []string{"n1", "n2", "n3"} {
		addItem(t, id, "새 글 "+id)
		entry := `{"id":"` + id + `--target","kind":"merge","new_id":"` + id + `","target_id":"target","created":"2026-11-20T00:00:00Z"}`
		if err := os.MkdirAll(filepath.Join(home, "review"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "review", id+"--target.json"), []byte(entry), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	count := func(args ...string) (shown, total int) {
		var got struct{ Shown, Total int }
		_, out, _ := runCmd(args...)
		if json.Unmarshal([]byte(out), &got) != nil {
			t.Fatalf("%s", out)
		}
		return got.Shown, got.Total
	}
	if s, n := count("review", "--json"); s != 2 || n != 3 {
		t.Errorf("batch 2: %d/%d", s, n)
	}
	if s, n := count("review", "--json", "--all"); s != 3 || n != 3 {
		t.Errorf("--all: %d/%d", s, n)
	}
	if code, out, errb := runCmd("review", "--notify"); code != 0 {
		t.Fatalf("notify: %d %q %q", code, out, errb)
	}
	b, _ := os.ReadFile(notes)
	if !strings.HasPrefix(string(b), "heka 검토 대기 3건 (가장 오래된 것 ") || !strings.Contains(string(b), "세션에서 'heka 검토해줘', 터미널에서 heka review") {
		t.Errorf("알림 = %q", b)
	}
}

func TestReviewInteractiveWhenTerminal(t *testing.T) {
	newHome(t)
	isTerminal = func(io.Reader) bool { return true }
	t.Cleanup(func() { isTerminal = func(io.Reader) bool { return false } })
	var out, errb bytes.Buffer
	if code := runIn([]string{"review"}, strings.NewReader(""), &out, &errb); code != 0 || !strings.Contains(out.String(), "검토할 항목이 없습니다.") {
		t.Errorf("터미널: %d %q %q", code, out.String(), errb.String())
	}
}

func TestHelpHasNoPending(t *testing.T) {
	_, out, _ := runCmd("help")
	if strings.Contains(out, "pending") || !strings.Contains(out, "judge-add") || !strings.Contains(out, "review") {
		t.Errorf("도움말: %s", out)
	}
}
