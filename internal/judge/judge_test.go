package judge

import (
	"context"
	"os"
	"path/filepath"
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
