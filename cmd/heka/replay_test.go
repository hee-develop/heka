package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hee-develop/heka/internal/replay"
)

const replayTestdata = "../../internal/replay/testdata"

// replayHome 은 testdata 의 항목과 설정으로 데이터 폴더를 만든다. judge 가 있으면 그 셸 본문을 판정 명령으로 붙인다.
func replayHome(t *testing.T, judge string) string {
	t.Helper()
	home := newHome(t)
	items, _ := filepath.Glob(filepath.Join(replayTestdata, "items", "*.md"))
	if len(items) == 0 {
		t.Fatal("testdata 항목이 없음")
	}
	for _, p := range items {
		b, _ := os.ReadFile(p)
		dst := filepath.Join(home, "items", filepath.Base(p))
		os.MkdirAll(filepath.Dir(dst), 0o755)
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, _ := os.ReadFile(filepath.Join(replayTestdata, "config.yaml"))
	if judge != "" {
		sh := filepath.Join(t.TempDir(), "judge.sh")
		os.WriteFile(sh, []byte("#!/bin/sh\n"+judge+"\n"), 0o755)
		cfg = append(cfg, []byte("judge:\n  command: ["+sh+"]\n  instruction: 지시문\n  timeout: 5s\n")...)
	}
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

const replayJudge = `in=$(cat); case "$in" in *"id: commit-message-korean"*) echo '{"inject": true, "reason": "답이 됨"}';; *) echo '{"inject": false, "reason": "무관"}';; esac`

func TestReplayCorpus(t *testing.T) {
	home := replayHome(t, replayJudge)
	cases := filepath.Join(replayTestdata, "cases.jsonl")
	out := filepath.Join(t.TempDir(), "out.jsonl")
	code, stdout, errb := runCmd("replay", cases, "--json", "--out", out, "--min-delivery", "1", "--min-precision", "1", "--min-silence", "1")
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, stdout, errb)
	}
	var rep replay.Report
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	r := rep.Runs[0]
	if rep.Cases != 10 || rep.Skipped != 0 || *r.Delivery.Rate != 1 || r.Delivery.N != 6 || r.DeliveryMust.N != 5 || r.Silence.N != 3 || *r.Silence.Rate != 1 || *r.Precision.Rate != 1 {
		t.Errorf("보고 = %+v", r)
	}
	if r.Groups["confirmed=true"].Cases != 8 || r.Groups["confirmed=false"].Cases != 2 || r.Groups["source=user"].Cases != 8 || r.Groups["source=ai"].Cases != 2 {
		t.Errorf("묶음 = %+v", r.Groups)
	}
	if r.Timing.WithJudge.N == 0 || r.Timing.WithJudge.N+r.Timing.WithoutJudge.N != 10 || r.Tokens.Max <= 0 || r.Tokens.Mean <= 0 {
		t.Errorf("시간·토큰 = %+v %+v", r.Timing, r.Tokens)
	}

	// 사람이 읽는 요약
	_, text, _ := runCmd("replay", cases)
	for _, want := range []string{"사례 10건, 1회", "전달: 6/6 (100.0%)", "침묵: 3/3 (100.0%)", "정밀도:", "걸린 시간(판정 있음)", "넣은 글의 토큰"} {
		if !strings.Contains(text, want) {
			t.Errorf("요약에 %q 가 없음:\n%s", want, text)
		}
	}

	// --out: 사례당 한 줄, 넣은 항목과 이유·탈락·맞았는지
	f, _ := os.Open(out)
	defer f.Close()
	var lines []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, m)
	}
	if len(lines) != 10 || lines[0]["id"] != "c1-prompt-push" || lines[0]["pass"] != true || lines[0]["run"] != float64(1) {
		t.Fatalf("--out = %d줄 %v", len(lines), lines[0])
	}
	for _, k := range []string{"injected", "dropped", "missing", "wrong", "unsure", "unlabeled", "judged", "duration_ms", "tokens"} {
		if _, ok := lines[0][k]; !ok {
			t.Errorf("--out 줄에 %q 가 없음", k)
		}
	}
	inj := lines[0]["injected"].([]any)[0].(map[string]any)
	if inj["id"] != "no-force-push" || inj["why"] == "" {
		t.Errorf("넣은 항목 = %v", inj)
	}

	// 세션 상태와 원장을 쓰지 않는다.
	for _, d := range []string{"ledger", "sessions"} {
		if _, err := os.Stat(filepath.Join(home, d)); err == nil {
			t.Errorf("%s 폴더가 생김", d)
		}
	}
}

