// Package action 은 AI 도구 호출 하나를 동작 이름(git.push, code.edit 같은 이름)으로 바꾼다.
// 셸 명령은 internal/shell 로 나누고 풀어서 읽는다. 도구 이름이나 스킬 이름만으로 아는 동작은
// 설정의 tool_actions 로 잇는다.
package action

import (
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/hee-develop/heka/internal/config"
)

// Call 은 AI 도구가 한 번 부른 호출이다.
type Call struct {
	Tool  string         // 도구 이름: Bash, Edit, Write, Task, Skill, ...
	Input map[string]any // 그 도구의 입력
	Cwd   string
}

// Resolved 는 호출을 해석한 결과다.
type Resolved struct {
	Actions []string `json:"actions"` // 동작 이름. 중복 없이, 나온 순서대로
	Paths   []string `json:"paths"`   // 쓰기 대상 경로(알 수 있을 때)
	Agent   string   `json:"agent"`   // Task 류 호출의 에이전트 종류
}

// 아래 이름은 새 체계에 없는 것도 있다. 기존 판정이 내던 이름을 그대로 둔 것이다.
const (
	actCodeEdit  = "code.edit"
	actDocEdit   = "doc.edit"
	actBashOther = "bash.other" // 동작이 잡히지 않은 Bash 호출
)

var editTools = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}

// docExts 에 있는 확장자의 편집은 doc.edit, 나머지는 code.edit 이다.
var docExts = []string{"md", "mdx", "markdown", "txt", "rst", "adoc", "yaml", "yml", "csv", "log"}

var mcpRe = regexp.MustCompile(`^mcp__(.+?)__(.+)$`)

// Resolve 는 호출을 동작 이름으로 바꾼다. 동작이 없는 도구(Read 등)면 Actions 가 빈 목록이다.
// cfg 가 nil 이면 설정의 tool_actions 는 쓰지 않는다.
func Resolve(c Call, cfg *config.Config) Resolved {
	r := Resolved{Actions: []string{}, Paths: []string{}}
	add := func(a ...string) {
		for _, x := range a {
			if !slices.Contains(r.Actions, x) {
				r.Actions = append(r.Actions, x)
			}
		}
	}
	keys := []string{c.Tool}
	switch {
	case editTools[c.Tool]:
		fp, ok := editPath(c.Input)
		if !ok {
			return r // 경로가 문자열이 아니면 해석하지 않는다
		}
		add(editAction(fp))
		if p, ok := absPath(fp, c.Cwd); ok {
			r.Paths = append(r.Paths, p)
		}
	case c.Tool == "Agent" || c.Tool == "Task":
		add("agent.delegate")
		r.Agent, _ = c.Input["subagent_type"].(string)
	case c.Tool == "Skill":
		add("skill.run")
		full, _ := c.Input["skill"].(string)
		if full == "" {
			full, _ = c.Input["name"].(string)
		}
		if full != "" {
			short := full[strings.LastIndex(full, ":")+1:]
			keys = append(keys, "Skill:"+full, "Skill:"+short)
		}
	case c.Tool == "Bash":
		cmd, _ := c.Input["command"].(string)
		acts, paths := bash(cmd, c.Cwd, cfg)
		add(acts...)
		if len(paths) > 0 {
			r.Paths = paths
		}
	case c.Tool == "Workflow":
		add("workflow.run")
	default:
		m := mcpRe.FindStringSubmatch(c.Tool)
		if m == nil || m[2] == "authenticate" || m[2] == "complete_authentication" {
			return r
		}
		add(mcpActions(m[1], m[2])...)
	}
	if cfg != nil {
		add(cfg.ToolActionsFor(keys...)...)
	}
	return r
}

// editPath 는 편집 도구 입력의 대상 경로를 돌려준다. 없으면 빈 문자열이고, 문자열이 아니면 ok=false 다.
func editPath(in map[string]any) (string, bool) {
	for _, k := range []string{"file_path", "notebook_path"} {
		if v, present := in[k]; present && v != nil {
			s, ok := v.(string)
			return s, ok
		}
	}
	return "", true
}

// editAction 은 경로의 확장자로 편집 동작을 고른다(편집 도구와 Bash 쓰기가 같은 규칙을 쓴다).
func editAction(p string) string {
	if slices.Contains(docExts, ext(p)) {
		return actDocEdit
	}
	return actCodeEdit
}

// ext 는 소문자 확장자다. 점으로 시작하는 파일 이름(.env)은 확장자가 없다.
func ext(p string) string {
	b := path.Base(strings.TrimRight(p, "/"))
	i := strings.LastIndex(b, ".")
	if i <= 0 {
		return ""
	}
	return strings.ToLower(b[i+1:])
}

// absPath 는 쓰기 대상을 절대 경로로 만든다. 상대 경로인데 cwd 가 없으면 ok=false.
func absPath(p, cwd string) (string, bool) {
	switch {
	case p == "":
		return "", false
	case filepath.IsAbs(p):
		return filepath.Clean(p), true
	case cwd == "":
		return "", false
	}
	return filepath.Join(cwd, p), true
}

var gwsMcpWrite = []string{"create_file", "update_file", "copy_file", "share_file", "trash_file", "untrash_file"}
var googleSrvRe = regexp.MustCompile(`(?i)google_(drive|sheets|docs)`)
var mcpWriteVerbs = []string{"create", "update", "delete", "remove", "send", "post", "edit", "write", "add", "set", "move", "merge", "close", "transition", "upload", "trash", "share", "copy", "schedule", "apply"}

// mcpActions 는 mcp__<서버>__<메서드> 호출의 동작 이름이다. 구글 문서 서버는 gws.read/gws.write 도 낸다.
func mcpActions(server, method string) []string {
	var out []string
	write := mcpIsWrite(server, method)
	if googleSrvRe.MatchString(server) {
		if gw := slices.Contains(gwsMcpWrite, method); gw {
			out, write = append(out, "gws.write"), true
		} else {
			out = append(out, "gws.read")
		}
	}
	if write {
		return append(out, "mcp.write")
	}
	return append(out, "mcp.read")
}

// mcpIsWrite 는 메서드 이름이 쓰기 동사로 시작하고 그 뒤가 끝·`_`·`-`·숫자·대문자일 때 참이다.
// 서버 이름의 낱말과 같은 접두(slack_send_message 의 slack_)는 떼고도 본다. executeWrite 와 executeDestructive 도 쓰기다.
func mcpIsWrite(server, method string) bool {
	if method == "executeWrite" || method == "executeDestructive" || startsWithVerb(method) {
		return true
	}
	lm := strings.ToLower(method)
	for _, tok := range regexp.MustCompile(`[_-]`).Split(strings.ToLower(server), -1) {
		if tok != "" && len(lm) > len(tok)+1 && strings.HasPrefix(lm, tok) && (lm[len(tok)] == '_' || lm[len(tok)] == '-') &&
			startsWithVerb(method[len(tok)+1:]) {
			return true
		}
	}
	return false
}

func startsWithVerb(m string) bool {
	for _, v := range mcpWriteVerbs {
		if !strings.HasPrefix(m, v) {
			continue
		}
		if len(m) == len(v) {
			return true
		}
		if c := m[len(v)]; c == '_' || c == '-' || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			return true
		}
	}
	return false
}
