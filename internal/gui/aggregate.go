package gui

import (
	"sort"
	"unicode/utf8"

	"github.com/hee-develop/heka/internal/item"
	"github.com/hee-develop/heka/internal/ledger"
	"github.com/hee-develop/heka/internal/stat"
)

// 순간의 값(원장 Row.Moment)이다. 줄이 없어도 이 순서로 다섯 개를 낸다.
var hookMoments = []string{"session_start", "prompt", "tool", "subagent_start", "stop"}

// 탈락 단계의 값(selector.Drop.Stage)이다. 모두 0 이라도 키를 낸다.
var allStages = []string{"범위", "자격", "일치", "중복", "예산", "판정"}

// inRange 는 줄의 ts 앞 10자(UTC 날짜)가 [since, until] 안인가. 빈 값은 그쪽 끝이 열려 있다는 뜻이다.
func inRange(r ledger.Row, since, until string) bool {
	d := r.TS
	if len(d) > 10 {
		d = d[:10]
	}
	return (since == "" || d >= since) && (until == "" || d <= until)
}

func filterRows(rows []ledger.Row, since, until string) []ledger.Row {
	if since == "" && until == "" {
		return rows
	}
	var out []ledger.Row
	for _, r := range rows {
		if inRange(r, since, until) {
			out = append(out, r)
		}
	}
	return out
}

// Dist 는 밀리초 분포다.
type Dist struct {
	Median int64 `json:"median"`
	P95    int64 `json:"p95"`
	Max    int64 `json:"max"`
}

func distOf(vals []int64) Dist {
	d := stat.Of(vals)
	return Dist{d.Median, d.P95, d.Max}
}

type Checks struct {
	Pass    int `json:"pass"`
	Fail    int `json:"fail"`
	Skip    int `json:"skip"`
	Blocked int `json:"blocked"`
}

type JudgeSummary struct {
	N       int  `json:"n"`
	Inject  int  `json:"inject"`
	Error   int  `json:"error"`
	Timeout int  `json:"timeout"`
	MS      Dist `json:"ms"`
}

type PickSummary struct {
	N            int `json:"n"`
	Error        int `json:"error"`
	Timeout      int `json:"timeout"`
	Picked       int `json:"picked"`
	PickedInject int `json:"picked_inject"`
}

type TopItem struct {
	ID string `json:"id"`
	N  int    `json:"n"`
}

// MomentSummary 는 순간 하나의 훅 줄을 모은 것이다.
type MomentSummary struct {
	Moment       string         `json:"moment"`
	Rows         int            `json:"rows"`
	RowsInjected int            `json:"rows_injected"`
	RowsSilent   int            `json:"rows_silent"`
	ToolMade     int            `json:"tool_made"`
	ShadowRows   int            `json:"shadow_rows"`
	Injected     int            `json:"injected"`
	Tokens       int            `json:"tokens"`
	Dropped      map[string]int `json:"dropped"`
	Asked        int            `json:"asked"`
	Refused      int            `json:"refused"`
	GrantsUsed   int            `json:"grants_used"`
	Confirm      int            `json:"confirm"`
	Checks       Checks         `json:"checks"`
	Judges       JudgeSummary   `json:"judges"`
	Pick         PickSummary    `json:"pick"`
	Duration     Dist           `json:"duration_ms"`
	TopItems     []TopItem      `json:"top_items"`
}

type Range struct {
	Since    string `json:"since"`
	Until    string `json:"until"`
	Rows     int    `json:"rows"`
	Sessions int    `json:"sessions"`
}

type Overview struct {
	Range   Range           `json:"range"`
	Moments []MomentSummary `json:"moments"`
	Events  map[string]int  `json:"events"`
}

// computeOverview 는 since~until(양끝 포함, 빈 값은 열림)의 줄을 순간별로 모은다.
func computeOverview(all []ledger.Row, since, until string) Overview {
	rows := filterRows(all, since, until)
	ov := Overview{Range: Range{Since: since, Until: until, Rows: len(rows)}, Events: map[string]int{}}
	sessions := map[string]bool{}
	byMoment := map[string][]ledger.Row{}
	for _, r := range rows {
		if r.Session != "" {
			sessions[r.Session] = true
		}
		if r.IsHook() {
			byMoment[r.Moment] = append(byMoment[r.Moment], r)
		} else {
			ov.Events[r.Event]++
		}
	}
	ov.Range.Sessions = len(sessions)
	for _, m := range hookMoments {
		ov.Moments = append(ov.Moments, summarizeMoment(m, byMoment[m]))
	}
	return ov
}

