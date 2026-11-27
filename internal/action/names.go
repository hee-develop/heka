package action

import (
	"slices"

	"github.com/hee-develop/heka/internal/config"
)

// 해석기(Resolve)가 스스로 내는 동작 이름이다. 코드는 이 상수만 쓰고, 이름 목록(Names)도 이 상수에서 만든다.
// 설정의 actions 에 적지 않아도 항목의 when.action 에 쓸 수 있는 이름이라는 뜻이다.
// 아래 이름은 새 체계에 없는 것도 있다. 기존 판정이 내던 이름을 그대로 둔 것이다.
const (
	// 파일 편집, 도구 호출
	actCodeEdit      = "code.edit"
	actDocEdit       = "doc.edit"
	actBashOther     = "bash.other" // 동작이 잡히지 않은 Bash 호출
	actAgentDelegate = "agent.delegate"
	actSkillRun      = "skill.run"
	actWorkflowRun   = "workflow.run"
	// git
	actGitPush         = "git.push"
	actGitPushForce    = "git.push.force"
	actGitCommit       = "git.commit"
	actGitRebase       = "git.rebase"
	actGitDestructive  = "git.destructive"
	actGitCreateBranch = "git.create_branch"
	actGitStash        = "git.stash"
	actGitWorktree     = "git.worktree"
	// PR, 테스트, 파일 삭제
	actPRCreate      = "pr.create"
	actPRMerge       = "pr.merge"
	actPRReview      = "pr.review"
	actPREdit        = "pr.edit"
	actPRComment     = "pr.comment"
	actTestRun       = "test.run"
	actFSDestructive = "fs.destructive"
	// 구글 문서 도구와 MCP
	actGwsRead  = "gws.read"
	actGwsWrite = "gws.write"
	actMCPRead  = "mcp.read"
	actMCPWrite = "mcp.write"
)

// emitted 는 위 상수 전부다. 상수를 더하면 여기에도 더해야 하고, 시험이 어긋남을 잡는다.
var emitted = []string{
	actCodeEdit,
	actDocEdit,
	actBashOther,
	actAgentDelegate,
	actSkillRun,
	actWorkflowRun,
	actGitPush,
	actGitPushForce,
	actGitCommit,
	actGitRebase,
	actGitDestructive,
	actGitCreateBranch,
	actGitStash,
	actGitWorktree,
	actPRCreate,
	actPRMerge,
	actPRReview,
	actPREdit,
	actPRComment,
	actTestRun,
	actFSDestructive,
	actGwsRead,
	actGwsWrite,
	actMCPRead,
	actMCPWrite,
}

// Names 는 해석기가 낼 수 있는 동작 이름 전체를 이름순으로 돌려준다.
// 설정의 actions·tool_actions 에 없어도 맞는 이름이므로, 항목의 when.action 어휘 검사에 쓴다.
func Names() []string {
	out := slices.Clone(emitted)
	slices.Sort(out)
	return out
}

// Vocab 은 항목의 when.action 에 쓸 수 있다고 아는 동작 이름 전체다.
// 설정이 아는 이름(actions 의 키와 in 의 값, tool_actions 의 값)에 해석기가 내는 이름(Names)을 더한 것이고,
// 점으로 이어진 이름의 위쪽(git.push.force 에 대한 git.push 와 git)과 설정의 in 관계도 설정의 규칙 그대로 함께 든다.
// 설정이 동작 이름을 하나도 두지 않았으면 비어 있다(어휘 검사를 하지 않는다는 뜻이다).
func Vocab(cfg *config.Config) map[string]bool {
	v := cfg.ActionVocab()
	if len(v) == 0 {
		return v
	}
	for _, n := range emitted {
		for _, x := range cfg.ActionClosure(n) {
			v[x] = true
		}
	}
	return v
}
