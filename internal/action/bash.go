package action

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/shell"
)

// maxDepth 는 bash -c·eval 문자열을 안으로 따라 읽는 최대 깊이다.
const maxDepth = 3

// bash 는 Bash 명령의 동작 이름과 쓰기 대상 경로를 돌려준다. 동작이 하나도 없으면 bash.other 다.
// 파일에 쓰는 명령(리다이렉션, tee, sed -i, cp, mv)은 대상의 확장자에 따라 code.edit 나 doc.edit 도 내며,
// 이때는 다른 동작이 없어도 bash.other 가 아니다.
// cfg 의 tool_actions 에 있는 "Bash:<명령 이름>" 키도 같은 구간 자리에서 동작을 낸다.
func bash(cmd, cwd string, cfg *config.Config) (actions, paths []string) {
	st := &scan{cfg: cfg, cwd: cwd, home: os.Getenv("HOME"), tmp: os.Getenv("TMPDIR")}
	st.run(cmd, 0)
	actions = st.actions
	for _, p := range st.paths {
		actions = append(actions, editAction(p))
	}
	if len(actions) == 0 {
		actions = []string{actBashOther}
	}
	return actions, st.paths
}

type scan struct {
	cfg            *config.Config
	cwd, home, tmp string
	actions, paths []string
}

func (s *scan) add(a string) {
	if !slices.Contains(s.actions, a) {
		s.actions = append(s.actions, a)
	}
}

func (s *scan) addPath(raw string) {
	if p, ok := s.writePath(raw); ok && !slices.Contains(s.paths, p) {
		s.paths = append(s.paths, p)
	}
}

func (s *scan) run(cmd string, depth int) {
	if depth > maxDepth {
		return
	}
	for _, seg := range shell.SplitCommand(cmd) {
		for _, r := range seg.Redirects {
			s.addPath(r)
		}
		ws := shell.StripPrefix(seg.Words)
		if len(ws) == 0 {
			continue
		}
		args := ws[1:]
		at := func(i int) string {
			if i < len(args) {
				return args[i]
			}
			return ""
		}
		w1, w2 := at(0), at(1)
		name := path.Base(ws[0])
		switch {
		case name == "git":
			s.git(ws)
		case name == "gh" && w1 == "pr":
			switch w2 {
			case "create", "merge", "review":
				s.add("pr." + w2)
			case "edit":
				if !slices.Contains(args, "--help") && !slices.Contains(args, "-h") {
					s.add("pr.edit")
				}
			}
		case name == "gh" && w1 == "api":
			s.ghAPI(args[1:])
		case name == "vitest" || name == "jest":
			s.add("test.run")
		case name == "npm" || name == "yarn" || name == "pnpm" || name == "bun":
			s.pkgManager(name, w1, w2)
		case name == "gws":
			s.gws(args)
		case name == "rm":
			s.rm(args)
		case name == "tee":
			for _, a := range args {
				if !strings.HasPrefix(a, "-") {
					s.addPath(a)
				}
			}
		case name == "sed":
			for _, a := range sedTargets(args) {
				s.addPath(a)
			}
		case name == "cp" || name == "mv":
			for _, a := range s.cpmvTargets(args) {
				s.addPath(a)
			}
		case slices.Contains([]string{"bash", "sh", "zsh", "dash", "ksh", "ash"}, name):
			if inner, ok := shell.ShellDashC(args); ok {
				s.run(inner, depth+1)
			}
		case name == "eval":
			s.run(strings.Join(args, " "), depth+1)
		}
		if s.cfg != nil {
			for _, k := range cmdKeys(name, args) {
				for _, a := range s.cfg.ToolActionsFor("Bash:" + k) {
					s.add(a)
				}
			}
		}
	}
}

// ghValueOpts 는 gh api 에서 값을 따로 받는 옵션이다. 엔드포인트를 찾을 때 값을 건너뛰는 데 쓴다.
var ghValueOpts = []string{"-H", "--header", "-q", "--jq", "-t", "--template", "--hostname", "--cache", "-p", "--preview", "--input"}

// ghFieldOpts 는 값이 있으면 요청 본문이 되는(메서드를 정하지 않으면 POST 로 가는) 옵션이다.
var ghFieldOpts = []string{"-f", "-F", "--field", "--raw-field", "--input"}

var ghPullNumRe = regexp.MustCompile(`^\d+$`)

