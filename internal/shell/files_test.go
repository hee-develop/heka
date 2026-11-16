package shell

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// 임시 폴더 이름에 시험 이름이 들어가므로 시험 이름에 `$` 같은 셸 글자를 쓰지 않는다.

// filesEnv 는 지어낸 작업 폴더(D)와 홈(H)에 파일을 만들고, 명령 속의 @D·@H 를 그 경로로 바꾼다.
type filesEnv struct {
	t    *testing.T
	d, h string
}

func newFilesEnv(t *testing.T, files map[string]string) *filesEnv {
	t.Helper()
	e := &filesEnv{t: t, d: t.TempDir(), h: t.TempDir()}
	for rel, body := range files {
		p := filepath.Join(e.d, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// sub 는 @D/…, @H/… 꼴 경로를 실제 경로로 바꾼다.
func (e *filesEnv) sub(s string) string {
	return strings.NewReplacer("@D", e.d, "@H", e.h).Replace(s)
}

func (e *filesEnv) files() Files {
	return Files{Cwd: e.d, Home: e.h, Read: os.ReadFile}
}

type fileCase struct {
	name  string
	files map[string]string
	cmd   string
	want  []string
	note  string // 글이 없을 때의 이유
}

func runFileCases(t *testing.T, kind string, cases []fileCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newFilesEnv(t, c.files)
			cmd := e.sub(c.cmd)
			var got Extracted
			if kind == "commit" {
				got = CommitMessagesIn(cmd, e.files())
			} else {
				got = PRBodiesIn(cmd, e.files())
			}
			want := make([]string, len(c.want))
			for i, w := range c.want {
				want[i] = e.sub(w)
			}
			if !(len(got.Texts) == 0 && len(want) == 0) && !reflect.DeepEqual(got.Texts, want) {
				t.Errorf("글 = %q, 기대 %q", got.Texts, want)
			}
			if got.Note != c.note {
				t.Errorf("이유 = %q, 기대 %q", got.Note, c.note)
			}
		})
	}
}

var one = map[string]string{"m.txt": "feat: 파일 메시지\n\n본문\n"}

