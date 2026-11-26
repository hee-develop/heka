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
	if texts["commit_message"][0] != "파일 제목" || len(notes) != 1 || notes["pr_body"] != shell.NoteNoTarget {
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

func TestQuestionTarget(t *testing.T) {
	in := map[string]any{"questions": []any{
		map[string]any{"question": "어느 쪽으로 할까요?", "header": "방식", "options": []any{
			map[string]any{"label": "가 (추천)", "description": "이 도구로는 할 수 없습니다"},
			map[string]any{"label": "나"},
		}},
		map[string]any{"question": "둘째 질문"},
	}}
	got := Targets("AskUserQuestion", in)
	want := "어느 쪽으로 할까요?\n가 (추천)\n이 도구로는 할 수 없습니다\n나\n둘째 질문"
	if len(got["question"]) != 1 || got["question"][0] != want {
		t.Errorf("question = %q", got["question"])
	}
	if got := Targets("AskUserQuestion", map[string]any{}); len(got) != 0 {
		t.Errorf("질문이 없으면 비어 있어야 함: %v", got)
	}
	if got := Targets("AskUserQuestion", map[string]any{"questions": "x"}); len(got) != 0 {
		t.Errorf("모양이 틀리면 비어 있어야 함: %v", got)
	}
	if got := Targets("Bash", map[string]any{"questions": in["questions"], "command": "ls"}); got["question"] != nil {
		t.Errorf("AskUserQuestion 이 아니면 question 이 없어야 함: %v", got)
	}
	if Label("question") != "질문 글" {
		t.Errorf("Label = %q", Label("question"))
	}
}

func TestEvalEvidence(t *testing.T) {
	c := item.Check{Target: "last_message", NotPattern: "할 수 없", Evidence: `(?i)--help|^WebFetch:`, Mode: "log"}
	bad := []string{"gh 로는 업로드할 수 없습니다"}
	ok := Turn{OK: true, Calls: []string{"Read: /a", "Bash: gh pr edit --help"}}
	tests := []struct {
		name         string
		c            item.Check
		texts        []string
		turn         Turn
		want, wantNt string
	}{
		{"증거가 있으면 Fail 이 Pass", c, bad, ok, Pass, "증거: Bash: gh pr edit --help"},
		{"증거가 없으면 Fail 그대로", c, bad, Turn{OK: true, Calls: []string{"Read: /a"}}, Fail, ""},
		{"기록이 비었으면 Fail 그대로", c, bad, Turn{OK: true}, Fail, ""},
		{"기록을 읽지 못하면 Skip(R9)", c, bad, Turn{}, Skip, ""},
		{"원래 Pass 면 그대로", c, []string{"괜찮습니다"}, ok, Pass, ""},
		{"글이 없으면 Skip", c, nil, ok, Skip, ""},
		{"evidence 가 없으면 기록을 안 봄", item.Check{Target: "last_message", NotPattern: "할 수 없", Mode: "log"}, bad, ok, Fail, ""},
		{"evidence 식이 틀리면 Skip", item.Check{Target: "last_message", NotPattern: "할 수 없", Evidence: "(?=x)", Mode: "log"}, bad, ok, Skip, ""},
		{"pattern 과 함께", item.Check{Target: "command", Pattern: "^git", Evidence: "WebSearch:", Mode: "log"}, []string{"ls"}, Turn{OK: true, Calls: []string{"WebSearch: x"}}, Pass, "증거: WebSearch: x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, why, note := EvalEvidence(tt.c, tt.texts, tt.turn)
			if got != tt.want || note != tt.wantNt {
				t.Errorf("= %s, %q, %q; 기대 %s, %q", got, why, note, tt.want, tt.wantNt)
			}
		})
	}
	// 읽지 못했을 때 이유
	if r, why, _ := EvalEvidence(c, bad, Turn{}); r != Skip || why != NoteNoTurnLog {
		t.Errorf("이유: %s %q", r, why)
	}
	if r, why, _ := EvalEvidence(item.Check{Target: "last_message", NotPattern: "할 수 없", Evidence: "(?=x)", Mode: "log"}, bad, ok); r != Skip || why != NoteBadPattern {
		t.Errorf("이유: %s %q", r, why)
	}
	// 증거 글은 앞 120자만 남긴다.
	long := Turn{OK: true, Calls: []string{"Bash: --help " + strings.Repeat("가", 300)}}
	if _, _, note := EvalEvidence(c, bad, long); len([]rune(note)) > len("증거: ")+121 || !strings.HasSuffix(note, "…") {
		t.Errorf("note 가 너무 김: %d", len([]rune(note)))
	}
}
