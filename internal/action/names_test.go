package action

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/hee-develop/heka/internal/item"
)

var nameRe = regexp.MustCompile(`^[a-z][a-z_]*(\.[a-z_]+)+$`)

// 패키지 소스에 적힌 점으로 이어진 이름 글자(동작 이름 꼴)가 Names 와 정확히 같은지 본다.
// 해석기가 새 이름을 내면서 목록에 빠뜨리면(또는 목록에만 있고 코드에 없으면) 여기서 걸린다.
func TestNamesMatchSource(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("소스를 찾지 못함: %v", err)
	}
	found := map[string]bool{}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil && nameRe.MatchString(s) {
					found[s] = true
				}
			}
			return true
		})
	}
	var got []string
	for s := range found {
		got = append(got, s)
	}
	slices.Sort(got)
	if want := Names(); !slices.Equal(got, want) {
		t.Errorf("소스의 동작 이름 %v\n Names()      %v", got, want)
	}
}

func TestNamesSortedAndUnique(t *testing.T) {
	n := Names()
	if !slices.IsSorted(n) || len(slices.Compact(slices.Clone(n))) != len(n) {
		t.Errorf("이름순이고 중복이 없어야 함: %v", n)
	}
	n[0] = "바꿔도 원본은 그대로"
	if Names()[0] == n[0] {
		t.Error("Names 는 복사본을 돌려줘야 함")
	}
}

// 시험 자료의 모든 호출에서 해석기가 낸 이름이 Names 에 있는지 본다(설정의 tool_actions 는 쓰지 않는다).
func TestResolvedActionsAreNames(t *testing.T) {
	t.Setenv("HOME", "/home/dev")
	t.Setenv("TMPDIR", "")
	b, err := os.ReadFile(filepath.Join("testdata", "calls.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []corpusCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	names := Names()
	seen := map[string]bool{}
	for _, c := range cases {
		for _, a := range Resolve(Call{Tool: c.Tool, Input: c.Input, Cwd: c.Cwd}, nil).Actions {
			seen[a] = true
			if !slices.Contains(names, a) {
				t.Errorf("%s: 해석기가 낸 %q 가 Names 에 없음", c.ID, a)
			}
		}
	}
	if len(seen) < 10 {
		t.Errorf("시험 자료가 너무 적어 확인이 되지 않음: %v", seen)
	}
}

func TestVocab(t *testing.T) {
	cfg := loadConfig(t, `
actions:
  git.write: {}
  git.commit: {in: git.write}
tool_actions:
  "Skill:make-flag": [feature_flag.create]
`)
	v := Vocab(cfg)
	for _, a := range []string{
		"git.commit", "git.write", // 설정의 actions
		"feature_flag.create", "feature_flag", // tool_actions 의 값과 그 위쪽
		"code.edit", "git.stash", "git.destructive", "git.worktree", "git.create_branch", "doc.edit", "test.run", "agent.delegate", // 해석기
		"git.push", "git.push.force", "git", // 점으로 이어진 위쪽
	} {
		if !v[a] {
			t.Errorf("어휘에 %q 가 있어야 함", a)
		}
	}
	for _, a := range []string{"git.comit", "git.pull", "code.edit.x", "feature_flag.delete"} {
		if v[a] {
			t.Errorf("어휘에 %q 가 없어야 함", a)
		}
	}
	for _, n := range Names() {
		if !v[n] {
			t.Errorf("해석기 이름 %q 가 어휘에 있어야 함", n)
		}
	}
	// 설정에 동작 이름이 하나도 없으면 어휘 검사를 하지 않는다.
	if got := Vocab(loadConfig(t, "scopes: {}\n")); len(got) != 0 {
		t.Errorf("설정이 비면 어휘도 비어야 함: %v", got)
	}
}

// 설정에 적지 않은 해석기 이름은 경고가 없고, 어디에도 없는 이름만 경고가 난다.
func TestVocabWarnsOnlyUnknownActions(t *testing.T) {
	cfg := loadConfig(t, "actions:\n  git.commit: {}\n")
	dir := t.TempDir()
	mk := func(id, action string) {
		body := "---\nid: " + id + "\ntext: 규칙.\nsource: user\nquote: 지어낸 말\nscope: [personal]\nabout: [규칙]\nwhen: {action: " + action + "}\n---\n"
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("a-known", "code.edit")
	mk("b-sub", "git.push.force")
	mk("c-config", "git.commit")
	mk("d-typo", "git.stahs")
	items, err := item.LoadDir(dir, Vocab(cfg))
	if err != nil || len(items) != 4 {
		t.Fatalf("항목 %d: %v", len(items), err)
	}
	for _, it := range items[:3] {
		if len(it.Problems) != 0 {
			t.Errorf("%s: 경고가 없어야 함: %v", it.ID, it.Problems)
		}
	}
	ps := items[3].Problems
	if len(ps) != 1 || ps[0].Level != item.Warn || !strings.Contains(ps[0].Msg, "해석기에도 없는 when.action: git.stahs") {
		t.Errorf("오타만 경고: %v", ps)
	}
}