func summarizeMoment(moment string, rows []ledger.Row) MomentSummary {
	s := MomentSummary{Moment: moment, Rows: len(rows), Dropped: map[string]int{}, TopItems: []TopItem{}}
	for _, st := range allStages {
		s.Dropped[st] = 0
	}
	var dur, judgeMS []int64
	inj := map[string]int{}
	for _, r := range rows {
		if len(r.Injected) > 0 {
			s.RowsInjected++
		} else {
			s.RowsSilent++
		}
		if r.ToolMade {
			s.ToolMade++
		}
		if r.Shadow {
			s.ShadowRows++
		}
		s.Injected += len(r.Injected)
		s.Tokens += r.Tokens
		for _, i := range r.Injected {
			inj[i.ID]++
		}
		for _, d := range r.Dropped {
			s.Dropped[d.Stage]++
		}
		s.Asked += len(r.Asked)
		s.Refused += len(r.Refused)
		s.GrantsUsed += len(r.GrantsUsed)
		s.Confirm += len(r.Confirm)
		for _, c := range r.Checks {
			switch c.Result {
			case "pass":
				s.Checks.Pass++
			case "fail":
				s.Checks.Fail++
			case "skip":
				s.Checks.Skip++
			}
			if c.Blocked {
				s.Checks.Blocked++
			}
		}
		for _, j := range r.Judges {
			s.Judges.N++
			if j.Inject {
				s.Judges.Inject++
			}
			if j.Error {
				s.Judges.Error++
			}
			if j.Timeout {
				s.Judges.Timeout++
			}
			judgeMS = append(judgeMS, j.MS)
		}
		if p := r.Pick; p != nil {
			s.Pick.N++
			if p.Error {
				s.Pick.Error++
			}
			if p.Timeout {
				s.Pick.Timeout++
			}
			for _, it := range p.Items {
				s.Pick.Picked++
				if it.Inject {
					s.Pick.PickedInject++
				}
			}
		}
		dur = append(dur, r.DurationMS)
	}
	s.Judges.MS, s.Duration = distOf(judgeMS), distOf(dur)
	for id, n := range inj {
		s.TopItems = append(s.TopItems, TopItem{id, n})
	}
	sort.Slice(s.TopItems, func(i, j int) bool {
		a, b := s.TopItems[i], s.TopItems[j]
		return a.N > b.N || (a.N == b.N && a.ID < b.ID)
	})
	if len(s.TopItems) > 5 {
		s.TopItems = s.TopItems[:5]
	}
	return s
}

// SessionSummary 는 세션 목록의 한 줄이다.
type SessionSummary struct {
	ID       string `json:"id"`
	FirstTS  string `json:"first_ts"`
	LastTS   string `json:"last_ts"`
	Rows     int    `json:"rows"`
	Injected int    `json:"injected"`
	Dropped  int    `json:"dropped"`
	Agents   int    `json:"agents"`
	Blocked  int    `json:"blocked"`
	Confirm  int    `json:"confirm"`
	Shadow   bool   `json:"shadow"`
	Prompt   string `json:"prompt"`
}

const promptRunes = 80

