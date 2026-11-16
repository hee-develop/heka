package shell

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// 이 파일은 파일로 넘긴 커밋 메시지·PR 본문(`git commit -F 경로`, `gh pr create --body-file 경로`)을 푼다.
// 경로 단어를 같은 명령 안의 앞선 대입과 cd 로 풀고, 같은 명령이 그 파일을 만들면 그 heredoc 본문을,
// 그렇지 않으면 주입받은 읽기 함수로 파일을 읽는다. 어떤 명령도 실행하지 않는다.
// 훅이 도는 시점의 파일 내용이 실제로 쓰일 내용과 달라질 수 있는 경우는 풀지 않고 건너뛴다.

// MaxFileSize 는 읽을 파일의 크기 상한(바이트)이다.
const MaxFileSize = 256 << 10

// 글을 뽑지 못한 이유다. 원장의 note 로 남으므로 몇 가지로 고정한다.
const (
	NoteNoTarget   = "대상 없음"
	NoteUnresolved = "변수를 풀지 못함"
	NoteBadPath    = "경로를 풀지 못함"
	NoteTouched    = "같은 명령에서 파일을 고침"
	NoteUnreadable = "파일을 읽지 못함"
)

// Files 는 경로를 풀고 파일을 읽는 데 필요한 바깥 사정이다.
// Cwd 는 훅 입력의 작업 폴더, Home 은 홈 디렉터리이고, Read 는 파일 내용을 돌려준다.
// Read 가 없으면 파일은 읽지 못한 것으로 친다(같은 명령이 heredoc 으로 만든 파일은 읽지 않으니 그래도 풀린다).
type Files struct {
	Cwd, Home string
	Read      func(path string) ([]byte, error)
}

// Extracted 는 한 대상(커밋 메시지나 PR 본문)에서 뽑은 글이다.
// 글이 하나도 없으면 Note 에 그 이유가 있다(건너뛴 것이 있으면 그중 첫 이유, 없으면 NoteNoTarget).
type Extracted struct {
	Texts []string
	Note  string
}

func (e *Extracted) skip(note string) {
	if e.Note == "" {
		e.Note = note
	}
}

func (e Extracted) done() Extracted {
	if len(e.Texts) > 0 {
		e.Note = ""
	} else if e.Note == "" {
		e.Note = NoteNoTarget
	}
	return e
}

