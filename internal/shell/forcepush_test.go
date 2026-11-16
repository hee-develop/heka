package shell

import "testing"

func TestForcePush(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		{"push --force", "git push --force", true},
		{"push -f origin main", "git push -f origin main", true},
		{"-C dir push --force", "git -C /tmp/x push --force", true},
		{"sudo push -f", "sudo git push -f", true},
		{"sudo -u 옵션", "sudo -u root git push -f", true},
		{"VAR= 접두", "FOO=1 git push --force", true},
		{"env 접두", "env -u X FOO=1 git push --force", true},
		{"cd && push", "cd repo && git push --force", true},
		{"+refspec", "git push origin +main", true},
		{"묶음 -uf", "git push -uf origin x", true},
		{"force 와 lease 혼용", "git push --force-with-lease --force origin x", true},
		{"절대경로 git", "/usr/bin/git push --force", true},
		{"-c 와 -C 전역옵션", "git -c user.name=a -C /x --no-pager push -f", true},
		{"--git-dir= 전역옵션", "git --git-dir=/x/.git push -f", true},
		{"time 접두", "time git push -f", true},
		{"subshell", "(cd x && git push -f)", true},
		{"$( 치환", "echo $(git push -f)", true},
		{"백틱", "echo `git push -f`", true},
		{"파이프 뒤", "echo y | git push -f", true},
		{"if then", "if true; then git push -f; fi", true},
		{"세미콜론 뒤", "true; git push --force", true},
		{"|| 뒤", "false || git push --force", true},
		{"개행 두 번째 줄", "echo hi\ngit push --force origin x", true},
		{"heredoc 뒤 줄", "cat <<EOF\nhello\nEOF\ngit push -f", true},
		{"bash -c", `bash -c "git push --force"`, true},
		{"sh -c 묶음 -lc", `sh -lc 'git push -f'`, true},
		{"zsh -c", `zsh -c "cd x && git push -f"`, true},
		{"eval", `eval "git push --force"`, true},
		{"중첩 bash -c 깊이3", `bash -c "bash -c 'bash -c \"git push -f\"'"`, true},
		{"sudo bash -c", `sudo bash -c "git push -f"`, true},
		{"산술 시프트는 heredoc 아님", "echo $((1<<3)); git push --force", true},
		{"git push", "git push", false},
		{"lease", "git push --force-with-lease", true},
		{"lease 값", "git push --force-with-lease=main origin main", true},
		{"lease+includes", "git push --force-with-lease --force-if-includes origin x", true},
		{"echo 인자", `echo "git push --force"`, false},
		{"commit -m 문구", `git commit -m "a && git push -f"`, false},
		{"push -o 값 +", "git push -o +x origin main", false},
		{"push --push-option 값 +", "git push --push-option +x origin main", false},
		{"주석", "ls # git push --force", false},
		{"깊이 4 초과는 검사 안 함", `bash -c "bash -c 'bash -c \"bash -c \\\"git push -f\\\"\"'"`, false},
		{"bash 스크립트 실행", "bash ./run.sh -c", false},
		{"git status", "git status", false},
		{"if-includes", "git -C x push --force-if-includes", true},
		{"git -C x push lease", "git -C x push --force-with-lease", true},
	}
	for _, c := range cases {
		if got := ForcePush(c.cmd); got != c.want {
			t.Errorf("%s: %q -> %v, 기대 %v", c.name, c.cmd, got, c.want)
		}
	}
}
