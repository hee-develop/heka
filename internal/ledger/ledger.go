// Package ledger 는 원장($HEKA_HOME/ledger/YYYY-MM.jsonl)에 줄을 덧붙인다.
// 한 줄은 훅 한 번의 판단이다. 쓰기에 실패해도 호출자는 작업을 계속한다.
package ledger

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// Row 는 원장 한 줄이다.
type Row struct {
	TS         string     `json:"ts"`
	Session    string     `json:"session"`
	Agent      string     `json:"agent,omitempty"` // 서브에이전트 안의 호출이면 agent_id
	Event      string     `json:"event"`
	Moment     string     `json:"moment"`
	ToolMade   bool       `json:"tool_made,omitempty"` // 도구가 만든 프롬프트라 건너뜀
	Actions    []string   `json:"actions"`             // 도구 호출을 해석한 동작 이름
	Injected   []Injected `json:"injected"`
	Dropped    []Dropped  `json:"dropped"`
	Asked      []string   `json:"asked"`       // 프롬프트가 시킨 동작
	Refused    []string   `json:"refused"`     // 프롬프트가 하지 말라고 한 동작
	GrantsUsed []string   `json:"grants_used"` // 확인 없이 진행하게 한 허용(동작)
	Confirm    []string   `json:"confirm"`     // 확인을 요구한 항목 id
	DurationMS int64      `json:"duration_ms"`
	Prompt     string     `json:"prompt,omitempty"`
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
