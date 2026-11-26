// Package session 은 세션 상태 파일($HEKA_HOME/sessions/<session_id>.json)을 읽고 쓴다.
// 훅은 이벤트마다 새 프로세스로 뜨고 여러 개가 동시에 돌 수 있으므로, 고치는 일은 잠금 안에서 한다.
// 잠금은 폴더 만들기(mkdir)이고 쓰기는 임시 파일을 만들어 이름을 바꾼다.
package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"
)

// 허용의 범위다.
const (
	Turn    = "turn"
	Session = "session"
)

const (
	lockRetry = 10 * time.Millisecond
	lockTries = 100             // 최대 1초 기다린다
	lockStale = 5 * time.Second // 이보다 오래된 잠금은 죽은 프로세스의 것으로 보고 치운다
	maxPend   = 16
)

// Delegation 은 위임 도구 호출에서 본 글이다. 서브에이전트가 시작될 때 꺼내 쓴다.
type Delegation struct {
	Agent string `json:"agent"`
	Text  string `json:"text"`
}

// State 는 세션 하나의 상태다. Shown 의 키는 주체의 agent_id 이고 메인은 빈 문자열이다.
type State struct {
	Shown      map[string][]string `json:"shown"`                 // 주체 → 이미 넣은 항목 id
	Grants     map[string]string   `json:"grants"`                // 동작 → turn|session
	Pending    []Delegation        `json:"pending"`               // 기다리는 위임. 오래된 것이 앞
	StopBlocks int                 `json:"stop_blocks,omitempty"` // 턴 종료 검사가 되돌린 횟수
	StopStreak map[string]int      `json:"stop_streak,omitempty"` // 항목 → 직전 턴 종료까지 연속으로 되돌린 횟수
	Calls      map[string][]string `json:"calls,omitempty"`       // 주체 → 이번 턴에 한 도구 호출 기록(`<도구>: <요약>`). 새 사용자 턴이 시작되면 비운다
	Transcript string              `json:"transcript,omitempty"`  // 이 세션의 기록 파일 경로. 등록할 때 인용 대조에 쓴다(프롬프트 글은 남기지 않는다)
}

// RecentTranscripts 는 since 이후에 고쳐진 세션 상태 파일들에 적힌 세션 기록 파일의 경로를 모은다.
// 같은 경로는 한 번만 담는다. 기록 파일이 있는지는 보지 않는다.
func RecentTranscripts(home string, since time.Time) []string {
	paths, _ := filepath.Glob(filepath.Join(home, "sessions", "*.json"))
	var out []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil || st.ModTime().Before(since) {
			continue
		}
		var s State
		if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, &s) == nil && s.Transcript != "" && !slices.Contains(out, s.Transcript) {
			out = append(out, s.Transcript)
		}
	}
	return out
}

// Seen 은 주체가 이미 받은 항목 id 집합이다.
func (s *State) Seen(agent string) map[string]bool {
	m := map[string]bool{}
	for _, id := range s.Shown[agent] {
		m[id] = true
	}
	return m
}

// Show 는 항목을 넣은 것으로 적는다. 이번에 처음 적었으면 참이다.
func (s *State) Show(agent, id string) bool {
	if slices.Contains(s.Shown[agent], id) {
		return false
	}
	s.Shown[agent] = append(s.Shown[agent], id)
	return true
}

// GrantFor 는 동작들 중 허용이 있는 첫 동작을 돌려준다. 없으면 빈 문자열이다.
// 상위 동작의 허용이 하위 동작에 통하도록 호출자가 상위 동작을 함께 넘긴다.
func (s *State) GrantFor(actions ...string) string {
	for _, a := range actions {
		if s.Grants[a] != "" {
			return a
		}
	}
	return ""
}

// Grant 는 허용을 적는다. 이미 session 허용인 것을 turn 으로 낮추지 않는다.
func (s *State) Grant(action, scope string) {
	if s.Grants[action] != Session {
		s.Grants[action] = scope
	}
}

// 턴의 도구 호출 기록 한도다. 한 줄은 글자(룬) 수, 한 턴은 줄 수이고 넘치면 오래된 줄부터 버린다.
const (
	MaxCallChars = 1000
	MaxCallLines = 64
)

// RecordCall 은 주체가 이번 턴에 한 도구 호출을 한 줄 더한다. 줄이 너무 길면 자르고, 줄이 넘치면 오래된 것부터 버린다.
func (s *State) RecordCall(agent, line string) {
	if r := []rune(line); len(r) > MaxCallChars {
		line = string(r[:MaxCallChars])
	}
	if s.Calls == nil {
		s.Calls = map[string][]string{}
	}
	c := append(s.Calls[agent], line)
	if len(c) > MaxCallLines {
		c = c[len(c)-MaxCallLines:]
	}
	s.Calls[agent] = c
}

