package shell

import (
	"path"
	"strings"
)

// forcePushMaxDepth 는 bash -c / eval 문자열을 재귀 검사하는 최대 깊이다.
const forcePushMaxDepth = 3

// ForcePush 는 명령 문자열에 force push(--force, -f 묶음, +refspec)가 있는지 본다.
// 접두(sudo·env 등)·git 전역 옵션·`bash -c`/`sh -c`/`eval` 안쪽(최대 깊이 3)까지 본다.
// --force-with-lease*·--force-if-includes 도 force 로 본다(덮어쓰기는 똑같이 일어나므로 동작 이름 git.push.force 와 같은 기준이다).
func ForcePush(cmd string) bool {
	_, ok := forcePushArgs(cmd, 0)
	return ok
}

// ForcePushRemote 는 force push 명령에 원격 이름(또는 URL)이 인자로 적혀 있으면 돌려준다. 없으면 빈 문자열이다(기본 원격).
// `--repo` 로 지정했거나 `+refspec` 만 있는 경우도 빈 문자열이다.
func ForcePushRemote(cmd string) string {
	args, ok := forcePushArgs(cmd, 0)
	if !ok {
		return ""
	}
	endOpts := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !endOpts && a == "--" {
			endOpts = true
			continue
		}
		if !endOpts && strings.HasPrefix(a, "-") {
			if a == "--repo" || strings.HasPrefix(a, "--repo=") {
				return ""
			}
			if pushValueOpts[a] {
				i++
			}
			continue
		}
		if strings.HasPrefix(a, "+") {
			return ""
		}
		return a // 첫 위치 인자가 저장소다
	}
	return ""
}

// forcePushArgs 는 force push 구간의 push 인자(하위 명령 뒤)를 돌려준다.
func forcePushArgs(cmd string, depth int) ([]string, bool) {
	if depth > forcePushMaxDepth {
		return nil, false
	}
	for _, seg := range SplitCommand(cmd) {
		ws := StripPrefix(seg.Words)
		if len(ws) == 0 {
			continue
		}
		switch path.Base(ws[0]) {
		case "git":
			if sub, rest, ok := GitSubcommand(ws); ok && sub == "push" && PushForces(rest) {
				return rest, true
			}
		case "bash", "sh", "zsh", "dash", "ksh", "ash":
			if inner, ok := ShellDashC(ws[1:]); ok {
				if a, ok := forcePushArgs(inner, depth+1); ok {
					return a, true
				}
			}
		case "eval":
			if a, ok := forcePushArgs(strings.Join(ws[1:], " "), depth+1); ok {
				return a, true
			}
		}
	}
	return nil, false
}

// ShellDashC 는 `bash -c "<문자열>"` 의 문자열을 꺼낸다. 묶음 옵션(-lc, -ec)도 본다.
func ShellDashC(args []string) (string, bool) {
	hasC := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "+") {
			if hasC {
				return a, true
			}
			return "", false // 스크립트 파일 실행
		}
		if a == "-o" || a == "+o" || a == "-O" || a == "+O" {
			i++
			continue
		}
		if isShortBundle(a) && strings.Contains(a, "c") {
			hasC = true
		}
	}
	return "", false
}

func isShortBundle(a string) bool {
	if len(a) < 2 || a[0] != '-' || a[1] == '-' {
		return false
	}
	for i := 1; i < len(a); i++ {
		if !((a[i] >= 'a' && a[i] <= 'z') || (a[i] >= 'A' && a[i] <= 'Z')) {
			return false
		}
	}
	return true
}

// push 옵션 중 다음 단어를 값으로 받는 것(그 값이 `+...` 여도 refspec 이 아니다).
var pushValueOpts = map[string]bool{"-o": true, "--push-option": true, "--repo": true, "--receive-pack": true, "--exec": true}

// PushForces 는 push 인자(하위 명령 뒤)에 강제 푸시 표시가 있는지 본다.
func PushForces(args []string) bool {
	endOpts := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !endOpts && a == "--" {
			endOpts = true
			continue
		}
		if !endOpts && strings.HasPrefix(a, "-") {
			if a == "--force" || strings.HasPrefix(a, "--force-with-lease") || a == "--force-if-includes" || (isShortBundle(a) && strings.IndexByte(a, 'f') >= 0) {
				return true
			}
			if pushValueOpts[a] {
				i++
			}
			continue
		}
		if strings.HasPrefix(a, "+") {
			return true
		}
	}
	return false
}
