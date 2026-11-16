// Package lookup 은 AI 나 사람이 직접 항목을 찾는 길이다.
// 프롬프트 순간의 선별과 달리 모델 판정을 부르지 않고, 동작 조건이 붙은 항목도 대상에 넣는다.
package lookup

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/index"
	"github.com/hee-develop/heka/internal/ledger"
	"github.com/hee-develop/heka/internal/selector"
)

// DefaultLimit 는 낱말 점수로 잡는 후보 수의 기본값이다.
const DefaultLimit = 10

// Query 는 조회 하나다. Budget 이 0 이하이면 설정의 budgets.find 를 쓴다.
type Query struct {
	Text   string
	Scopes []string
	Budget int
	Limit  int
	Today  string // YYYY-MM-DD
}

// Hit 은 돌려주는 항목이다. Why 는 걸린 이유(주제어 또는 낱말 점수)다.
type Hit struct {
	ID          string              `json:"id"`
	Text        string              `json:"text"`
	Body        string              `json:"body"`
	Must        bool                `json:"must"`
	When        map[string][]string `json:"when"`
	Why         string              `json:"why"`
	Score       float64             `json:"score"`
	BodyOmitted bool                `json:"body_omitted,omitempty"`
}

// Result 는 조회 결과다. OmittedIDs 는 예산에 걸려 빠진 항목이다.
type Result struct {
	Items      []Hit    `json:"items"`
	Omitted    int      `json:"omitted"`
	OmittedIDs []string `json:"omitted_ids"`
}

// Scopes 는 인자로 받은 범위가 있으면 그것을, 없으면 작업 폴더에서 찾은 범위를 돌려준다.
func Scopes(cfg *config.Config, given []string, cwd string) []string {
	if len(given) > 0 {
		return given
	}
	s, _ := cfg.Locate(cwd)
	return s
}

// Find 는 질의에 맞는 항목을 순서대로 모아 예산 안에서 돌려준다.
// 후보는 주제어가 걸린 항목과 낱말 점수 상위 Limit 건이고, 범위와 자격은 선별기와 같은 규칙으로 거른다.
func Find(ix *index.Index, cfg *config.Config, q Query) (Result, error) {
	if q.Limit <= 0 {
		q.Limit = DefaultLimit
	}
	if q.Budget <= 0 {
		q.Budget = cfg.Budgets["find"]
	}
	scopes := selector.ScopeSet(cfg, q.Scopes)
	ranked, err := ix.Lexical(q.Text, math.MaxInt32)
	if err != nil {
		return Result{}, err
	}
	score := map[string]float64{}
	for _, s := range ranked {
		score[s.ID] = s.Score
	}
	about, err := ix.AboutMatches(q.Text)
	if err != nil {
		return Result{}, err
	}
	terms := map[string][]string{}
	for _, m := range about {
		terms[m.ID] = append(terms[m.ID], fmt.Sprintf("%q", m.Term))
	}

	var hits []Hit
	add := func(id string) error {
		r, err := ix.Item(id)
		if err != nil {
			return err
		}
		if stage, _, _ := selector.Qualify(scopes, r, q.Today); stage != "" {
			return nil
		}
		why := fmt.Sprintf("낱말 점수 %.2f", score[id])
		if t := terms[id]; len(t) > 0 {
			why = "주제어 " + strings.Join(t, ", ") + " 가 맞음"
			if score[id] > 0 {
				why += fmt.Sprintf(", 낱말 점수 %.2f", score[id])
			}
		}
		hits = append(hits, Hit{ID: id, Text: strings.TrimSpace(r.Text), Body: strings.TrimSpace(r.Body), Must: r.Must,
			When: r.When, Why: why, Score: score[id]})
		return nil
	}
	done := map[string]bool{}
	for id := range terms {
		done[id] = true
		if err := add(id); err != nil {
			return Result{}, err
		}
	}
	picked := 0
	for _, s := range ranked { // 점수 내림차순이다. 자격 없는 항목은 건너뛰고 위에서부터 채운다.
		if picked >= q.Limit {
			break
		}
		if done[s.ID] {
			continue
		}
		before := len(hits)
		if err := add(s.ID); err != nil {
			return Result{}, err
		}
		if len(hits) > before {
			picked++
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if ta, tb := terms[a.ID] != nil, terms[b.ID] != nil; ta != tb {
			return ta
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.ID < b.ID
	})
	return fit(hits, q.Budget), nil
}

// fit 은 순서대로 예산에 넣는다. 안 들어가면 본문을 빼고 주입문만으로 다시 시도하고, 그래도 안 되면 빠진 목록에 넣는다.
func fit(hits []Hit, budget int) Result {
	res := Result{Items: []Hit{}, OmittedIDs: []string{}}
	used := 0
	for _, h := range hits {
		if t := selector.Tokens(h.Text + "\n" + h.Body); used+t <= budget {
			used += t
			res.Items = append(res.Items, h)
			continue
		}
		if t := selector.Tokens(h.Text); h.Body != "" && used+t <= budget {
			used += t
			h.Body, h.BodyOmitted = "", true
			res.Items = append(res.Items, h)
			continue
		}
		res.OmittedIDs = append(res.OmittedIDs, h.ID)
	}
	res.Omitted = len(res.OmittedIDs)
	return res
}

// Text 는 결과를 사람이 읽는 글로 만든다. 항목마다 id 와 주입문, 본문은 들여쓴다.
func (r Result) Text() string {
	if len(r.Items) == 0 && r.Omitted == 0 {
		return "찾은 항목이 없습니다."
	}
	var b strings.Builder
	for _, h := range r.Items {
		fmt.Fprintf(&b, "[%s] %s\n", h.ID, h.Text)
		meta := []string{"걸린 이유: " + h.Why}
		if h.Must {
			meta = append([]string{"필수"}, meta...)
		}
		for _, k := range []string{"moment", "action", "path", "repo", "agent"} {
			if v := h.When[k]; len(v) > 0 {
				meta = append(meta, "when."+k+"="+strings.Join(v, "|"))
			}
		}
		b.WriteString("  (" + strings.Join(meta, " · ") + ")\n")
		if h.BodyOmitted {
			b.WriteString("  (본문은 예산 때문에 생략함)\n")
		}
		for _, l := range strings.Split(h.Body, "\n") {
			if h.Body != "" {
				b.WriteString("    " + l + "\n")
			}
		}
		b.WriteString("\n")
	}
	if r.Omitted > 0 {
		fmt.Fprintf(&b, "예산 때문에 빠진 항목 %d건: %s (예산을 늘려 다시 찾을 수 있음)\n", r.Omitted, strings.Join(r.OmittedIDs, ", "))
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// Log 는 조회를 원장에 한 줄 남긴다. 실패해도 조회는 성공이다.
func Log(home string, q Query, r Result, now time.Time) {
	row := ledger.Row{Event: "find", Moment: "find", Prompt: q.Text}
	for _, h := range r.Items {
		row.Injected = append(row.Injected, ledger.Injected{ID: h.ID, Why: h.Why})
	}
	for _, id := range r.OmittedIDs {
		row.Dropped = append(row.Dropped, ledger.Dropped{ID: id, Stage: "예산", Reason: "조회의 예산을 넘음"})
	}
	if err := ledger.Append(home, row, now); err != nil {
		fmt.Fprintln(os.Stderr, "heka: 원장 쓰기 실패:", err)
	}
}
