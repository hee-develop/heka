package selector

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hee-develop/heka/internal/config"
)

// 모델 후보(기록 전용) 갈래의 시험 자료. 이름이 곧 id 다.
var pickItems = map[string]string{
	"p-alpha": u + "text: 배포는 금요일에 하지 않는다.\nabout: [배포 절차]\nscope: [personal]",
	"p-beta":  u + "text: 정규식은 RE2 문법이다.\nabout: [정규식]\nscope: [personal]",
	"p-gamma": u + "text: 오래된 기록 파일은 주 단위로 돌려 쓴다.\nabout: [기록 순환]\nscope: [personal]",
	"p-delta": u + "text: 캐시는 배포 뒤에 비운다.\nabout: [캐시 비움]\nscope: [personal]",
	// 목록 대상이 아닌 항목: 동작 조건, 도구 전용
	"p-act":  u + "text: 배포 전 점검.\nmust: true\nwhen: {action: x.deploy}\nabout: [점검]\nscope: [personal]",
	"p-tool": u + "text: 도구에서만.\nwhen: {moment: tool}\nabout: [도구]\nscope: [personal]",
}

// pickScript 는 두 꼴의 입력을 구분해 답하는 명령이다. 입력에 "[항목 목록]" 이 있으면 고르는 호출이고 pickBody 를 실행한다.
// 아니면 한 건씩 판정이고, yesIDs 의 항목만 넣으라고 답한다. 호출마다 임시 폴더에 pick·judge 파일에 한 줄씩 남긴다.
func pickScript(t *testing.T, pickBody string, yesIDs ...string) (cmd []string, count func(kind string) int) {
	t.Helper()
	var cases []string
	for _, id := range yesIDs {
		cases = append(cases, `*"id: `+id+`"*) echo '{"inject": true, "reason": "맞음"}';;`)
	}
	body := `in=$(cat); d=$(dirname "$0")
case "$in" in
*"[항목 목록]"*) echo p >> "$d/pick"; ` + pickBody + `;;
*) echo j >> "$d/judge"; case "$in" in ` + strings.Join(cases, "\n") + `
*) echo '{"inject": false, "reason": "아님"}';; esac;;
esac`
	cmd = script(t, body)
	dir := filepath.Dir(cmd[0])
	count = func(kind string) int {
		b, err := os.ReadFile(filepath.Join(dir, kind))
		if err != nil {
			return 0
		}
		return strings.Count(string(b), "\n")
	}
	return cmd, count
}

func echoJSON(s string) string { return "echo '" + s + "'" }

// pickEnv 는 기록 갈래를 켠 설정을 만든다.
func pickEnv(t *testing.T, items map[string]string, cmd []string) (cfgFn func() *config.Config, run func(text string) Result) {
	t.Helper()
	ix, cfg := env(t, items, testCfg)
	cfg.Judge.Command = cmd
	cfg.Judge.Candidates = 1
	cfg.Judge.Pick = config.Pick{Mode: "record", Instruction: "목록에서 고른다", Max: 8}
	return func() *config.Config { return cfg }, func(text string) Result { return sel1(t, ix, cfg, Moment{Kind: "prompt", Text: text}) }
}

func pickOf(r Result, id string) *Picked {
	if r.Pick == nil {
		return nil
	}
	for i := range r.Pick.Items {
		if r.Pick.Items[i].ID == id {
			return &r.Pick.Items[i]
		}
	}
	return nil
}

// 시간은 실행마다 달라 견주지 않는다.
func noMS(r Result) Result {
	r.Pick = nil
	r.Judges = append([]Judged(nil), r.Judges...)
	for i := range r.Judges {
		r.Judges[i].MS = 0
	}
	return r
}

const pickAsk = "배포 절차가 뭐야?"