// EndTurn 은 turn 허용과 이번 턴의 도구 호출 기록을 모두 지운다.
func (s *State) EndTurn() {
	s.Calls = nil
	for a, sc := range s.Grants {
		if sc == Turn {
			delete(s.Grants, a)
		}
	}
}

// Delegate 는 기다리는 위임을 더한다. 넘치면 오래된 것부터 버린다.
func (s *State) Delegate(d Delegation) {
	s.Pending = append(s.Pending, d)
	if len(s.Pending) > maxPend {
		s.Pending = s.Pending[len(s.Pending)-maxPend:]
	}
}

// TakeDelegation 은 에이전트 종류가 같은 가장 오래된 위임을 꺼낸다. 없으면 ok 가 거짓이다.
func (s *State) TakeDelegation(agent string) (d Delegation, ok bool) {
	i := slices.IndexFunc(s.Pending, func(p Delegation) bool { return p.Agent == agent })
	if i < 0 {
		return d, false
	}
	d = s.Pending[i]
	s.Pending = slices.Delete(s.Pending, i, i+1)
	return d, true
}

// 턴 종료 검사가 한 세션에서 되돌릴 수 있는 최대 횟수와, 같은 항목이 연속으로 되돌릴 수 있는 최대 횟수다.
const (
	MaxStopBlocks = 3
	MaxStopRun    = 2
)

// StopDecide 는 턴 종료 검사에 걸린 block 항목 id 들 가운데 이번에 되돌릴 것을 정하고 횟수를 적는다.
// 세션 전체로 MaxStopBlocks 회까지만 되돌리고, 직전 턴 종료에서도 되돌린 항목이 이미 MaxStopRun 번 연속이면 되돌리지 않는다.
// 되돌리지 않은 항목은 limited 로 돌려준다. 되돌린 항목이 없으면 연속 기록을 지운다.
func (s *State) StopDecide(ids []string) (blocked, limited []string) {
	next := map[string]int{}
	if s.StopBlocks < MaxStopBlocks {
		for _, id := range ids {
			if s.StopStreak[id] >= MaxStopRun {
				limited = append(limited, id)
				continue
			}
			blocked = append(blocked, id)
			next[id] = s.StopStreak[id] + 1
		}
	} else {
		limited = ids
	}
	if len(blocked) > 0 {
		s.StopBlocks++
	}
	s.StopStreak = next
	return blocked, limited
}

func empty() *State {
	return &State{Shown: map[string][]string{}, Grants: map[string]string{}}
}

var unsafeRe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// Path 는 상태 파일 경로다. session_id 는 파일 이름으로 쓰기 전에 안전한 글자만 남긴다.
func Path(home, id string) string {
	name := unsafeRe.ReplaceAllString(id, "_")
	if name == "" || name == "." || name == ".." {
		name = "_" + name
	}
	return filepath.Join(home, "sessions", name+".json")
}

// Load 는 상태를 읽는다. 없거나 깨졌으면 빈 상태다.
func Load(home, id string) *State {
	s := empty()
	b, err := os.ReadFile(Path(home, id))
	if err != nil || json.Unmarshal(b, s) != nil {
		return empty()
	}
	if s.Shown == nil {
		s.Shown = map[string][]string{}
	}
	if s.Grants == nil {
		s.Grants = map[string]string{}
	}
	return s
}

// LoadExisting 은 Load 와 같되, 상태 파일이 없거나 읽지 못하거나 깨졌으면 ok 가 거짓이다.
// 상태를 모른다는 것과 빈 상태를 구별해야 할 때 쓴다.
func LoadExisting(home, id string) (s *State, ok bool) {
	if id == "" {
		return empty(), false
	}
	b, err := os.ReadFile(Path(home, id))
	if err != nil {
		return empty(), false
	}
	s = empty()
	if json.Unmarshal(b, s) != nil {
		return empty(), false
	}
	if s.Shown == nil {
		s.Shown = map[string][]string{}
	}
	if s.Grants == nil {
		s.Grants = map[string]string{}
	}
	return s, true
}

// Update 는 잠금을 잡고 파일을 다시 읽어 fn 으로 고친 뒤 원자적으로 쓴다.
// 잠금을 못 잡으면 fn 을 부르지 않고 오류를 돌려준다.
func Update(home, id string, fn func(*State)) error {
	file := Path(home, id)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	lock := file + ".lock"
	if err := acquire(lock); err != nil {
		return err
	}
	defer os.Remove(lock)
	s := Load(home, id)
	fn(s)
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := file + "." + strconv.Itoa(os.Getpid()) + "." + strconv.FormatInt(time.Now().UnixNano(), 36) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, file); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func acquire(lock string) error {
	for range lockTries {
		err := os.Mkdir(lock, 0o755)
		if err == nil {
			return nil
		}
		if st, e := os.Stat(lock); e == nil && time.Since(st.ModTime()) > lockStale {
			_ = os.Remove(lock)
			continue
		}
		time.Sleep(lockRetry)
	}
	return errors.New("세션 상태 잠금을 잡지 못함")
}
