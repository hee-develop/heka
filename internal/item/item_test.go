package item

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const good = `---
id: commit-message-language
text: 커밋 메시지는 한국어로 쓴다.
must: true
when:
  action: git.commit
about: [커밋 메시지, commit message]
scope: [repo/sample-app]
source: user
quote: "커밋 메시지는 한국어로 써 줘."
checked: 2026-11-13
expires:
confirm: false
---
배경과 예시.
`

// put 은 dir 에 항목 파일을 만들고 경로를 돌려준다.
func put(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func problems(t *testing.T, name, body string) []Problem {
	t.Helper()
	it, err := Parse(put(t, t.TempDir(), name, body))
	if err != nil {
		t.Fatal(err)
	}
	return it.Problems
}

func has(ps []Problem, level, sub string) bool {
	for _, p := range ps {
		if p.Level == level && strings.Contains(p.Msg, sub) {
			return true
		}
	}
	return false
}

func TestParseGood(t *testing.T) {
	it, err := Parse(put(t, t.TempDir(), "commit-message-language.md", good))
	if err != nil {
		t.Fatal(err)
	}
	if len(it.Problems) != 0 || !it.Valid() {
		t.Fatalf("문제가 없어야 함: %v", it.Problems)
	}
	if it.ID != "commit-message-language" || !it.Must || it.Source != "user" || it.Checked != "2026-11-13" || it.Expires != "" {
		t.Errorf("필드가 맞지 않음: %+v", it)
	}
	if len(it.When.Action) != 1 || it.When.Action[0] != "git.commit" || len(it.About) != 2 {
		t.Errorf("when/about: %+v %v", it.When, it.About)
	}
	if strings.TrimSpace(it.Body) != "배경과 예시." || len(it.Hash) != 16 || it.ConfirmValue() != "false" {
		t.Errorf("본문/해시/confirm: %q %q %q", it.Body, it.Hash, it.ConfirmValue())
	}
}

func TestWhenListAndConfirm(t *testing.T) {
	body := strings.Replace(good, "action: git.commit", `action: ["git.push.force", "git.rebase"]
  moment: tool`, 1)
	for conf, want := range map[string]string{"true": "true", "always": "always", `"true"`: "true", "false": "false"} {
		b := strings.Replace(body, "confirm: false", "confirm: "+conf, 1)
		it, _ := Parse(put(t, t.TempDir(), "commit-message-language.md", b))
		if !it.Valid() || it.ConfirmValue() != want {
			t.Errorf("confirm %s: 값 %q, 문제 %v", conf, it.ConfirmValue(), it.Problems)
		}
		if len(it.When.Action) != 2 || len(it.When.Moment) != 1 {
			t.Errorf("when 을 목록으로 읽어야 함: %+v", it.When)
		}
	}
}

func TestErrors(t *testing.T) {
	sub := func(old, new string) string { return strings.Replace(good, old, new, 1) }
	tests := []struct {
		name, file, body, want string
	}{
		{"id 없음", "commit-message-language.md", sub("id: commit-message-language\n", ""), "id 가 없음"},
		{"id 모양", "Bad_ID.md", sub("commit-message-language", "Bad_ID"), "에 맞지 않음"},
		{"id 와 파일 이름", "other.md", good, "파일 이름"},
		{"text 없음", "commit-message-language.md", sub("text: 커밋 메시지는 한국어로 쓴다.\n", ""), "text 가 비어"},
		{"source", "commit-message-language.md", sub("source: user", "source: web"), "source"},
		{"about 0개", "commit-message-language.md", sub("about: [커밋 메시지, commit message]", "about: []"), "about"},
		{"scope 0개", "commit-message-language.md", sub("scope: [repo/sample-app]", "scope: []"), "scope"},
		{"must 인데 조건 없음", "commit-message-language.md", sub("when:\n  action: git.commit\n", ""), "must: true"},
		{"moment", "commit-message-language.md", sub("action: git.commit", "moment: later"), "when.moment"},
		{"checked 날짜", "commit-message-language.md", sub("2026-11-13", "2026-13-45"), "checked"},
		{"expires 날짜", "commit-message-language.md", sub("expires:", "expires: 내일"), "expires"},
		{"confirm 값", "commit-message-language.md", sub("confirm: false", "confirm: maybe"), "confirm"},
		{"check target", "commit-message-language.md", sub("---\n배경", "check:\n  target: file\n  pattern: x\n  mode: block\n---\n배경"), "check.target"},
		{"check mode", "commit-message-language.md", sub("---\n배경", "check:\n  target: command\n  pattern: x\n  mode: stop\n---\n배경"), "check.mode"},
		{"check 둘 다 있음", "commit-message-language.md", sub("---\n배경", "check:\n  target: command\n  pattern: x\n  not_pattern: y\n  mode: log\n---\n배경"), "정확히 하나"},
		{"check 둘 다 없음", "commit-message-language.md", sub("---\n배경", "check:\n  target: command\n  mode: log\n---\n배경"), "정확히 하나"},
		{"check 정규식", "commit-message-language.md", sub("---\n배경", "check:\n  target: command\n  pattern: \"(?=x)\"\n  mode: log\n---\n배경"), "컴파일"},
		{"YAML 오류", "commit-message-language.md", "---\nid: [깨짐\n---\n", "YAML 해석 실패"},
		{"머리말 없음", "commit-message-language.md", "본문뿐\n", "머리말"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps := problems(t, tt.file, tt.body)
			if !has(ps, Error, tt.want) {
				t.Errorf("error %q 가 있어야 함: %v", tt.want, ps)
			}
		})
	}
}