// computeSessions 는 since~until 의 줄을 세션별로 모아 마지막 시각 내림차순으로 limit 건을 돌려준다.
// 세션 id 가 빈 줄은 뺀다. 넣음·탈락·막음·확인은 훅의 줄만 센다.
func computeSessions(all []ledger.Row, since, until string, limit int) []SessionSummary {
	type acc struct {
		s      SessionSummary
		agents map[string]bool
		prompt bool
	}
	m := map[string]*acc{}
	for _, r := range filterRows(all, since, until) {
		if r.Session == "" {
			continue
		}
		a := m[r.Session]
		if a == nil {
			a = &acc{s: SessionSummary{ID: r.Session, FirstTS: r.TS, LastTS: r.TS}, agents: map[string]bool{}}
			m[r.Session] = a
		}
		s := &a.s
		if r.TS < s.FirstTS {
			s.FirstTS = r.TS
		}
		if r.TS > s.LastTS {
			s.LastTS = r.TS
		}
		s.Rows++
		if r.Agent != "" {
			a.agents[r.Agent] = true
		}
		if r.Shadow {
			s.Shadow = true
		}
		if r.IsHook() {
			s.Injected += len(r.Injected)
			s.Dropped += len(r.Dropped)
			s.Confirm += len(r.Confirm)
			for _, c := range r.Checks {
				if c.Blocked {
					s.Blocked++
				}
			}
		}
		// 첫 프롬프트: 도구가 만든 프롬프트는 사람이 쓴 것이 아니라 건너뛴다.
		if !a.prompt && r.Event == "UserPromptSubmit" && !r.ToolMade && r.Prompt != "" {
			a.prompt = true
			s.Prompt = r.Prompt
			if utf8.RuneCountInString(s.Prompt) > promptRunes {
				s.Prompt = string([]rune(s.Prompt)[:promptRunes])
			}
		}
	}
	out := make([]SessionSummary, 0, len(m))
	for _, a := range m {
		a.s.Agents = len(a.agents)
		out = append(out, a.s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastTS != out[j].LastTS {
			return out[i].LastTS > out[j].LastTS
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// sessionRows 는 그 세션의 모든 줄을 시각 오름차순으로 돌려준다(같은 시각은 원장 순서).
func sessionRows(all []ledger.Row, id string) []ledger.Row {
	rows := []ledger.Row{}
	for _, r := range all {
		if r.Session == id {
			rows = append(rows, r)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].TS < rows[j].TS })
	return rows
}

// ItemRef 는 세션 상세에서 항목 id 에 붙이는 지금 항목의 모습이다.
type ItemRef struct {
	Text   string `json:"text"`
	Must   bool   `json:"must"`
	Exists bool   `json:"exists"`
}

func itemRefs(rows []ledger.Row, items map[string]*item.Item) map[string]ItemRef {
	out := map[string]ItemRef{}
	for _, r := range rows {
		for _, id := range r.IDs() {
			if _, done := out[id]; done {
				continue
			}
			if it := items[id]; it != nil {
				out[id] = ItemRef{Text: it.Text, Must: it.Must, Exists: true}
			} else {
				out[id] = ItemRef{}
			}
		}
	}
	return out
}

// ItemStats 는 항목 목록·상세에 붙는 원장 횟수다.
type ItemStats struct {
	Injected     int            `json:"injected"`
	LastInjected string         `json:"last_injected"`
	Dropped      int            `json:"dropped"`
	CheckFail    int            `json:"check_fail"`
	Flagged      int            `json:"flagged"`
	ByStage      map[string]int `json:"dropped_by_stage,omitempty"`
}

// ItemView 는 항목 목록의 한 건이다.
type ItemView struct {
	ID       string    `json:"id"`
	Text     string    `json:"text"`
	Must     bool      `json:"must"`
	Push     bool      `json:"push"`
	When     item.When `json:"when"`
	Scope    []string  `json:"scope"`
	About    []string  `json:"about"`
	Source   string    `json:"source"`
	Checked  string    `json:"checked"`
	Expires  string    `json:"expires"`
	Confirm  string    `json:"confirm"`
	HasCheck bool      `json:"has_check"`
	Stats    ItemStats `json:"stats"`
}

// ItemDetail 은 항목 하나의 상세다.
type ItemDetail struct {
	ItemView
	Body   string       `json:"body"`
	Recent []ledger.Row `json:"recent"`
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func buildItemView(it *item.Item, st *ledger.ItemStat) ItemView {
	v := ItemView{
		ID: it.ID, Text: it.Text, Must: it.Must, Push: it.Pushes(), When: it.When,
		Scope: nonNil(it.Scope), About: nonNil(it.About), Source: it.Source,
		Checked: string(it.Checked), Expires: string(it.Expires), Confirm: it.ConfirmValue(), HasCheck: it.Check != nil,
	}
	if st != nil {
		v.Stats = ItemStats{Injected: st.Injected, LastInjected: st.LastInjected, CheckFail: st.CheckFails, Flagged: st.Flags}
		for _, n := range st.Dropped {
			v.Stats.Dropped += n
		}
	}
	return v
}

// computeItems 는 id 오름차순 항목 목록이다. stats 는 원장 전체를 한 번 훑어 모은 것이다.
func computeItems(items map[string]*item.Item, stats map[string]*ledger.ItemStat) []ItemView {
	ids := make([]string, 0, len(items))
	for id := range items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]ItemView, 0, len(ids))
	for _, id := range ids {
		out = append(out, buildItemView(items[id], stats[id]))
	}
	return out
}

const recentRows = 30

// computeItemDetail 은 항목 상세다. recent 는 이 항목이 나온 줄 최근 30건(시각 내림차순)이다.
func computeItemDetail(it *item.Item, rows []ledger.Row, stats map[string]*ledger.ItemStat) ItemDetail {
	st := stats[it.ID]
	d := ItemDetail{ItemView: buildItemView(it, st), Body: it.Body, Recent: []ledger.Row{}}
	d.Stats.ByStage = map[string]int{}
	for _, s := range ledger.StatsStages {
		d.Stats.ByStage[s] = 0
	}
	if st != nil {
		for s, n := range st.Dropped {
			d.Stats.ByStage[s] = n
		}
	}
	var hit []ledger.Row
	for _, r := range rows {
		for _, id := range r.IDs() {
			if id == it.ID {
				hit = append(hit, r)
				break
			}
		}
	}
	sort.SliceStable(hit, func(i, j int) bool { return hit[i].TS > hit[j].TS })
	if len(hit) > recentRows {
		hit = hit[:recentRows]
	}
	if hit != nil {
		d.Recent = hit
	}
	return d
}
