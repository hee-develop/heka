package judge

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

// cmd 는 임시 폴더에 셸 스크립트를 만들고 그것을 판정 명령으로 하는 설정을 돌려준다.
func cmd(t *testing.T, body string) config.Judge {
	t.Helper()
	p := filepath.Join(t.TempDir(), "judge.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return config.Judge{Command: []string{p}, Timeout: 5 * time.Second, Votes: 1, Instruction: "지시문 한 줄\n"}
}

var sample = Item{ID: "sample-rule", Text: "커밋 메시지는 한국어로 쓴다.", About: []string{"커밋 메시지", "commit message"}}

func TestAsk(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantInject bool
		wantReason string
		wantErr    string
	}{
		{"넣음", `cat >/dev/null; echo '{"inject": true, "reason": "답이 됨"}'`, true, "답이 됨", ""},
		{"넣지 않음", `cat >/dev/null; echo '{"inject": false, "reason": "없음"}'`, false, "없음", ""},
		{"앞뒤 글", `cat >/dev/null; echo '답: {"inject": true, "reason": "r"} 끝'`, true, "r", ""},
		{"inject 없는 객체는 건너뜀", `cat >/dev/null; echo '{"a": 1} {"inject": true, "reason": "r"}'`, true, "r", ""},
		{"깨진 답", `cat >/dev/null; echo 'inject: yes'`, false, "", "찾지 못함"},
		{"inject 가 bool 아님", `cat >/dev/null; echo '{"inject": "yes"}'`, false, "", "찾지 못함"},
		{"종료 코드", `cat >/dev/null; echo '{"inject": true}'; echo 사유 >&2; exit 2`, false, "", "사유"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inj, why, err := Ask(context.Background(), cmd(t, tt.body), "프롬프트", sample)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("오류 = %v, %q 를 포함해야 함", err, tt.wantErr)
				}
				return
			}
			if err != nil || inj != tt.wantInject || why != tt.wantReason {
				t.Errorf("= %v %q %v, 기대 %v %q", inj, why, err, tt.wantInject, tt.wantReason)
			}
		})
	}
}