func firstNote(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// state 는 명령을 앞에서부터 읽으며 쌓는 사정이다: 글자 그대로 알려진 변수와 작업 폴더.
type state struct {
	f     Files
	vars  map[string]string   // 값이 글자 그대로인 변수만 있다. 없으면 풀 수 없는 변수다.
	deps  map[string][]string // 변수가 값을 얻을 때 쓴 다른 변수
	cwd   string
	cwdOK bool
}

func newState(f Files) *state {
	return &state{f: f, vars: map[string]string{}, deps: map[string][]string{}, cwd: f.Cwd, cwdOK: f.Cwd != ""}
}

var varRe = regexp.MustCompile(`\$(?:\{([A-Za-z_][A-Za-z0-9_]*)\}|([A-Za-z_][A-Za-z0-9_]*))`)

// expand 는 단어의 `~/`(맨 앞)와 `$이름`·`${이름}` 을 푼다. 값은 앞선 대입에서만 찾고 환경 변수는 보지 않는다.
// $(…)·백틱·풀 수 없는 변수·그 밖의 `$` 꼴이 있으면 note 를 돌려준다. used 는 쓴 변수 이름(간접으로 쓴 것 포함)이다.
func (st *state) expand(w string) (out string, used []string, note string) {
	if strings.Contains(w, "$(") || strings.Contains(w, "`") {
		return "", nil, NoteUnresolved
	}
	if strings.Contains(varRe.ReplaceAllString(w, ""), "$") {
		return "", nil, NoteUnresolved
	}
	if w == "~" || strings.HasPrefix(w, "~/") {
		if st.f.Home == "" {
			return "", nil, NoteBadPath
		}
		w = st.f.Home + w[1:]
	} else if strings.HasPrefix(w, "~") {
		return "", nil, NoteBadPath // ~이름
	}
	bad := false
	out = varRe.ReplaceAllStringFunc(w, func(m string) string {
		sub := varRe.FindStringSubmatch(m)
		name := sub[1] + sub[2]
		v, ok := st.vars[name]
		if !ok {
			bad = true
			return ""
		}
		used = append(append(used, name), st.deps[name]...)
		return v
	})
	if bad {
		return "", nil, NoteUnresolved
	}
	return out, used, ""
}

// abs 는 단어를 절대 경로로 푼다. 상대 경로는 base 기준이다.
func (st *state) abs(w, base string, baseOK bool) (p string, used []string, note string) {
	if w == "" {
		return "", nil, NoteBadPath
	}
	s, used, note := st.expand(w)
	if note != "" {
		return "", nil, note
	}
	if s == "" || strings.ContainsAny(s, "*?[{") {
		return "", nil, NoteBadPath
	}
	if filepath.IsAbs(s) {
		return filepath.Clean(s), used, ""
	}
	if !baseOK {
		return "", nil, NoteBadPath
	}
	return filepath.Join(base, s), used, ""
}

// dirOf 는 명령이 상대 경로를 풀 기준 디렉터리다. cd 로 옮긴 작업 폴더에 git -C 를 차례로 얹는다.
func (st *state) dirOf(dirs []string) (string, bool, string) {
	base, ok := st.cwd, st.cwdOK
	for _, d := range dirs {
		p, _, note := st.abs(d, base, ok)
		if note != "" {
			return "", false, note
		}
		base, ok = p, true
	}
	return base, ok, ""
}

// apply 는 구간 하나가 변수와 작업 폴더에 미치는 영향을 반영한다.
// 대입만으로 된 구간(`A=1`, `export A=1`)과 cd 만 본다. 앞의 것은 isAssignOnly 가 true 를 돌려준다.
func (st *state) apply(seg Segment) {
	if as := assignWords(seg); as != nil {
		for i, w := range as {
			j := strings.IndexByte(w, '=')
			name, val := w[:j], w[j+1:]
			appendOp := strings.HasSuffix(name, "+")
			name = strings.TrimSuffix(name, "+")
			delete(st.vars, name)
			delete(st.deps, name)
			if appendOp || val == "" || (seg.Subst && i == len(as)-1) {
				continue // 덧붙이기, 빈 값, 바로 뒤에 치환이 붙은 값은 글자 그대로가 아니다
			}
			v, used, note := st.expand(val)
			if note != "" {
				continue
			}
			st.vars[name], st.deps[name] = v, used
		}
		return
	}
	ws := StripPrefix(seg.Words)
	if len(ws) == 0 {
		return
	}
	switch path.Base(ws[0]) {
	case "cd":
		args := ws[1:]
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) != 1 || strings.HasPrefix(args[0], "-") || seg.Subst {
			st.cwdOK = false
			return
		}
		p, _, note := st.abs(args[0], st.cwd, st.cwdOK)
		if note != "" {
			st.cwdOK = false
			return
		}
		st.cwd, st.cwdOK = p, true
	case "pushd", "popd":
		st.cwdOK = false
	}
}

// assignWords 는 구간이 대입만으로 되어 있으면(앞에 export 가 와도 된다) 그 `이름=값` 단어들을 돌려준다. 아니면 nil 이다.
func assignWords(seg Segment) []string {
	ws := seg.Words
	if len(ws) > 1 && ws[0] == "export" {
		ws = ws[1:]
	}
	if len(ws) == 0 {
		return nil
	}
	for _, w := range ws {
		if !isAssign(w) {
			return nil
		}
	}
	return ws
}

// fileOperand 는 구간이 읽는 글 파일의 경로 단어다. idx 는 seg.Words 안의 위치다.
type fileOperand struct {
	idx  int
	val  string
	dirs []string
}

// fileOperands 는 구간이 git commit·gh pr create|edit·gh api 로 파일에서 글을 읽을 때 그 경로 단어들이다.
func fileOperands(seg Segment) []fileOperand {
	ws := StripPrefix(seg.Words)
	off := len(seg.Words) - len(ws)
	var parts []part
	var dirs []string
	base := 0
	if sub, rest, ok := GitSubcommand(ws); ok {
		if sub != "commit" {
			return nil
		}
		parts, dirs, base = commitParts(rest), GitDirs(ws), len(ws)-len(rest)
	} else if len(ws) >= 3 && path.Base(ws[0]) == "gh" && ws[1] == "pr" && (ws[2] == "create" || ws[2] == "edit") {
		parts, base = prParts(ws[3:]), 3
	} else if len(ws) >= 2 && path.Base(ws[0]) == "gh" && ws[1] == "api" {
		if p, ok := ghAPIBody(ws[2:]); ok {
			parts = []part{p}
		}
		base = 2
	}
	var out []fileOperand
	for _, p := range parts {
		if p.kind == partFile {
			out = append(out, fileOperand{idx: off + base + p.at, val: p.val, dirs: dirs})
		}
	}
	return out
}

