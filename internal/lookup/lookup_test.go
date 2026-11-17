package lookup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/index"
	"github.com/hee-develop/heka/internal/ledger"
)

const cfgYAML = "scopes:\n  repo/sample-app: [team/sample]\n  team/sample: [personal]\nglobal_scopes: [personal]\nlocations:\n  - path: /work/sample-app\n    scopes: [repo/sample-app]\n"

func put(t *testing.T, home, id, head, body string) {
	t.Helper()
	dir := filepath.Join(home, "items")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := "---\nid: " + id + "\n" + head + "---\n" + body
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) (*index.Index, *config.Config, string) {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("본문입니다 ", 40) // 어림 토큰이 150 을 넘는다
	put(t, home, "topic-hit", "text: 커밋 메시지는 한국어로 쓴다.\nsource: doc\nabout: [커밋 메시지]\nscope: [personal]\n", long)
	put(t, home, "lex-hit", "text: 배포 전에 점검표를 확인한다.\nsource: doc\nabout: [릴리스]\nscope: [repo/sample-app]\n", "")
	put(t, home, "action-rule", "text: 푸시할 때 테스트를 돌린다.\nmust: true\nwhen:\n  action: git.push\nsource: user\nchecked: 2026-11-01\nabout: [푸시 규칙]\nscope: [personal]\n", "")
	put(t, home, "other-scope", "text: 배포 점검표는 다른 팀 것이다.\nsource: doc\nabout: [배포]\nscope: [repo/other]\n", "")
	put(t, home, "expired", "text: 배포 점검표 기한 지난 것.\nsource: doc\nexpires: 2020-01-01\nabout: [배포]\nscope: [personal]\n", "")
	put(t, home, "unchecked-ai", "text: 배포 점검표 미확인 추측.\nsource: ai\nabout: [배포]\nscope: [personal]\n", "")
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(home, cfg); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix, cfg, home
}

func ids(r Result) []string {
	out := []string{}
	for _, h := range r.Items {
		out = append(out, h.ID)
	}
	return out
}

func TestFindSentenceAndKeywords(t *testing.T) {
	ix, cfg, _ := setup(t)
	sc := []string{"repo/sample-app"}
	// 문장 질의: 주제어가 먼저, 낱말로만 걸린 것이 뒤에 온다. 범위 밖·기한 지남·미확인 ai 는 빠진다.
	r, err := Find(ix, cfg, Query{Text: "커밋 메시지를 쓸 때 배포 점검표도 봐야 해?", Scopes: sc, Budget: 5000, Today: "2026-11-16"})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(r); !reflect.DeepEqual(got, []string{"topic-hit", "lex-hit"}) {
		t.Errorf("순서 = %v", got)
	}
	if !strings.Contains(r.Items[0].Why, `주제어 "커밋 메시지"`) || !strings.Contains(r.Items[1].Why, "낱말 점수") {
		t.Errorf("걸린 이유 = %q, %q", r.Items[0].Why, r.Items[1].Why)
	}
	// 낱말 나열도 같은 방식이다.
	r, _ = Find(ix, cfg, Query{Text: "배포 점검표", Scopes: sc, Budget: 5000, Today: "2026-11-16"})
	if got := ids(r); !reflect.DeepEqual(got, []string{"lex-hit"}) {
		t.Errorf("낱말 질의 = %v", got)
	}
	// 범위가 personal 뿐이면 repo 범위 항목은 빠진다.
	r, _ = Find(ix, cfg, Query{Text: "배포 점검표", Scopes: nil, Budget: 5000, Today: "2026-11-16"})
	if len(r.Items) != 0 {
		t.Errorf("범위 밖 = %v", ids(r))
	}
}