func TestWarnings(t *testing.T) {
	ps := problems(t, "commit-message-language.md", strings.Replace(good, "confirm: false", "confirm: false\nextra: 1", 1))
	if !has(ps, Warn, "모르는 머리말 키: extra") || has(ps, Error, "") {
		t.Errorf("모르는 키는 warn 만: %v", ps)
	}

	dir := t.TempDir()
	put(t, dir, "commit-message-language.md", strings.Replace(good, "git.commit", "git.comit", 1))
	vocab := map[string]bool{"git.commit": true}
	items, _ := LoadDir(dir, vocab)
	if !has(items[0].Problems, Warn, "git.comit") || !items[0].Valid() {
		t.Errorf("어휘 밖 동작은 warn: %v", items[0].Problems)
	}
	if !has(items[0].Problems, Warn, "어느 것도 이 동작을 내지 않음: 설정 actions·tool_actions 에도, 해석기에도 없는 when.action: git.comit") {
		t.Errorf("경고 글: %v", items[0].Problems)
	}
	items, _ = LoadDir(dir, nil)
	if len(items[0].Problems) != 0 {
		t.Errorf("어휘가 비면 경고 없음: %v", items[0].Problems)
	}
}

func TestLoadDirSkipsSubdirs(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "commit-message-language.md", good)
	put(t, dir, "notes.txt", "x")
	if err := os.Mkdir(filepath.Join(dir, "_pending"), 0o755); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(dir, "_pending"), "pending-one.md", good)
	items, err := LoadDir(dir, nil)
	if err != nil || len(items) != 1 {
		t.Fatalf("하위 폴더는 읽지 않아야 함: %d건 %v", len(items), err)
	}
	if none, err := LoadDir(filepath.Join(dir, "nosuch"), nil); err != nil || len(none) != 0 {
		t.Errorf("폴더가 없으면 빈 목록: %v %v", none, err)
	}
}

// pushHead 는 push 시험용 머리말이다. extra 를 끝에 붙인다.
func pushHead(extra string) string {
	return "---\nid: note-1\ntext: 지어낸 노트.\nabout: [노트]\nscope: [personal]\nsource: doc\n" + extra + "\n---\n"
}

func TestPushParsing(t *testing.T) {
	for _, tc := range []struct {
		extra string
		want  bool
	}{{"", true}, {"push: true", true}, {"push: false", false}} {
		it, err := Parse(put(t, t.TempDir(), "note-1.md", pushHead(tc.extra)))
		if err != nil {
			t.Fatal(err)
		}
		if !it.Valid() || len(it.Problems) != 0 || it.Pushes() != tc.want {
			t.Errorf("%q: Pushes=%v 기대 %v, 문제 %v", tc.extra, it.Pushes(), tc.want, it.Problems)
		}
	}
	// 기존 항목 파일(필드 없음)은 그대로 유효하다.
	it, _ := Parse(put(t, t.TempDir(), "commit-message-language.md", good))
	if !it.Valid() || !it.Pushes() {
		t.Errorf("기존 항목: %v", it.Problems)
	}
	if ps := problems(t, "note-1.md", pushHead("push: 아니오")); !has(ps, Error, "해석 실패") {
		t.Errorf("bool 이 아닌 push: %v", ps)
	}
}

func TestPushFalseConflicts(t *testing.T) {
	for _, tc := range []struct{ name, extra, sub string }{
		{"must", "push: false\nmust: true\nwhen: {action: git.push}", "must: true 가 있음"},
		{"when", "push: false\nwhen: {path: 'docs/*'}", "when 에 조건이 있음"},
		{"confirm-true", "push: false\nconfirm: true", "confirm 이 true 임"},
		{"confirm-always", "push: false\nconfirm: always", "confirm 이 always 임"},
		{"check", "push: false\ncheck: {target: command, pattern: x, mode: log}", "check 가 있음"},
	} {
		if ps := problems(t, "note-1.md", pushHead(tc.extra)); !has(ps, Error, "push: false 인데") || !has(ps, Error, tc.sub) {
			t.Errorf("%s: %v", tc.name, ps)
		}
	}
	// push: true 이거나 없으면 같은 필드를 함께 둘 수 있다.
	if ps := problems(t, "note-1.md", pushHead("push: true\nconfirm: always\nwhen: {action: git.push}")); has(ps, Error, "push") || has(ps, Error, "") {
		t.Errorf("push: true: %v", ps)
	}
	// confirm: false 는 함께 둘 수 있다.
	if ps := problems(t, "note-1.md", pushHead("push: false\nconfirm: false")); has(ps, Error, "") {
		t.Errorf("confirm: false: %v", ps)
	}
}

func TestPushKnownKey(t *testing.T) {
	if ps := problems(t, "note-1.md", pushHead("push: false")); has(ps, Warn, "모르는 머리말 키") {
		t.Errorf("push 는 아는 키여야 함: %v", ps)
	}
}
