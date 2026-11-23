package ledger

import "testing"

func TestStatsByItem(t *testing.T) {
	rows := []Row{
		{TS: "t1", Event: "UserPromptSubmit", Injected: []Injected{{ID: "a"}}, Dropped: []Dropped{{ID: "a", Stage: "판정"}, {ID: "a", Stage: "범위"}},
			Checks: []Checked{{ID: "a", Result: "fail"}, {ID: "a", Result: "pass"}}},
		{TS: "t2", Event: "PreToolUse", Injected: []Injected{{ID: "a"}}},
		{TS: "t3", Event: "find", Injected: []Injected{{ID: "a"}}, Checks: []Checked{{ID: "a", Result: "fail"}}},
		{TS: "t4", Event: "flag", Injected: []Injected{{ID: "a", Why: "사유"}}},
	}
	st := StatsByItem(rows)["a"]
	if st.Injected != 2 || st.LastInjected != "t2" || st.Dropped["판정"] != 1 || len(st.Dropped) != 3 || st.CheckFails != 1 || st.Flags != 1 || st.LastFlag != "사유" {
		t.Errorf("st = %+v", st)
	}
	if StatsByItem(rows)["b"] != nil {
		t.Error("없는 id 는 맵에 없어야 함")
	}
}

func TestRowIDs(t *testing.T) {
	r := Row{Injected: []Injected{{ID: "a"}}, Dropped: []Dropped{{ID: "b"}, {ID: "a"}}, Confirm: []string{"c"}, Checks: []Checked{{ID: "d"}}, Judges: []Judged{{ID: "e"}}, Pick: &Pick{Items: []Picked{{ID: "f"}}}}
	got := r.IDs()
	if len(got) != 6 || got[0] != "a" || got[5] != "f" {
		t.Errorf("IDs = %v", got)
	}
}
