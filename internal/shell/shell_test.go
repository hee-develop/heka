package shell

import (
	"reflect"
	"testing"
)

func words(segs []Segment) [][]string {
	out := [][]string{}
	for _, s := range segs {
		out = append(out, s.Words)
	}
	return out
}

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want [][]string
	}{
		{"빈", "", [][]string{}},
		{"단순", "git status", [][]string{{"git", "status"}}},
		{"&&", "cd x && git add -A && git push", [][]string{{"cd", "x"}, {"git", "add", "-A"}, {"git", "push"}}},
		{"; || |", "a; b || c | d", [][]string{{"a"}, {"b"}, {"c"}, {"d"}}},
		{"개행", "a\nb", [][]string{{"a"}, {"b"}}},
		{"괄호", "(cd sub && git push)", [][]string{{"cd", "sub"}, {"git", "push"}}},
		{"$( 와 백틱", "echo $(git log) `date`", [][]string{{"echo"}, {"git", "log"}, {"date"}}},
		{"큰따옴표 안 안 나눔", `git commit -m "a && b; c | d"`, [][]string{{"git", "commit", "-m", "a && b; c | d"}}},
		{"작은따옴표", `echo 'a $(b) "c"'`, [][]string{{"echo", `a $(b) "c"`}}},
		{"따옴표 이어붙임", `echo a"b c"d`, [][]string{{"echo", "ab cd"}}},
		{"빈 따옴표 단어", `echo ""`, [][]string{{"echo", ""}}},
		{"역슬래시 이스케이프", `echo a\ b \&\& c`, [][]string{{"echo", "a b", "&&", "c"}}},
		{"큰따옴표 안 이스케이프", `echo "a\"b\$c\n"`, [][]string{{"echo", `a"b$c\n`}}},
		{"줄 이음", "git \\\npush", [][]string{{"git", "push"}}},
		{"2>&1 은 경계 아님", "npm test 2>&1 | tail", [][]string{{"npm", "test"}, {"tail"}}},
		{"& 백그라운드", "sleep 1 & echo x", [][]string{{"sleep", "1"}, {"echo", "x"}}},
		{"&> 는 리다이렉션", "cmd &>out.log", [][]string{{"cmd"}}},
		{"heredoc 따옴표", "cat <<'EOF'\ngit push --force\nEOF\nls", [][]string{{"cat"}, {"ls"}}},
		{"heredoc 무따옴표", "cat <<EOF\nrm -rf ~\nEOF\nls", [][]string{{"cat"}, {"ls"}}},
		{"heredoc -", "cat <<-EOF\n\tgit push -f\n\tEOF\nls", [][]string{{"cat"}, {"ls"}}},
		{"heredoc 구분자 공백", "cat << 'END' > f\nx\nEND\nls", [][]string{{"cat"}, {"ls"}}},
		{"heredoc 두 개", "cat <<A <<B\n1\nA\n2\nB\nls", [][]string{{"cat"}, {"ls"}}},
		{"heredoc 뒤 파이프", "cat <<EOF | grep x\nbody\nEOF\nls", [][]string{{"cat"}, {"grep", "x"}, {"ls"}}},
		{"heredoc 구분자 미종결", "cat <<EOF\nbody", [][]string{{"cat"}}},
		{"here-string 은 heredoc 아님", "cat <<< hi; ls", [][]string{{"cat", "<<<", "hi"}, {"ls"}}},
		{"산술 시프트", "echo $((1<<3)); ls", [][]string{{"echo"}, {"1<<3"}, {"ls"}}},
		{"주석", "ls # git push -f\npwd", [][]string{{"ls"}, {"pwd"}}},
		{"단어 중간 #", "echo a#b", [][]string{{"echo", "a#b"}}},
		{"따옴표 안 #", `echo "# x"`, [][]string{{"echo", "# x"}}},
		{"한글", `git commit -m "한글 메시지"`, [][]string{{"git", "commit", "-m", "한글 메시지"}}},
	}
	for _, c := range cases {
		got := words(SplitCommand(c.cmd))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: SplitCommand(%q)\n got %q\nwant %q", c.name, c.cmd, got, c.want)
		}
	}
}