func TestReplayThresholdsExitCode(t *testing.T) {
	replayHome(t, replayJudge)
	dir := t.TempDir()
	// 넣어야 하는 항목이 없는 사례 하나와, 침묵해야 하는데 넣는 사례 하나.
	p := filepath.Join(dir, "bad.jsonl")
	body := `{"id":"x1","moment":{"kind":"prompt","text":"푸시해 줘","scope":["repo/sample-app"]},"expect":{"inject":["no-force-push","없는-항목"]}}
{"id":"x2","moment":{"kind":"prompt","text":"푸시해 줘","scope":["repo/sample-app"]},"expect":{"silent":true}}
`
	os.WriteFile(p, []byte(body), 0o644)
	if code, _, _ := runCmd("replay", p, "--no-judge"); code != 0 {
		t.Errorf("합격선이 없으면 재기만 하고 0: %d", code)
	}
	for _, flag := range []string{"--min-delivery", "--min-precision", "--min-silence"} {
		code, _, errb := runCmd("replay", p, "--no-judge", flag, "0.9")
		if code != 1 || !strings.Contains(errb, "합격선 미달") {
			t.Errorf("%s: 종료 코드 %d, %q", flag, code, errb)
		}
		if code, _, _ := runCmd("replay", p, "--no-judge", flag, "0"); code != 0 {
			t.Errorf("%s 0 은 통과: %d", flag, code)
		}
		if code, _, _ := runCmd("replay", p, flag, "1.5"); code != 2 {
			t.Errorf("%s 1.5 는 사용법 오류: %d", flag, code)
		}
	}
	// 사용법 오류
	if code, _, _ := runCmd("replay"); code != 2 {
		t.Errorf("파일 없음: %d", code)
	}
	if code, _, _ := runCmd("replay", p, "--runs", "0"); code != 2 {
		t.Errorf("--runs 0: %d", code)
	}
}

func TestReplayBrokenLinesAndRuns(t *testing.T) {
	replayHome(t, "")
	p := filepath.Join(t.TempDir(), "c.jsonl")
	body := "{깨짐\n" +
		`{"id":"ok","moment":{"kind":"prompt","text":"푸시해 줘","scope":["repo/sample-app"]},"expect":{"inject":["no-force-push"]}}` + "\n" +
		`{"id":"x","moment":{"kind":"nope"}}` + "\n"
	os.WriteFile(p, []byte(body), 0o644)
	code, out, errb := runCmd("replay", p, "--json", "--runs", "2")
	if code != 0 || !strings.Contains(errb, "건너뛴 줄 2건") {
		t.Fatalf("%d %q", code, errb)
	}
	var rep replay.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Cases != 1 || rep.Skipped != 2 || len(rep.SkippedLines) != 2 || len(rep.Runs) != 2 || rep.Runs[1].Run != 2 {
		t.Errorf("보고 = %+v", rep)
	}
	_, text, _ := runCmd("replay", p)
	if !strings.Contains(text, "읽지 못해 건너뛴 줄 2건") {
		t.Errorf("요약에 건너뛴 수가 없음: %s", text)
	}
	// 읽을 수 있는 사례가 없으면 실패
	os.WriteFile(p, []byte("{깨짐\n"), 0o644)
	if code, _, _ := runCmd("replay", p); code != 1 {
		t.Errorf("사례 없음: %d", code)
	}
}
