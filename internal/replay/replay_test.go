package replay

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/index"
)

// 판정 명령: commit-message-korean 만 넣으라고 답한다.
const judgeYes = `in=$(cat); case "$in" in *"id: commit-message-korean"*) echo '{"inject": true, "reason": "답이 됨"}';; *) echo '{"inject": false, "reason": "무관"}';; esac`

// setup 은 testdata 의 항목과 설정을 임시 데이터 폴더에 복사하고 판정 명령(judgeBody)을 붙여 색인을 열어 준다.
func setup(t *testing.T, judgeBody string) (*index.Index, *config.Config, string) {
	t.Helper()
	home := t.TempDir()
	items, _ := filepath.Glob(filepath.Join("testdata", "items", "*.md"))
	for _, p := range items {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(home, "items", filepath.Base(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfgText, err := os.ReadFile(filepath.Join("testdata", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if judgeBody != "" {
		sh := filepath.Join(t.TempDir(), "judge.sh")
		if err := os.WriteFile(sh, []byte("#!/bin/sh\n"+judgeBody+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		cfgText = append(cfgText, []byte("judge:\n  command: ["+sh+"]\n  instruction: 지시문\n  timeout: 5s\n")...)
	}
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), cfgText, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(home, cfg); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix, cfg, home
}

func load(t *testing.T, jsonl string) []Case {
	t.Helper()
	cases, skips, err := Load(strings.NewReader(jsonl))
	if err != nil || len(skips) != 0 {
		t.Fatalf("읽기: %v %v", err, skips)
	}
	return cases
}

func TestLoadSkipsBrokenLines(t *testing.T) {
	in := strings.Join([]string{
		`{"id":"ok1","moment":{"kind":"prompt","text":"x","scope":"repo/sample-app"},"expect":{"silent":true,"모르는키":1},"앞으로":"무시"}`,
		`{깨진 줄`,
		``,
		`{"moment":{"kind":"prompt"}}`,
		`{"id":"bad-kind","moment":{"kind":"nope"}}`,
		`{"id":"ok1","moment":{"kind":"prompt"}}`,
		`{"id":"ok2","moment":{"kind":"tool","action":["git.push"],"scopes":["a","b"]},"expect":{"inject":["x"]},"confirmed":true,"source":"user"}`,
	}, "\n")
	cases, skips, err := Load(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 || cases[0].ID != "ok1" || cases[1].ID != "ok2" {
		t.Fatalf("사례 = %+v", cases)
	}
	if !reflect.DeepEqual([]int{skips[0].Line, skips[1].Line, skips[2].Line, skips[3].Line}, []int{2, 4, 5, 6}) || len(skips) != 4 {
		t.Errorf("건너뛴 줄 = %+v", skips)
	}
	// scope 는 문자열 하나도 받고, scope 와 scopes 둘 다 받는다.
	if !reflect.DeepEqual([]string(cases[0].Moment.Scope), []string{"repo/sample-app"}) || !reflect.DeepEqual([]string(cases[1].Moment.Scopes), []string{"a", "b"}) {
		t.Errorf("범위 = %+v %+v", cases[0].Moment, cases[1].Moment)
	}
	if cases[1].Confirmed == nil || !*cases[1].Confirmed || cases[1].Source != "user" || !reflect.DeepEqual([]string(cases[1].Moment.Action), []string{"git.push"}) {
		t.Errorf("사례 2 = %+v", cases[1])
	}
}

// 프롬프트·도구·침묵 사례와 also_*, unsure 의 채점. 손으로 센 값과 견준다.
func TestScoring(t *testing.T) {
	ix, cfg, _ := setup(t, "")
	push := `"moment":{"kind":"prompt","text":"푸시해 줘","scope":["repo/sample-app"]}`
	jsonl := strings.Join([]string{
		`{"id":"a",` + push + `,"expect":{"inject":["no-force-push"]}}`,
		`{"id":"b","moment":{"kind":"tool","tool":{"name":"Edit","input":{"file_path":"/work/sample-app/README.md"}},"scope":["repo/sample-app"]},"expect":{"inject":["doc-edit-rule"]}}`,
		`{"id":"c",` + push + `,"expect":{"inject":["doc-edit-rule"]}}`,
		`{"id":"d",` + push + `,"expect":{"not_inject":["no-force-push"]}}`,
		`{"id":"e",` + push + `,"expect":{"silent":true}}`,
		`{"id":"f","moment":{"kind":"prompt","text":"오늘 날씨 어때"},"expect":{"silent":true}}`,
		`{"id":"g",` + push + `,"expect":{"unsure":["no-force-push"]}}`,
		`{"id":"h",` + push + `,"expect":{"also_inject":["no-force-push"]}}`,
		`{"id":"i",` + push + `,"expect":{"also_not_inject":["no-force-push"]}}`,
	}, "\n")
	runs := Run(context.Background(), ix, cfg, load(t, jsonl), Options{Runs: 1, Parallel: 3, Today: "2026-11-16"})
	byID := map[string]CaseResult{}
	for _, r := range runs[0] {
		byID[r.ID] = r
	}
	wantPass := map[string]bool{"a": true, "b": true, "c": false, "d": false, "e": false, "f": true, "g": true, "h": true, "i": false}
	for id, w := range wantPass {
		if byID[id].Pass != w {
			t.Errorf("%s: pass = %v, 기대 %v (%+v)", id, byID[id].Pass, w, byID[id])
		}
	}
	if !reflect.DeepEqual(byID["c"].Missing, []string{"doc-edit-rule"}) || !reflect.DeepEqual(byID["c"].Unlabeled, []string{"no-force-push"}) {
		t.Errorf("c = %+v", byID["c"])
	}
	if !reflect.DeepEqual(byID["g"].Unsure, []string{"no-force-push"}) || !reflect.DeepEqual(byID["e"].Wrong, []string{"no-force-push"}) {
		t.Errorf("g=%+v e=%+v", byID["g"], byID["e"])
	}
	if len(byID["a"].Injected) != 1 || byID["a"].Injected[0].Why == "" || byID["a"].Tokens <= 0 || byID["f"].Tokens != 0 {
		t.Errorf("a = %+v", byID["a"])
	}

	m := Summarize(runs, nil).Runs[0].Metrics
	// 전달: inject 가 있는 a b c 중 a b 가 들어감. 필수 대상은 a 뿐. 정밀도: 맞음 a b h = 3, 틀림 d e i = 3.
	// 정답 없음 1(c), 모르는 것 1(g). 침묵: e f 중 f.
	if m.Delivery.N != 3 || m.Delivery.OK != 2 || m.DeliveryMust.N != 1 || m.DeliveryMust.OK != 1 {
		t.Errorf("전달 = %+v / %+v", m.Delivery, m.DeliveryMust)
	}
	p := m.Precision
	if p.Correct != 3 || p.Wrong != 3 || p.Unlabeled != 1 || p.Unsure != 1 || p.Rate == nil || *p.Rate != 0.5 {
		t.Errorf("정밀도 = %+v", p)
	}
	if m.Silence.N != 2 || m.Silence.OK != 1 {
		t.Errorf("침묵 = %+v", m.Silence)
	}
}

// 도구 사례는 도구 이름과 입력으로 동작을 해석하고, action 을 직접 줘도 되며, 도구가 만든 프롬프트는 건너뛴다.
func TestToolInterpretationAndToolMade(t *testing.T) {
	ix, cfg, _ := setup(t, "")
	jsonl := strings.Join([]string{
		`{"id":"bash","moment":{"kind":"tool","tool":{"name":"Bash","input":{"command":"git push --force"}},"scope":["repo/sample-app"]},"expect":{"inject":["no-force-push"]}}`,
		`{"id":"edit","moment":{"kind":"tool","tool":{"name":"Edit","input":{"file_path":"/work/a/README.md"}},"scope":["repo/sample-app"]},"expect":{"inject":["doc-edit-rule"]}}`,
		`{"id":"direct","moment":{"kind":"tool","action":"git.push","scope":["repo/sample-app"]},"expect":{"inject":["no-force-push"]}}`,
		`{"id":"read","moment":{"kind":"tool","tool":{"name":"Read","input":{"file_path":"/work/a/README.md"}},"scope":["repo/sample-app"]},"expect":{"silent":true}}`,
		`{"id":"made","moment":{"kind":"prompt","text":"<task-notification>푸시해 줘","scope":["repo/sample-app"]},"expect":{"silent":true}}`,
		`{"id":"plain","moment":{"kind":"prompt","text":"푸시해 줘","scope":["repo/sample-app"]},"expect":{"inject":["no-force-push"]}}`,
	}, "\n")
	for _, r := range Run(context.Background(), ix, cfg, load(t, jsonl), Options{})[0] {
		if !r.Pass {
			t.Errorf("%s: %+v", r.ID, r)
		}
	}
}

// 판정이 든 사례와 --no-judge, --runs 2 에서 가장 낮은 회차, 합격선.
func TestJudgeNoJudgeAndLowestRun(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "n")
	// 이 항목에 대한 첫 호출만 넣으라고 답한다. 회차마다 답이 달라진다.
	body := `in=$(cat); case "$in" in *"id: commit-message-korean"*) echo x >> "` + counter + `"; n=$(wc -l < "` + counter + `"); if [ "$n" -le 1 ]; then echo '{"inject": true, "reason": "r"}'; else echo '{"inject": false, "reason": "r"}'; fi;; *) echo '{"inject": false, "reason": "무관"}';; esac`
	ix, cfg, _ := setup(t, body)
	cases := load(t, `{"id":"topic","moment":{"kind":"prompt","text":"커밋 메시지는 어떻게 써야 해?"},"expect":{"inject":["commit-message-korean"]}}`)

	runs := Run(context.Background(), ix, cfg, cases, Options{Runs: 2, Parallel: 1})
	if len(runs) != 2 || !runs[0][0].Pass || runs[1][0].Pass || !runs[0][0].Judged || !runs[1][0].Judged {
		t.Fatalf("회차별 = %+v / %+v", runs[0][0], runs[1][0])
	}
	if runs[0][0].Run != 1 || runs[1][0].Run != 2 {
		t.Errorf("회차 번호 = %d %d", runs[0][0].Run, runs[1][0].Run)
	}
	rep := Summarize(runs, nil)
	if *rep.Runs[0].Delivery.Rate != 1 || *rep.Runs[1].Delivery.Rate != 0 || *rep.Lowest.Delivery != 0 {
		t.Errorf("회차별 전달 = %+v %+v 가장 낮음 %v", rep.Runs[0].Delivery, rep.Runs[1].Delivery, *rep.Lowest.Delivery)
	}
	if rep.Runs[0].Timing.WithJudge.N != 1 || rep.Runs[0].Timing.WithoutJudge.N != 0 {
		t.Errorf("시간 나눔 = %+v", rep.Runs[0].Timing)
	}
	half := 0.5
	fails, _ := Check(rep, Thresholds{Delivery: &half})
	if len(fails) == 0 {
		t.Error("가장 낮은 회차가 합격선에 못 미치면 실패여야 함")
	}
	zero := 0.0
	if fails, _ := Check(rep, Thresholds{Delivery: &zero}); len(fails) != 0 {
		t.Errorf("합격선 0 은 통과: %v", fails)
	}

	// --no-judge: 판정 명령을 부르지 않으니 참고 항목은 들어가지 않는다.
	no := Run(context.Background(), ix, cfg, cases, Options{NoJudge: true})[0][0]
	if no.Judged || no.Pass || len(no.Injected) != 0 {
		t.Errorf("no-judge = %+v", no)
	}
	if len(no.Dropped) == 0 || no.Dropped[0].Stage != "판정" {
		t.Errorf("no-judge 탈락 = %+v", no.Dropped)
	}
}

// 병렬로 돌려도 결과는 같다.
func TestParallelSameAsSerial(t *testing.T) {
	ix, cfg, _ := setup(t, judgeYes)
	f, err := os.Open(filepath.Join("testdata", "cases.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cases, skips, err := Load(f)
	if err != nil || len(skips) != 0 || len(cases) != 10 {
		t.Fatalf("%v %v %d", err, skips, len(cases))
	}
	strip := func(rs []CaseResult) []CaseResult {
		for i := range rs {
			rs[i].DurationMS = 0
		}
		return rs
	}
	a := strip(Run(context.Background(), ix, cfg, cases, Options{Parallel: 1})[0])
	b := strip(Run(context.Background(), ix, cfg, cases, Options{Parallel: 8})[0])
	if !reflect.DeepEqual(a, b) {
		t.Error("병렬과 직렬의 결과가 다름")
	}
	for _, r := range a {
		if !r.Pass {
			t.Errorf("testdata 사례 %s 가 맞지 않음: %+v", r.ID, r)
		}
	}
}
