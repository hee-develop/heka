// Package ledger 는 원장($HEKA_HOME/ledger/YYYY-MM.jsonl)에 줄을 덧붙인다.
// 한 줄은 훅 한 번의 판단이다. 쓰기에 실패해도 호출자는 작업을 계속한다.
package ledger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	promptRunes = 200 // 프롬프트는 앞 200자만 남긴다
	maxDrops    = 30  // 한 줄이 너무 길어지지 않게 탈락은 이만큼만 남긴다
)

// Injected 는 넣은 항목이다. Hash 는 그때 항목의 내용 해시다.
type Injected struct {
	ID   string `json:"id"`
	Hash string `json:"hash"`
	Why  string `json:"why"`
}

// Dropped 는 탈락한 항목이다.
type Dropped struct {
	ID     string `json:"id"`
	Stage  string `json:"stage"`
	Reason string `json:"reason"`
}

// Checked 는 결과물 검사 하나의 결과다. Result 는 pass|fail|skip 이고,
// skip 은 검사할 글을 뽑지 못했거나 식을 쓸 수 없어 판정하지 않았다는 뜻이고, Note 에 그 이유를 짧게 남긴다
// (대상 없음, 변수를 풀지 못함, 경로를 풀지 못함, 같은 명령에서 파일을 고침, 파일을 읽지 못함, 식을 쓸 수 없음).
// 파일의 내용이나 경로는 남기지 않는다.
// Blocked 는 막는 판정이 났는가(그림자 방식에서는 실제로 막지는 않았지만 막았을 판정을 뜻한다).
type Checked struct {
	ID      string `json:"id"`
	Target  string `json:"target"`
	Result  string `json:"result"`
	Blocked bool   `json:"blocked"`
	Note    string `json:"note,omitempty"`
}

// Judged 는 판정 명령을 한 번 부른 기록이다. MS 는 걸린 시간(밀리초)이고,
// Error 는 시간 초과를 포함해 답을 얻지 못했다는 뜻, Timeout 은 그중 시간 안에 끝나지 않았다는 뜻이다.
// votes 가 2 이면 호출마다 한 건이 남는다.
type Judged struct {
	ID      string `json:"id"`
	Inject  bool   `json:"inject"`
	MS      int64  `json:"ms"`
	Error   bool   `json:"error,omitempty"`
	Timeout bool   `json:"timeout,omitempty"`
}

// Pick 은 모델이 항목 목록에서 후보를 고른 기록이다(judge.pick.mode: record). 무엇을 넣을지에는 쓰이지 않는다.
// Listed 는 모델에게 준 항목 수, ListTokens 는 목록 글의 토큰 어림, MS 는 고르는 호출에 걸린 시간(밀리초)이다.
// Error 는 시간 초과를 포함해 답을 얻지 못했다는 뜻, Timeout 은 그중 시간 안에 끝나지 않았다는 뜻,
// Unknown 은 목록에 없는 id 를 답한 수다.
type Pick struct {
	Listed     int      `json:"listed"`
	ListTokens int      `json:"list_tokens"`
	MS         int64    `json:"ms"`
	Error      bool     `json:"error,omitempty"`
	Timeout    bool     `json:"timeout,omitempty"`
	Unknown    int      `json:"unknown,omitempty"`
	Items      []Picked `json:"items"`
}

// Picked 는 모델이 고른 항목 하나다. Direct 는 바로 넣으라고 골랐는가(아니면 따져 볼 만하다고 고름),
// Judged 는 한 건씩 판정을 받았는가, Inject 는 그 판정의 답, Reused 는 지금 방식의 판정을 그대로 썼는가,
// MS 는 판정에 걸린 시간(다시 쓴 것은 0)이다.
type Picked struct {
	ID     string `json:"id"`
	Direct bool   `json:"direct"`
	Judged bool   `json:"judged"`
	Inject bool   `json:"inject"`
	Reused bool   `json:"reused,omitempty"`
	MS     int64  `json:"ms,omitempty"`
	Error  bool   `json:"error,omitempty"`
}

// Verdict 는 등록 판정이 후보 하나에 낸 답이다. Verdict 는 separate|merge|conflict|unjudged 이고,
// unjudged 는 답을 얻지 못했거나 쓸 수 없어 검토 목록으로 넘겼다는 뜻이다.
type Verdict struct {
	ID      string `json:"id"`
	Verdict string `json:"verdict"`
	Reason  string `json:"reason,omitempty"`
	// DraftProblem 은 merge 의 합친 초안이 기계 검사를 통과하지 못한 까닭이다. 이때 초안은 버려진다.
	DraftProblem string `json:"draft_problem,omitempty"`
}

// RegisterJudged 는 등록 판정(heka judge-add) 한 번의 기록이다. 새 항목 id 는 줄의 Injected 에 있다.
// MS 는 판정 명령을 부른 시간(밀리초), Error 는 시간 초과를 포함해 답을 얻지 못했다는 뜻,
// Timeout 은 그중 시간 안에 끝나지 않았다는 뜻이다.
type RegisterJudged struct {
	Verdicts []Verdict `json:"verdicts"`
	MS       int64     `json:"ms"`
	Error    bool      `json:"error,omitempty"`
	Timeout  bool      `json:"timeout,omitempty"`
}

