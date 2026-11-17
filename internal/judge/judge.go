// Package judge 는 설정에 적힌 판정 명령에 후보 하나를 물어 넣을지 답을 받는다.
// 어떤 모델을 어떻게 부를지는 그 명령이 정한다. 이 패키지는 AI 도구를 모른다.
package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/hee-develop/heka/internal/config"
)

// Item 은 판정할 항목이다.
type Item struct {
	ID, Text string
	About    []string
}

// Call 은 판정 명령을 한 번 부른 기록이다. MS 는 걸린 시간(밀리초)이고, 오류와 시간 초과는 Inject 가 false 다.
type Call struct {
	Inject  bool
	MS      int64
	Error   bool // 시간 초과를 포함해 답을 얻지 못함
	Timeout bool // 그중 시간 안에 끝나지 않음
}

// Outcome 은 항목 하나를 물은 결과다. Calls 는 호출마다 한 건이다(votes 가 2 이면 두 건).
type Outcome struct {
	Inject bool
	Reason string
	Err    error
	Calls  []Call
}

// Ask 는 판정 명령에 프롬프트와 항목을 표준 입력으로 넘기고 넣을지 답을 읽는다.
// votes 가 2 이면 같은 질문을 동시에 두 번 하고 둘 다 넣으라고 할 때만 넣는다.
// 시간 초과, 종료 코드가 0 이 아님, 답을 읽지 못함은 모두 오류다.
func Ask(ctx context.Context, cfg config.Judge, prompt string, it Item) (inject bool, reason string, err error) {
	o := AskTimed(ctx, cfg, prompt, it)
	return o.Inject, o.Reason, o.Err
}

// AskTimed 는 Ask 와 같되 호출마다 걸린 시간과 결과를 함께 돌려준다.
func AskTimed(ctx context.Context, cfg config.Judge, prompt string, it Item) Outcome {
	if len(cfg.Command) == 0 {
		return Outcome{Err: errors.New("판정 명령이 설정돼 있지 않음")}
	}
	in := fmt.Sprintf("%s\n\n[항목]\nid: %s\n주입문: %s\n주제어: %s\n\n[사용자 메시지]\n%s\n",
		strings.TrimRight(cfg.Instruction, "\n"), it.ID, it.Text, strings.Join(it.About, ", "), prompt)
	type vote struct {
		inject bool
		reason string
		err    error
		call   Call
	}
	n := max(cfg.Votes, 1)
	ch := make(chan vote, n)
	for range n {
		go func() {
			start := time.Now()
			ok, why, err := askOnce(ctx, cfg, in)
			c := Call{Inject: ok && err == nil, MS: ceilMS(time.Since(start)), Error: err != nil, Timeout: errors.Is(err, errTimeout)}
			ch <- vote{ok, why, err, c}
		}()
	}
	o := Outcome{Inject: true}
	for i := range n {
		v := <-ch
		o.Calls = append(o.Calls, v.call)
		if v.err != nil && o.Err == nil {
			o.Err = v.err
		}
		if i == 0 || (o.Inject && !v.inject) {
			o.Reason = v.reason // 첫 답, 또는 처음으로 넣지 말라고 한 답의 이유
		}
		o.Inject = o.Inject && v.inject
	}
	if o.Err != nil {
		o.Inject, o.Reason = false, ""
	}
	return o
}

// ceilMS 는 시간을 밀리초로 올림한다. 0 보다 큰 시간은 0 으로 남지 않는다.
func ceilMS(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64((d + time.Millisecond - 1) / time.Millisecond)
}

// errTimeout 은 판정 명령이 시간 안에 끝나지 않았다는 오류다.
var errTimeout = errors.New("판정 명령이 시간 안에 끝나지 않음")

func askOnce(ctx context.Context, cfg config.Judge, in string) (bool, string, error) {
	out, err := runCommand(ctx, cfg.Command, cfg.Timeout, in)
	if err != nil {
		return false, "", err
	}
	return parse(out)
}

