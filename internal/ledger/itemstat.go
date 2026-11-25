package ledger

// StatsStages 는 항목별로 세는 탈락 단계다. 나머지 단계(범위·자격·일치)는 "후보가 못 된" 것이라 세지 않는다.
var StatsStages = []string{"예산", "중복", "판정"}

// ItemStat 는 원장 전체에서 모은 항목 하나의 횟수다.
// 주입·탈락·검사는 훅의 줄만, flag 는 flag 줄만 센다(조회·등록 줄은 세지 않는다).
type ItemStat struct {
	Injected     int
	LastInjected string         // 마지막으로 넣은 줄의 ts(원장 그대로)
	Dropped      map[string]int // StatsStages 의 단계만
	CheckFails   int            // 결과물 검사에 걸린(fail) 횟수
	Flags        int
	LastFlag     string // 마지막 flag 사유
}

// StatsByItem 은 원장 줄을 한 번 훑어 항목 id 별 횟수를 모은다. 나온 적 없는 id 는 맵에 없다.
func StatsByItem(rows []Row) map[string]*ItemStat {
	out := map[string]*ItemStat{}
	get := func(id string) *ItemStat {
		st := out[id]
		if st == nil {
			st = &ItemStat{Dropped: map[string]int{}}
			for _, s := range StatsStages {
				st.Dropped[s] = 0
			}
			out[id] = st
		}
		return st
	}
	for _, r := range rows {
		if r.IsHook() {
			for _, i := range r.Injected {
				st := get(i.ID)
				st.Injected++
				st.LastInjected = r.TS
			}
			for _, d := range r.Dropped {
				st := get(d.ID)
				if _, ok := st.Dropped[d.Stage]; ok {
					st.Dropped[d.Stage]++
				}
			}
			for _, c := range r.Checks {
				if c.Result == "fail" {
					get(c.ID).CheckFails++
				}
			}
		}
		if r.Event == "flag" {
			for _, i := range r.Injected {
				st := get(i.ID)
				st.Flags++
				st.LastFlag = i.Why
			}
		}
	}
	return out
}

// IDs 는 줄에 나온 모든 항목 id(넣음·탈락·확인·검사·판정·모델 후보)를 중복 없이 순서대로 돌려준다.
func (r Row) IDs() []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, i := range r.Injected {
		add(i.ID)
	}
	for _, d := range r.Dropped {
		add(d.ID)
	}
	for _, id := range r.Confirm {
		add(id)
	}
	for _, c := range r.Checks {
		add(c.ID)
	}
	for _, j := range r.Judges {
		add(j.ID)
	}
	if r.Pick != nil {
		for _, p := range r.Pick.Items {
			add(p.ID)
		}
	}
	return out
}