// ghAPI 는 gh api 호출에서 PR 을 만들거나 고치는 호출을 찾는다. args 는 "api" 뒤의 단어다.
// 엔드포인트 경로가 pulls 로 끝나고 메서드가 POST 이면(메서드가 없을 때는 필드가 있어 POST 로 가면) pr.create,
// 경로에 pulls/<번호> 가 있고 메서드가 PATCH 이면 pr.edit 이다. 메서드가 없거나 GET 이면 아무것도 내지 않는다.
func (s *scan) ghAPI(args []string) {
	var endpoint, method string
	hasField := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch {
		case a == "-X" || a == "--method":
			method = next()
		case strings.HasPrefix(a, "--method="):
			method = strings.TrimPrefix(a, "--method=")
		case strings.HasPrefix(a, "-X") && !strings.HasPrefix(a, "--"):
			method = strings.TrimPrefix(a, "-X")
		case slices.Contains(ghFieldOpts, a):
			hasField = true
			next()
		case slices.ContainsFunc(ghFieldOpts, func(o string) bool {
			return strings.HasPrefix(a, o+"=") || (len(o) == 2 && strings.HasPrefix(a, o))
		}):
			hasField = true // -fkey=value, --field=key=value
		case slices.Contains(ghValueOpts, a):
			next()
		case strings.HasPrefix(a, "-"):
		case endpoint == "":
			endpoint = a
		}
	}
	if i := strings.IndexAny(endpoint, "?#"); i >= 0 {
		endpoint = endpoint[:i]
	}
	segs := strings.FieldsFunc(endpoint, func(r rune) bool { return r == '/' })
	method = strings.ToUpper(method)
	if method == "" && hasField {
		method = "POST"
	}
	if len(segs) > 0 && segs[len(segs)-1] == "pulls" && method == "POST" {
		s.add("pr.create")
		return
	}
	for i := 0; i+1 < len(segs); i++ {
		if segs[i] == "pulls" && ghPullNumRe.MatchString(segs[i+1]) && method == "PATCH" {
			s.add("pr.edit")
			return
		}
	}
}

// cmdKeys 는 tool_actions 의 "Bash:<이름>" 과 견줄 이름이다. 명령 이름, npx 의 첫 인자(옵션 제외),
// npm|yarn|pnpm|bun 의 run 다음 이름(npm 이 아니면 run 없이 바로 쓴 스크립트 이름도)이다.
func cmdKeys(name string, args []string) []string {
	keys := []string{name}
	switch name {
	case "npx":
		if i := slices.IndexFunc(args, func(a string) bool { return !strings.HasPrefix(a, "-") }); i >= 0 {
			keys = append(keys, args[i])
		}
	case "npm", "yarn", "pnpm", "bun":
		switch {
		case len(args) > 1 && args[0] == "run":
			keys = append(keys, args[1])
		case len(args) > 0 && name != "npm" && args[0] != "run":
			keys = append(keys, args[0])
		}
	}
	return keys
}

// pkgManager 는 npm·yarn·pnpm·bun 호출에서 테스트 실행과 코드 생성을 찾는다.
func (s *scan) pkgManager(name, w1, w2 string) {
	isTest := func(x string) bool { return strings.HasPrefix(x, "test") }
	switch {
	case w1 == "test" || w1 == "t" || (w1 == "run" && isTest(w2)) || (name != "npm" && isTest(w1)):
		s.add("test.run")
	case (name == "yarn" || name == "pnpm") && (w1 == "vitest" || w1 == "jest"):
		s.add("test.run")
	}
}

var branchBenign = []string{"-d", "--delete", "-m", "-M", "-c", "-C", "--list", "-l", "-a", "-r", "--show-current", "-v", "-vv", "--merged", "--no-merged", "--contains", "-u", "--set-upstream-to", "--unset-upstream", "--edit-description"}

var shortOptRe = regexp.MustCompile(`^-[A-Za-z]+$`)

// shortHas 는 a 가 `-abc` 꼴의 짧은 옵션 묶음이고 그 안에 글자 ch 가 있는가.
func shortHas(a, ch string) bool { return shortOptRe.MatchString(a) && strings.Contains(a, ch) }