func TestRedirects(t *testing.T) {
	cases := []struct {
		cmd   string
		words []string
		redir []string
	}{
		{"echo hi > out.txt", []string{"echo", "hi"}, []string{"out.txt"}},
		{"echo hi >out.txt", []string{"echo", "hi"}, []string{"out.txt"}},
		{"echo hi >> log", []string{"echo", "hi"}, []string{"log"}},
		{"cmd 2> err.log", []string{"cmd"}, []string{"err.log"}},
		{"cmd > a 2>&1", []string{"cmd"}, []string{"a"}},
		{"cmd >&2", []string{"cmd"}, []string{}},
		{"cmd &> all", []string{"cmd"}, []string{"all"}},
		{"cmd >| f", []string{"cmd"}, []string{"f"}},
		{`echo ">"`, []string{"echo", ">"}, []string{}},
		{`echo "a" > "my file"`, []string{"echo", "a"}, []string{"my file"}},
		{"> only", []string{}, []string{"only"}},
		{"cat > notes.md <<'EOF'\nx\nEOF", []string{"cat"}, []string{"notes.md"}},
	}
	for _, c := range cases {
		segs := SplitCommand(c.cmd)
		if len(segs) != 1 {
			t.Errorf("%q: 구간 %d개", c.cmd, len(segs))
			continue
		}
		if !reflect.DeepEqual(segs[0].Words, c.words) || !reflect.DeepEqual(segs[0].Redirects, c.redir) {
			t.Errorf("%q: words=%q redirects=%q, 기대 %q %q", c.cmd, segs[0].Words, segs[0].Redirects, c.words, c.redir)
		}
	}
}

func TestStripPrefix(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"git", "push"}, []string{"git", "push"}},
		{[]string{"sudo", "git", "push"}, []string{"git", "push"}},
		{[]string{"sudo", "-u", "root", "-E", "git", "push"}, []string{"git", "push"}},
		{[]string{"sudo", "--", "git"}, []string{"git"}},
		{[]string{"env", "-u", "FOO", "BAR=1", "git", "push"}, []string{"git", "push"}},
		{[]string{"FOO=1", "BAR=2", "git", "push"}, []string{"git", "push"}},
		{[]string{"FOO+=x", "git"}, []string{"git"}},
		{[]string{"time", "npm", "test"}, []string{"npm", "test"}},
		{[]string{"time", "-p", "git", "push"}, []string{"git", "push"}},
		{[]string{"command", "git", "push"}, []string{"git", "push"}},
		{[]string{"nohup", "git", "push"}, []string{"git", "push"}},
		{[]string{"npx", "-y", "vitest"}, []string{"vitest"}},
		{[]string{"/usr/bin/env", "git"}, []string{"git"}},
		{[]string{"sudo", "env", "A=1", "time", "rm", "-rf", "x"}, []string{"rm", "-rf", "x"}},
		{[]string{"{", "git", "push"}, []string{"git", "push"}},
		{[]string{"then", "git", "push"}, []string{"git", "push"}},
		{[]string{"echo", "FOO=1"}, []string{"echo", "FOO=1"}},
		{[]string{"=x", "git"}, []string{"=x", "git"}},
		{[]string{"sudo"}, []string{}},
		{[]string{}, []string{}},
	}
	for _, c := range cases {
		got := StripPrefix(c.in)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("StripPrefix(%q) = %q, 기대 %q", c.in, got, c.want)
		}
	}
}

func TestGitSubcommand(t *testing.T) {
	cases := []struct {
		in   []string
		sub  string
		rest []string
		ok   bool
	}{
		{[]string{"git", "push", "-f"}, "push", []string{"-f"}, true},
		{[]string{"git", "-C", "/tmp/x", "push", "--force"}, "push", []string{"--force"}, true},
		{[]string{"git", "-c", "user.name=a", "commit", "-m", "x"}, "commit", []string{"-m", "x"}, true},
		{[]string{"git", "--git-dir=/a/.git", "--work-tree=/a", "status"}, "status", []string{}, true},
		{[]string{"git", "--git-dir", "/a/.git", "log"}, "log", []string{}, true},
		{[]string{"git", "--no-pager", "-P", "diff"}, "diff", []string{}, true},
		{[]string{"git", "-C", "a", "-c", "k=v", "--no-pager", "reflog", "expire"}, "reflog", []string{"expire"}, true},
		{[]string{"/usr/bin/git", "gc"}, "gc", []string{}, true},
		{[]string{"git"}, "", nil, false},
		{[]string{"git", "-C", "x"}, "", nil, false},
		{[]string{"git", "--version"}, "", nil, false},
		{[]string{"gitk", "push"}, "", nil, false},
		{[]string{"echo", "git", "push"}, "", nil, false},
		{[]string{}, "", nil, false},
	}
	for _, c := range cases {
		sub, rest, ok := GitSubcommand(c.in)
		if c.ok && len(rest) == 0 && len(c.rest) == 0 {
			rest = c.rest
		}
		if sub != c.sub || ok != c.ok || (c.ok && !reflect.DeepEqual(rest, c.rest)) {
			t.Errorf("GitSubcommand(%q) = %q %q %v, 기대 %q %q %v", c.in, sub, rest, ok, c.sub, c.rest, c.ok)
		}
	}
}
