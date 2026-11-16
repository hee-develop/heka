package check

import (
	"strings"
	"testing"

	"github.com/hee-develop/heka/internal/item"
)

func TestTargets(t *testing.T) {
	got := Targets("Bash", map[string]any{"command": `git commit -m "첫 줄"`})
	if got["command"][0] != `git commit -m "첫 줄"` || got["commit_message"][0] != "첫 줄" || got["pr_body"] != nil {
		t.Errorf("Targets = %v", got)
	}
	if got := Targets("Write", map[string]any{"command": "x"}); len(got) != 0 {
		t.Errorf("Bash 가 아니면 비어 있어야 함: %v", got)
	}
	if got := Targets("Bash", map[string]any{"command": 3}); len(got) != 0 {
		t.Errorf("명령이 글이 아니면 비어 있어야 함: %v", got)
	}
	got = Targets("Bash", map[string]any{"command": `git commit -F msg.txt`})
	if _, ok := got["commit_message"]; ok {
		t.Errorf("파일로 넘기면 뽑지 못함: %v", got)
	}
}

func TestEval(t *testing.T) {
	first50 := item.Check{Target: "commit_message", Pattern: `^.{1,50}(\n|$)`, Mode: "block"}
	long := strings.Repeat("가", 51)
	cases := []struct {
		name  string
		c     item.Check
		texts []string
		want  string
	}{
		{"pattern 통과", first50, []string{"짧은 제목\n\n본문"}, Pass},
		{"pattern 실패", first50, []string{long + "\n본문"}, Fail},
		{"여러 글 중 하나가 실패", first50, []string{"짧다", long}, Fail},
		{"not_pattern 통과", item.Check{Target: "command", NotPattern: `--no-verify`}, []string{"git commit -m x"}, Pass},
		{"not_pattern 실패", item.Check{Target: "command", NotPattern: `--no-verify`}, []string{"git commit --no-verify -m x"}, Fail},
		{"글 없음", first50, nil, Skip},
		{"틀린 pattern", item.Check{Target: "command", Pattern: `(`}, []string{"x"}, Skip},
		{"틀린 not_pattern", item.Check{Target: "command", NotPattern: `[`}, []string{"x"}, Skip},
		{"식 없음", item.Check{Target: "command"}, []string{"x"}, Skip},
	}
	for _, c := range cases {
		if got, _ := Eval(c.c, c.texts); got != c.want {
			t.Errorf("%s: %s, 기대 %s", c.name, got, c.want)
		}
	}
	_, why := Eval(first50, []string{long})
	if !strings.Contains(why, "커밋 메시지") || !strings.Contains(why, "pattern") {
		t.Errorf("이유 = %q", why)
	}
	_, why = Eval(item.Check{Target: "last_message", NotPattern: "TODO"}, []string{"TODO 남김"})
	if !strings.Contains(why, "마지막 응답") || !strings.Contains(why, "not_pattern") {
		t.Errorf("이유 = %q", why)
	}
}
