package review

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/hee-develop/heka/internal/judge"
	"github.com/hee-develop/heka/internal/ledger"
	"github.com/hee-develop/heka/internal/register"
)

const (
	sampleInterval = 7 * 24 * time.Hour
	notifyTimeout  = 30 * time.Second
)

// ConflictText 는 모순 판정을 곧바로 알리는 글이다. 모순이 여러 건이면 건수를 적는다.
func ConflictText(newID string, targets []string) string {
	if len(targets) == 1 {
		return fmt.Sprintf("heka: 새 항목 %s 가 %s 과 모순으로 판정됐습니다. 세션에서 'heka 검토해줘', 터미널에서 heka review", newID, targets[0])
	}
	return fmt.Sprintf("heka: 새 항목 %s 가 %d건과 모순으로 판정됐습니다. 세션에서 'heka 검토해줘', 터미널에서 heka review", newID, len(targets))
}

// PendingText 는 검토 대기를 알리는 글이다.
func PendingText(count, days int) string {
	return fmt.Sprintf("heka 검토 대기 %d건 (가장 오래된 것 %d일). 세션에서 'heka 검토해줘', 터미널에서 heka review", count, days)
}

// Notify 는 알림 명령(review.notify_command)에 글을 마지막 인자로 덧붙여 부른다. 명령이 비어 있으면 out 에만 쓴다.
// 원장에 review_notify 줄을 남긴다. moment 는 알림의 까닭(conflict|pending)이다.
func Notify(e register.Env, out io.Writer, text, moment string, count int) error {
	var cmd []string
	if e.Cfg != nil {
		cmd = slices.Clone(e.Cfg.Review.NotifyCommand)
	}
	var err error
	via := "stdout"
	if len(cmd) == 0 {
		fmt.Fprintln(out, text)
	} else {
		via = "command"
		_, err = judge.RunCommand(context.Background(), append(cmd, text), notifyTimeout, "")
		if err != nil {
			ledger.LogError(e.Home, "review_notify", "알림 명령 실패: "+err.Error(), e.Now)
			via = "error"
		}
	}
	row := ledger.Row{Event: "review_notify", Moment: moment + ":" + via, Reason: text, Count: count}
	if aerr := ledger.Append(e.Home, row, e.Now); aerr != nil {
		ledger.LogError(e.Home, "review_notify", "원장 쓰기 실패: "+aerr.Error(), e.Now)
	}
	return err
}

type sampleState struct {
	Last string `json:"last"`
}

// Sample 은 표본을 올릴 때가 됐으면(마지막 표본이 7일 넘었거나 없으면) 지난 7일 원장의 register_judge 줄에서
// separate 로 판정된 (새, 후보) 쌍 중 두 항목이 다 있고 아직 검토 항목이 아닌 것을 n 건 무작위로 골라 sample 로 올린다.
// 올린 수를 돌려준다. 때가 아니면 0 이다.
func Sample(e register.Env, n int, rng *rand.Rand) (int, error) {
	state := dir(e.Home) + "/" + sampleStateFn
	if b, err := os.ReadFile(state); err == nil {
		var s sampleState
		if json.Unmarshal(b, &s) == nil {
			if last, err := time.Parse(timeLayout, s.Last); err == nil && e.Now.Sub(last) <= sampleInterval {
				return 0, nil
			}
		}
	}
	since := e.Now.Add(-sampleInterval)
	type pair struct{ n, t, reason string }
	var pairs []pair
	seen := map[string]bool{}
	for _, r := range ledger.ReadAll(e.Home) {
		if r.Event != "register_judge" || r.RegJudge == nil || len(r.Injected) == 0 {
			continue
		}
		if ts, err := time.Parse("2006-01-02T15:04:05.000Z", r.TS); err != nil || ts.Before(since) {
			continue
		}
		newID := r.Injected[0].ID
		for _, v := range r.RegJudge.Verdicts {
			id := EntryID(newID, v.ID)
			if v.Verdict != "separate" || seen[id] || !itemExists(e.Home, newID) || !itemExists(e.Home, v.ID) {
				continue
			}
			if _, err := os.Stat(entryPath(e.Home, id)); err == nil {
				continue
			}
			seen[id] = true
			pairs = append(pairs, pair{newID, v.ID, v.Reason})
		}
	}
	slices.SortFunc(pairs, func(a, b pair) int { return strings.Compare(EntryID(a.n, a.t), EntryID(b.n, b.t)) })
	rng.Shuffle(len(pairs), func(i, j int) { pairs[i], pairs[j] = pairs[j], pairs[i] })
	if len(pairs) > n {
		pairs = pairs[:n]
	}
	created := e.Now.UTC().Format(timeLayout)
	for _, p := range pairs {
		en := Entry{ID: EntryID(p.n, p.t), Kind: KindSample, NewID: p.n, TargetID: p.t, Reason: p.reason, Created: created}
		if err := Save(e.Home, en); err != nil {
			return 0, err
		}
	}
	b, _ := json.Marshal(sampleState{Last: created})
	if err := writeAtomic(dir(e.Home), sampleStateFn, append(b, '\n')); err != nil {
		return len(pairs), err
	}
	return len(pairs), nil
}

// CheckAndNotify 는 `heka review --notify` 의 일이다. 표본을 올리고, 건수가 notify_min 이상이거나
// 가장 오래된 것이 notify_days 일 이상이면 알린다. 알렸는지를 돌려준다.
func CheckAndNotify(e register.Env, out io.Writer, rng *rand.Rand) (bool, error) {
	rv := e.Cfg.Review
	sampled, serr := Sample(e, rv.Sample, rng)
	entries := List(e)
	days := OldestDays(entries, e.Now)
	notify := len(entries) > 0 && (len(entries) >= rv.NotifyMin || days >= rv.NotifyDays)
	if !notify {
		fmt.Fprintf(out, "검토 대기 %d건. 알림 기준에 못 미칩니다.\n", len(entries))
		row := ledger.Row{Event: "review_notify", Moment: "skip", Count: len(entries)}
		_ = ledger.Append(e.Home, row, e.Now)
	} else if err := Notify(e, out, PendingText(len(entries), days), "pending", len(entries)); err != nil {
		return true, err
	}
	if sampled > 0 {
		register.FinishRow(e, ledger.Row{Event: "review", Moment: "sample", Count: sampled}, fmt.Sprintf("review: 표본 %d건", sampled))
	}
	return notify, serr
}