func TestPickRecordKeepsScriptResult(t *testing.T) {
	cmd, _ := pickScript(t, echoJSON(`{"inject": ["p-gamma", "p-alpha"], "maybe": ["p-beta"]}`), "p-alpha", "p-gamma")
	cfgOf, run := pickEnv(t, pickItems, cmd)

	on := run(pickAsk)
	cfgOf().Judge.Pick.Mode = "off"
	off := run(pickAsk)
	if off.Pick != nil {
		t.Fatalf("off 인데 Pick 이 있음: %+v", off.Pick)
	}
	if on.Pick == nil {
		t.Fatal("record 인데 Pick 이 없음")
	}
	if !reflect.DeepEqual(noMS(on), noMS(off)) {
		t.Errorf("지금 방식의 결과가 달라짐:\nrecord %+v\noff    %+v", noMS(on), noMS(off))
	}
	wantIDs(t, on, "p-alpha")
	if len(on.Judges) != 1 || on.Judges[0].ID != "p-alpha" {
		t.Errorf("Judges 에는 지금 방식의 판정만 있어야 함: %+v", on.Judges)
	}
	// 판정 명령이 없을 때도 같다.
	cfgOf().Judge.Pick.Mode = "record"
	cfgOf().Judge.Pick.Command = cmd
	cfgOf().Judge.Command = nil
	on2 := run(pickAsk)
	cfgOf().Judge.Pick.Mode = "off"
	if off2 := run(pickAsk); !reflect.DeepEqual(noMS(on2), noMS(off2)) || on2.Pick == nil {
		t.Errorf("판정 명령 없음: %+v / %+v", noMS(on2), noMS(off2))
	}
}

func TestPickJudgesChosenAndReuses(t *testing.T) {
	cmd, count := pickScript(t, echoJSON(`{"inject": ["p-gamma", "p-alpha", "p-gamma", "nope"], "maybe": ["p-beta", "p-alpha"]}`), "p-alpha", "p-gamma")
	_, run := pickEnv(t, pickItems, cmd)
	r := run(pickAsk)
	p := r.Pick
	if p == nil {
		t.Fatal("Pick 이 없음")
	}
	// 대상은 p-alpha, p-beta, p-gamma, p-delta 넷이다(동작 조건·도구 전용은 아님).
	if p.Listed != 4 || p.ListTokens <= 0 || p.MS <= 0 || p.Error || p.Timeout || p.Unknown != 1 {
		t.Errorf("Pick = %+v", p)
	}
	if len(p.Items) != 3 {
		t.Fatalf("Items = %+v", p.Items)
	}
	g, a, b := pickOf(r, "p-gamma"), pickOf(r, "p-alpha"), pickOf(r, "p-beta")
	if g == nil || !g.Direct || !g.Judged || !g.Inject || g.Reused || g.MS <= 0 {
		t.Errorf("p-gamma(스크립트 후보에 없던 항목) = %+v", g)
	}
	if a == nil || !a.Direct || !a.Judged || !a.Inject || !a.Reused || a.MS != 0 {
		t.Errorf("p-alpha(지금 방식이 판정한 항목) = %+v", a)
	}
	if b == nil || b.Direct || !b.Judged || b.Inject || b.Reused {
		t.Errorf("p-beta = %+v", b)
	}
	// p-alpha 는 지금 방식이 한 번, p-gamma 와 p-beta 는 기록 갈래가 한 번씩 판정한다.
	if got := count("judge"); got != 3 {
		t.Errorf("한 건씩 판정 호출 수 = %d, 기대 3", got)
	}
	if got := count("pick"); got != 1 {
		t.Errorf("고르는 호출 수 = %d, 기대 1", got)
	}
	// 기록 갈래의 판정은 주입에 쓰이지 않는다.
	wantIDs(t, r, "p-alpha")
}

func TestPickMax(t *testing.T) {
	cmd, count := pickScript(t, echoJSON(`{"inject": ["p-gamma", "p-alpha"], "maybe": ["p-beta", "p-delta"]}`), "p-alpha", "p-gamma")
	cfgOf, run := pickEnv(t, pickItems, cmd)
	cfgOf().Judge.Pick.Max = 2
	r := run(pickAsk)
	if r.Pick == nil || len(r.Pick.Items) != 4 {
		t.Fatalf("Pick = %+v", r.Pick)
	}
	for id, judged := range map[string]bool{"p-gamma": true, "p-alpha": true, "p-beta": false, "p-delta": false} {
		if it := pickOf(r, id); it == nil || it.Judged != judged || (!judged && (it.Inject || it.MS != 0 || it.Reused)) {
			t.Errorf("%s = %+v, judged 기대 %v", id, it, judged)
		}
	}
	// p-alpha 는 다시 쓰고 p-gamma 만 새로 판정한다.
	if got := count("judge"); got != 2 {
		t.Errorf("한 건씩 판정 호출 수 = %d, 기대 2", got)
	}
}

