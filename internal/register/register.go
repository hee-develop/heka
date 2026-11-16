// Package register 는 항목 등록(검증, 인용 대조, 비슷한 항목 찾기, 쓰기)과 대기함의 결정을 맡는다.
// 등록 자체는 거절하지 않는다. 비슷한 항목이 있으면 대기함(items/_pending/)에 두고 결정을 받는다.
package register

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hee-develop/heka/internal/claudecode"
	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/index"
	"github.com/hee-develop/heka/internal/item"
	"github.com/hee-develop/heka/internal/ledger"
	"github.com/hee-develop/heka/internal/session"
)

// similarLexical 은 새 항목의 text 를 질의로 한 낱말 점수가 이 값 이상이면 비슷한 항목으로 본다. 잠정.
const similarLexical = 0.5

// quoteWindow 는 인용을 대조할 세션 상태 파일과 기록 파일의 수정 시각 범위다.
const quoteWindow = 24 * time.Hour

// Env 는 등록이 쓰는 환경이다.
type Env struct {
	Home string
	Cfg  *config.Config
	Now  time.Time
}

// Input 은 등록할 항목의 필드다. Checked 는 사람이 명령줄에서 직접 줄 때만 채우고 MCP 로는 받지 않는다.
type Input struct {
	ID      string      `json:"id"`
	Text    string      `json:"text"`
	Must    bool        `json:"must"`
	When    item.When   `json:"when"`
	About   []string    `json:"about"`
	Scope   []string    `json:"scope"`
	Source  string      `json:"source"`
	Quote   string      `json:"quote"`
	Expires string      `json:"expires"`
	Confirm string      `json:"confirm"`
	Check   *item.Check `json:"check"`
	Body    string      `json:"body"`
	Checked string      `json:"-"`
}

const pendingDir = "items/_pending"

// FromItem 은 읽은 항목 파일을 등록 입력으로 바꾼다.
func FromItem(it *item.Item) Input {
	return Input{ID: it.ID, Text: it.Text, Must: it.Must, When: it.When, About: it.About, Scope: it.Scope, Source: it.Source,
		Quote: it.Quote, Checked: string(it.Checked), Expires: string(it.Expires), Confirm: string(it.Confirm), Check: it.Check, Body: it.Body}
}

// Candidate 는 비슷한 기존 항목이다.
type Candidate struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Reason string `json:"reason"`
}

// Result 는 등록 결과다. Status 는 added|pending|invalid 이다.
type Result struct {
	Status          string      `json:"status"`
	ID              string      `json:"id"`
	Problems        []string    `json:"problems,omitempty"`
	Candidates      []Candidate `json:"candidates,omitempty"`
	QuoteUnverified bool        `json:"quote_unverified,omitempty"`
}