// refsVar 는 text 가 변수 name 을 `$name`·`${name}` 으로 가리키는가.
func refsVar(text, name string) bool {
	for _, m := range varRe.FindAllStringSubmatch(text, -1) {
		if m[1] == name || m[2] == name {
			return true
		}
	}
	return false
}

// fileText 는 segs[k] 가 경로 단어 word 로 가리키는 글 파일의 내용을 돌려준다.
// dirs 는 그 구간의 `git -C` 값이다. 글을 얻지 못하면 text 가 비고 note 에 이유가 있다.
func fileText(segs []Segment, k int, word string, dirs []string, f Files) (text, note string) {
	// 1) 사용 직전까지의 변수와 작업 폴더로 경로를 푼다.
	st := newState(f)
	for j := 0; j < k; j++ {
		st.apply(segs[j])
	}
	base, baseOK, note := st.dirOf(dirs)
	if note != "" {
		return "", note
	}
	p, used, note := st.abs(word, base, baseOK)
	if note != "" {
		return "", note
	}

	// 2) 사용 앞의 구간을 다시 훑으며, 이 경로를 쓰는 cat heredoc 과 그 밖의 언급을 찾는다.
	st = newState(f)
	var body string
	written, touched := false, false
	for j := 0; j < k; j++ {
		seg := segs[j]
		if assignWords(seg) == nil {
			if b, ok := catWrite(seg, st, p); ok {
				body, written, touched = b, true, false // 통째로 덮어쓰므로 앞의 언급은 상관없다
			} else if mentions(seg, st, word, p, used) {
				touched = true
			}
		}
		st.apply(seg)
	}
	switch {
	case touched:
		return "", NoteTouched
	case written:
		return strings.TrimRight(body, "\r\n"), ""
	}

	// 3) 그 밖에는 파일을 읽는다.
	if f.Read == nil {
		return "", NoteUnreadable
	}
	b, err := f.Read(p)
	if err != nil || len(b) > MaxFileSize || !utf8.Valid(b) {
		return "", NoteUnreadable
	}
	return strings.TrimRight(string(b), "\r\n"), ""
}

// catWrite 는 구간이 `cat > p <<EOF` 꼴(`cat <<EOF > p`, `cat >p <<-EOF` 포함)이면 그 마지막 heredoc 본문을 돌려준다.
// 덧붙이기(>>)나 다른 인자·리다이렉션이 있으면 아니다.
func catWrite(seg Segment, st *state, p string) (string, bool) {
	ws := StripPrefix(seg.Words)
	if len(ws) != 1 || path.Base(ws[0]) != "cat" || len(seg.Redirects) != 1 || len(seg.Appends) != 0 || len(seg.Heredocs) == 0 {
		return "", false
	}
	got, _, note := st.abs(seg.Redirects[0], st.cwd, st.cwdOK)
	if note != "" || got != p {
		return "", false
	}
	return seg.Heredocs[len(seg.Heredocs)-1], true
}

// mentions 는 구간이 그 경로나 그 경로를 푸는 데 쓴 변수를 언급하는가(인자, 리다이렉션 대상, heredoc 본문).
// 같은 경로를 글 파일로 읽기만 하는 인자(`git commit -F p`)는 파일을 고치지 않으므로 언급으로 세지 않는다.
func mentions(seg Segment, st *state, word, p string, used []string) bool {
	reads := map[int]bool{}
	for _, op := range fileOperands(seg) {
		base, ok, note := st.dirOf(op.dirs)
		if note != "" {
			continue
		}
		if q, _, note := st.abs(op.val, base, ok); note == "" && q == p {
			reads[op.idx] = true
		}
	}
	hit := func(text string) bool {
		for _, v := range used {
			if refsVar(text, v) {
				return true
			}
		}
		if (word != "" && strings.Contains(text, word)) || strings.Contains(text, p) {
			return true
		}
		q, _, note := st.abs(text, st.cwd, st.cwdOK)
		return note == "" && q == p
	}
	for i, w := range seg.Words {
		if !reads[i] && hit(w) {
			return true
		}
	}
	for _, r := range seg.Redirects {
		if hit(r) {
			return true
		}
	}
	for _, h := range seg.Heredocs {
		if hit(h) {
			return true
		}
	}
	return false
}