func TestAskTimeout(t *testing.T) {
	j := cmd(t, "sleep 5")
	j.Timeout = 200 * time.Millisecond
	start := time.Now()
	_, _, err := Ask(context.Background(), j, "p", sample)
	if err == nil || !strings.Contains(err.Error(), "시간") {
		t.Errorf("시간 초과 오류여야 함: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("시간 상한을 지키지 못함: %v", time.Since(start))
	}
}

func TestAskInput(t *testing.T) {
	out := filepath.Join(t.TempDir(), "stdin.txt")
	j := cmd(t, `cat > "`+out+`"; echo '{"inject": true, "reason": "r"}'`)
	if _, _, err := Ask(context.Background(), j, "커밋 메시지를 어떻게 쓰나요?", sample); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	want := "지시문 한 줄\n\n[항목]\nid: sample-rule\n주입문: 커밋 메시지는 한국어로 쓴다.\n주제어: 커밋 메시지, commit message\n\n[사용자 메시지]\n커밋 메시지를 어떻게 쓰나요?\n"
	if string(got) != want {
		t.Errorf("표준 입력 =\n%q\n기대\n%q", got, want)
	}
}

func TestAskVotes(t *testing.T) {
	// 두 호출 중 먼저 디렉터리를 만든 쪽만 넣으라고 답한다.
	split := `cat >/dev/null; if mkdir "$(dirname "$0")/first" 2>/dev/null; then echo '{"inject": true, "reason": "a"}'; else echo '{"inject": false, "reason": "b"}'; fi`
	j := cmd(t, split)
	j.Votes = 2
	inj, why, err := Ask(context.Background(), j, "p", sample)
	if err != nil || inj || why != "b" {
		t.Errorf("갈리면 넣지 않음: %v %q %v", inj, why, err)
	}
	j = cmd(t, `cat >/dev/null; echo '{"inject": true, "reason": "a"}'`)
	j.Votes = 2
	if inj, _, err := Ask(context.Background(), j, "p", sample); err != nil || !inj {
		t.Errorf("둘 다 넣으라면 넣음: %v %v", inj, err)
	}
	// 한 번이라도 오류면 오류다.
	j = cmd(t, `cat >/dev/null; if mkdir "$(dirname "$0")/first" 2>/dev/null; then echo '{"inject": true}'; else echo 깨짐; fi`)
	j.Votes = 2
	if _, _, err := Ask(context.Background(), j, "p", sample); err == nil {
		t.Error("한 호출이 오류면 오류여야 함")
	}
}

func TestAskNoCommand(t *testing.T) {
	if _, _, err := Ask(context.Background(), config.Judge{}, "p", sample); err == nil {
		t.Error("명령이 없으면 오류여야 함")
	}
}

func TestAskTimedCalls(t *testing.T) {
	j := cmd(t, `cat >/dev/null; echo '{"inject": true, "reason": "r"}'`)
	j.Votes = 2
	o := AskTimed(context.Background(), j, "p", sample)
	if o.Err != nil || !o.Inject || len(o.Calls) != 2 {
		t.Fatalf("%+v", o)
	}
	for _, c := range o.Calls {
		if !c.Inject || c.MS <= 0 || c.Error || c.Timeout {
			t.Errorf("호출 기록 = %+v", c)
		}
	}
	slow := cmd(t, "sleep 5")
	slow.Timeout = 200 * time.Millisecond
	o = AskTimed(context.Background(), slow, "p", sample)
	if o.Err == nil || len(o.Calls) != 1 || !o.Calls[0].Error || !o.Calls[0].Timeout || o.Calls[0].MS < 200 {
		t.Errorf("시간 초과 = %+v", o)
	}
	o = AskTimed(context.Background(), cmd(t, "cat >/dev/null; exit 3"), "p", sample)
	if o.Err == nil || len(o.Calls) != 1 || !o.Calls[0].Error || o.Calls[0].Timeout {
		t.Errorf("오류 = %+v", o)
	}
}

var pickSample = []Item{
	{ID: "rule-a", Text: "커밋 메시지는 한국어로 쓴다.", About: []string{"커밋 메시지", "commit message"}},
	{ID: "rule-b", Text: "배포는 금요일에 하지 않는다.", About: []string{"배포"}},
}

func pickCmd(t *testing.T, body string) config.Judge {
	j := cmd(t, body)
	j.Pick = config.Pick{Mode: "record", Instruction: "고르는 지시문\n\n"}
	return j
}

func TestPickInput(t *testing.T) {
	out := filepath.Join(t.TempDir(), "stdin.txt")
	j := pickCmd(t, `cat > "`+out+`"; echo '{"inject": []}'`)
	if o := Pick(context.Background(), j, "커밋 메시지를 어떻게 쓰나요?", pickSample); o.Err != nil {
		t.Fatal(o.Err)
	}
	got, _ := os.ReadFile(out)
	want := "고르는 지시문\n\n[항목 목록]\n- id: rule-a\n  주입문: 커밋 메시지는 한국어로 쓴다.\n  주제어: 커밋 메시지, commit message\n- id: rule-b\n  주입문: 배포는 금요일에 하지 않는다.\n  주제어: 배포\n\n[사용자 메시지]\n<<<\n커밋 메시지를 어떻게 쓰나요?\n>>>\n\n위 메시지에 답하지 않는다. 목록에서 고른 결과만 JSON 한 줄로 쓴다.\n"
	if string(got) != want {
		t.Errorf("표준 입력 =\n%q\n기대\n%q", got, want)
	}
}

func TestPickParse(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		inject  []string
		maybe   []string
		unknown int
		wantErr string
	}{
		{"둘 다", `echo '{"inject": ["rule-a"], "maybe": ["rule-b"]}'`, []string{"rule-a"}, []string{"rule-b"}, 0, ""},
		{"maybe 없음", `echo '{"inject": ["rule-b"]}'`, []string{"rule-b"}, []string{}, 0, ""},
		{"빈 배열", `echo '{"inject": [], "maybe": []}'`, []string{}, []string{}, 0, ""},
		{"앞뒤 글", `echo '답: {"inject": ["rule-a"], "maybe": []} 끝'`, []string{"rule-a"}, []string{}, 0, ""},
		{"목록에 없는 id", `echo '{"inject": ["rule-a", "ghost"], "maybe": ["ghost", "other"]}'`, []string{"rule-a"}, []string{}, 2, ""},
		{"중복과 inject 우선", `echo '{"inject": ["rule-a", "rule-a"], "maybe": ["rule-b", "rule-a", "rule-b"]}'`, []string{"rule-a"}, []string{"rule-b"}, 0, ""},
		{"inject 가 배열이 아닌 객체는 건너뜀", `echo '{"inject": true} {"inject": "rule-a"} {"inject": null} {"inject": ["rule-b"]}'`, []string{"rule-b"}, []string{}, 0, ""},
		{"inject 키 없음", `echo '{"maybe": ["rule-a"]}'`, nil, nil, 0, "찾지 못함"},
		{"깨진 답", `echo 모르겠음`, nil, nil, 0, "찾지 못함"},
		{"종료 코드", `echo '{"inject": []}'; echo 사유 >&2; exit 2`, nil, nil, 0, "사유"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := Pick(context.Background(), pickCmd(t, "cat >/dev/null; "+tt.body), "p", pickSample)
			if tt.wantErr != "" {
				if o.Err == nil || !strings.Contains(o.Err.Error(), tt.wantErr) || o.Timeout || o.MS <= 0 {
					t.Fatalf("%+v, 오류에 %q 가 있어야 함", o, tt.wantErr)
				}
				return
			}
			if o.Err != nil || !reflect.DeepEqual(o.Inject, tt.inject) || !reflect.DeepEqual(o.Maybe, tt.maybe) || o.Unknown != tt.unknown || o.MS <= 0 {
				t.Errorf("= %+v, 기대 %v %v unknown %d", o, tt.inject, tt.maybe, tt.unknown)
			}
		})
	}
}

