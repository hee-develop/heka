package review

import (
	"bytes"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/item"
	"github.com/hee-develop/heka/internal/ledger"
	"github.com/hee-develop/heka/internal/register"
)

var now = time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)

func newEnv(t *testing.T) register.Env {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("global_scopes: [personal]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	return register.Env{Home: home, Cfg: cfg, Now: now, Spawn: func(string) error { return nil }}
}

func add(t *testing.T, e register.Env, in register.Input) {
	t.Helper()
	if in.Scope == nil {
		in.Scope = []string{"personal"}
	}
	if in.About == nil {
		in.About = []string{"주제 " + in.ID}
	}
	if r, err := register.Add(e, in); err != nil || r.Status != "added" {
		t.Fatalf("add %s: %+v %v", in.ID, r, err)
	}
}

func has(e register.Env, id string) bool {
	_, err := os.Stat(filepath.Join(e.Home, "items", id+".md"))
	return err == nil
}

func parse(t *testing.T, e register.Env, id string) *item.Item {
	t.Helper()
	it, err := item.Parse(filepath.Join(e.Home, "items", id+".md"))
	if err != nil || !it.Valid() {
		t.Fatalf("%s: %v %v", id, err, it)
	}
	return it
}

func save(t *testing.T, e register.Env, en Entry) {
	t.Helper()
	if en.Created == "" {
		en.Created = "2026-11-26T09:00:00Z"
	}
	if err := Save(e.Home, en); err != nil {
		t.Fatal(err)
	}
}

func mergeEntry(newID, target string) Entry {
	return Entry{ID: EntryID(newID, target), Kind: KindMerge, NewID: newID, TargetID: target, Reason: "같은 규칙",
		Merged: &Merged{Text: "합친 주입문", Body: "합친 본문", About: []string{"합친 주제"}}}
}

func lastReview(t *testing.T, e register.Env) ledger.Row {
	t.Helper()
	var found ledger.Row
	for _, r := range ledger.ReadAll(e.Home) {
		if r.Event == "review" {
			found = r
		}
	}
	if found.Event == "" {
		t.Fatal("원장에 review 줄이 없음")
	}
	return found
}

func TestApplyMergeSourceRules(t *testing.T) {
	cases := []struct {
		name, newSrc, newQuote, tgtSrc, tgtQuote string
		wantSrc, wantQuote, wantLine             string
	}{
		{"새 것이 user", "user", "새 인용", "doc", "", "user", "새 인용", `합친 항목 old: 출처 doc`},
		{"대상이 user", "doc", "", "user", "옛 인용", "user", "옛 인용", `합친 항목 new: 출처 doc`},
		{"새 것이 doc 이고 대상이 ai", "doc", "", "ai", "", "doc", "", `합친 항목 old: 출처 ai`},
		{"같으면 대상 쪽", "user", "새 인용", "user", "옛 인용", "user", "옛 인용", `합친 항목 new: 출처 user, 인용 "새 인용"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			// 인용 대조는 등록의 일이라 파일을 직접 쓴다.
			for _, in := range []register.Input{
				{ID: "old", Text: "옛 글", Source: c.tgtSrc, Quote: c.tgtQuote, Must: true, When: item.When{Action: item.List{"git.push"}},
					Confirm: "true", Expires: "2027-01-01", Checked: "2026-11-01", Body: "옛 본문", About: []string{"옛"}, Scope: []string{"personal"}},
				{ID: "new", Text: "새 글", Source: c.newSrc, Quote: c.newQuote, About: []string{"새"}, Scope: []string{"personal"}},
			} {
				if err := register.Rewrite(e, in); err != nil {
					t.Fatal(err)
				}
			}
			save(t, e, mergeEntry("new", "old"))
			if _, err := Apply(e, "new--old", DecMerge, Options{}); err != nil {
				t.Fatal(err)
			}
			it := parse(t, e, "old")
			if it.Source != c.wantSrc || it.Quote != c.wantQuote {
				t.Errorf("출처 = %s %q", it.Source, it.Quote)
			}
			if it.Text != "합친 주입문" || !slices.Equal(it.About, []string{"합친 주제"}) {
				t.Errorf("초안이 반영돼야 함: %q %v", it.Text, it.About)
			}
			if !strings.HasPrefix(it.Body, "합친 본문\n\n") || !strings.HasSuffix(it.Body, c.wantLine+"\n") {
				t.Errorf("본문 = %q", it.Body)
			}
			// 조건·필수·범위·확인·기한·checked 는 대상 것이다.
			if !it.Must || !slices.Equal(it.When.Action, item.List{"git.push"}) || it.ConfirmValue() != "true" || string(it.Expires) != "2027-01-01" || string(it.Checked) != "2026-11-01" {
				t.Errorf("대상 필드가 유지돼야 함: %+v", it)
			}
			if has(e, "new") {
				t.Error("새 항목은 은퇴해야 함")
			}
			if _, err := os.Stat(filepath.Join(e.Home, "review", "new--old.json")); err == nil {
				t.Error("검토 항목이 남음")
			}
			var retired bool
			for _, r := range ledger.ReadAll(e.Home) {
				if r.Event == "retire" && r.Reason == "합침: old 에 합침" {
					retired = true
				}
			}
			if rv := lastReview(t, e); rv.Moment != "merge" || rv.Entry != "new--old" || !retired {
				t.Errorf("원장: %+v 은퇴=%v", rv, retired)
			}
		})
	}
}

func TestApplyMergeFlagsAndInvalid(t *testing.T) {
	e := newEnv(t)
	add(t, e, register.Input{ID: "old", Text: "옛 글", Source: "doc"})
	add(t, e, register.Input{ID: "new", Text: "새 글", Source: "ai"})
	save(t, e, mergeEntry("new", "old"))
	if _, err := Apply(e, "new--old", DecMerge, Options{Text: "고친 주입문", Body: "고친 본문", BodySet: true, About: []string{"가", "나"}}); err != nil {
		t.Fatal(err)
	}
	it := parse(t, e, "old")
	if it.Text != "고친 주입문" || !strings.HasPrefix(it.Body, "고친 본문\n\n합친 항목 new") || !slices.Equal(it.About, []string{"가", "나"}) {
		t.Errorf("플래그 값이 초안을 덮어야 함: %+v", it)
	}

	// 합친 결과가 검증을 통과하지 못하면 아무것도 바꾸지 않는다(about 이 비면 오류).
	e = newEnv(t)
	add(t, e, register.Input{ID: "old", Text: "옛 글", Source: "doc"})
	add(t, e, register.Input{ID: "new", Text: "새 글", Source: "ai"})
	bad := mergeEntry("new", "old")
	bad.Merged.About = nil
	save(t, e, bad)
	before, _ := os.ReadFile(filepath.Join(e.Home, "items", "old.md"))
	if _, err := Apply(e, "new--old", DecMerge, Options{}); err == nil {
		t.Fatal("검증 실패는 오류")
	}
	after, _ := os.ReadFile(filepath.Join(e.Home, "items", "old.md"))
	if !bytes.Equal(before, after) || !has(e, "new") {
		t.Error("실패하면 아무것도 바뀌면 안 됨")
	}
	if _, err := os.Stat(filepath.Join(e.Home, "review", "new--old.json")); err != nil {
		t.Error("실패하면 검토 항목이 남아야 함")
	}
	// 초안이 없는 표본은 --text 없이는 합칠 수 없다.
	save(t, e, Entry{ID: "new--old", Kind: KindSample, NewID: "new", TargetID: "old"})
	if _, err := Apply(e, "new--old", DecMerge, Options{}); err == nil {
		t.Error("초안이 없으면 오류")
	}
}

func TestApplyKeepDropAndCascade(t *testing.T) {
	setup := func() register.Env {
		e := newEnv(t)
		for _, id := range []string{"a", "b", "c"} {
			add(t, e, register.Input{ID: id, Text: "글 " + id, Source: "doc"})
		}
		save(t, e, Entry{ID: "b--a", Kind: KindConflict, NewID: "b", TargetID: "a"})
		save(t, e, Entry{ID: "c--b", Kind: KindMerge, NewID: "c", TargetID: "b", Merged: &Merged{Text: "t"}})
		save(t, e, Entry{ID: "c--a", Kind: KindSample, NewID: "c", TargetID: "a"})
		save(t, e, Entry{ID: "c--unjudged", Kind: KindUnjudged, NewID: "c", Candidates: []string{"a", "b"}})
		return e
	}
	ids := func(e register.Env) []string {
		var out []string
		for _, en := range List(e) {
			out = append(out, en.ID)
		}
		return out
	}

	e := setup() // keep: 항목은 그대로, 검토 항목만 지움
	if _, err := Apply(e, "b--a", DecKeep, Options{}); err != nil {
		t.Fatal(err)
	}
	if rv := lastReview(t, e); rv.Reason != "따로 두기" || rv.Moment != "keep" || !has(e, "a") || !has(e, "b") || len(ids(e)) != 3 {
		t.Errorf("keep: %+v %v", rv, ids(e))
	}
	if _, err := Apply(e, "c--b", DecKeep, Options{Reason: "다른 규칙"}); err != nil || lastReview(t, e).Reason != "다른 규칙" {
		t.Errorf("keep 사유: %v", err)
	}

	e = setup() // drop-new: b 은퇴 -> b 를 가리키는 c--b 도 지우고, 미판정의 후보에서 b 를 뺀다
	if _, err := Apply(e, "b--a", DecDropNew, Options{}); err != nil {
		t.Fatal(err)
	}
	if has(e, "b") || !has(e, "a") || !slices.Equal(ids(e), []string{"c--unjudged", "c--a"}) {
		t.Errorf("drop-new: %v", ids(e))
	}
	if en, _ := Load(e.Home, "c--unjudged"); !slices.Equal(en.Candidates, []string{"a"}) {
		t.Errorf("미판정 후보 = %v", en.Candidates)
	}
	if rv := lastReview(t, e); rv.Reason != "검토에서 버림" || rv.Moment != "drop-new" {
		t.Errorf("원장 = %+v", rv)
	}

	e = setup() // drop-old: a 은퇴 -> a 를 가리키는 b--a, c--a 도 지우고, 미판정 후보는 b 만 남는다
	if _, err := Apply(e, "b--a", DecDropOld, Options{Reason: "새 교정이 맞음"}); err != nil {
		t.Fatal(err)
	}
	if has(e, "a") || !has(e, "b") || !slices.Equal(ids(e), []string{"c--b", "c--unjudged"}) {
		t.Errorf("drop-old: %v", ids(e))
	}
	if en, _ := Load(e.Home, "c--unjudged"); !slices.Equal(en.Candidates, []string{"b"}) {
		t.Errorf("미판정 후보 = %v", en.Candidates)
	}

	// 미판정은 대상이 없어 합치기와 옛 것 버리기를 쓸 수 없다. 모르는 결정과 없는 항목은 오류다.
	for _, c := range [][2]string{{"c--unjudged", DecMerge}, {"c--unjudged", DecDropOld}, {"c--unjudged", "bogus"}, {"nope--x", DecKeep}, {"../x", DecKeep}} {
		if _, err := Apply(e, c[0], c[1], Options{}); err == nil {
			t.Errorf("%v: 오류여야 함", c)
		}
	}
	if _, err := Apply(e, "c--unjudged", DecDropNew, Options{}); err != nil || has(e, "c") {
		t.Errorf("미판정에서 새 것 버리기: %v", err)
	}
}

func TestListOrderAndStalePruning(t *testing.T) {
	e := newEnv(t)
	for _, id := range []string{"a", "b", "c", "d"} {
		add(t, e, register.Input{ID: id, Text: "글 " + id, Source: "doc"})
	}
	mk := func(id, kind, newID, target, created string) {
		save(t, e, Entry{ID: id, Kind: kind, NewID: newID, TargetID: target, Created: created})
	}
	mk("d--a", KindSample, "d", "a", "2026-11-20T00:00:00Z")
	mk("c--unjudged", KindUnjudged, "c", "", "2026-11-21T00:00:00Z")
	mk("c--b", KindMerge, "c", "b", "2026-11-22T00:00:00Z")
	mk("b--a", KindConflict, "b", "a", "2026-11-25T00:00:00Z")
	mk("d--b", KindMerge, "d", "b", "2026-11-22T00:00:00Z") // 같은 시각이면 id 순
	mk("c--a", KindConflict, "c", "a", "2026-11-19T00:00:00Z")
	mk("gone--a", KindConflict, "gone", "a", "2026-11-01T00:00:00Z") // 새 항목이 없음
	mk("d--gone", KindMerge, "d", "gone", "2026-11-01T00:00:00Z")    // 대상 항목이 없음
	if err := os.WriteFile(filepath.Join(e.Home, "review", "broken.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, en := range List(e) {
		got = append(got, en.ID)
	}
	want := []string{"c--a", "b--a", "c--b", "d--b", "c--unjudged", "d--a"}
	if !slices.Equal(got, want) {
		t.Errorf("순서 = %v, 기대 %v", got, want)
	}
	for _, id := range []string{"gone--a", "d--gone"} {
		if _, err := os.Stat(filepath.Join(e.Home, "review", id+".json")); err == nil {
			t.Errorf("%s 가 지워져야 함", id)
		}
	}
	var stale int
	for _, r := range ledger.ReadAll(e.Home) {
		if r.Event == "review" && r.Moment == "stale" {
			stale++
		}
	}
	if stale != 2 {
		t.Errorf("정리한 것을 원장에 남겨야 함: %d", stale)
	}
	if d := OldestDays(List(e), now); d != 7 {
		t.Errorf("가장 오래된 것의 나이 = %d", d)
	}
}

func notifyEnv(t *testing.T, minN, days int) (register.Env, string) {
	e := newEnv(t)
	log := filepath.Join(t.TempDir(), "notify.txt")
	e.Cfg.Review.NotifyCommand = []string{"sh", "-c", `echo "$0" >> ` + log}
	e.Cfg.Review.NotifyMin = minN
	e.Cfg.Review.NotifyDays = days
	return e, log
}

func fill(t *testing.T, e register.Env, n int, created string) {
	t.Helper()
	add(t, e, register.Input{ID: "target", Text: "대상", Source: "doc"})
	for i := range n {
		id := "n" + string(rune('a'+i))
		add(t, e, register.Input{ID: id, Text: "새 " + id, Source: "doc"})
		save(t, e, Entry{ID: id + "--target", Kind: KindMerge, NewID: id, TargetID: "target", Created: created})
	}
}

func TestNotifyThresholds(t *testing.T) {
	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }
	rng := func() *rand.Rand { return rand.New(rand.NewSource(1)) }

	e, log := notifyEnv(t, 3, 7) // 건수 기준 미달, 나이 기준 미달
	fill(t, e, 2, "2026-11-25T09:00:00Z")
	var out bytes.Buffer
	if sent, err := CheckAndNotify(e, &out, rng()); err != nil || sent || read(log) != "" {
		t.Errorf("기준 미달: sent=%v %v %q", sent, err, read(log))
	}

	e, log = notifyEnv(t, 3, 7) // 건수 기준 충족
	fill(t, e, 3, "2026-11-25T09:00:00Z")
	if sent, err := CheckAndNotify(e, &out, rng()); err != nil || !sent || read(log) != "heka 검토 대기 3건 (가장 오래된 것 1일). 세션에서 'heka 검토해줘', 터미널에서 heka review\n" {
		t.Errorf("건수: sent=%v %v %q", sent, err, read(log))
	}
	if last := ledger.ReadAll(e.Home); last[len(last)-1].Event != "review_notify" || last[len(last)-1].Count != 3 {
		t.Errorf("원장: %+v", last[len(last)-1])
	}

	e, log = notifyEnv(t, 5, 7) // 나이 기준 충족(정확히 7일)
	fill(t, e, 1, "2026-11-19T09:00:00Z")
	if sent, _ := CheckAndNotify(e, &out, rng()); !sent || !strings.Contains(read(log), "1건 (가장 오래된 것 7일)") {
		t.Errorf("나이: %q", read(log))
	}

	e, log = notifyEnv(t, 1, 1) // 목록이 비면 알리지 않는다
	if sent, _ := CheckAndNotify(e, &out, rng()); sent || read(log) != "" {
		t.Error("빈 목록은 알리지 않음")
	}

	e, _ = notifyEnv(t, 1, 1) // 명령이 비면 표준 출력에만 쓴다
	e.Cfg.Review.NotifyCommand = nil
	fill(t, e, 1, "2026-11-25T09:00:00Z")
	out.Reset()
	if sent, _ := CheckAndNotify(e, &out, rng()); !sent || !strings.Contains(out.String(), "heka 검토 대기 1건") {
		t.Errorf("표준 출력: %q", out.String())
	}

	e, _ = notifyEnv(t, 1, 1) // 명령이 실패하면 오류
	e.Cfg.Review.NotifyCommand = []string{"sh", "-c", "exit 1"}
	fill(t, e, 1, "2026-11-25T09:00:00Z")
	if _, err := CheckAndNotify(e, &out, rng()); err == nil {
		t.Error("알림 명령 실패는 오류")
	}
}

func judgeRow(e register.Env, at time.Time, newID string, vs ...ledger.Verdict) {
	_ = ledger.Append(e.Home, ledger.Row{Event: "register_judge", Moment: "judge-add", Injected: []ledger.Injected{{ID: newID}},
		RegJudge: &ledger.RegisterJudged{Verdicts: vs, MS: 10}}, at)
}

func TestSample(t *testing.T) {
	e := newEnv(t)
	e.Cfg.Review.NotifyMin = 100
	e.Cfg.Review.NotifyDays = 100
	for _, id := range []string{"t1", "t2", "t3", "t4", "n1", "n2"} {
		add(t, e, register.Input{ID: id, Text: "글 " + id, Source: "doc"})
	}
	sep := func(id string) ledger.Verdict {
		return ledger.Verdict{ID: id, Verdict: "separate", Reason: "다름 " + id}
	}
	judgeRow(e, now.Add(-time.Hour), "n1", sep("t1"), sep("t2"), ledger.Verdict{ID: "t3", Verdict: "merge"}, sep("gone")) // t3 는 따로가 아님, gone 은 항목이 없음
	judgeRow(e, now.Add(-24*time.Hour), "n2", sep("t1"), sep("t4"))
	judgeRow(e, now.Add(-8*24*time.Hour), "n2", sep("t3"))                        // 7일이 지난 줄
	save(t, e, Entry{ID: "n2--t4", Kind: KindMerge, NewID: "n2", TargetID: "t4"}) // 이미 검토 항목인 쌍

	e.Cfg.Review.Sample = 2
	n, err := Sample(e, 2, rand.New(rand.NewSource(42)))
	if err != nil || n != 2 {
		t.Fatalf("표본 수 = %d %v", n, err)
	}
	var first []string
	for _, en := range List(e) {
		if en.Kind == KindSample {
			first = append(first, en.ID)
			if en.Reason == "" || en.Created != "2026-11-26T09:00:00Z" {
				t.Errorf("표본 = %+v", en)
			}
		}
	}
	slices.Sort(first)
	pool := []string{"n1--t1", "n1--t2", "n2--t1"}
	if len(first) != 2 || !slices.Contains(pool, first[0]) || !slices.Contains(pool, first[1]) {
		t.Errorf("표본 = %v, 후보 풀 %v", first, pool)
	}
	// 시드가 같으면 같은 표본이다.
	e2 := newEnv(t)
	for _, id := range []string{"t1", "t2", "t3", "t4", "n1", "n2"} {
		add(t, e2, register.Input{ID: id, Text: "글 " + id, Source: "doc"})
	}
	judgeRow(e2, now.Add(-time.Hour), "n1", sep("t1"), sep("t2"), ledger.Verdict{ID: "t3", Verdict: "merge"}, sep("gone"))
	judgeRow(e2, now.Add(-24*time.Hour), "n2", sep("t1"), sep("t4"))
	save(t, e2, Entry{ID: "n2--t4", Kind: KindMerge, NewID: "n2", TargetID: "t4"})
	if _, err := Sample(e2, 2, rand.New(rand.NewSource(42))); err != nil {
		t.Fatal(err)
	}
	var second []string
	for _, en := range List(e2) {
		if en.Kind == KindSample {
			second = append(second, en.ID)
		}
	}
	slices.Sort(second)
	if !slices.Equal(first, second) {
		t.Errorf("시드가 같은데 표본이 다름: %v %v", first, second)
	}

	// 마지막 표본이 7일 안이면 다시 뽑지 않고, 7일을 넘으면 뽑는다.
	for _, en := range List(e) {
		if en.Kind == KindSample {
			Remove(e.Home, en.ID)
		}
	}
	if n, _ := Sample(e, 2, rand.New(rand.NewSource(1))); n != 0 {
		t.Errorf("7일 안에는 뽑지 않음: %d", n)
	}
	e.Now = now.Add(7*24*time.Hour + time.Minute)
	judgeRow(e, e.Now.Add(-time.Hour), "n1", sep("t4"))
	if n, _ := Sample(e, 3, rand.New(rand.NewSource(1))); n < 1 {
		t.Errorf("7일이 넘으면 뽑음: %d", n)
	}
}

func TestSampleViaNotifyAndCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 없음")
	}
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.invalid", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.invalid"} {
		t.Setenv(k, v)
	}
	e, _ := notifyEnv(t, 100, 100)
	if out, err := exec.Command("git", "-C", e.Home, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	add(t, e, register.Input{ID: "t1", Text: "대상 글", Source: "doc"})
	add(t, e, register.Input{ID: "n1", Text: "새 글", Source: "doc"})
	judgeRow(e, now.Add(-time.Hour), "n1", ledger.Verdict{ID: "t1", Verdict: "separate", Reason: "다름"})
	if _, err := CheckAndNotify(e, &bytes.Buffer{}, rand.New(rand.NewSource(1))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.Home, "review", ".sample-state.json")); err != nil {
		t.Error("표본 시각이 적혀야 함")
	}
	out, _ := exec.Command("git", "-C", e.Home, "ls-files", "review").Output()
	if !strings.Contains(string(out), "review/n1--t1.json") {
		t.Errorf("검토 목록이 커밋돼야 함: %q", out)
	}
	// 결정을 적용하면 검토 항목의 삭제도 커밋된다.
	if _, err := Apply(e, "n1--t1", DecKeep, Options{}); err != nil {
		t.Fatal(err)
	}
	if st, _ := exec.Command("git", "-C", e.Home, "status", "--porcelain", "--", "items", "review").Output(); strings.Contains(string(st), "n1--t1") {
		t.Errorf("삭제가 커밋 안 됨: %q", st)
	}
}

func TestInteractive(t *testing.T) {
	e := newEnv(t)
	for _, id := range []string{"a", "b", "c", "d"} {
		add(t, e, register.Input{ID: id, Text: "글 " + id, Source: "doc"})
	}
	save(t, e, Entry{ID: "b--a", Kind: KindConflict, NewID: "b", TargetID: "a", Reason: "반대", Created: "2026-11-20T00:00:00Z"})
	save(t, e, mergeEntry("c", "a"))
	save(t, e, Entry{ID: "d--unjudged", Kind: KindUnjudged, NewID: "d", Candidates: []string{"a"}, Reason: "시간 초과"})
	save(t, e, Entry{ID: "d--b", Kind: KindSample, NewID: "d", TargetID: "b"})
	var rejudged []string
	rejudge := func(id string) error { rejudged = append(rejudged, id); return nil }

	var out bytes.Buffer
	// 1) 모순: 잘못된 키 m(초안 없음)이면 다시 묻고 k, 2) 합치기: s 건너뜀, 3) 미판정: j, 4) 표본: n 새 것 버림
	in := strings.NewReader("m\nk\ns\nj\nn\n")
	if err := Interactive(e, in, &out, 5, rejudge); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "알 수 없는 키입니다.") || !strings.Contains(s, "모순: 새 b <-> 옛 a") || !strings.Contains(s, "합친 초안:") ||
		!strings.Contains(s, "[j]다시 판정") || strings.Count(s, "[m]합치기") != 1 || !strings.Contains(s, "남은 검토") {
		t.Errorf("출력:\n%s", s)
	}
	if !slices.Equal(rejudged, []string{"d"}) {
		t.Errorf("다시 판정 = %v", rejudged)
	}
	if has(e, "d") {
		t.Error("표본에서 n 은 새 항목 d 를 버림")
	}
	left := List(e)
	if len(left) != 1 || left[0].ID != "c--a" { // 건너뛴 합치기만 남는다(미판정 d 는 d 가 은퇴하며 사라짐)
		t.Errorf("남은 항목 = %+v", left)
	}

	// batch 만큼만 보이고 q 로 끝낸다. 합치기 m 은 초안 그대로.
	out.Reset()
	e2 := newEnv(t)
	for _, id := range []string{"a", "b", "c"} {
		add(t, e2, register.Input{ID: id, Text: "글 " + id, Source: "doc"})
	}
	save(t, e2, mergeEntry("b", "a"))
	save(t, e2, mergeEntry("c", "a"))
	if err := Interactive(e2, strings.NewReader("m\nm\n"), &out, 1, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "[2/") || has(e2, "b") || !has(e2, "c") || parse(t, e2, "a").Text != "합친 주입문" {
		t.Errorf("batch 1: %s", out.String())
	}
	out.Reset()
	if err := Interactive(newEnv(t), strings.NewReader(""), &out, 5, nil); err != nil || !strings.Contains(out.String(), "검토할 항목이 없습니다.") {
		t.Errorf("빈 목록: %q", out.String())
	}
}