func TestPickErrorsStayInRecord(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		timeout time.Duration
		timeo   bool
	}{
		{name: "종료 코드", body: "exit 3"},
		{name: "깨진 답", body: "echo 모르겠음"},
		{name: "inject 가 배열이 아님", body: echoJSON(`{"inject": true, "maybe": []}`)},
		{name: "시간 초과", body: "sleep 5", timeout: 200 * time.Millisecond, timeo: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, _ := pickScript(t, tt.body, "p-alpha")
			cfgOf, run := pickEnv(t, pickItems, cmd)
			cfgOf().Judge.Pick.Timeout = tt.timeout
			r := run(pickAsk) // sel1 은 오류가 나면 시험을 실패시킨다
			if r.Pick == nil || !r.Pick.Error || r.Pick.Timeout != tt.timeo || r.Pick.Listed != 4 || r.Pick.Items == nil || len(r.Pick.Items) != 0 {
				t.Errorf("Pick = %+v", r.Pick)
			}
			wantIDs(t, r, "p-alpha")
		})
	}
}

func TestPickNoTargets(t *testing.T) {
	cmd, count := pickScript(t, echoJSON(`{"inject": []}`))
	_, run := pickEnv(t, map[string]string{"p-act": pickItems["p-act"], "p-tool": pickItems["p-tool"]}, cmd)
	r := run(pickAsk)
	if r.Pick != nil || count("pick") != 0 {
		t.Errorf("대상이 없으면 부르지도 기록하지도 않음: %+v, 호출 %d", r.Pick, count("pick"))
	}
}

func TestPickWithoutScriptCandidates(t *testing.T) {
	// 지금 방식의 후보가 없어 일찍 끝나는 경로에서도, 판정 명령이 없는 경로에서도 기록 갈래가 돈다.
	cmd, _ := pickScript(t, echoJSON(`{"inject": ["p-gamma"]}`), "p-gamma")
	cfgOf, run := pickEnv(t, pickItems, cmd)
	r := run("zzqq")
	if wantIDs(t, r); r.Pick == nil || len(r.Pick.Items) != 1 || !r.Pick.Items[0].Inject || r.Pick.Items[0].Reused {
		t.Errorf("후보 0건: %+v", r.Pick)
	}
	cfgOf().Judge.Pick.Command = cmd
	cfgOf().Judge.Command = nil
	r = run(pickAsk)
	// 판정 명령이 없으면 한 건씩 판정은 오류로 남는다.
	if r.Pick == nil || len(r.Pick.Items) != 1 || !r.Pick.Items[0].Judged || !r.Pick.Items[0].Error || r.Pick.Items[0].Inject {
		t.Errorf("판정 명령 없음: %+v", r.Pick)
	}
	wantDrop(t, r, "p-alpha", "판정")
	// 쓸 명령이 아예 없으면 돌지 않는다.
	cfgOf().Judge.Pick.Command = nil
	if r = run(pickAsk); r.Pick != nil {
		t.Errorf("명령 없음: %+v", r.Pick)
	}
}

func TestPickSubagentStart(t *testing.T) {
	cmd, _ := pickScript(t, echoJSON(`{"inject": ["p-gamma"]}`), "p-gamma")
	ix, cfg := env(t, pickItems, testCfg)
	cfg.Judge.Command = cmd
	cfg.Judge.Pick = config.Pick{Mode: "record", Instruction: "목록에서 고른다", Max: 8}
	r, err := Select(context.Background(), ix, cfg, Moment{Kind: "subagent_start", Text: pickAsk, Today: "2026-11-15"})
	if err != nil || r.Pick == nil || r.Pick.Listed != 4 {
		t.Errorf("%+v %v", r.Pick, err)
	}
}
