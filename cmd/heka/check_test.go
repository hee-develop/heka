package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 결과물 검사 기록이 든 원장과 항목을 가진 데이터 폴더를 만든다.
func checkHome(t *testing.T) string {
	t.Helper()
	home := newHome(t)
	items := map[string]string{
		"fmt-a": "id: fmt-a\ntext: 첫 줄은 짧게.\nwhen: {action: git.commit}\nabout: [커밋]\nscope: [personal]\nsource: doc\nchecked: 2026-11-01\ncheck: {target: commit_message, pattern: '^.{1,50}$', mode: block}",
		"fmt-b": "id: fmt-b\ntext: 금지어를 쓰지 않는다.\nabout: [응답]\nscope: [personal]\nsource: doc\nchecked: 2026-11-01\ncheck: {target: last_message, not_pattern: 'zzz', mode: log}",
		"plain": "id: plain\ntext: 검사 없는 항목.\nabout: [참고]\nscope: [personal]\nsource: doc\nchecked: 2026-11-01",
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
	rows := `{"ts":"2026-11-02T01:00:00.000Z","session":"s1","event":"PreToolUse","moment":"tool","actions":["git.commit"],"injected":[],"dropped":[],"checks":[{"id":"fmt-a","target":"commit_message","result":"fail","blocked":true}]}
{"ts":"2026-11-02T02:00:00.000Z","session":"s1","event":"PreToolUse","moment":"tool","actions":["git.commit"],"injected":[],"dropped":[],"checks":[{"id":"fmt-a","target":"commit_message","result":"pass","blocked":false}]}
{"ts":"2026-11-02T03:00:00.000Z","session":"s1","event":"PreToolUse","moment":"tool","actions":["git.commit"],"injected":[],"dropped":[],"checks":[{"id":"fmt-a","target":"commit_message","result":"fail","blocked":true}],"shadow":true}
{"ts":"2026-11-02T04:00:00.000Z","session":"s1","event":"Stop","moment":"stop","actions":[],"injected":[],"dropped":[],"checks":[{"id":"fmt-b","target":"last_message","result":"fail","blocked":false},{"id":"fmt-a","target":"commit_message","result":"skip","blocked":false,"note":"변수를 풀지 못함"}]}
{"ts":"2026-11-03T03:00:00.000Z","session":"s1","event":"flag","moment":"flag","actions":[],"injected":[{"id":"fmt-b","hash":"h","why":"사유"}],"dropped":[]}
`
	p := filepath.Join(home, "ledger", "2026-11.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestStatsCheckFailures(t *testing.T) {
	checkHome(t)
	code, out, errb := runCmd("stats", "fmt-a")
	if code != 0 || errb != "" || !strings.Contains(out, "검사에 걸린 횟수 2회") {
		t.Errorf("fmt-a: %d %q %q", code, out, errb)
	}
	code, out, _ = runCmd("stats", "fmt-b", "--json")
	var st struct {
		CheckFails int `json:"check_fails"`
		Flags      int `json:"flags"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil || st.CheckFails != 1 || st.Flags != 1 {
		t.Errorf("fmt-b: %d %q", code, out)
	}
	_, out, _ = runCmd("stats", "plain")
	if !strings.Contains(out, "검사에 걸린 횟수 0회") {
		t.Errorf("plain: %q", out)
	}
}

func TestListViolated(t *testing.T) {
	checkHome(t)
	ids := func(args ...string) []string {
		code, out, errb := runCmd(append([]string{"list", "--json"}, args...)...)
		var got []struct {
			ID         string `json:"id"`
			Violations int    `json:"violations"`
		}
		if code != 0 || json.Unmarshal([]byte(out), &got) != nil {
			t.Fatalf("list %v: %d %q %q", args, code, out, errb)
		}
		var r []string
		for _, g := range got {
			r = append(r, g.ID)
		}
		return r
	}
	if got := ids("--violated", "2"); strings.Join(got, ",") != "fmt-a,fmt-b" { // fmt-a 검사 2 + flag 0, fmt-b 검사 1 + flag 1
		t.Errorf("--violated 2: %v", got)
	}
	if got := ids("--violated", "3"); len(got) != 0 {
		t.Errorf("--violated 3: %v", got)
	}
	if got := ids("--violated", "1"); strings.Join(got, ",") != "fmt-a,fmt-b" {
		t.Errorf("--violated 1: %v", got)
	}
	if got := ids(); len(got) != 3 {
		t.Errorf("옵션 없음: %v", got)
	}
	if code, _, _ := runCmd("list", "--violated", "-1"); code != 2 {
		t.Errorf("음수는 사용법 오류: %d", code)
	}
}

func TestWhyShowsChecksAndShadow(t *testing.T) {
	checkHome(t)
	_, out, _ := runCmd("why", "--last", "10")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("줄 수: %q", out)
	}
	if !strings.Contains(lines[0], "검사: fmt-a commit_message fail(막음)") || strings.Contains(lines[0], "(그림자)") {
		t.Errorf("첫 줄: %s", lines[0])
	}
	if !strings.HasSuffix(lines[2], "(그림자)") {
		t.Errorf("그림자 줄: %s", lines[2])
	}
	if !strings.Contains(lines[3], "fmt-b last_message fail; fmt-a commit_message skip(변수를 풀지 못함)") {
		t.Errorf("Stop 줄: %s", lines[3])
	}
}

func TestHookPrintSettingsShadow(t *testing.T) {
	newHome(t)
	hook := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := runIn(append([]string{"hook"}, args...), strings.NewReader(""), &out, &errb)
		return code, out.String(), errb.String()
	}
	for _, args := range [][]string{{"--print-settings", "--shadow"}, {"--shadow", "--print-settings"}} {
		code, out, _ := hook(args...)
		var st struct {
			Hooks map[string][]struct {
				Hooks []struct {
					Command string `json:"command"`
					Async   bool   `json:"async"`
				} `json:"hooks"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal([]byte(out), &st); err != nil || code != 0 || len(st.Hooks) != 5 || st.Hooks["Stop"] == nil {
			t.Fatalf("%v: %d %q", args, code, out)
		}
		for ev, es := range st.Hooks {
			if h := es[0].Hooks[0]; h.Command != "HEKA_SHADOW=1 heka hook" || !h.Async {
				t.Errorf("%s: %+v", ev, h)
			}
		}
	}
	code, out, _ := hook("--print-settings")
	if code != 0 || strings.Contains(out, "HEKA_SHADOW") || strings.Contains(out, "async") || !strings.Contains(out, `"Stop"`) {
		t.Errorf("평소 설정: %d %q", code, out)
	}
	if code, _, e := hook("--shadow"); code != 2 || e == "" {
		t.Errorf("--shadow 단독은 사용법 오류: %d %q", code, e)
	}
}
