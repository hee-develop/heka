// Package review 는 검토 목록($HEKA_HOME/review/<entry-id>.json)을 맡는다.
// 등록 판정이 사람에게 올린 것(모순·합치기·미판정)과 주마다 뽑는 표본을 저장하고, 결정을 적용하고, 알린다.
package review

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hee-develop/heka/internal/item"
	"github.com/hee-develop/heka/internal/ledger"
	"github.com/hee-develop/heka/internal/register"
)

// 검토 항목의 종류다.
const (
	KindConflict  = "conflict"
	KindMerge     = "merge"
	KindUnjudged  = "unjudged"
	KindSample    = "sample"
	timeLayout    = time.RFC3339
	dirName       = "review"
	sampleStateFn = ".sample-state.json"
)

var kindRank = map[string]int{KindConflict: 0, KindMerge: 1, KindUnjudged: 2, KindSample: 3}

// Merged 는 판정 명령이 쓴 합친 항목의 초안이다.
type Merged struct {
	Text  string   `json:"text"`
	Body  string   `json:"body"`
	About []string `json:"about"`
}

// Entry 는 검토 항목 하나다. ID 는 `<new>--<target>` 또는 `<new>--unjudged` 이다.
// Candidates 는 unjudged 에서 판정을 얻지 못한 후보 id 들이다.
type Entry struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	NewID      string   `json:"new_id"`
	TargetID   string   `json:"target_id,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Merged     *Merged  `json:"merged,omitempty"`
	// DraftProblem 이 있으면 판정 명령의 합친 초안이 기계 검사를 통과하지 못해 Merged 를 비웠다는 뜻이다.
	DraftProblem string `json:"draft_problem,omitempty"`
	Created      string `json:"created"`
}

var entryIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// EntryID 는 검토 항목 id 다. target 이 비면 미판정이다.
func EntryID(newID, target string) string {
	if target == "" {
		target = "unjudged"
	}
	return newID + "--" + target
}

func dir(home string) string { return filepath.Join(home, dirName) }

func entryPath(home, id string) string { return filepath.Join(dir(home), id+".json") }

// Save 는 검토 항목을 임시 파일에 쓴 뒤 이름을 바꿔 둔다.
func Save(home string, en Entry) error {
	if !entryIDRe.MatchString(en.ID) {
		return fmt.Errorf("검토 항목 id %q 가 모양에 맞지 않음", en.ID)
	}
	b, err := json.MarshalIndent(en, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(dir(home), en.ID+".json", append(b, '\n'))
}

func writeAtomic(d, name string, b []byte) error {
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d, ".tmp-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(d, name))
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// Load 는 검토 항목 하나를 읽는다.
func Load(home, id string) (Entry, error) {
	var en Entry
	if !entryIDRe.MatchString(id) {
		return en, fmt.Errorf("검토 항목 id %q 가 모양에 맞지 않음", id)
	}
	b, err := os.ReadFile(entryPath(home, id))
	if err != nil {
		return en, fmt.Errorf("검토 항목 %q 가 없음", id)
	}
	if err := json.Unmarshal(b, &en); err != nil {
		return en, fmt.Errorf("검토 항목 %q 를 읽지 못함: %w", id, err)
	}
	return en, nil
}

// Remove 는 검토 항목 파일을 지운다. 없으면 아무것도 하지 않는다.
func Remove(home, id string) {
	if entryIDRe.MatchString(id) {
		_ = os.Remove(entryPath(home, id))
	}
}

// loadAll 은 검토 항목을 읽어 목록 순서로 돌려준다. 읽지 못하는 파일은 건너뛴다.
func loadAll(home string) []Entry {
	files, _ := filepath.Glob(filepath.Join(dir(home), "*.json"))
	var out []Entry
	for _, f := range files {
		if strings.HasPrefix(filepath.Base(f), ".") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var en Entry
		if json.Unmarshal(b, &en) != nil || en.ID == "" || en.ID+".json" != filepath.Base(f) {
			continue
		}
		out = append(out, en)
	}
	slices.SortFunc(out, func(a, b Entry) int {
		if ra, rb := kindRank[a.Kind], kindRank[b.Kind]; ra != rb {
			return ra - rb
		}
		if c := strings.Compare(a.Created, b.Created); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func itemExists(home, id string) bool {
	st, err := os.Stat(filepath.Join(home, "items", id+".md"))
	return err == nil && !st.IsDir()
}

// List 는 검토 목록을 순서대로(모순, 합치기, 미판정, 표본, 같으면 오래된 것, 같으면 id) 돌려준다.
// 새 항목이나 대상 항목 파일이 이미 없는 항목은 목록에서 빼고 지우며 원장에 남긴다.
func List(e register.Env) []Entry {
	var out []Entry
	for _, en := range loadAll(e.Home) {
		why := ""
		switch {
		case !itemExists(e.Home, en.NewID):
			why = "새 항목 " + en.NewID + " 가 이미 없음"
		case en.TargetID != "" && !itemExists(e.Home, en.TargetID):
			why = "대상 항목 " + en.TargetID + " 가 이미 없음"
		}
		if why == "" {
			out = append(out, en)
			continue
		}
		Remove(e.Home, en.ID)
		row := ledger.Row{Event: "review", Moment: "stale", Entry: en.ID, Reason: why}
		if err := ledger.Append(e.Home, row, e.Now); err != nil {
			ledger.LogError(e.Home, "review", "원장 쓰기 실패: "+err.Error(), e.Now)
		}
	}
	return out
}

// OldestDays 는 가장 오래된 항목의 나이(일)다. 항목이 없으면 0 이다.
func OldestDays(entries []Entry, now time.Time) int {
	days := 0
	for _, en := range entries {
		t, err := time.Parse(timeLayout, en.Created)
		if err != nil {
			continue
		}
		days = max(days, int(now.Sub(t).Hours()/24))
	}
	return days
}

// ItemView 는 검토 때 보이는 항목의 필드다.
type ItemView struct {
	ID     string    `json:"id"`
	Text   string    `json:"text"`
	Body   string    `json:"body"`
	Source string    `json:"source"`
	Quote  string    `json:"quote"`
	Scope  []string  `json:"scope"`
	About  []string  `json:"about"`
	Must   bool      `json:"must"`
	When   item.When `json:"when"`
}

// View 는 항목 파일을 읽어 보이는 꼴로 만든다. 읽지 못하면 nil 이다.
func View(home, id string) *ItemView {
	it, err := item.Parse(filepath.Join(home, "items", id+".md"))
	if err != nil {
		return nil
	}
	return &ItemView{ID: it.ID, Text: strings.TrimSpace(it.Text), Body: strings.TrimSpace(it.Body), Source: it.Source, Quote: it.Quote,
		Scope: nz(it.Scope), About: nz(it.About), Must: it.Must, When: it.When}
}

func nz(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}
