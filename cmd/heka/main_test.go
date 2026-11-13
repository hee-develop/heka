package main

import (
	"bytes"
	"strings"
	"testing"
)

// run 의 경우(version, 인자 없음, help, 모르는 명령)를 확인한다.
func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // 부분 문자열
		wantStderr string // 부분 문자열
	}{
		{"version", []string{"version"}, 0, version, ""},
		{"인자 없음", nil, 0, "사용법", ""},
		{"help", []string{"help"}, 0, "사용법", ""},
		{"모르는 명령", []string{"nosuch"}, 2, "", "nosuch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run(tt.args, &out, &errb)
			if code != tt.wantCode {
				t.Errorf("종료 코드 = %d, 기대 %d", code, tt.wantCode)
			}
			if !strings.Contains(out.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, %q 를 포함해야 함", out.String(), tt.wantStdout)
			}
			if !strings.Contains(errb.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, %q 를 포함해야 함", errb.String(), tt.wantStderr)
			}
		})
	}
}
