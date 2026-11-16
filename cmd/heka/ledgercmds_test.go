package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hee-develop/heka/internal/ledger"
)

// 지어낸 항목과 두 달에 걸친 원장(깨진 줄 포함)을 가진 데이터 폴더를 만든다.
func ledgerHome(t *testing.T) string {
	t.Helper()
	home := newHome(t)
	items := map[string]string{
		"git-must":  "id: git-must\ntext: 푸시 전에 확인한다.\nmust: true\nwhen: {action: git.push, agent: reviewer}\nabout: [푸시]\nscope: [personal]\nsource: doc\nchecked: 2026-11-01\nexpires: 2027-01-01",
		"topic-ref": "id: topic-ref\ntext: 배포 안내.\nabout: [배포]\nscope: [personal, team/sample]\nsource: doc",
		"flagged-1": "id: flagged-1\ntext: 낡은 규칙.\nmust: true\nwhen: {action: code.edit}\nabout: [편집]\nscope: [personal]\nsource: doc",
	}
	for id, body := range items {
		p := filepath.Join(home, "items", id+".md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("---\n"+body+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 깨진 항목은 list 에서 빠진다.
	os.WriteFile(filepath.Join(home, "items", "broken.md"), []byte("머리말 없음"), 0o644)
	ledger := map[string]string{
		"2026-10.jsonl": `{"ts":"2026-10-30T01:00:00.000Z","session":"s1","event":"SessionStart","moment":"session_start","actions":[],"injected":[{"id":"topic-ref","hash":"h","why":"주제어 맞음"}],"dropped":[],"tokens":7}
{깨진 줄
{"ts":"2026-10-31T02:00:00.000Z","session":"s1","event":"PreToolUse","moment":"tool","actions":["git.push"],"injected":[{"id":"git-must","hash":"h","why":"동작 git.push"}],"dropped":[{"id":"topic-ref","stage":"예산","reason":"tool 순간의 상한 5토큰을 넘음"}],"tokens":12}
`,
		"2026-11.jsonl": `{"ts":"2026-11-02T03:00:00.000Z","session":"s2","event":"PreToolUse","moment":"tool","actions":["git.push"],"injected":[],"dropped":[{"id":"git-must","stage":"중복","reason":"이미 넣음"},{"id":"topic-ref","stage":"판정","reason":"넣지 않음: 무관"},{"id":"topic-ref","stage":"예산","reason":"참고 항목은 한 번에 최대 3건"}]}
"배열이 아닌 줄"
{"ts":"2026-11-03T04:00:00.000Z","session":"s2","event":"flag","moment":"flag","actions":[],"injected":[{"id":"flagged-1","hash":"h","why":"첫 번째 사유"}],"dropped":[]}
{"ts":"2026-11-04T05:00:00.000Z","session":"s2","event":"UserPromptSubmit","moment":"prompt","actions":[],"injected":[{"id":"git-must","hash":"h","why":"동작 git.push"},{"id":"topic-ref","hash":"h","why":"판정: 관련"}],"dropped":[],"tokens":30}
{"ts":"2026-11-05T06:00:00.000Z","session":"s2","event":"flag","moment":"flag","actions":[],"injected":[{"id":"flagged-1","hash":"h","why":"두 번째\n사유"}],"dropped":[]}
{"ts":"2026-11-06T07:00:00.000Z","event":"find","moment":"find","actions":[],"injected":[{"id":"git-must","why":"찾음"}],"dropped":[{"id":"git-must","stage":"예산","reason":"조회의 예산을 넘음"}]}
`,
	}
	for name, body := range ledger {
		p := filepath.Join(home, "ledger", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestWhy(t *testing.T) {
	ledgerHome(t)
	code, out, errb := runCmd("why")
	if code != 0 || errb != "" {
		t.Fatalf("why: %d %q", code, errb)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 7 { // 깨진 줄 둘을 뺀 7줄(두 달치)
		t.Fatalf("줄 수 %d:\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "2026-10-30T01:00:00Z SessionStart session_start | 넣음: topic-ref(주제어 맞음) | 토큰 7") {
		t.Errorf("첫 줄: %s", lines[0])
	}
	if want := "2026-10-31T02:00:00Z PreToolUse git.push | 넣음: git-must(동작 git.push) | 뺌: topic-ref[예산: tool 순간의 상한 5토큰을 넘음] | 토큰 12"; lines[1] != want {
		t.Errorf("둘째 줄: %s", lines[1])
	}
	if !strings.Contains(lines[2], "넣은 항목 없음") || !strings.Contains(lines[2], "git-must[중복: 이미 넣음]; topic-ref[판정: 넣지 않음: 무관]; topic-ref[예산:") {
		t.Errorf("셋째 줄: %s", lines[2])
	}
	if lines[3] != "2026-11-03T04:00:00Z flag flag | 대상: flagged-1(첫 번째 사유)" {
		t.Errorf("flag 줄: %s", lines[3])
	}

	_, out, _ = runCmd("why", "--last", "2")
	if n := strings.Count(out, "\n"); n != 2 || !strings.Contains(out, "2026-11-06T07:00:00Z find") {
		t.Errorf("--last 2: %q", out)
	}
	_, out, _ = runCmd("why", "--session", "s1")
	if n := strings.Count(out, "\n"); n != 2 {
		t.Errorf("--session s1: %q", out)
	}
	_, out, _ = runCmd("why", "--id", "flagged-1", "--last", "10")
	if n := strings.Count(out, "\n"); n != 2 || strings.Contains(out, "git-must") || !strings.Contains(out, "두 번째 사유") {
		t.Errorf("--id flagged-1: %q", out)
	}
	_, out, _ = runCmd("why", "--id", "topic-ref", "--session", "s2")
	if n := strings.Count(out, "\n"); n != 2 { // 중복 줄(뺌) + 프롬프트 줄(넣음)
		t.Errorf("--id topic-ref --session s2: %q", out)
	}
	_, out, _ = runCmd("why", "--id", "없는-항목")
	if !strings.Contains(out, "해당하는 원장 줄이 없습니다") {
		t.Errorf("빈 결과: %q", out)
	}

	code, out, _ = runCmd("why", "--json", "--id", "git-must", "--last", "3")
	var rows []map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 3 || rows[2]["event"] != "find" {
		t.Errorf("--json: %d %q", code, out)
	}
	if code, _, _ := runCmd("why", "--last", "0"); code != 2 {
		t.Errorf("--last 0 은 사용법 오류: %d", code)
	}
}

func TestWhyWithoutLedger(t *testing.T) {
	newHome(t)
	code, out, _ := runCmd("why")
	if code != 0 || !strings.Contains(out, "해당하는 원장 줄이 없습니다") {
		t.Errorf("%d %q", code, out)
	}
}

func TestList(t *testing.T) {
	ledgerHome(t)
	code, out, errb := runCmd("list")
	if code != 0 || !strings.Contains(errb, "검증에 실패한 항목 1건") {
		t.Fatalf("list: %d %q", code, errb)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("줄 수:\n%s", out)
	}
	want := "git-must  필수  조건: action=git.push agent=reviewer  범위: personal  확인: 2026-11-01  기한: 2027-01-01"
	if !contains(lines, want) {
		t.Errorf("git-must 줄이 없음:\n%s", out)
	}
	if !contains(lines, "topic-ref  참고  조건: -  범위: personal,team/sample  확인: -  기한: -") {
		t.Errorf("topic-ref 줄이 없음:\n%s", out)
	}
	if !strings.Contains(out, "flagged-1  필수  조건: action=code.edit  범위: personal  확인: -  기한: -  표시됨") {
		t.Errorf("flag 표시:\n%s", out)
	}

	_, out, _ = runCmd("list", "--must")
	if strings.Contains(out, "topic-ref") || strings.Count(out, "\n") != 2 {
		t.Errorf("--must: %q", out)
	}
	_, out, _ = runCmd("list", "--agent", "reviewer")
	if strings.Count(out, "\n") != 1 || !strings.HasPrefix(out, "git-must") {
		t.Errorf("--agent: %q", out)
	}
	_, out, _ = runCmd("list", "--flagged")
	if strings.Count(out, "\n") != 1 || !strings.HasPrefix(out, "flagged-1") {
		t.Errorf("--flagged: %q", out)
	}
	_, out, _ = runCmd("list", "--agent", "nobody")
	if !strings.Contains(out, "해당하는 항목이 없습니다") {
		t.Errorf("빈 목록: %q", out)
	}

	code, out, _ = runCmd("list", "--json", "--must", "--flagged")
	var got []struct {
		ID      string
		Must    bool
		When    map[string][]string
		Scope   []string
		Checked string
		Expires string
		Flagged bool
	}
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || len(got) != 1 || got[0].ID != "flagged-1" || !got[0].Flagged || got[0].When["action"][0] != "code.edit" {
		t.Errorf("--json: %d %q", code, out)
	}
	_, out, _ = runCmd("list", "--json", "--agent", "reviewer")
	json.Unmarshal([]byte(out), &got)
	if len(got) != 1 || got[0].Checked != "2026-11-01" || got[0].Expires != "2027-01-01" || got[0].Scope[0] != "personal" {
		t.Errorf("--json 필드: %q", out)
	}
	_, out, _ = runCmd("list", "--json", "--agent", "nobody")
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("빈 JSON 은 []: %q", out)
	}
}

func TestStats(t *testing.T) {
	ledgerHome(t)
	code, out, errb := runCmd("stats", "topic-ref")
	if code != 0 || errb != "" {
		t.Fatalf("stats: %d %q", code, errb)
	}
	for _, w := range []string{"topic-ref", "주입 2회, 마지막 주입 2026-11-04T05:00:00Z", "예산 2, 중복 0, 판정 1", "flag 0회"} {
		if !strings.Contains(out, w) {
			t.Errorf("%q 가 없음:\n%s", w, out)
		}
	}
	// 조회(find) 줄의 넣음·예산 탈락은 세지 않는다.
	code, out, _ = runCmd("stats", "git-must", "--json")
	var st struct {
		ID           string         `json:"id"`
		Injected     int            `json:"injected"`
		LastInjected string         `json:"last_injected"`
		Dropped      map[string]int `json:"dropped"`
		Flags        int            `json:"flags"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil {
		t.Fatalf("--json: %d %q", code, out)
	}
	if st.Injected != 2 || st.LastInjected != "2026-11-04T05:00:00Z" || st.Dropped["중복"] != 1 || st.Dropped["예산"] != 0 || st.Dropped["판정"] != 0 || st.Flags != 0 {
		t.Errorf("git-must: %+v", st)
	}
	_, out, _ = runCmd("stats", "flagged-1")
	if !strings.Contains(out, "주입 0회, 마지막 주입 없음") || !strings.Contains(out, "flag 2회, 마지막 사유: 두 번째 사유") {
		t.Errorf("flagged-1:\n%s", out)
	}
	_, out, _ = runCmd("stats", "flagged-1", "--json")
	var f map[string]any
	json.Unmarshal([]byte(out), &f)
	if f["flags"] != float64(2) || f["last_flag_reason"] != "두 번째\n사유" {
		t.Errorf("flag --json: %v", f)
	}
	if code, _, errb := runCmd("stats", "전혀-없는-항목"); code != 1 || !strings.Contains(errb, "원장 기록도 없습니다") {
		t.Errorf("없는 항목: %d %q", code, errb)
	}
	if code, _, _ := runCmd("stats"); code != 2 {
		t.Errorf("id 없음: %d", code)
	}
}

func TestRetireCommand(t *testing.T) {
	home := ledgerHome(t)
	if code, _, errb := runCmd("retire", "topic-ref"); code != 2 || !strings.Contains(errb, "--reason") {
		t.Errorf("사유 없이: %d %q", code, errb)
	}
	if code, _, errb := runCmd("retire", "no-such", "--reason", "x"); code != 1 || !strings.Contains(errb, "없음") {
		t.Errorf("없는 항목: %d %q", code, errb)
	}
	code, out, errb := runCmd("retire", "topic-ref", "--reason", "더 쓰지 않는다")
	if code != 0 || !strings.Contains(out, "topic-ref") || errb != "" {
		t.Fatalf("retire: %d %q %q", code, out, errb)
	}
	if _, err := os.Stat(filepath.Join(home, "items", "topic-ref.md")); err == nil {
		t.Error("항목 파일이 남음")
	}
	if _, err := os.Stat(filepath.Join(home, "index", "index.db")); err != nil {
		t.Error("색인을 다시 만들지 않음")
	}
	_, out, _ = runCmd("why", "--last", "1")
	if !strings.Contains(out, "retire retire | 대상: topic-ref | 사유: 더 쓰지 않는다") {
		t.Errorf("retire 줄: %q", out)
	}
	_, out, _ = runCmd("list")
	if strings.Contains(out, "topic-ref") {
		t.Errorf("list 에 남음: %q", out)
	}
	// 은퇴한 항목의 기록은 stats 로 여전히 본다.
	if code, _, _ := runCmd("stats", "topic-ref"); code != 0 {
		t.Errorf("은퇴 항목 stats: %d", code)
	}
}

func TestWhyPick(t *testing.T) {
	row := ledger.Row{TS: "2026-11-17T01:00:00.000Z", Event: "UserPromptSubmit", Moment: "prompt", Pick: &ledger.Pick{Listed: 14, MS: 2850, Items: []ledger.Picked{
		{ID: "a", Direct: true, Judged: true, Inject: true},
		{ID: "b", Judged: true},
		{ID: "c", Direct: true},
		{ID: "d", Judged: true, Error: true},
	}}}
	want := " | 모델 후보(14건 중, 2850ms): a 바로·판정 넣음, b 후보·판정 넣지 않음, c 바로·판정 안 함, d 후보·판정 오류"
	if got := whyLine(row); !strings.Contains(got, want) {
		t.Errorf("모델 후보 줄:\n%s", got)
	}
	row.Pick = &ledger.Pick{Listed: 14, Error: true}
	if got := whyLine(row); !strings.HasSuffix(got, " | 모델 후보: 오류") {
		t.Errorf("오류: %s", got)
	}
	row.Pick = &ledger.Pick{Listed: 2, MS: 10, Items: []ledger.Picked{}}
	if got := whyLine(row); !strings.HasSuffix(got, " | 모델 후보(2건 중, 10ms): 고른 항목 없음") {
		t.Errorf("빈 선택: %s", got)
	}
	row.Pick = nil
	if got := whyLine(row); strings.Contains(got, "모델 후보") {
		t.Errorf("pick 없음: %s", got)
	}
}