// git 은 git 호출의 동작 이름을 낸다. ws 는 접두를 벗긴 단어다.
func (s *scan) git(ws []string) {
	sub, rest, ok := shell.GitSubcommand(ws)
	if !ok {
		return
	}
	has := func(xs ...string) bool {
		return slices.ContainsFunc(rest, func(a string) bool { return slices.Contains(xs, a) })
	}
	anyRest := func(f func(string) bool) bool { return slices.ContainsFunc(rest, f) }
	var nonOpt []string
	for _, a := range rest {
		if !strings.HasPrefix(a, "-") {
			nonOpt = append(nonOpt, a)
		}
	}
	whole := func(p string) bool { return p == "." || p == ":/" || p == "*" }
	switch sub {
	case "push":
		if shell.PushForces(rest) {
			s.add("git.push.force") // 강제 푸시는 git.push 의 하위이므로 이 이름 하나만 낸다
		} else {
			s.add("git.push")
		}
	case "commit", "rebase":
		s.add("git." + sub)
	case "reset":
		if has("--hard") {
			s.add("git.destructive")
		}
	case "reflog":
		if len(rest) > 0 && rest[0] == "expire" {
			s.add("git.destructive")
		}
	case "gc":
		if anyRest(func(a string) bool { return a == "--prune" || strings.HasPrefix(a, "--prune=") }) {
			s.add("git.destructive")
		}
	case "clean":
		if anyRest(func(a string) bool { return a == "--force" || shortHas(a, "f") }) {
			s.add("git.destructive")
		}
	case "branch":
		switch {
		case anyRest(func(a string) bool { return a == "-D" || shortHas(a, "D") }):
			s.add("git.destructive")
		case anyRest(func(a string) bool {
			return slices.Contains(branchBenign, a) || strings.HasPrefix(a, "--format") || strings.HasPrefix(a, "--sort") || strings.HasPrefix(a, "--set-upstream-to=")
		}):
		case len(nonOpt) > 0:
			s.add("git.create_branch")
		}
	case "checkout":
		if has("-b", "-B", "--orphan") {
			s.add("git.create_branch")
			return
		}
		dd := slices.Index(rest, "--")
		if (dd >= 0 && slices.ContainsFunc(rest[dd+1:], whole)) || (dd < 0 && len(nonOpt) == 1 && nonOpt[0] == ".") {
			s.add("git.destructive")
		}
	case "restore":
		if (!has("--staged", "-S") || has("--worktree", "-W")) && slices.ContainsFunc(nonOpt, whole) {
			s.add("git.destructive")
		}
	case "switch":
		if has("-c", "-C", "--create", "--force-create", "--orphan") {
			s.add("git.create_branch")
		}
	case "stash":
		if len(rest) == 0 || (rest[0] != "list" && rest[0] != "show") {
			s.add("git.stash")
		}
	case "worktree":
		if len(rest) > 0 && slices.Contains([]string{"add", "remove", "prune", "move"}, rest[0]) {
			s.add("git.worktree")
		}
	}
}

var gwsWrite = []string{"create", "update", "batchUpdate", "append", "clear", "batchClear", "delete", "insert", "patch", "copy", "move", "emptyTrash", "import"}

// gws 는 구글 문서 도구 호출이다. 옵션을 건너뛰고 처음 나온 단어가 쓰기 동사면 gws.write, 아니면 gws.read 다.
func (s *scan) gws(args []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
			continue
		}
		if slices.Contains(gwsWrite, a) {
			s.add("gws.write")
			return
		}
	}
	s.add("gws.read")
}

var rmSuffixRe = regexp.MustCompile(`/\*$`)
var rmHomeRe = regexp.MustCompile(`^\$\{?HOME\}?`)

// rm 은 -r 과 -f 가 함께 있고 대상이 루트·홈·작업 폴더(또는 그 상위)면 fs.destructive 를 낸다.
func (s *scan) rm(args []string) {
	rec, force := false, false
	for _, a := range args {
		rec = rec || a == "--recursive" || shortHas(a, "r") || shortHas(a, "R")
		force = force || a == "--force" || shortHas(a, "f")
	}
	if !rec || !force {
		return
	}
	base := ""
	if s.cwd != "" {
		base, _ = filepath.Abs(s.cwd)
	}
	for _, raw := range args {
		if strings.HasPrefix(raw, "-") {
			continue
		}
		t := rmHomeRe.ReplaceAllString(rmSuffixRe.ReplaceAllString(raw, "/"), "~")
		switch t {
		case "/", "~", "~/", ".", "./", "*", "..", "../":
			s.add("fs.destructive")
			return
		}
		if base == "" {
			continue
		}
		if strings.HasPrefix(t, "~/") {
			t = filepath.Join(s.home, t[2:])
		}
		abs := t
		if !filepath.IsAbs(t) {
			abs = filepath.Join(base, t)
		}
		abs = filepath.Clean(abs)
		if base == abs || strings.HasPrefix(base, strings.TrimSuffix(abs, "/")+"/") {
			s.add("fs.destructive")
			return
		}
	}
}