func TestCommitFromFile(t *testing.T) {
	runFileCases(t, "commit", []fileCase{
		{"절대 경로", one, `git commit -F @D/m.txt`, []string{"feat: 파일 메시지\n\n본문"}, ""},
		{"--file", one, `git commit --file @D/m.txt`, []string{"feat: 파일 메시지\n\n본문"}, ""},
		{"--file=", one, `git commit --file=@D/m.txt`, []string{"feat: 파일 메시지\n\n본문"}, ""},
		{"-qF 묶음", one, `git commit -qF @D/m.txt`, []string{"feat: 파일 메시지\n\n본문"}, ""},
		{"-F 붙여 씀", one, `git commit -F@D/m.txt`, []string{"feat: 파일 메시지\n\n본문"}, ""},
		{"-m 과 -F 는 빈 줄로 이음", one, `git commit -m 앞 -F @D/m.txt`, []string{"앞\n\n" + "feat: 파일 메시지\n\n본문"}, ""},
		{"-F - heredoc 은 그대로", one, "git commit -F - <<'EOF'\nfeat: x\nEOF", []string{"feat: x"}, ""},
		{"값 없는 -F", one, `git commit -F`, nil, NoteBadPath},

		// 변수
		{"변수 앞 달러", one, `B=@D/m.txt && git commit -F $B`, []string{"feat: 파일 메시지\n\n본문"}, ""},
		{"변수 중괄호", one, `B=@D/m.txt; git commit -F ${B}`, []string{"feat: 파일 메시지\n\n본문"}, ""},
		{"변수 뒤에 경로를 이음", one, `S=@D; git commit -F $S/m.txt`, []string{"feat: 파일 메시지\n\n본문"}, ""},
		{"앞선 변수를 가리키는 변수", one, `S=@D; B=$S/m.txt; git commit -F $B`, []string{"feat: 파일 메시지\n\n본문"}, ""},
		{"이어진 대입과 다른 명령", map[string]string{"msg2.txt": "fix: 둘\n", "src/a.go": "x"},
			`S=@D; F=src/a.go; git add -- $F && git commit -q -F $S/msg2.txt && git show --stat`, []string{"fix: 둘"}, ""},
		{"export 대입", one, `export B=@D/m.txt && git commit -F $B`, []string{"feat: 파일 메시지\n\n본문"}, ""},
		{"나중에 다시 대입하면 마지막 값", one, `B=@D/none.txt; B=@D/m.txt; git commit -F $B`, []string{"feat: 파일 메시지\n\n본문"}, ""},
		{"풀 수 없는 변수", one, `git commit -F $NOSUCH/m.txt`, nil, NoteUnresolved},
		{"환경 변수는 보지 않는다", one, `git commit -F $HEKA_TEST_ENV_NAME/m.txt`, nil, NoteUnresolved},
		{"$(…) 값", one, `B=$(mktemp) && git commit -F $B`, nil, NoteUnresolved},
		{"큰따옴표 안의 $(…) 값", one, `B="$(mktemp)" && git commit -F $B`, nil, NoteUnresolved},
		{"백틱 값", one, "B=`mktemp` && git commit -F $B", nil, NoteUnresolved},
		{"값 일부가 $(…)", one, `B=@D/$(date).txt && git commit -F $B`, nil, NoteUnresolved},
		{"풀 수 없는 변수를 가리키는 변수", one, `B=$NOSUCH/m.txt; C=$B; git commit -F $C`, nil, NoteUnresolved},
		{"경로 단어 안의 $(…)", one, `git commit -F $(mktemp)`, nil, NoteBadPath},
		{"${이름:-값} 꼴", one, `B=@D; git commit -F ${B:-x}/m.txt`, nil, NoteUnresolved},
		{"대입 앞에 붙은 접두는 값을 주지 않는다", one, `B=@D/m.txt git commit -F $B`, nil, NoteUnresolved},
		{"+= 는 풀지 않는다", one, `B=@D/m.txt; B+=x; git commit -F $B`, nil, NoteUnresolved},

		// ~ 와 상대 경로
		{"~/", map[string]string{}, `git commit -F ~/m.txt`, nil, NoteUnreadable},
		{"cwd 기준 상대 경로", one, `git commit -F m.txt`, []string{"feat: 파일 메시지\n\n본문"}, ""},
		{"./ 상대 경로", one, `git commit -F ./m.txt`, []string{"feat: 파일 메시지\n\n본문"}, ""},
		{"cd 뒤의 상대 경로", map[string]string{"sub/m.txt": "in sub\n"}, `cd @D/sub && git commit -F m.txt`, []string{"in sub"}, ""},
		{"상대 cd 를 이어 감", map[string]string{"a/b/m.txt": "ab\n"}, `cd a && cd b && git commit -F m.txt`, []string{"ab"}, ""},
		{"cd 의 변수 경로", map[string]string{"sub/m.txt": "in sub\n"}, `W=@D/sub; cd $W; git commit -F m.txt`, []string{"in sub"}, ""},
		{"cd 를 풀 수 없으면 상대 경로는 못 뽑는다", one, `cd $NOSUCH && git commit -F m.txt`, nil, NoteBadPath},
		{"cd 를 풀 수 없어도 절대 경로는 뽑는다", one, `cd $NOSUCH && git commit -F @D/m.txt`, []string{"feat: 파일 메시지\n\n본문"}, ""},
		{"cd - 는 풀지 못한다", one, `cd - && git commit -F m.txt`, nil, NoteBadPath},
		{"git -C 기준", map[string]string{"sub/m.txt": "in sub\n"}, `git -C @D/sub commit -F m.txt`, []string{"in sub"}, ""},
		{"git -C 상대 경로", map[string]string{"sub/m.txt": "in sub\n"}, `git -C sub commit -F m.txt`, []string{"in sub"}, ""},
		{"git -C 를 풀 수 없음", one, `git -C $NOSUCH commit -F m.txt`, nil, NoteUnresolved},

		// 같은 명령에서 그 파일을 만들거나 고침
		{"cat > 경로 heredoc", nil, "cat > @D/n.txt <<'EOF'\nfeat: 새로\nEOF\ngit commit -F @D/n.txt", []string{"feat: 새로"}, ""},
		{"cat <<EOF > 경로", nil, "cat <<EOF > @D/n.txt\nfeat: 새로\nEOF\ngit commit -F @D/n.txt", []string{"feat: 새로"}, ""},
		{"cat >경로 <<-EOF", nil, "cat >@D/n.txt <<-EOF\nfeat: 탭\n\tEOF\ngit commit -F @D/n.txt", []string{"feat: 탭"}, ""},
		{"변수로 쓰고 변수로 읽음", nil, "M=@D/n.txt && cat > $M <<'EOF'\nfeat: 변수\nEOF\ngit commit -q -F $M", []string{"feat: 변수"}, ""},
		{"파일이 이미 있어도 heredoc 이 값", one, "cat > @D/m.txt <<'EOF'\n덮어씀\nEOF\ngit commit -F @D/m.txt", []string{"덮어씀"}, ""},
		{"여러 번이면 마지막", nil, "cat > @D/n.txt <<'EOF'\n처음\nEOF\ncat > @D/n.txt <<'EOF'\n나중\nEOF\ngit commit -F @D/n.txt", []string{"나중"}, ""},
		{"상대 경로로 쓰고 읽음", nil, "cat > n.txt <<'EOF'\nrel\nEOF\ngit commit -F ./n.txt", []string{"rel"}, ""},
		{"cat 쓰기 앞의 언급은 덮어써서 상관없다", one, "rm -f @D/m.txt\ncat > @D/m.txt <<'EOF'\n새것\nEOF\ngit commit -F @D/m.txt", []string{"새것"}, ""},
		{"cat 쓰기 뒤의 언급은 막는다", nil, "cat > @D/n.txt <<'EOF'\nx\nEOF\nsed -i s/x/y/ @D/n.txt\ngit commit -F @D/n.txt", nil, NoteTouched},
		{"덧붙이기(>>)는 고침이다", one, "cat >> @D/m.txt <<'EOF'\n더\nEOF\ngit commit -F @D/m.txt", nil, NoteTouched},
		{"리다이렉션 대상", one, `echo x > @D/m.txt && git commit -F @D/m.txt`, nil, NoteTouched},
		{"다른 명령의 인자(sed -i)", one, `sed -i s/a/b/ @D/m.txt && git commit -F @D/m.txt`, nil, NoteTouched},
		{"다른 명령의 인자(cp)", one, `cp @D/other @D/m.txt && git commit -F @D/m.txt`, nil, NoteTouched},
		{"다른 변수로 같은 경로를 고침", one, `B=@D/m.txt; C=@D/m.txt; echo x > $C; git commit -F $B`, nil, NoteTouched},
		{"변수를 언급하는 구간", one, `B=@D/m.txt && python3 - "$B" <<'EOF'` + "\nprint(1)\nEOF\ngit commit -F $B", nil, NoteTouched},
		{"경로를 만든 변수를 언급하는 구간", one, `S=@D; B=$S/m.txt; rm -rf $S/tmp; git commit -F $B`, nil, NoteTouched},
		{"heredoc 본문이 경로를 언급", one, "python3 - <<'EOF'\nopen('@D/m.txt','w')\nEOF\ngit commit -F @D/m.txt", nil, NoteTouched},
		{"사용 뒤의 언급은 괜찮다", one, `git commit -F @D/m.txt && rm @D/m.txt`, []string{"feat: 파일 메시지\n\n본문"}, ""},
		{"사용 뒤 변수 언급도 괜찮다", one, `B=@D/m.txt; git commit -F $B; rm -f $B`, []string{"feat: 파일 메시지\n\n본문"}, ""},
		{"같은 파일을 읽기만 하는 앞선 사용은 고침이 아니다", one, `git commit -F @D/m.txt && git commit --amend -F @D/m.txt`, []string{"feat: 파일 메시지\n\n본문", "feat: 파일 메시지\n\n본문"}, ""},
		{"다른 파일의 언급은 상관없다", one, `echo x > @D/other.txt && git commit -F @D/m.txt`, []string{"feat: 파일 메시지\n\n본문"}, ""},

		// 읽기 실패
		{"없는 파일", nil, `git commit -F @D/none.txt`, nil, NoteUnreadable},
		{"디렉터리", map[string]string{"dir/x": "x"}, `git commit -F @D/dir`, nil, NoteUnreadable},
		{"크기 초과", map[string]string{"big.txt": strings.Repeat("a", MaxFileSize+1)}, `git commit -F @D/big.txt`, nil, NoteUnreadable},
		{"크기 상한 정확히", map[string]string{"big.txt": strings.Repeat("a", MaxFileSize)}, `git commit -F @D/big.txt`, []string{strings.Repeat("a", MaxFileSize)}, ""},
		{"UTF-8 이 아님", map[string]string{"bad.txt": "\xff\xfe"}, `git commit -F @D/bad.txt`, nil, NoteUnreadable},
		{"글자 패턴 경로는 풀지 않는다", one, `git commit -F @D/*.txt`, nil, NoteBadPath},
		{"하나는 풀리고 하나는 아니면 못 푼 쪽만 뺀다", one, `git commit -F @D/m.txt && git commit -F $NOSUCH`, []string{"feat: 파일 메시지\n\n본문"}, ""},
		{"메시지 없는 commit", one, `git commit --no-edit`, nil, NoteNoTarget},
	})
}