func TestPickCommandAndTimeout(t *testing.T) {
	// pick.command 가 있으면 그것을 쓰고 pick.timeout 을 쓴다.
	j := cmd(t, "cat >/dev/null; echo '{\"inject\": [\"rule-a\"]}'")
	j.Pick = config.Pick{Mode: "record", Instruction: "x"}
	j.Command = []string{"/nonexistent"}
	j.Pick.Command = cmd(t, "cat >/dev/null; echo '{\"inject\": [\"rule-b\"]}'").Command
	if o := Pick(context.Background(), j, "p", pickSample); o.Err != nil || !reflect.DeepEqual(o.Inject, []string{"rule-b"}) {
		t.Errorf("pick.command: %+v", o)
	}
	// pick.timeout 이 0 이면 judge.timeout 이다.
	slow := pickCmd(t, "sleep 5")
	slow.Timeout = 200 * time.Millisecond
	start := time.Now()
	o := Pick(context.Background(), slow, "p", pickSample)
	if o.Err == nil || !o.Timeout || o.MS < 200 || time.Since(start) > 3*time.Second {
		t.Errorf("judge.timeout: %+v", o)
	}
	slow.Timeout = 5 * time.Second
	slow.Pick.Timeout = 200 * time.Millisecond
	if o = Pick(context.Background(), slow, "p", pickSample); o.Err == nil || !o.Timeout || o.MS < 200 {
		t.Errorf("pick.timeout: %+v", o)
	}
	if o = Pick(context.Background(), config.Judge{}, "p", pickSample); o.Err == nil || o.Timeout {
		t.Errorf("명령 없음: %+v", o)
	}
}