// writePath 는 쓰기 대상 문자열을 절대 경로로 만든다. 무시할 곳(/dev, /tmp, $TMPDIR)이나 풀 수 없는 경로면 ok=false.
func (s *scan) writePath(raw string) (string, bool) {
	if raw == "" || raw == "-" {
		return "", false
	}
	p := raw
	for _, v := range []string{"$TMPDIR", "${TMPDIR}"} {
		if p == v || strings.HasPrefix(p, v+"/") {
			return "", false
		}
	}
	for _, v := range []string{"$HOME", "${HOME}"} {
		if p == v || strings.HasPrefix(p, v+"/") {
			p = "~" + p[len(v):]
		}
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if s.home == "" {
			return "", false
		}
		p = s.home + p[1:]
	}
	if strings.ContainsAny(p, "$`") {
		return "", false
	}
	p, ok := absPath(p, s.cwd)
	if !ok {
		return "", false
	}
	roots := []string{"/dev", "/tmp", "/private/tmp"}
	if filepath.IsAbs(s.tmp) && filepath.Clean(s.tmp) != "/" {
		roots = append(roots, filepath.Clean(s.tmp))
	}
	for _, r := range roots {
		if p == r || strings.HasPrefix(p, r+"/") {
			return "", false
		}
	}
	return p, true
}

// sedTargets 는 sed -i(제자리 수정)의 파일 인자다. -i 가 없으면 nil 이다.
// 스크립트를 -e·-f 로 주지 않았으면 첫 위치 인자는 스크립트이므로 뺀다.
func sedTargets(args []string) []string {
	inplace, script := false, false
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--in-place" || strings.HasPrefix(a, "--in-place="):
			inplace = true
		case a == "--expression" || a == "--file":
			script = true
			i++
		case strings.HasPrefix(a, "--"):
		case len(a) > 1 && a[0] == '-':
			if strings.Contains(a, "i") {
				inplace = true
				// BSD sed 는 `-i ''`, `-i .bak` 처럼 접미사가 다음 단어다.
				if strings.HasSuffix(a, "i") && i+1 < len(args) && (args[i+1] == "" || (args[i+1][0] == '.' && !strings.Contains(args[i+1], "/"))) {
					i++
				}
			}
			if strings.HasSuffix(a, "e") || strings.HasSuffix(a, "f") {
				script = true
				i++
			}
		default:
			pos = append(pos, a)
		}
	}
	if !inplace {
		return nil
	}
	if !script && len(pos) > 0 {
		pos = pos[1:]
	}
	return pos
}

// cpmvTargets 는 cp·mv 가 쓰는 경로다. 목적지가 디렉터리(끝이 /, 이미 있는 디렉터리, -t)면 원본 이름을 붙인 경로다.
func (s *scan) cpmvTargets(args []string) []string {
	var pos []string
	target := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "--target-directory="):
			target = strings.TrimPrefix(a, "--target-directory=")
		case (a == "--target-directory" || a == "-t") && i+1 < len(args):
			target = args[i+1]
			i++
		case strings.HasPrefix(a, "-") && a != "-":
		default:
			pos = append(pos, a)
		}
	}
	srcs := pos
	if target == "" {
		if len(pos) < 2 {
			return nil
		}
		target, srcs = pos[len(pos)-1], pos[:len(pos)-1]
		if p, ok := s.writePath(target); !strings.HasSuffix(target, "/") && !(ok && isDir(p)) {
			return []string{target}
		}
	}
	var out []string
	for _, src := range srcs {
		if b := path.Base(src); b != "" && !strings.ContainsAny(b, "$`") {
			out = append(out, strings.TrimRight(target, "/")+"/"+b)
		}
	}
	return out
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