// Row 는 원장 한 줄이다.
type Row struct {
	TS         string          `json:"ts"`
	Session    string          `json:"session"`
	Agent      string          `json:"agent,omitempty"` // 서브에이전트 안의 호출이면 agent_id
	Event      string          `json:"event"`
	Moment     string          `json:"moment"`
	ToolMade   bool            `json:"tool_made,omitempty"` // 도구가 만든 프롬프트라 건너뜀
	Actions    []string        `json:"actions"`             // 도구 호출을 해석한 동작 이름
	Injected   []Injected      `json:"injected"`
	Dropped    []Dropped       `json:"dropped"`
	Asked      []string        `json:"asked"`       // 프롬프트가 시킨 동작
	Refused    []string        `json:"refused"`     // 프롬프트가 하지 말라고 한 동작
	GrantsUsed []string        `json:"grants_used"` // 확인 없이 진행하게 한 허용(동작)
	Confirm    []string        `json:"confirm"`     // 확인을 요구한 항목 id
	DurationMS int64           `json:"duration_ms"`
	Tokens     int             `json:"tokens,omitempty"` // 넣은 글의 토큰 어림
	Reason     string          `json:"reason,omitempty"` // retire 의 사유
	Prompt     string          `json:"prompt,omitempty"`
	Checks     []Checked       `json:"checks,omitempty"`         // 결과물 검사 결과
	Judges     []Judged        `json:"judges,omitempty"`         // 판정 명령을 부른 기록
	Shadow     bool            `json:"shadow,omitempty"`         // 그림자 방식으로 돈 줄(아무것도 내보내지 않았다)
	Pick       *Pick           `json:"pick,omitempty"`           // 모델이 목록에서 후보를 고른 기록
	RegJudge   *RegisterJudged `json:"register_judge,omitempty"` // 등록 판정(register_judge 줄)의 기록
	Entry      string          `json:"entry,omitempty"`          // review 줄의 검토 항목 id
	Count      int             `json:"count,omitempty"`          // review_notify 줄의 검토 대기 건수
}

// IsHook 은 훅 이벤트(항목을 실제로 넣거나 뺀 판단)의 줄인가. find·add·flag·retire 등의 줄은 아니다.
func (r Row) IsHook() bool {
	switch r.Event {
	case "SessionStart", "UserPromptSubmit", "PreToolUse", "SubagentStart", "Stop":
		return true
	}
	return false
}

// Append 는 줄을 이번 달 파일에 한 번의 쓰기로 덧붙인다. 비어 있는 목록은 빈 배열로 쓴다.
func Append(home string, r Row, now time.Time) error {
	r.TS = now.UTC().Format("2006-01-02T15:04:05.000Z")
	if utf8.RuneCountInString(r.Prompt) > promptRunes {
		r.Prompt = string([]rune(r.Prompt)[:promptRunes])
	}
	if len(r.Dropped) > maxDrops {
		r.Dropped = r.Dropped[:maxDrops]
	}
	if r.Actions == nil {
		r.Actions = []string{}
	}
	if r.Injected == nil {
		r.Injected = []Injected{}
	}
	if r.Dropped == nil {
		r.Dropped = []Dropped{}
	}
	for _, p := range []*[]string{&r.Asked, &r.Refused, &r.GrantsUsed, &r.Confirm} {
		if *p == nil {
			*p = []string{}
		}
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return appendFile(filepath.Join(home, "ledger", now.UTC().Format("2006-01")+".jsonl"), append(b, '\n'))
}

// LogError 는 errors.log 에 오류 한 줄(시각, 이벤트, 내용)을 덧붙인다.
func LogError(home, event, msg string, now time.Time) {
	msg = strings.Join(strings.Fields(msg), " ")
	line := now.UTC().Format("2006-01-02T15:04:05.000Z") + "\t" + event + "\t" + msg + "\n"
	_ = appendFile(filepath.Join(home, "ledger", "errors.log"), []byte(line))
}

func appendFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(b)
	return err
}

// ReadAll 은 원장 폴더의 모든 월 파일(YYYY-MM.jsonl)을 달 순서로, 파일 안에서는 쓴 순서로 읽는다.
// 깨진 줄과 읽을 수 없는 파일은 건너뛴다. 원장이 없으면 빈 목록이다.
func ReadAll(home string) []Row {
	files, _ := filepath.Glob(filepath.Join(home, "ledger", "*.jsonl"))
	sort.Strings(files)
	var rows []Row
	for _, f := range files {
		rows = append(rows, readFile(f)...)
	}
	return rows
}

func readFile(path string) []Row {
	fh, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer fh.Close()
	r := bufio.NewReaderSize(fh, 1<<16)
	var rows []Row
	for {
		line, err := r.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var row Row
			if json.Unmarshal(line, &row) == nil && row.Event != "" {
				rows = append(rows, row)
			}
		}
		if err != nil { // EOF 이거나 읽기 오류. 어느 쪽이든 읽은 데까지만 쓴다
			return rows
		}
	}
}