func TestFindIncludesActionConditionedItems(t *testing.T) {
	ix, cfg, _ := setup(t)
	r, err := Find(ix, cfg, Query{Text: "푸시 규칙", Today: "2026-11-16", Budget: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 1 || r.Items[0].ID != "action-rule" || !r.Items[0].Must || r.Items[0].When["action"][0] != "git.push" {
		t.Errorf("동작 조건 항목 = %+v", r.Items)
	}
}

func TestFindBudget(t *testing.T) {
	ix, cfg, home := setup(t)
	q := Query{Text: "커밋 메시지 배포 점검표", Scopes: []string{"repo/sample-app"}, Today: "2026-11-16"}
	q.Budget = 150 // topic-hit 은 본문이 길어 주입문만 들어간다
	r, _ := Find(ix, cfg, q)
	if len(r.Items) < 1 || r.Items[0].ID != "topic-hit" || !r.Items[0].BodyOmitted || r.Items[0].Body != "" {
		t.Fatalf("본문 생략 = %+v", r.Items)
	}
	q.Budget = 10 // 주입문만으로도 안 들어간다
	r, _ = Find(ix, cfg, q)
	if r.Omitted == 0 || len(r.OmittedIDs) != r.Omitted {
		t.Errorf("빠진 목록 = %+v", r)
	}
	if !strings.Contains(r.Text(), "예산 때문에 빠진 항목") {
		t.Errorf("글에 빠진 항목 안내가 없음: %s", r.Text())
	}
	// 예산 0 이하이면 설정의 budgets.find(기본 1500)를 쓴다.
	q.Budget = 0
	if r, _ = Find(ix, cfg, q); r.Omitted != 0 || len(r.Items) != 2 {
		t.Errorf("기본 예산 = %+v", r)
	}
	// 원장에 한 줄 남긴다.
	Log(home, q, r, time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC))
	b, err := os.ReadFile(filepath.Join(home, "ledger", "2026-11.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var row ledger.Row
	if err := json.Unmarshal(b, &row); err != nil || row.Event != "find" || row.Prompt != q.Text || len(row.Injected) != 2 {
		t.Errorf("원장 = %s (%v)", b, err)
	}
	if strings.Count(string(b), "\n") != 1 {
		t.Errorf("한 줄이어야 함: %s", b)
	}
}

func TestFindJSONShape(t *testing.T) {
	ix, cfg, _ := setup(t)
	r, _ := Find(ix, cfg, Query{Text: "푸시 규칙", Today: "2026-11-16"})
	b, _ := json.Marshal(r)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"items", "omitted", "omitted_ids"} {
		if _, ok := m[k]; !ok {
			t.Errorf("%s 키가 없음: %s", k, b)
		}
	}
	it := m["items"].([]any)[0].(map[string]any)
	for _, k := range []string{"id", "text", "body", "must", "when", "why", "score"} {
		if _, ok := it[k]; !ok {
			t.Errorf("항목에 %s 키가 없음: %s", k, b)
		}
	}
}

func TestScopesFromCwd(t *testing.T) {
	_, cfg, _ := setup(t)
	if got := Scopes(cfg, []string{"x"}, "/work/sample-app"); !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("인자 우선: %v", got)
	}
	if got := Scopes(cfg, nil, "/work/sample-app/sub"); !reflect.DeepEqual(got, []string{"repo/sample-app"}) {
		t.Errorf("작업 폴더: %v", got)
	}
}

// 낱말 후보 수의 기본값은 5 이고 Limit 으로 바꿀 수 있다.
func TestDefaultLimitIsFive(t *testing.T) {
	if DefaultLimit != 5 {
		t.Fatalf("DefaultLimit = %d", DefaultLimit)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("global_scopes: [personal]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 8; i++ {
		id := "chk-" + string(rune('a'+i))
		put(t, home, id, "text: checklist item "+id+".\nsource: doc\nabout: [없는주제"+id+"]\nscope: [personal]\n", "")
	}
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(home, cfg); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	for _, c := range []struct{ limit, want int }{{0, 5}, {-1, 5}, {3, 3}, {8, 8}} {
		r, err := Find(ix, cfg, Query{Text: "checklist", Limit: c.limit, Budget: 100000, Today: "2026-11-16"})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Items) != c.want {
			t.Errorf("Limit %d: 항목 %d건, 기대 %d", c.limit, len(r.Items), c.want)
		}
	}
}

func TestFindReachesFindOnlyItems(t *testing.T) {
	ix, cfg, home := setup(t)
	put(t, home, "fo-note", "text: 옛 노트의 요약 한 줄.\nsource: doc\nabout: [옛 노트]\nscope: [personal]\npush: false\n", "본문")
	if err := index.Ensure(home, cfg); err != nil {
		t.Fatal(err)
	}
	ix.Close()
	ix, err := index.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	r, err := Find(ix, cfg, Query{Text: "옛 노트", Today: "2026-11-16", Budget: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 1 || r.Items[0].ID != "fo-note" || r.Items[0].Push == nil || *r.Items[0].Push {
		t.Fatalf("조회 전용 항목을 찾지 못함: %+v", r.Items)
	}
	if !strings.Contains(r.Text(), "push=false") {
		t.Errorf("글 출력에 push=false 가 없음: %s", r.Text())
	}
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"push":false`) {
		t.Errorf("JSON 에 push 가 없음: %s", b)
	}
	// push 가 true 인 항목의 출력은 그대로다.
	r, _ = Find(ix, cfg, Query{Text: "푸시 규칙", Today: "2026-11-16", Budget: 5000})
	b, _ = json.Marshal(r)
	if strings.Contains(string(b), `"push":`) || strings.Contains(r.Text(), "push=") {
		t.Errorf("true 인 항목에 push 표시가 있음: %s", b)
	}
}