func TestCommitHomeFile(t *testing.T) {
	e := newFilesEnv(t, nil)
	if err := os.WriteFile(filepath.Join(e.h, "m.txt"), []byte("home msg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := CommitMessagesIn(`git commit -F ~/m.txt`, e.files())
	if !reflect.DeepEqual(got.Texts, []string{"home msg"}) {
		t.Errorf("~/ = %+v", got)
	}
	got = CommitMessagesIn(`H=~/m.txt; git commit -F $H`, e.files())
	if !reflect.DeepEqual(got.Texts, []string{"home msg"}) {
		t.Errorf("대입 값의 ~/ = %+v", got)
	}
	f := e.files()
	f.Home = ""
	if got := CommitMessagesIn(`git commit -F ~/m.txt`, f); got.Note != NoteBadPath {
		t.Errorf("홈을 모르면 풀지 못함: %+v", got)
	}
}

func TestFileWithoutReader(t *testing.T) {
	cmd := "cat > /tmp/work/m.txt <<'EOF'\nfeat: 읽기 없이\nEOF\ngit commit -F /tmp/work/m.txt"
	if got := CommitMessagesIn(cmd, Files{}); !reflect.DeepEqual(got.Texts, []string{"feat: 읽기 없이"}) {
		t.Errorf("같은 명령의 cat heredoc 은 읽기 함수 없이도 풀려야 함: %+v", got)
	}
	if got := CommitMessagesIn(`git commit -F /tmp/work/m.txt`, Files{}); got.Note != NoteUnreadable || len(got.Texts) != 0 {
		t.Errorf("읽기 함수가 없으면 읽지 못함: %+v", got)
	}
	if got := CommitMessages(cmd); !reflect.DeepEqual(got, []string{"feat: 읽기 없이"}) {
		t.Errorf("CommitMessages = %q", got)
	}
}

func TestPRBodyFromFile(t *testing.T) {
	body := map[string]string{"b.md": "## 배경\n본문\n"}
	want := []string{"## 배경\n본문"}
	runFileCases(t, "pr", []fileCase{
		{"--body-file", body, `gh pr create --draft --base main --title "제목" --body-file @D/b.md`, want, ""},
		{"--body-file=", body, `gh pr edit 1 --body-file=@D/b.md`, want, ""},
		{"-F", body, `gh pr edit 1 -F @D/b.md`, want, ""},
		{"상대 경로와 cd", map[string]string{"w/b.md": "rel\n"}, `cd @D/w && gh pr create --body-file b.md`, []string{"rel"}, ""},
		{"변수", body, `B=@D/b.md && gh pr edit 12 --body-file $B >/dev/null`, want, ""},
		{"cat heredoc 으로 쓰고 변수로 읽음", nil, "B=@D/b.md && cat > $B <<'EOF'\n## 배경\n본문\nEOF\ngh pr edit 12 --body-file $B >/dev/null", want, ""},
		{"--body-file - heredoc 은 그대로", nil, "gh pr create --body-file - <<'EOF'\nx\nEOF", []string{"x"}, ""},
		{"파일을 고치는 스크립트", body, "B=@D/b.md && gh pr view 12 --json body -q .body > $B && python3 - \"$B\" <<'EOF'\nprint(1)\nEOF\ngh pr edit 12 --body-file $B", nil, NoteTouched},
		{"풀 수 없는 변수", body, `gh pr edit 12 --body-file $B`, nil, NoteUnresolved},
		{"앞선 create 가 읽기만 한다", body, `gh pr create --body-file @D/b.md && gh pr edit 1 --body-file @D/b.md`, []string{"## 배경\n본문", "## 배경\n본문"}, ""},
		{"없는 파일", nil, `gh pr create --body-file @D/none.md`, nil, NoteUnreadable},
		{"크기 초과", map[string]string{"b.md": strings.Repeat("가", MaxFileSize/3+1)}, `gh pr create --body-file @D/b.md`, nil, NoteUnreadable},
		{"본문 없음", nil, `gh pr create --fill`, nil, NoteNoTarget},

		// gh api
		{"gh api -F body=@파일", body, `gh api repos/o/r/pulls -f title=t -F body=@@D/b.md -f head=h`, want, ""},
		{"gh api --field body=@파일", body, `gh api -X PATCH repos/o/r/pulls/3 --field body=@@D/b.md`, want, ""},
		{"gh api 변수", body, `B=@D/b.md; gh api repos/o/r/pulls -F body=@$B`, want, ""},
		{"gh api -f body=@파일 은 글자 그대로의 문자열", body, `gh api repos/o/r/pulls -f body=@@D/b.md`, []string{"@@D/b.md"}, ""},
		{"gh api 없는 파일", nil, `gh api repos/o/r/pulls -F body=@@D/none.md`, nil, NoteUnreadable},
		{"gh api 이슈 댓글은 대상 아님", body, `gh api repos/o/r/issues/1/comments -F body=@@D/b.md`, nil, NoteNoTarget},
		{"gh api stdin", nil, "gh api repos/o/r/pulls -F body=@- <<'EOF'\n본문\nEOF", []string{"본문"}, ""},
	})
}

func TestEnvVarsAreIgnored(t *testing.T) {
	e := newFilesEnv(t, one)
	t.Setenv("HEKA_TEST_ENV_NAME", e.d)
	got := CommitMessagesIn(`git commit -F $HEKA_TEST_ENV_NAME/m.txt`, e.files())
	if len(got.Texts) != 0 || got.Note != NoteUnresolved {
		t.Errorf("환경 변수는 보지 않아야 함: %+v", got)
	}
}

func TestSegmentSubstAndAppends(t *testing.T) {
	segs := SplitCommand("B=$(mktemp) && echo a >> f && echo b > g && echo c >>h")
	if len(segs) != 5 || !segs[0].Subst || segs[1].Subst {
		t.Fatalf("Subst: %+v", segs)
	}
	if !reflect.DeepEqual(segs[2].Appends, []string{"f"}) || !reflect.DeepEqual(segs[4].Appends, []string{"h"}) {
		t.Errorf("Appends: %+v", segs)
	}
	if len(segs[3].Appends) != 0 || !reflect.DeepEqual(segs[3].Redirects, []string{"g"}) {
		t.Errorf("> 는 덧붙이기가 아님: %+v", segs[3])
	}
}
