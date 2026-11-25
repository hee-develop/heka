// Package regjudge 는 등록 직후의 중복·모순 판정(heka judge-add)을 맡는다.
// 새 항목과 비슷한 후보를 설정의 등록 판정 명령에 넘겨 후보마다 답을 받고, 검토 목록과 원장에만 쓴다.
// 항목 파일은 고치거나 지우지 않는다.
package regjudge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/item"
	"github.com/hee-develop/heka/internal/judge"
	"github.com/hee-develop/heka/internal/ledger"
	"github.com/hee-develop/heka/internal/register"
	"github.com/hee-develop/heka/internal/review"
)

// bodyRunes 는 판정 명령에 넘기는 본문의 최대 글자(룬) 수다.
const bodyRunes = 1500

// Block 은 항목 하나를 판정 입력의 필드 블록으로 만든다. 측정 도구와 같은 모양이어야 하므로 바꾸면 단위 시험이 깨진다.
func Block(it *item.Item) string {
	var when []string
	for _, k := range []struct {
		name string
		vs   item.List
	}{{"moment", it.When.Moment}, {"action", it.When.Action}, {"repo", it.When.Repo}, {"path", it.When.Path}, {"agent", it.When.Agent}} {
		if len(k.vs) > 0 {
			when = append(when, k.name+"="+strings.Join(k.vs, "|"))
		}
	}
	cond := "없음"
	if len(when) > 0 {
		cond = strings.Join(when, "; ")
	}
	body := strings.Trim(it.Body, "\n")
	if r := []rune(body); len(r) > bodyRunes {
		body = string(r[:bodyRunes]) + "…(생략)"
	}
	return fmt.Sprintf("id: %s\n출처: %s\n범위: %s\n필수: %t\n조건: %s\n주제어: %s\n주입문: %s\n본문:\n%s",
		it.ID, it.Source, strings.Join(it.Scope, ", "), it.Must, cond, strings.Join(it.About, ", "), strings.TrimSpace(it.Text), body)
}

// BuildInput 은 판정 명령의 표준 입력이다. 지시문, [새 항목], [후보 1] … 블록을 빈 줄 하나로 잇는다.
func BuildInput(instruction string, newIt *item.Item, cands []*item.Item) string {
	parts := []string{strings.TrimRight(instruction, "\n"), "[새 항목]\n" + Block(newIt)}
	for i, c := range cands {
		parts = append(parts, fmt.Sprintf("[후보 %d]\n%s", i+1, Block(c)))
	}
	return strings.Join(parts, "\n\n") + "\n"
}

// verdict 는 판정 명령이 후보 하나에 낸 답이다.
type verdict struct {
	ID      string `json:"id"`
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
	Merged  *struct {
		Text  string   `json:"text"`
		Body  string   `json:"body"`
		About []string `json:"about"`
	} `json:"merged"`
}

// Parse 는 출력의 첫 `{` 부터 짝이 맞는 `}` 까지를 JSON 으로 읽어 후보에 대한 답만 돌려준다.
// 코드 울타리나 앞뒤 글이 섞여도 된다. 후보에 없는 id, 모르는 verdict, merged 가 빈 merge 는 버리고(미판정으로 친다),
// 같은 id 가 여러 번 나오면 처음 것만 쓴다.
func Parse(out string, candidates []string) (map[string]verdict, error) {
	obj, ok := firstObject(out)
	if !ok {
		return nil, errors.New("답에서 JSON 객체를 찾지 못함")
	}
	var a struct {
		Verdicts []verdict `json:"verdicts"`
	}
	if err := json.Unmarshal([]byte(obj), &a); err != nil {
		return nil, fmt.Errorf("답의 JSON 을 읽지 못함: %w", err)
	}
	got := map[string]verdict{}
	for _, v := range a.Verdicts {
		if !slices.Contains(candidates, v.ID) {
			continue
		}
		if _, dup := got[v.ID]; dup {
			continue
		}
		switch v.Verdict {
		case "separate", "conflict":
		case "merge":
			if v.Merged == nil || strings.TrimSpace(v.Merged.Text) == "" {
				continue
			}
		default:
			continue
		}
		got[v.ID] = v
	}
	return got, nil
}

