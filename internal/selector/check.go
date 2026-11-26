package selector

import (
	"context"
	"encoding/json"
	"path"
	"slices"
	"strings"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/index"
	"github.com/hee-develop/heka/internal/item"
)

// Checkable 은 결과물 검사를 받아야 하는 항목이다. Text 는 그 항목의 주입문이다.
type Checkable struct {
	ID    string
	Text  string
	Hash  string
	Check item.Check
}

// 도구 호출 직전에 Bash 명령에서 뽑아 검사하는 대상이다. last_message 는 턴 종료에서, question 은 AskUserQuestion 호출 직전에 검사한다.
var toolTargets = []string{"command", "commit_message", "pr_body"}

func parseCheck(raw string) (item.Check, bool) {
	var c item.Check
	if raw == "" || json.Unmarshal([]byte(raw), &c) != nil {
		return c, false
	}
	return c, true
}

// ToolChecks 는 도구 순간(m.Action, m.Path 가 정해진 순간)에서 검사할 항목을 모은다.
// 동작과 조건이 맞고 범위와 자격을 통과한 항목 가운데 check 가 있는 것이다.
// 이미 넣은 항목과 예산으로 빠질 항목도 포함한다. 검사는 넣는 일과 따로 매번 하기 때문이다.
func ToolChecks(ctx context.Context, ix *index.Index, cfg *config.Config, m Moment) ([]Checkable, error) {
	m.Seen = nil
	res, err := Gather(ctx, ix, cfg, m)
	if err != nil {
		return nil, err
	}
	var out []Checkable
	for _, inj := range res.Injections {
		c, ok := parseCheck(inj.Check)
		if ok && slices.Contains(toolTargets, c.Target) {
			out = append(out, Checkable{ID: inj.ID, Text: inj.Text, Hash: inj.Hash, Check: c})
		}
	}
	return out, nil
}

// StopChecks 는 턴 종료에서 검사할 항목을 모은다. check.target 이 last_message 이고
// 범위와 자격을 통과한 항목이다. when 은 repo 와 agent 조건만 본다(action, path, moment 는 보지 않는다).
func StopChecks(ix *index.Index, cfg *config.Config, m Moment) ([]Checkable, error) {
	return targetChecks(ix, cfg, m, "last_message")
}

// QuestionChecks 는 AskUserQuestion 호출 직전에 검사할 항목을 모은다. check.target 이 question 이고
// 범위와 자격을 통과한 항목이다. 이 호출은 동작 이름을 내지 않으므로(동작이 없는 도구) 동작으로 고르지 않고,
// StopChecks 처럼 when 은 repo 와 agent 조건만 본다.
func QuestionChecks(ix *index.Index, cfg *config.Config, m Moment) ([]Checkable, error) {
	return targetChecks(ix, cfg, m, "question")
}

func targetChecks(ix *index.Index, cfg *config.Config, m Moment, target string) ([]Checkable, error) {
	ids, err := ix.WithCheck()
	if err != nil {
		return nil, err
	}
	scopes := ScopeSet(cfg, m.Scopes)
	var out []Checkable
	for _, id := range ids {
		r, err := ix.Item(id)
		if err != nil {
			return nil, err
		}
		if !r.Push {
			continue // 방어: 조회 전용 항목에는 check 가 있을 수 없다.
		}
		c, ok := parseCheck(r.CheckJSON)
		if !ok || c.Target != target {
			continue
		}
		if stage, _, _ := Qualify(scopes, r, m.Today); stage != "" {
			continue
		}
		if !stopWhenOK(r, m) {
			continue
		}
		out = append(out, Checkable{ID: r.ID, Text: strings.TrimSpace(r.Text), Hash: r.Hash, Check: c})
	}
	return out, nil
}

func stopWhenOK(r *index.Record, m Moment) bool {
	if vs := r.When["repo"]; len(vs) > 0 {
		if m.Repo == "" || !slices.ContainsFunc(vs, func(v string) bool { ok, _ := path.Match(v, m.Repo); return ok }) {
			return false
		}
	}
	if vs := r.When["agent"]; len(vs) > 0 {
		if m.Agent == "" || !slices.Contains(vs, m.Agent) {
			return false
		}
	}
	return true
}
