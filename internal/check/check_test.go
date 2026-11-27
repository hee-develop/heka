package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hee-develop/heka/internal/item"
	"github.com/hee-develop/heka/internal/shell"
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

func TestExtractFromFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "m.txt"), []byte("파일 제목\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := shell.Files{Cwd: dir, Read: ReadFile}
	texts, notes := Extract("Bash", map[string]any{"command": `git commit -F m.txt`}, f)
	if texts["commit_message"][0] != "파일 제목" || len(notes) != 2 || notes["pr_body"] != shell.NoteNoTarget || notes["review_body"] != shell.NoteNoTarget {
		t.Errorf("Extract = %v, %v", texts, notes)
	}
	texts, notes = Extract("Bash", map[string]any{"command": `git commit -F none.txt && gh pr edit 1 --body-file $X`}, f)
	if _, ok := texts["commit_message"]; ok || notes["commit_message"] != shell.NoteUnreadable || notes["pr_body"] != shell.NoteUnresolved {
		t.Errorf("Extract = %v, %v", texts, notes)
	}
	if _, notes := Extract("Write", nil, f); len(notes) != 0 {
		t.Errorf("Bash 가 아니면 이유도 없음: %v", notes)
	}
}

// 리뷰·코멘트 본문은 글마다 판정하고, 본문 없는 승인의 빈 글도 판정한다.
func TestReviewBodyCheck(t *testing.T) {
	c := item.Check{Target: "review_body", Pattern: "^\\s*(`p[1-5]`\\s+)?from Claude: ", Mode: "block"}
	eval := func(cmd string) string {
		texts, _ := Extract("Bash", map[string]any{"command": cmd}, shell.Files{})
		got, _ := Eval(c, texts["review_body"])
		return got
	}
	cases := []struct {
		name, cmd, want string
	}{
		{"맞는 본문", `gh pr comment 3 --body "from Claude: LGTM"`, Pass},
		{"우선순위 표시", "gh api repos/o/r/pulls/1/comments/2/replies -f body='`p3` from Claude: 고쳐 주세요'", Pass},
		{"앞에 없음", `gh pr review 3 --comment --body "LGTM"`, Fail},
		{"끝에 붙음", `gh pr comment 3 --body "확인했습니다 from Claude:"`, Fail},
		{"본문 없는 승인", `gh pr review 3 --approve`, Fail},
		{"둘 중 하나만 맞음", `gh api repos/o/r/pulls/1/comments/2/replies -f body='from Claude: a' && gh api repos/o/r/pulls/1/comments/3/replies -f body=b`, Fail},
		{"둘 다 맞음", `gh api repos/o/r/pulls/1/comments/2/replies -f body='from Claude: a' && gh pr comment 1 -b 'from Claude: b'`, Pass},
		{"본문 없는 코멘트 리뷰는 판정하지 않음", `gh pr review 3 --comment`, Skip},
	}
	for _, tc := range cases {
		if got := eval(tc.cmd); got != tc.want {
			t.Errorf("%s: %s, 기대 %s", tc.name, got, tc.want)
		}
	}
	if r, _ := Eval(c, []string{""}); r != Fail {
		t.Errorf("빈 글도 판정해야 함: %s", r)
	}
	_, why := Eval(c, []string{"LGTM"})
	if !strings.Contains(why, "리뷰·코멘트 본문") {
		t.Errorf("이유 = %q", why)
	}
}

func TestReadFileLimits(t *testing.T) {
	dir := t.TempDir()
	put := func(name string, n int) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(strings.Repeat("a", n)), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if b, err := ReadFile(put("ok", shell.MaxFileSize)); err != nil || len(b) != shell.MaxFileSize {
		t.Errorf("상한 크기는 읽어야 함: %d %v", len(b), err)
	}
	if _, err := ReadFile(put("big", shell.MaxFileSize+1)); err == nil {
		t.Error("상한을 넘으면 읽지 않아야 함")
	}
	if _, err := ReadFile(dir); err == nil {
		t.Error("디렉터리는 읽지 않아야 함")
	}
	if _, err := ReadFile(filepath.Join(dir, "none")); err == nil {
		t.Error("없는 파일은 오류")
	}
}

func TestEvalSkipNote(t *testing.T) {
	if r, why := Eval(item.Check{Target: "command", Pattern: "x"}, nil); r != Skip || why != shell.NoteNoTarget {
		t.Errorf("글 없음: %s %q", r, why)
	}
	if r, why := Eval(item.Check{Target: "command", Pattern: "("}, []string{"x"}); r != Skip || why != NoteBadPattern {
		t.Errorf("틀린 식: %s %q", r, why)
	}
}
