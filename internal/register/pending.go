package register

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hee-develop/heka/internal/item"
)

// PendingEntry 는 대기함의 항목 하나와 비슷한 기존 항목이다.
type PendingEntry struct {
	ID         string      `json:"id"`
	Text       string      `json:"text"`
	Candidates []Candidate `json:"candidates"`
}

// Pending 은 대기함의 항목을 id 순으로 돌려준다.
func Pending(e Env) ([]PendingEntry, error) {
	its, err := item.LoadDir(filepath.Join(e.Home, pendingDir), nil)
	if err != nil {
		return nil, err
	}
	out := []PendingEntry{}
	for _, it := range its {
		if !it.Valid() {
			continue
		}
		c, err := similar(e, it)
		if err != nil {
			return nil, err
		}
		out = append(out, PendingEntry{it.ID, strings.TrimSpace(it.Text), c})
	}
	return out, nil
}

// Decide 는 대기함의 항목 하나에 결정을 적용한다. action 은 fix|replace|keep|drop 이다.
// fix 와 replace 는 target(기존 항목 id)이, keep 은 reason 이 필요하다. 결과는 한 줄 설명이다.
func Decide(e Env, action, id, target, reason string) (string, error) {
	p := filepath.Join(e.Home, pendingDir, id+".md")
	it, err := item.Parse(p)
	if err != nil {
		return "", fmt.Errorf("대기함에 %q 가 없음", id)
	}
	if action != "drop" && !it.Valid() {
		return "", errors.New("대기함의 항목이 검증을 통과하지 못함: " + it.Problems[0].String())
	}
	live := func(name string) string { return filepath.Join(e.Home, "items", name+".md") }
	in := FromItem(it)
	var msg string
	switch action {
	case "fix":
		if _, err := os.Stat(live(target)); err != nil || target == "" {
			return "", fmt.Errorf("고칠 기존 항목 %q 가 없음", target)
		}
		in.ID = target
		if err := write(e, "items", in); err != nil {
			return "", err
		}
		msg = fmt.Sprintf("기존 항목 %s 의 내용을 %s 의 내용으로 고쳤습니다.", target, id)
	case "replace":
		if _, err := os.Stat(live(target)); err != nil || target == "" {
			return "", fmt.Errorf("바꿀 기존 항목 %q 가 없음", target)
		}
		if err := os.Remove(live(target)); err != nil {
			return "", err
		}
		if err := write(e, "items", in); err != nil {
			return "", err
		}
		msg = fmt.Sprintf("기존 항목 %s 을 지우고 %s 를 등록했습니다.", target, id)
	case "keep":
		if strings.TrimSpace(reason) == "" {
			return "", errors.New("따로 두려면 다른 항목이라는 이유(reason)가 필요함")
		}
		if _, err := os.Stat(live(id)); err == nil {
			return "", fmt.Errorf("같은 id 의 항목 %s 가 있어 따로 둘 수 없음. 고치기나 바꾸기를 쓰세요", id)
		}
		in.Body = strings.TrimRight(in.Body, "\n")
		if in.Body != "" {
			in.Body += "\n\n"
		}
		in.Body += "다른 항목으로 둔 이유: " + strings.TrimSpace(reason) + "\n"
		if err := write(e, "items", in); err != nil {
			return "", err
		}
		msg = fmt.Sprintf("%s 를 다른 항목으로 등록했습니다.", id)
	case "drop":
		msg = fmt.Sprintf("대기함의 %s 를 지웠습니다.", id)
	default:
		return "", fmt.Errorf("알 수 없는 결정 %q (fix|replace|keep|drop)", action)
	}
	if err := os.Remove(p); err != nil {
		return "", err
	}
	ids := []string{id}
	if target != "" {
		ids = append(ids, target)
	}
	finish(e, "pending", action, action+": "+id, ids...)
	return msg, nil
}
