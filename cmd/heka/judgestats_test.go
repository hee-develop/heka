package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 두 달에 걸친 원장. 판정 10건의 걸린 시간(ms, 넣으라는 답이면 +):
// 10월 1000+ 2500- 3000+ / 11월 3001+ 4500- 5000+ 6500+ 9000- 11000+ 16000(시간 초과)
func judgeHome(t *testing.T) {
	t.Helper()
	home := newHome(t)
	files := map[string]string{
		"2026-10.jsonl": `{"ts":"2026-10-30T01:00:00.000Z","session":"s1","event":"UserPromptSubmit","moment":"prompt","injected":[],"dropped":[],"judges":[{"id":"a","inject":true,"ms":1000},{"id":"b","inject":false,"ms":2500},{"id":"c","inject":true,"ms":3000}]}
`,
		"2026-11.jsonl": `{"ts":"2026-11-02T01:00:00.000Z","session":"s2","event":"UserPromptSubmit","moment":"prompt","injected":[],"dropped":[],"judges":[{"id":"a","inject":true,"ms":3001},{"id":"b","inject":false,"ms":4500},{"id":"c","inject":true,"ms":5000}]}
{깨진 줄
{"ts":"2026-11-03T01:00:00.000Z","session":"s2","event":"SubagentStart","moment":"subagent_start","injected":[],"dropped":[],"judges":[{"id":"a","inject":true,"ms":6500},{"id":"b","inject":false,"ms":9000},{"id":"c","inject":true,"ms":11000},{"id":"d","inject":false,"ms":16000,"error":true,"timeout":true}]}
{"ts":"2026-11-04T01:00:00.000Z","session":"s2","event":"PreToolUse","moment":"tool","actions":["git.push"],"injected":[],"dropped":[]}
`,
	}
	for name, body := range files {
		p := filepath.Join(home, "ledger", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestJudgeStats(t *testing.T) {
	judgeHome(t)
	// 손으로 센 값. 걸린 시간이 상한을 넘은 판정만 잘린다(같으면 안 잘림).
	// 정렬: 1000 2500 3000 3001 4500 5000 6500 9000 11000 16000 → 중앙값 (4500+5000)/2, 95% 는 10번째
	want := judgeStats{Count: 10, Errors: 1, Timeouts: 1, MedianMS: 4750, P95MS: 16000, MaxMS: 16000, Caps: []capRow{
		{3, 7, 4}, {4, 6, 3}, {5, 4, 2}, {6, 4, 2}, {8, 3, 1}, {10, 2, 1}, {15, 1, 0},
	}}
	code, out, errb := runCmd("judge-stats", "--json")
	if code != 0 || errb != "" {
		t.Fatalf("%d %q", code, errb)
	}
	var got judgeStats
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if !equalStats(got, want) {
		t.Errorf("결과 = %+v\n기대 %+v", got, want)
	}

	// 사람이 읽는 표
	_, out, _ = runCmd("judge-stats")
	if !strings.Contains(out, "판정 10건 (오류 1, 시간 초과 1)") || !strings.Contains(out, "중앙값 4750ms, 95% 16000ms, 최댓값 16000ms") {
		t.Errorf("요약: %s", out)
	}
	rows := map[string][]string{}
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) == 3 && strings.HasSuffix(f[0], "초") {
			rows[f[0]] = f[1:]
		}
	}
	for _, c := range want.Caps {
		r := rows[itoa(c.CapSeconds)+"초"]
		if len(r) != 2 || r[0] != itoa(c.Cut) || r[1] != itoa(c.CutInject) {
			t.Errorf("%d초 줄 = %v, 기대 %d %d", c.CapSeconds, r, c.Cut, c.CutInject)
		}
	}

	// --since: 11월 이후 7건(3001 4500 5000 6500 9000 11000 16000)
	_, out, _ = runCmd("judge-stats", "--json", "--since", "2026-11-01")
	got = judgeStats{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	want = judgeStats{Since: "2026-11-01", Count: 7, Errors: 1, Timeouts: 1, MedianMS: 6500, P95MS: 16000, MaxMS: 16000, Caps: []capRow{
		{3, 7, 4}, {4, 6, 3}, {5, 4, 2}, {6, 4, 2}, {8, 3, 1}, {10, 2, 1}, {15, 1, 0},
	}}
	if !equalStats(got, want) {
		t.Errorf("--since 결과 = %+v\n기대 %+v", got, want)
	}

	if code, _, _ := runCmd("judge-stats", "--since", "어제"); code != 2 {
		t.Errorf("날짜가 틀리면 사용법 오류: %d", code)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func equalStats(a, b judgeStats) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestJudgeStatsEmpty(t *testing.T) {
	newHome(t)
	code, out, _ := runCmd("judge-stats")
	if code != 0 || !strings.Contains(out, "판정 기록이 없습니다") {
		t.Errorf("%d %q", code, out)
	}
	_, out, _ = runCmd("judge-stats", "--json")
	var st judgeStats
	if json.Unmarshal([]byte(out), &st) != nil || st.Count != 0 || len(st.Caps) != 7 {
		t.Errorf("빈 JSON = %s", out)
	}
}

func TestWhyShowsJudges(t *testing.T) {
	home := newHome(t)
	row := `{"ts":"2026-11-04T05:00:00.000Z","session":"s2","event":"UserPromptSubmit","moment":"prompt","actions":[],"injected":[{"id":"topic-ref","hash":"h","why":"판정: 관련"}],"dropped":[{"id":"other","stage":"판정","reason":"넣지 않음: 무관"}],"judges":[{"id":"topic-ref","inject":true,"ms":1234},{"id":"other","inject":false,"ms":800},{"id":"slow","inject":false,"ms":15000,"error":true,"timeout":true},{"id":"bad","inject":false,"ms":40,"error":true}]}
`
	p := filepath.Join(home, "ledger", "2026-11.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out, _ := runCmd("why")
	want := "판정: topic-ref 넣음 1234ms; other 안 넣음 800ms; slow 시간 초과 15000ms; bad 오류 40ms"
	if !strings.Contains(out, want) {
		t.Errorf("why = %q, %q 를 포함해야 함", out, want)
	}
}
