package shell

import (
	"path"
	"strings"
)

// 값을 따로(다음 단어로) 받는 git 전역 옵션. `--git-dir=x` 처럼 `=` 로 붙으면 한 단어다.
var gitGlobalValueOpts = map[string]bool{
	"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true,
	"--config-env": true, "--super-prefix": true, "--exec-path": false,
}

// GitSubcommand 는 words(StripPrefix 를 거친 것)가 git 명령이면 전역 옵션(-C <dir>, -c k=v, --git-dir=…,
// --work-tree=…, --no-pager 등)을 건너뛰고 하위 명령과 그 뒤 인자를 돌려준다.
// git 이 아니거나 하위 명령이 없으면 ok=false.
func GitSubcommand(words []string) (sub string, rest []string, ok bool) {
	if len(words) == 0 || path.Base(words[0]) != "git" {
		return "", nil, false
	}
	i := 1
	for i < len(words) && strings.HasPrefix(words[i], "-") && words[i] != "-" {
		if words[i] == "--" {
			i++
			break
		}
		if gitGlobalValueOpts[words[i]] {
			i++
		}
		i++
	}
	if i >= len(words) {
		return "", nil, false
	}
	return words[i], words[i+1:], true
}

// GitDirs 는 words(StripPrefix 를 거친 것)가 git 명령이면 하위 명령 앞의 `-C <경로>` 값을 나온 순서대로 돌려준다.
// 상대 경로는 앞의 -C 가 가리킨 디렉터리 기준이다(git 이 그렇게 처리한다).
func GitDirs(words []string) []string {
	if len(words) == 0 || path.Base(words[0]) != "git" {
		return nil
	}
	var dirs []string
	i := 1
	for i < len(words) && strings.HasPrefix(words[i], "-") && words[i] != "-" {
		if words[i] == "--" {
			break
		}
		if words[i] == "-C" && i+1 < len(words) {
			dirs = append(dirs, words[i+1])
		}
		if gitGlobalValueOpts[words[i]] {
			i++
		}
		i++
	}
	return dirs
}
