package shell

import "path"

// 명령 앞에 붙어 실제 명령을 감싸는 접두와 셸 예약어.
var shellKeywords = map[string]bool{
	"{": true, "!": true, "$": true, "if": true, "then": true, "else": true, "elif": true,
	"do": true, "while": true, "until": true,
}

// 옵션이 값을 따로 받는 접두(그 값도 함께 건너뛴다).
var prefixValueOpts = map[string]map[string]bool{
	"sudo":    {"-u": true, "-g": true, "-C": true, "-h": true, "-p": true, "-r": true, "-t": true, "-U": true, "--user": true, "--group": true, "--host": true, "--prompt": true},
	"doas":    {"-u": true, "-C": true},
	"env":     {"-u": true, "-C": true, "--unset": true, "--chdir": true},
	"npx":     {"-p": true, "--package": true},
	"command": {},
	"nohup":   {},
	"exec":    {"-a": true},
	"builtin": {},
	"nice":    {"-n": true},
}

// StripPrefix 는 sudo(옵션 포함)·env(VAR=값, -u X)·`VAR=값` 선행 대입·time·command·nohup·npx·exec 등을 벗기고
// 실제 명령부터의 단어를 돌려준다. 셸 예약어({ ! if then do ...)도 건너뛴다. 입력을 바꾸지 않고 부분 슬라이스를 돌려준다.
func StripPrefix(words []string) []string {
	i := 0
	for i < len(words) {
		x := words[i]
		if shellKeywords[x] {
			i++
			continue
		}
		if isAssign(x) {
			i++
			continue
		}
		base := path.Base(x)
		vopts, isPrefix := prefixValueOpts[base]
		if base == "time" {
			vopts, isPrefix = map[string]bool{"-f": true, "-o": true, "--format": true, "--output": true}, true
		}
		if !isPrefix {
			break
		}
		i++
		for i < len(words) && len(words[i]) > 1 && words[i][0] == '-' {
			if words[i] == "--" {
				i++
				break
			}
			if vopts[words[i]] {
				i++
			}
			i++
		}
	}
	if i > len(words) {
		i = len(words)
	}
	return words[i:]
}

// isAssign 는 `NAME=값`·`NAME+=값` 형태의 선행 변수 대입인가.
func isAssign(s string) bool {
	if s == "" || !(isAlpha(s[0]) || s[0] == '_') {
		return false
	}
	for j := 1; j < len(s); j++ {
		c := s[j]
		switch {
		case c == '=':
			return true
		case c == '+' && j+1 < len(s) && s[j+1] == '=':
			return true
		case isAlpha(c) || isDigit(c) || c == '_':
		default:
			return false
		}
	}
	return false
}