// Text 는 결과를 읽는 글로 만든다.
func (r Result) Text() string {
	var b strings.Builder
	switch r.Status {
	case "invalid":
		b.WriteString("등록하지 않았습니다. 고칠 문제:\n")
	case "pending":
		fmt.Fprintf(&b, "비슷한 항목이 있어 대기함에 뒀습니다(%s). 결정하기 전에는 주입되지 않습니다. 비슷한 항목:\n", r.ID)
		for _, c := range r.Candidates {
			fmt.Fprintf(&b, "- %s: %s (%s)\n", c.ID, c.Text, c.Reason)
		}
		b.WriteString("사용자에게 고치기·바꾸기·따로 두기 중 무엇으로 할지 물어 주세요. 정해지면 heka_pending 으로 fix(고치기)·replace(바꾸기)·keep(따로 두기, 이유 필요)을 부르고, 필요 없으면 drop 입니다.\n")
	default:
		fmt.Fprintf(&b, "등록했습니다: %s\n", r.ID)
	}
	for _, p := range r.Problems {
		b.WriteString("- " + p + "\n")
	}
	if r.QuoteUnverified {
		b.WriteString("인용을 최근 세션 기록에서 확인하지 못해 quote 를 비우고 본문에 적었습니다. 사람이 확인하기 전에는 주입되지 않습니다.\n")
	}
	return b.String()
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Add 는 항목 하나를 등록한다. 입력이 틀렸으면 쓰지 않고 문제를 돌려준다.
func Add(e Env, in Input) (Result, error) {
	res := Result{ID: in.ID}
	if in.Source == "user" && in.Quote != "" && !quoteSeen(e, in.Quote) {
		line := fmt.Sprintf("확인되지 않은 인용: %q", in.Quote)
		in.Body = strings.TrimRight(in.Body, "\n")
		if in.Body != "" {
			in.Body += "\n\n"
		}
		in.Body += line + "\n"
		in.Quote, res.QuoteUnverified = "", true
	}
	it, err := validate(in)
	if err != nil {
		return res, err
	}
	for _, p := range it.Problems {
		res.Problems = append(res.Problems, p.Level+": "+p.Msg)
	}
	if !it.Valid() {
		res.Status = "invalid"
		return res, nil
	}
	res.Candidates, err = similar(e, it)
	if err != nil {
		return res, err
	}
	if len(res.Candidates) == 0 {
		res.Status = "added"
		err = write(e, "items", in)
	} else {
		res.Status = "pending"
		err = write(e, pendingDir, in)
	}
	if err != nil {
		return res, err
	}
	finish(e, "add", res.Status, "add: "+it.ID, it.ID)
	return res, nil
}

// validate 는 입력을 항목 파일 모양으로 만들어 item 의 검증을 그대로 적용한다.
func validate(in Input) (*item.Item, error) {
	dir, err := os.MkdirTemp("", "heka-add")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	name := in.ID
	if !idRe.MatchString(name) {
		name = "invalid"
	}
	p := filepath.Join(dir, name+".md")
	if err := os.WriteFile(p, render(in), 0o644); err != nil {
		return nil, err
	}
	return item.Parse(p)
}

// quoteSeen 은 최근 24시간 안에 고쳐진 세션의 기록에서 사용자가 친 글에 quote 가 들어 있는가. 공백은 정규화해 견준다.
// 볼 기록은 세션 상태 파일에 적힌 기록 파일과, 설정의 claude_code.transcripts 에 맞는 파일이다.
// 기록 파일이 없거나 못 읽으면 그 파일은 건너뛴다.
func quoteSeen(e Env, quote string) bool {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	q := norm(quote)
	if q == "" {
		return false
	}
	since := e.Now.Add(-quoteWindow)
	paths := session.RecentTranscripts(e.Home, since)
	var prefixes []string
	if e.Cfg != nil {
		prefixes = e.Cfg.ClaudeCode.ToolPromptPrefixes
		for _, p := range claudecode.Transcripts(e.Cfg.ClaudeCode.Transcripts, since) {
			if !slices.Contains(paths, p) {
				paths = append(paths, p)
			}
		}
	}
	for _, p := range paths {
		texts, _ := claudecode.UserTexts(p, prefixes) // 못 읽은 파일은 읽은 데까지만 본다
		if slices.ContainsFunc(texts, func(t string) bool { return strings.Contains(norm(t), q) }) {
			return true
		}
	}
	return false
}

// similar 는 새 항목과 비슷한 기존 항목을 id 순으로 돌려준다.
func similar(e Env, it *item.Item) ([]Candidate, error) {
	if err := index.Ensure(e.Home, e.Cfg); err != nil {
		return nil, err
	}
	ix, err := index.Open(e.Home)
	if err != nil {
		return nil, err
	}
	defer ix.Close()
	ranked, err := ix.Lexical(it.Text, math.MaxInt32)
	if err != nil {
		return nil, err
	}
	score := map[string]float64{}
	for _, s := range ranked {
		score[s.ID] = s.Score
	}
	files, _ := filepath.Glob(filepath.Join(e.Home, "items", "*.md"))
	var out []Candidate
	seen := map[string]bool{}
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".md")
		seen[id] = true
		var why []string
		r, err := ix.Item(id)
		if err != nil { // 색인에 없는(검증 실패) 파일은 같은 id 일 때만 본다
			if id == it.ID {
				out = append(out, Candidate{ID: id, Reason: "같은 id 의 파일이 있음"})
			}
			continue
		}
		if id == it.ID {
			why = append(why, "같은 id")
		}
		if sc := overlap(it.Scope, r.Scope); len(sc) > 0 {
			if ac := overlap(it.When.Action, r.When["action"]); len(ac) > 0 {
				why = append(why, "범위("+strings.Join(sc, ", ")+")와 동작("+strings.Join(ac, ", ")+")이 겹침")
			}
		}
		if ab := overlapNorm(it.About, r.About); len(ab) > 0 {
			why = append(why, "주제어가 같음("+strings.Join(ab, ", ")+")")
		}
		if score[id] >= similarLexical {
			why = append(why, fmt.Sprintf("낱말 점수 %.2f", score[id]))
		}
		if len(why) > 0 {
			out = append(out, Candidate{ID: id, Text: strings.TrimSpace(r.Text), Reason: strings.Join(why, "; ")})
		}
	}
	slices.SortFunc(out, func(a, b Candidate) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func overlap(a, b []string) []string {
	var out []string
	for _, x := range a {
		if slices.Contains(b, x) && !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

func overlapNorm(a, b []string) []string {
	var out []string
	for _, x := range a {
		for _, y := range b {
			if index.Norm(x) == index.Norm(y) && !slices.Contains(out, x) {
				out = append(out, x)
			}
		}
	}
	return out
}

// write 는 항목을 home/dir/<id>.md 로 임시 파일에 쓴 뒤 이름을 바꿔 둔다.
func write(e Env, dir string, in Input) error {
	d := filepath.Join(e.Home, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d, ".tmp-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(render(in))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(d, in.ID+".md"))
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// finish 는 색인을 다시 만들고, 원장에 한 줄 남기고, 데이터 폴더가 git 저장소면 커밋을 시도한다.
// 모두 실패해도 호출한 일은 성공이다(자동화를 멈추지 않는다).
func finish(e Env, event, moment, msg string, ids ...string) {
	row := ledger.Row{Event: event, Moment: moment}
	for _, id := range ids {
		row.Injected = append(row.Injected, ledger.Injected{ID: id})
	}
	finishRow(e, row, msg)
}

// finishRow 는 finish 와 같되 원장에 남길 줄을 직접 받는다. row.Event 가 오류 기록의 이벤트 이름이다.
func finishRow(e Env, row ledger.Row, msg string) {
	event := row.Event
	if err := index.Ensure(e.Home, e.Cfg); err != nil {
		ledger.LogError(e.Home, event, "색인을 다시 만들지 못함: "+err.Error(), e.Now)
	}
	if err := ledger.Append(e.Home, row, e.Now); err != nil {
		ledger.LogError(e.Home, event, "원장 쓰기 실패: "+err.Error(), e.Now)
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".git")); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, args := range [][]string{{"add", "-A", "--", "items"}, {"commit", "-m", msg, "--", "items"}} {
		if err := exec.CommandContext(ctx, "git", append([]string{"-C", e.Home}, args...)...).Run(); err != nil {
			ledger.LogError(e.Home, event, "git "+args[0]+" 실패: "+err.Error(), e.Now)
			return
		}
	}
}

// Retire 는 항목 파일(items/<id>.md)을 지우고 색인을 다시 만들고 원장에 한 줄(이벤트 retire, 사유) 남긴다.
// 데이터 폴더가 git 저장소이면 사유를 메시지로 커밋을 시도한다. 커밋이 실패해도 성공이다.
func Retire(e Env, id, reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if !idRe.MatchString(id) {
		return "", fmt.Errorf("id %q 가 ^[a-z0-9][a-z0-9-]*$ 에 맞지 않음", id)
	}
	if reason == "" {
		return "", errors.New("은퇴시키려면 사유(reason)가 필요함")
	}
	p := filepath.Join(e.Home, "items", id+".md")
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		return "", fmt.Errorf("항목 %q 가 없음", id)
	}
	if err := os.Remove(p); err != nil {
		return "", err
	}
	row := ledger.Row{Event: "retire", Moment: "retire", Reason: reason, Injected: []ledger.Injected{{ID: id}}}
	finishRow(e, row, reason)
	return fmt.Sprintf("항목 %s 을 지웠습니다. 사유: %s", id, reason), nil
}
