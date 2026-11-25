package review

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hee-develop/heka/internal/item"
	"github.com/hee-develop/heka/internal/ledger"
	"github.com/hee-develop/heka/internal/register"
)

// 검토 결정이다.
const (
	DecMerge   = "merge"
	DecKeep    = "keep"
	DecDropNew = "drop-new"
	DecDropOld = "drop-old"
)

// Options 는 합치기에서 초안 대신 쓸 값과 결정의 사유다. 비어 있지 않은 값만 초안을 덮는다.
type Options struct {
	Reason  string
	Text    string
	Body    string
	BodySet bool // Body 가 빈 글이어도 초안의 본문을 덮는다
	About   []string
}

var trust = map[string]int{"user": 3, "doc": 2, "ai": 1}

// Apply 는 검토 항목 하나에 결정을 적용하고 한 줄 설명을 돌려준다.
// 합치기는 대상 항목(target id 를 유지)을 다시 쓰고 새 항목을 은퇴시킨다. 합친 결과가 검증을 통과하지 못하면 아무것도 바꾸지 않는다.
func Apply(e register.Env, entryID, decision string, o Options) (string, error) {
	en, err := Load(e.Home, entryID)
	if err != nil {
		return "", err
	}
	reason := strings.TrimSpace(o.Reason)
	var msg string
	var retired []string
	switch decision {
	case DecMerge:
		if en.TargetID == "" {
			return "", errors.New("대상 항목이 없는 검토 항목은 합칠 수 없음")
		}
		if err := merge(e, en, o); err != nil {
			return "", err
		}
		if _, err := register.Retire(e, en.NewID, "합침: "+en.TargetID+" 에 합침"); err != nil {
			return "", fmt.Errorf("%s 에 합쳤으나 새 항목 %s 를 은퇴시키지 못함: %w", en.TargetID, en.NewID, err)
		}
		retired = []string{en.NewID}
		msg = fmt.Sprintf("%s 에 합치고 %s 를 은퇴시켰습니다.", en.TargetID, en.NewID)
	case DecKeep:
		if reason == "" {
			reason = "따로 두기"
		}
		msg = fmt.Sprintf("%s 를 따로 두었습니다.", en.ID)
	case DecDropNew:
		if reason == "" {
			reason = "검토에서 버림"
		}
		if _, err := register.Retire(e, en.NewID, reason); err != nil {
			return "", err
		}
		retired = []string{en.NewID}
		msg = fmt.Sprintf("새 항목 %s 를 은퇴시켰습니다.", en.NewID)
	case DecDropOld:
		if en.TargetID == "" {
			return "", errors.New("대상 항목이 없는 검토 항목은 옛 것을 버릴 수 없음")
		}
		if reason == "" {
			reason = "검토에서 버림"
		}
		if _, err := register.Retire(e, en.TargetID, reason); err != nil {
			return "", err
		}
		retired = []string{en.TargetID}
		msg = fmt.Sprintf("대상 항목 %s 를 은퇴시켰습니다.", en.TargetID)
	default:
		return "", fmt.Errorf("알 수 없는 결정 %q (merge|keep|drop-new|drop-old)", decision)
	}
	Remove(e.Home, en.ID)
	for _, id := range retired {
		dropRefs(e.Home, id)
	}
	ids := []ledger.Injected{{ID: en.NewID}}
	if en.TargetID != "" {
		ids = append(ids, ledger.Injected{ID: en.TargetID})
	}
	if reason == "" {
		reason = decision
	}
	register.FinishRow(e, ledger.Row{Event: "review", Moment: decision, Entry: en.ID, Reason: reason, Injected: ids}, "review: "+en.ID+" "+decision)
	return msg, nil
}

// merge 는 대상 항목을 초안(또는 플래그 값)으로 다시 쓴다.
func merge(e register.Env, en Entry, o Options) error {
	newIt, err := item.Parse(filepath.Join(e.Home, "items", en.NewID+".md"))
	if err != nil || !newIt.Valid() {
		return fmt.Errorf("새 항목 %s 를 읽지 못함", en.NewID)
	}
	target, err := item.Parse(filepath.Join(e.Home, "items", en.TargetID+".md"))
	if err != nil || !target.Valid() {
		return fmt.Errorf("대상 항목 %s 를 읽지 못함", en.TargetID)
	}
	var d Merged
	if en.Merged != nil {
		d = *en.Merged
	}
	if o.Text != "" {
		d.Text = o.Text
	}
	if o.BodySet {
		d.Body = o.Body
	}
	if len(o.About) > 0 {
		d.About = o.About
	}
	if strings.TrimSpace(d.Text) == "" {
		return errors.New("합칠 초안이 없음: --text 로 주입문을 주세요")
	}
	// 초안의 본문이나 주제어가 비면(그리고 플래그도 없으면) 두 항목의 것을 이어 지식이 사라지지 않게 한다.
	if !o.BodySet && strings.TrimSpace(d.Body) == "" {
		var parts []string
		for _, b := range []string{target.Body, newIt.Body} {
			if b = strings.Trim(b, "\n"); strings.TrimSpace(b) != "" {
				parts = append(parts, b)
			}
		}
		d.Body = strings.Join(parts, "\n\n")
	}
	if len(d.About) == 0 {
		for _, a := range append(slices.Clone(target.About), newIt.About...) {
			if !slices.Contains(d.About, a) {
				d.About = append(d.About, a)
			}
		}
	}
	in := register.FromItem(target)
	in.Text, in.About = strings.TrimSpace(d.Text), d.About
	// 검사(check)는 대상 것을 쓰되, 대상에 없고 새 항목에 있으면 새 항목 것을 쓴다. 둘 다 있고 다르면 합치지 않는다.
	switch {
	case target.Check == nil && newIt.Check != nil:
		in.Check = newIt.Check
	case target.Check != nil && newIt.Check != nil && *target.Check != *newIt.Check:
		return errors.New("검사(check)가 서로 달라 자동으로 합칠 수 없음. 항목 파일을 직접 고치세요")
	}
	// 출처는 더 믿을 만한 쪽(user > doc > ai, 같으면 대상 쪽)을 따르고, 다른 쪽의 출처와 인용은 본문에 남긴다.
	keep, other := target, newIt
	if trust[newIt.Source] > trust[target.Source] {
		keep, other = newIt, target
	}
	in.Source, in.Quote = keep.Source, keep.Quote
	line := fmt.Sprintf("합친 항목 %s: 출처 %s", other.ID, other.Source)
	if other.Quote != "" {
		line += fmt.Sprintf(", 인용 %q", other.Quote)
	}
	body := strings.TrimRight(d.Body, "\n")
	if body != "" {
		body += "\n\n"
	}
	in.Body = body + line + "\n"
	return register.Rewrite(e, in)
}

// dropRefs 는 은퇴한 항목을 가리키는 검토 항목을 지운다. 미판정 항목은 그 후보만 뺀다.
func dropRefs(home, id string) {
	for _, en := range loadAll(home) {
		switch {
		case en.NewID == id || en.TargetID == id:
			Remove(home, en.ID)
		case slices.Contains(en.Candidates, id):
			en.Candidates = slices.DeleteFunc(slices.Clone(en.Candidates), func(c string) bool { return c == id })
			if len(en.Candidates) == 0 {
				Remove(home, en.ID)
			} else {
				_ = Save(home, en)
			}
		}
	}
}