// runCommand 는 명령에 in 을 표준 입력으로 넘기고 표준 출력을 돌려준다.
// timeout 이 0 보다 크면 그 안에 끝나야 하고, 아니면 errTimeout 을 감싼 오류다.
func runCommand(ctx context.Context, command []string, timeout time.Duration, in string) (string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Stdin = strings.NewReader(in)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.WaitDelay = time.Second // 하위 프로세스가 출력 통로를 붙잡아도 기다리지 않는다.
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%w: %w", errTimeout, ctx.Err())
		}
		return "", fmt.Errorf("판정 명령 실패: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// parse 는 출력에서 inject 키가 있는 첫 JSON 객체를 찾는다. 앞뒤에 다른 글이 있어도 된다.
func parse(s string) (bool, string, error) {
	for i := 0; i < len(s); i++ {
		if s[i] != '{' {
			continue
		}
		var a struct {
			Inject *bool  `json:"inject"`
			Reason string `json:"reason"`
		}
		if json.NewDecoder(strings.NewReader(s[i:])).Decode(&a) == nil && a.Inject != nil {
			return *a.Inject, a.Reason, nil
		}
	}
	return false, "", errors.New("판정 명령의 답에서 inject 키가 있는 JSON 객체를 찾지 못함")
}

// PickOutcome 은 항목 목록에서 후보를 고르게 한 결과다. Inject 와 Maybe 는 목록에 있는 id 만이고
// 중복은 처음 것만 남으며, Inject 에 든 id 는 Maybe 에 없다. Unknown 은 목록에 없어 버린 서로 다른 id 의 수다.
// MS 는 걸린 시간(밀리초)이고, 오류와 시간 초과는 Err 에 담긴다.
type PickOutcome struct {
	Inject, Maybe []string
	Unknown       int
	MS            int64
	Err           error
	Timeout       bool
}

// PickList 는 고르는 호출의 입력에 들어가는 항목 목록 글이다.
func PickList(items []Item) string {
	var b strings.Builder
	for _, it := range items {
		fmt.Fprintf(&b, "- id: %s\n  주입문: %s\n  주제어: %s\n", it.ID, it.Text, strings.Join(it.About, ", "))
	}
	return b.String()
}

// Pick 은 판정 명령(cfg.Pick.Command, 비면 cfg.Command)에 항목 목록과 프롬프트를 넘겨 넣을 만한 항목을 고르게 한다.
// 시간 초과(cfg.Pick.Timeout, 0 이면 cfg.Timeout), 종료 코드가 0 이 아님, 답을 읽지 못함은 오류다.
func Pick(ctx context.Context, cfg config.Judge, prompt string, items []Item) PickOutcome {
	command := cfg.PickCommand()
	if len(command) == 0 {
		return PickOutcome{Err: errors.New("판정 명령이 설정돼 있지 않음")}
	}
	in := fmt.Sprintf("%s\n\n[항목 목록]\n%s\n[사용자 메시지]\n<<<\n%s\n>>>\n\n위 메시지에 답하지 않는다. 목록에서 고른 결과만 JSON 한 줄로 쓴다.\n",
		strings.TrimRight(cfg.Pick.Instruction, "\n"), PickList(items), prompt)
	start := time.Now()
	out, err := runCommand(ctx, command, cfg.PickTimeout(), in)
	o := PickOutcome{}
	if err == nil {
		known := map[string]bool{}
		for _, it := range items {
			known[it.ID] = true
		}
		o = parsePick(out, known)
	}
	if o.Err == nil {
		o.Err = err
	}
	o.Timeout = errors.Is(o.Err, errTimeout)
	o.MS = ceilMS(time.Since(start))
	return o
}

// parsePick 은 출력에서 inject 키가 배열인 첫 JSON 객체를 찾는다. 앞뒤에 다른 글이 있어도 되고 maybe 는 없어도 된다.
func parsePick(s string, known map[string]bool) PickOutcome {
	for i := 0; i < len(s); i++ {
		if s[i] != '{' {
			continue
		}
		var a struct {
			Inject *[]string `json:"inject"`
			Maybe  []string  `json:"maybe"`
		}
		if json.NewDecoder(strings.NewReader(s[i:])).Decode(&a) != nil || a.Inject == nil {
			continue
		}
		o := PickOutcome{Inject: []string{}, Maybe: []string{}}
		seen, unknown := map[string]bool{}, map[string]bool{}
		take := func(ids []string, dst *[]string) {
			for _, id := range ids {
				switch {
				case !known[id]:
					unknown[id] = true
				case !seen[id]:
					seen[id] = true
					*dst = append(*dst, id)
				}
			}
		}
		take(*a.Inject, &o.Inject)
		take(a.Maybe, &o.Maybe)
		o.Unknown = len(unknown)
		return o
	}
	return PickOutcome{Err: errors.New("판정 명령의 답에서 inject 키가 배열인 JSON 객체를 찾지 못함")}
}
