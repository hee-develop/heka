package shell

import (
	"reflect"
	"testing"
)

func TestCommitMessages(t *testing.T) {
	cases := []struct {
		name, cmd string
		want      []string
	}{
		{"-m", `git commit -m "a b"`, []string{"a b"}},
		{"-m 여럿은 빈 줄로 이음", `git commit -m a -m b`, []string{"a\n\nb"}},
		{"-am", `git commit -am "x"`, []string{"x"}},
		{"--message", `git commit --message "x"`, []string{"x"}},
		{"--message=", `git commit --message=x`, []string{"x"}},
		{"-m 붙여 씀", `git commit -mfoo`, []string{"foo"}},
		{"git -C", `git -C sub commit -m x`, []string{"x"}},
		{"git -c 와 -C", `git -c user.name=a -C sub commit -m x`, []string{"x"}},
		{"--amend -m", `git commit --amend -m "고친 메시지"`, []string{"고친 메시지"}},
		{"--amend 만", `git commit --amend --no-edit`, nil},
		{"-C 로 메시지 재사용", `git commit -C HEAD`, nil},
		{"-F 파일", `git commit -F msg.txt`, nil},
		{"-F 파일과 -m", `git commit -m x -F msg.txt`, nil},
		{"-F - heredoc", "git commit -F - <<'EOF'\nfeat: x\n\nbody\nEOF", []string{"feat: x\n\nbody"}},
		{"--file=- heredoc", "git commit --file=- <<EOF\nz\nEOF", []string{"z"}},
		{"-F - 인데 heredoc 없음", `git commit -F -`, nil},
		{"$(cat <<EOF)", "git commit -m \"$(cat <<'EOF'\nfeat: a \"q\" (p)\n\nb\nEOF\n)\"", []string{"feat: a \"q\" (p)\n\nb"}},
		{"$(cat <<-EOF)", "git commit -m \"$(cat <<-EOF\n\tx\n\tEOF\n)\"", []string{"\tx"}},
		{"&& 로 이은 명령", `cd sub && git add -A && git commit -m "x y" && git push`, []string{"x y"}},
		{"commit 이 둘", `git commit -m a && git commit -m b`, []string{"a", "b"}},
		{"접두", `GIT_AUTHOR_NAME=a sudo -u me git commit -m x`, []string{"x"}},
		{"commit 이 아님", `git log -m x`, nil},
		{"heredoc 본문 속 commit 은 무시", "cat <<'EOF'\ngit commit -m fake\nEOF", nil},
		{"메시지 없음", `git commit`, nil},
	}
	for _, c := range cases {
		if got := CommitMessages(c.cmd); !reflect.DeepEqual(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
			t.Errorf("%s: %q → %q, 기대 %q", c.name, c.cmd, got, c.want)
		}
	}
}

func TestPRBodies(t *testing.T) {
	cases := []struct {
		name, cmd string
		want      []string
	}{
		{"--body", `gh pr create --title t --body "본문"`, []string{"본문"}},
		{"-b", `gh pr create -b 본문`, []string{"본문"}},
		{"--body=", `gh pr edit 1 --body=본문`, []string{"본문"}},
		{"gh pr edit", `gh pr edit 7 --title t -b "고친 본문"`, []string{"고친 본문"}},
		{"--body-file 파일", `gh pr create --body-file body.md`, nil},
		{"-F 파일", `gh pr edit 1 -F body.md`, nil},
		{"--body-file - heredoc", "gh pr create --title t --body-file - <<'EOF'\n## 요약\n- a\nEOF", []string{"## 요약\n- a"}},
		{"--body-file=- heredoc", "gh pr create --body-file=- <<EOF\nx\nEOF", []string{"x"}},
		{"--body-file - 인데 heredoc 없음", `gh pr create --body-file -`, nil},
		{"본문 없음", `gh pr create --fill`, nil},
		{"heredoc 치환", "gh pr create --body \"$(cat <<'EOF'\n## 요약\n- a\nEOF\n)\"", []string{"## 요약\n- a"}},
		{"&& 로 이은 명령", `git push -u origin feat && gh pr create --title t --body x`, []string{"x"}},
		{"gh pr view 는 대상 아님", `gh pr view 1 --body x`, nil},
		{"gh api 생성", `gh api repos/o/r/pulls -f title=t -f body=본문 -f head=h -f base=main`, []string{"본문"}},
		{"gh api -F", `gh api repos/o/r/pulls -F body=본문 -f head=h`, []string{"본문"}},
		{"gh api --raw-field", `gh api repos/o/r/pulls --raw-field body=본문`, []string{"본문"}},
		{"gh api 붙여 씀", `gh api repos/o/r/pulls -fbody=본문`, []string{"본문"}},
		{"gh api 수정", `gh api -X PATCH repos/o/r/pulls/12 -f body=고침`, []string{"고침"}},
		{"gh api 수정은 PATCH 여야", `gh api repos/o/r/pulls/12 -f body=고침 -X GET`, nil},
		{"gh api 파일", `gh api repos/o/r/pulls -F body=@body.md`, nil},
		{"gh api stdin heredoc", "gh api repos/o/r/pulls -F body=@- <<'EOF'\n본문\nEOF", []string{"본문"}},
		{"gh api 이슈 댓글은 대상 아님", `gh api repos/o/r/issues/1/comments -f body=댓글`, nil},
		{"gh api body 없음", `gh api repos/o/r/pulls -f title=t`, nil},
	}
	for _, c := range cases {
		if got := PRBodies(c.cmd); !reflect.DeepEqual(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
			t.Errorf("%s: %q → %q, 기대 %q", c.name, c.cmd, got, c.want)
		}
	}
}
