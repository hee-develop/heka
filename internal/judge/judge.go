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
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, cfg.Command[0], cfg.Command[1:]...)
	cmd.Stdin = strings.NewReader(in)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.WaitDelay = time.Second // 하위 프로세스가 출력 통로를 붙잡아도 기다리지 않는다.
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return false, "", fmt.Errorf("%w: %w", errTimeout, ctx.Err())
		}
		return false, "", fmt.Errorf("판정 명령 실패: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	return parse(out.String())
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