// firstObject 는 s 의 첫 `{` 부터 짝이 맞는 `}` 까지다. 문자열 안의 괄호는 세지 않는다.
func firstObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr:
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
		case c == '"':
			inStr = true
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

// Run 은 `heka judge-add <id>` 의 일이다. 새 항목(items/<id>.md)과 후보를 읽어 판정 명령을 부르고
// 결과를 검토 목록과 원장에 쓴다. 새 항목을 읽을 수 없을 때만 오류다. 모순 판정은 곧바로 알린다(알림 글은 out 에도 쓴다).
func Run(e register.Env, out io.Writer, id string) error {
	newIt, err := item.Parse(filepath.Join(e.Home, "items", id+".md"))
	if err != nil || !newIt.Valid() {
		return fmt.Errorf("새 항목 %q 를 읽지 못함", id)
	}
	cands, err := register.Similar(e, newIt)
	if err != nil {
		return err
	}
	var items []*item.Item
	var ids []string
	for _, c := range cands {
		if it, err := item.Parse(filepath.Join(e.Home, "items", c.ID+".md")); err == nil && it.Valid() {
			items = append(items, it)
			ids = append(ids, c.ID)
		}
	}
	unjudgedID := review.EntryID(id, "")
	if len(items) == 0 {
		review.Remove(e.Home, unjudgedID) // 다시 판정했더니 후보가 없어짐
		return nil
	}
	rj := e.Cfg.RegisterJudge
	instruction := rj.Instruction
	if strings.TrimSpace(instruction) == "" {
		instruction = config.DefaultRegisterJudgeInstruction
	}
	rec := &ledger.RegisterJudged{}
	var got map[string]verdict
	var failure string
	if len(rj.Command) == 0 {
		rec.Error, failure = true, "등록 판정 명령이 설정돼 있지 않음"
	} else {
		start := time.Now()
		stdout, err := judge.RunCommand(context.Background(), rj.Command, rj.Timeout, BuildInput(instruction, newIt, items))
		rec.MS = max(time.Since(start).Milliseconds(), 1)
		if err == nil {
			got, err = Parse(stdout, ids)
		}
		if err != nil {
			rec.Error, rec.Timeout, failure = true, judge.IsTimeout(err), err.Error()
		}
	}
	created := e.Now.UTC().Format(time.RFC3339)
	var unjudged []string
	var conflicts []string
	for _, cid := range ids {
		v, ok := got[cid]
		if !ok {
			unjudged = append(unjudged, cid)
			rec.Verdicts = append(rec.Verdicts, ledger.Verdict{ID: cid, Verdict: "unjudged"})
			continue
		}
		rec.Verdicts = append(rec.Verdicts, ledger.Verdict{ID: cid, Verdict: v.Verdict, Reason: v.Reason})
		en := review.Entry{ID: review.EntryID(id, cid), NewID: id, TargetID: cid, Reason: v.Reason, Created: created}
		switch v.Verdict {
		case "separate":
			continue
		case "merge":
			en.Kind = review.KindMerge
			en.Merged = &review.Merged{Text: v.Merged.Text, Body: v.Merged.Body, About: v.Merged.About}
			if en.Merged.About == nil {
				en.Merged.About = []string{}
			}
		case "conflict":
			en.Kind = review.KindConflict
			conflicts = append(conflicts, cid)
		}
		if err := review.Save(e.Home, en); err != nil {
			return err
		}
	}
	if len(unjudged) > 0 {
		reason := failure
		if reason == "" {
			reason = "판정 명령이 일부 후보에 답하지 않았거나 쓸 수 없는 답을 냄"
		}
		en := review.Entry{ID: unjudgedID, Kind: review.KindUnjudged, NewID: id, Candidates: unjudged, Reason: reason, Created: created}
		if err := review.Save(e.Home, en); err != nil {
			return err
		}
	} else {
		review.Remove(e.Home, unjudgedID)
	}
	for _, cid := range conflicts {
		_ = review.Notify(e, out, review.ConflictText(id, cid), "conflict", 0)
	}
	row := ledger.Row{Event: "register_judge", Moment: "judge-add", Injected: []ledger.Injected{{ID: id}}, RegJudge: rec}
	if failure != "" {
		row.Reason = failure
	}
	register.FinishRow(e, row, "judge: "+id)
	return nil
}
