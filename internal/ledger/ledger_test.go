package ledger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAppendLines(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := Append(home, Row{Session: "s", Event: "PreToolUse", Moment: "tool", Prompt: strings.Repeat("가", 300),
				Injected: []Injected{{"a", "h", "why"}}}, now)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := Append(home, Row{Event: "x"}, now.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, "ledger", "2026-11.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 20 {
		t.Fatalf("%d 줄", len(lines))
	}
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("JSON 이 아님: %q", l)
		}
		if m["ts"] != "2026-11-15T09:00:00.000Z" || len([]rune(m["prompt"].(string))) != 200 || m["actions"] == nil || m["confirm"] == nil {
			t.Errorf("%v", m)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "2026-12.jsonl")); err != nil {
		t.Errorf("달이 바뀌면 새 파일: %v", err)
	}
}

func TestLogErrorAndFailure(t *testing.T) {
	home := t.TempDir()
	LogError(home, "PreToolUse", "줄\n바꿈  포함", time.Now())
	b, _ := os.ReadFile(filepath.Join(home, "ledger", "errors.log"))
	if strings.Count(string(b), "\n") != 1 || !strings.Contains(string(b), "PreToolUse\t줄 바꿈 포함") {
		t.Errorf("errors.log = %q", b)
	}
	// 쓸 수 없는 자리면 오류를 돌려줄 뿐 패닉하지 않는다.
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Append(filepath.Join(file, "sub"), Row{}, time.Now()); err == nil {
		t.Error("오류가 나야 함")
	}
	LogError(filepath.Join(file, "sub"), "x", "y", time.Now())
}

func TestPickRoundTrip(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	pick := &Pick{Listed: 14, ListTokens: 420, MS: 2850, Unknown: 1, Items: []Picked{
		{ID: "a", Direct: true, Judged: true, Inject: true, MS: 900},
		{ID: "b", Judged: true, Reused: true},
		{ID: "c", Direct: true, Judged: true, Error: true},
		{ID: "d"},
	}}
	if err := Append(home, Row{Session: "s", Event: "UserPromptSubmit", Moment: "prompt", Pick: pick}, now); err != nil {
		t.Fatal(err)
	}
	if err := Append(home, Row{Session: "s", Event: "UserPromptSubmit", Moment: "prompt", Pick: &Pick{Listed: 3, Error: true, Timeout: true, Items: []Picked{}}}, now); err != nil {
		t.Fatal(err)
	}
	if err := Append(home, Row{Session: "s", Event: "UserPromptSubmit", Moment: "prompt"}, now); err != nil {
		t.Fatal(err)
	}
	rows := ReadAll(home)
	if len(rows) != 3 {
		t.Fatalf("%d 줄", len(rows))
	}
	if !reflect.DeepEqual(rows[0].Pick, pick) {
		t.Errorf("왕복 = %+v", rows[0].Pick)
	}
	if p := rows[1].Pick; p == nil || !p.Error || !p.Timeout || p.Items == nil || len(p.Items) != 0 {
		t.Errorf("오류 기록 = %+v", p)
	}
	if rows[2].Pick != nil {
		t.Errorf("pick 이 없는 줄에 생김: %+v", rows[2].Pick)
	}
	b, _ := os.ReadFile(filepath.Join(home, "ledger", "2026-11.jsonl"))
	if lines := strings.Split(strings.TrimSpace(string(b)), "\n"); strings.Contains(lines[2], `"pick"`) || !strings.Contains(lines[0], `"list_tokens":420`) {
		t.Errorf("줄 꼴: %s", b)
	}
}
