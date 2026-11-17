package index

import (
	"database/sql"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/item"
)

func mk(t *testing.T, home, id, extra string) {
	t.Helper()
	dir := filepath.Join(home, "items")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nid: " + id + "\n" + extra + "\n---\n본문 " + id + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const cfgYAML = `
scopes:
  repo/sample-app: [team/sample]
actions:
  git.push: {in: git.write, phrases: ["푸시해"]}
  git.push.force: {}
`

// build 는 home 의 항목과 설정으로 색인을 만든다.
func build(t *testing.T, home string) int {
	t.Helper()
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	fp, _ := Fingerprint(home)
	items, err := item.LoadDir(filepath.Join(home, "items"), cfg.ActionVocab())
	if err != nil {
		t.Fatal(err)
	}
	n, err := Build(home, items, cfg, fp)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func setup(t *testing.T) string {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "config.yaml"), []byte(cfgYAML), 0o644)
	mk(t, home, "push-up", "text: 푸시 전에 확인한다.\nmust: true\nwhen: {action: git.push}\nabout: [푸시]\nscope: [repo/sample-app]\nsource: user")
	mk(t, home, "force-push", "text: 강제 푸시는 금지한다.\nmust: true\nwhen: {action: git.push.force, moment: tool}\nabout: [강제 푸시]\nscope: [repo/sample-app, team/sample]\nsource: user\nconfirm: always\ncheck: {target: command, not_pattern: \"--force\", mode: block}")
	mk(t, home, "commit-msg", "text: 커밋 메시지는 한국어로 쓴다.\nabout: [커밋 메시지, commit message, pr]\nscope: [repo/sample-app]\nsource: doc")
	mk(t, home, "broken", "text: 오류 항목\nabout: []\nscope: [repo/sample-app]\nsource: user")
	if n := build(t, home); n != 3 {
		t.Fatalf("색인된 항목 = %d, 기대 3 (error 항목 제외)", n)
	}
	return home
}

func open(t *testing.T, home string) *Index {
	t.Helper()
	ix, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix
}

func TestItem(t *testing.T) {
	ix := open(t, setup(t))
	r, err := ix.Item("force-push")
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "강제 푸시는 금지한다." || !r.Must || r.Confirm != "always" || r.Hash == "" || r.Body == "" ||
		!reflect.DeepEqual(r.Scope, []string{"repo/sample-app", "team/sample"}) ||
		!reflect.DeepEqual(r.When["action"], []string{"git.push.force"}) || r.CheckJSON == "" {
		t.Errorf("항목 내용이 맞지 않음: %+v", r)
	}
	if _, err := ix.Item("broken"); err != ErrNotFound {
		t.Errorf("error 항목은 색인에 없어야 함: %v", err)
	}
	if _, err := ix.Item("nosuch"); err != ErrNotFound {
		t.Errorf("없는 id: %v", err)
	}
}

func TestByAction(t *testing.T) {
	ix := open(t, setup(t))
	tests := []struct {
		action string
		want   []string
	}{
		{"git.push.force", []string{"force-push", "push-up"}}, // 상위에 건 조건도 걸린다
		{"git.push", []string{"push-up"}},                     // 하위에 건 조건은 안 걸린다
		{"git.write", nil},
		{"git.push.force.x", []string{"force-push", "push-up"}}, // 설정에 없는 이름도 점 규칙으로
		{"git.commit", nil},
	}
	for _, tt := range tests {
		got, err := ix.ByAction(tt.action)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ByAction(%s) = %v %v, 기대 %v", tt.action, got, err, tt.want)
		}
	}
}

func TestAboutMatches(t *testing.T) {
	ix := open(t, setup(t))
	has := func(text, id string) bool {
		ms, err := ix.AboutMatches(text)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ms {
			if m.ID == id {
				return true
			}
		}
		return false
	}
	tests := []struct {
		text string
		id   string
		want bool
	}{
		{"커밋 메시지를 고쳐 줘", "commit-msg", true},
		{"커밋메시지 고쳐", "commit-msg", true},            // 글의 띄어쓰기가 주제어와 달라도
		{"Commit  Message 써 줘", "commit-msg", true}, // 대소문자와 공백 개수는 무관
		{"재커밋 메시지", "commit-msg", false},            // 어절 앞부분이 아님
		{"좋은 커밋 메시지", "commit-msg", true},
		{"(커밋 메시지)", "commit-msg", true}, // 앞이 글자가 아닌 문자
		{"pr 올려", "commit-msg", true},
		{"pr을 올려", "commit-msg", true},
		{"PR", "commit-msg", true},
		{"sprint 계획", "commit-msg", false},
		{"prd 작성", "commit-msg", false},
		{"pr2 작성", "commit-msg", false},
		{"강제푸시 하지 마", "force-push", true},
		{"푸시해 줘", "push-up", true},
		{"무관한 글", "push-up", false},
	}
	for _, tt := range tests {
		if got := has(tt.text, tt.id); got != tt.want {
			t.Errorf("AboutMatches(%q) 에 %s 있음 = %v, 기대 %v", tt.text, tt.id, got, tt.want)
		}
	}
	// 영문 주제어 "commit message" 도 같은 규칙으로 맞는다.
	ms, _ := ix.AboutMatches("write a commit message")
	if len(ms) != 1 || ms[0] != (AboutMatch{"commit-msg", "commit message"}) {
		t.Errorf("영문 주제어: %v", ms)
	}
}

func TestGrams(t *testing.T) {
	got := Grams("커밋 메시지, git-push v1.2 a x_y 푸")
	want := map[string]bool{"커밋": true, "메시": true, "시지": true, "git-push": true, "git": true, "push": true, "v1.2": true, "v1": true, "x_y": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Grams = %v, 기대 %v", got, want)
	}
}

func TestLexical(t *testing.T) {
	home := t.TempDir()
	mk(t, home, "a", "text: deploy checklist\nabout: [release]\nscope: [x]\nsource: user")
	mk(t, home, "b", "text: deploy guide\nabout: [release]\nscope: [x]\nsource: user")
	build(t, home)
	ix := open(t, home)
	// N=2. df(deploy)=2, df(checklist)=1, df(zzzz)=0.
	// idf(deploy)=ln(1+0.5/2.5), idf(checklist)=ln(1+1.5/1.5)=ln2, idf(zzzz)=ln(1+2.5/0.5)=ln6
	d, c, z := math.Log(1.2), math.Log(2), math.Log(6)
	got, err := ix.Lexical("Deploy checklist zzzz", 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("Lexical = %v %v", got, err)
	}
	total := d + c + z
	if got[0].ID != "a" || math.Abs(got[0].Score-(d+c)/total) > 1e-9 || got[1].ID != "b" || math.Abs(got[1].Score-d/total) > 1e-9 {
		t.Errorf("점수가 손계산과 다름: %v, 기대 a=%v b=%v", got, (d+c)/total, d/total)
	}
	if got, _ := ix.Lexical("deploy checklist", 1); len(got) != 1 || got[0].ID != "a" || math.Abs(got[0].Score-1) > 1e-9 {
		t.Errorf("k=1: %v", got)
	}
	if got, _ := ix.Lexical("deploy", 5); len(got) != 2 || got[0].ID != "a" { // 같은 점수는 id 오름차순
		t.Errorf("동점 순서: %v", got)
	}
	if got, _ := ix.Lexical("a ? !", 5); len(got) != 0 {
		t.Errorf("조각이 없는 질의는 빈 결과: %v", got)
	}
	// 한글: 두 글자 조각 "결제" 는 "결제를" 에서도 나온다.
	mk(t, home, "c", "text: 결제 화면은 개편 중이다.\nabout: [결제]\nscope: [x]\nsource: user")
	build(t, home)
	ix2 := open(t, home)
	if got, _ := ix2.Lexical("결제를 바꿔", 3); len(got) == 0 || got[0].ID != "c" {
		t.Errorf("한글 조각: %v", got)
	}
}

func TestReplaceAndStale(t *testing.T) {
	home := setup(t)
	if Stale(home) {
		t.Error("방금 만든 색인은 낡지 않음")
	}
	old := open(t, home)
	// 항목을 고치면 낡은 색인이 된다(수정 시각이 달라지도록 시각을 옮긴다).
	mk(t, home, "push-up", "text: 푸시 전에 두 번 확인한다.\nmust: true\nwhen: {action: git.push}\nabout: [푸시]\nscope: [repo/sample-app]\nsource: user")
	later := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(home, "items", "push-up.md"), later, later)
	if !Stale(home) {
		t.Error("항목을 고친 뒤에는 낡은 색인")
	}
	build(t, home)
	if Stale(home) {
		t.Error("다시 만든 뒤에는 낡지 않음")
	}
	// 열어 둔 옛 색인도 계속 읽히고, 새로 열면 바뀐 내용이 보인다.
	if _, err := old.Item("push-up"); err != nil {
		t.Errorf("옛 색인 읽기: %v", err)
	}
	r, _ := open(t, home).Item("push-up")
	if r.Text != "푸시 전에 두 번 확인한다." {
		t.Errorf("교체 뒤 내용: %q", r.Text)
	}
	if ents, _ := filepath.Glob(filepath.Join(home, "index", "*tmp*")); len(ents) != 0 {
		t.Errorf("임시 파일이 남음: %v", ents)
	}
	// 색인 파일이 없어도 낡은 것으로 본다.
	os.Remove(Path(home))
	if !Stale(home) {
		t.Error("색인이 없으면 stale")
	}
}

func TestReadOnly(t *testing.T) {
	ix := open(t, setup(t))
	if _, err := ix.db.Exec(`INSERT INTO meta VALUES('x','y')`); err == nil {
		t.Error("읽기 전용이어야 함")
	}
}

func TestPushColumnAndOldSchema(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "config.yaml"), []byte(cfgYAML), 0o644)
	mk(t, home, "normal", "text: 보통 항목.\nabout: [보통]\nscope: [repo/sample-app]\nsource: doc")
	mk(t, home, "find-only", "text: 조회 전용 항목.\nabout: [조회 전용]\nscope: [repo/sample-app]\nsource: doc\npush: false")
	build(t, home)
	ix := open(t, home)
	for id, want := range map[string]bool{"normal": true, "find-only": false} {
		r, err := ix.Item(id)
		if err != nil || r.Push != want {
			t.Errorf("%s: Push=%v 기대 %v (%v)", id, r.Push, want, err)
		}
	}
	// 조회 전용 항목도 낱말·주제어 색인에는 들어간다.
	if got, _ := ix.Lexical("조회 전용 항목", 5); len(got) == 0 || got[0].ID != "find-only" {
		t.Errorf("낱말 색인: %+v", got)
	}
	if got, _ := ix.AboutMatches("조회 전용"); len(got) != 1 || got[0].ID != "find-only" {
		t.Errorf("주제어 색인: %+v", got)
	}
	ix.Close()

	// 옛 꼴의 색인(스키마 1)은 Open 이 거절하고, Stale 이 낡았다고 하며, Ensure 가 다시 만든다.
	db, err := sql.Open("sqlite", Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE meta SET value='1' WHERE key='schema'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(home); !errors.Is(err, ErrOldSchema) {
		t.Errorf("옛 색인 Open = %v", err)
	}
	if !Stale(home) {
		t.Error("옛 색인은 낡은 것으로 봐야 함")
	}
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := Ensure(home, cfg); err != nil {
		t.Fatal(err)
	}
	if Stale(home) {
		t.Error("다시 만든 뒤에는 낡지 않아야 함")
	}
	ix2 := open(t, home)
	if r, err := ix2.Item("find-only"); err != nil || r.Push {
		t.Errorf("다시 만든 색인: %+v %v", r, err)
	}
}
