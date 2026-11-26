package session

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPathIsSafe(t *testing.T) {
	home := t.TempDir()
	for _, id := range []string{"abc-123_x.y", "../../etc/passwd", "a b/c", "", ".", "..", "한글"} {
		p := Path(home, id)
		if filepath.Dir(p) != filepath.Join(home, "sessions") || !strings.HasSuffix(p, ".json") {
			t.Errorf("%q → %q: 세션 폴더 안의 .json 이어야 함", id, p)
		}
	}
	if Path(home, "abc-123_x.y") != filepath.Join(home, "sessions", "abc-123_x.y.json") {
		t.Error("안전한 글자는 그대로 둔다")
	}
}

func TestStateOperations(t *testing.T) {
	home := t.TempDir()
	if s := Load(home, "x"); len(s.Shown) != 0 || len(s.Grants) != 0 || len(s.Pending) != 0 {
		t.Errorf("없는 파일은 빈 상태: %+v", s)
	}
	err := Update(home, "x", func(s *State) {
		s.Show("", "a")
		s.Show("ag", "a")
		s.Grant("git.push", Session)
		s.Grant("git.push", Turn) // session 을 낮추지 않는다
		s.Grant("git.commit", Turn)
		s.Delegate(Delegation{"Explore", "첫째"})
		s.Delegate(Delegation{"Plan", "둘째"})
		s.Delegate(Delegation{"Explore", "셋째"})
	})
	if err != nil {
		t.Fatal(err)
	}
	s := Load(home, "x")
	if !s.Seen("")["a"] || !s.Seen("ag")["a"] || s.Seen("other")["a"] || s.Show("", "a") {
		t.Errorf("shown: %+v", s.Shown)
	}
	if s.Grants["git.push"] != Session || s.GrantFor("git.push.force", "git.push", "git.write") != "git.push" || s.GrantFor("pr.create") != "" {
		t.Errorf("grants: %+v", s.Grants)
	}
	s.EndTurn()
	if s.Grants["git.commit"] != "" || s.Grants["git.push"] != Session {
		t.Errorf("EndTurn: %+v", s.Grants)
	}
	if d, ok := s.TakeDelegation("Explore"); !ok || d.Text != "첫째" {
		t.Errorf("가장 오래된 것을 꺼내야 함: %+v", d)
	}
	if d, ok := s.TakeDelegation("Explore"); !ok || d.Text != "셋째" {
		t.Errorf("%+v", d)
	}
	if _, ok := s.TakeDelegation("Explore"); ok {
		t.Error("없는데 꺼내짐")
	}
	if len(s.Pending) != 1 || s.Pending[0].Agent != "Plan" {
		t.Errorf("pending: %+v", s.Pending)
	}
	for i := 0; i < 40; i++ {
		s.Delegate(Delegation{"A", "t"})
	}
	if len(s.Pending) != maxPend {
		t.Errorf("상한: %d", len(s.Pending))
	}
}

func TestBrokenFileIsEmpty(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(home, "x"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := Load(home, "x"); s.Shown == nil || s.Grants == nil {
		t.Error("깨진 파일은 빈 상태")
	}
	if err := Update(home, "x", func(s *State) { s.Show("", "a") }); err != nil {
		t.Fatal(err)
	}
	if !Load(home, "x").Seen("")["a"] {
		t.Error("깨진 파일 위에 다시 쓰지 못함")
	}
}

// 여러 고루틴이 동시에 고쳐도 하나도 잃지 않는다.
func TestUpdateConcurrent(t *testing.T) {
	home := t.TempDir()
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := string(rune('a'+i%26)) + string(rune('A'+i/26))
			if err := Update(home, "x", func(s *State) { s.Show("", id) }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := len(Load(home, "x").Shown[""]); n != 40 {
		t.Errorf("%d 건만 남음", n)
	}
}

func TestRecentTranscripts(t *testing.T) {
	home := t.TempDir()
	for id, tr := range map[string]string{"a": "/x/a.jsonl", "b": "/x/b.jsonl", "c": "/x/a.jsonl", "none": ""} {
		if err := Update(home, id, func(s *State) { s.Transcript = tr }); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(Path(home, "b"), old, old); err != nil {
		t.Fatal(err)
	}
	got := RecentTranscripts(home, time.Now().Add(-time.Hour))
	slices.Sort(got)
	if !slices.Equal(got, []string{"/x/a.jsonl"}) {
		t.Errorf("최근 파일의 기록 경로만, 중복 없이: %v", got)
	}
	if len(RecentTranscripts(home, time.Now().Add(time.Hour))) != 0 {
		t.Error("기준 시각 이후에 고쳐진 파일만 읽어야 함")
	}
	if got := RecentTranscripts(home, time.Now().Add(-100*time.Hour)); len(got) != 2 {
		t.Errorf("기준을 넓히면 오래된 것도: %v", got)
	}
}

// 이전 판이 남긴 prompts 필드가 있어도 읽히고, 다시 쓰면 사라진다.
func TestOldPromptsFieldIsDropped(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(Path(home, "o")), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(Path(home, "o"), []byte(`{"shown":{"":["a"]},"prompts":["옛 프롬프트"]}`), 0o644)
	if err := Update(home, "o", func(s *State) { s.Show("", "b") }); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path(home, "o"))
	if strings.Contains(string(b), "옛 프롬프트") || strings.Contains(string(b), "prompts") {
		t.Errorf("프롬프트가 남음: %s", b)
	}
}

func TestRecordCallLimitsAndEndTurn(t *testing.T) {
	s := empty()
	s.RecordCall("", strings.Repeat("가", MaxCallChars+50))
	if got := []rune(s.Calls[""][0]); len(got) != MaxCallChars {
		t.Errorf("한 줄 상한: %d", len(got))
	}
	s.RecordCall("", strings.Repeat("a", MaxCallChars))
	if got := s.Calls[""][1]; len(got) != MaxCallChars {
		t.Errorf("상한 이하는 그대로: %d", len(got))
	}
	s = empty()
	for i := range MaxCallLines + 10 {
		s.RecordCall("", "Bash: "+strconv.Itoa(i))
	}
	c := s.Calls[""]
	if len(c) != MaxCallLines || c[0] != "Bash: 10" || c[len(c)-1] != "Bash: "+strconv.Itoa(MaxCallLines+9) {
		t.Errorf("오래된 것부터 버려야 함: %d줄 %q..%q", len(c), c[0], c[len(c)-1])
	}
	s.RecordCall("ag", "Read: /x")
	if len(s.Calls["ag"]) != 1 || len(s.Calls[""]) != MaxCallLines {
		t.Error("주체별로 따로 쌓아야 함")
	}
	s.Grant("git.push", Session)
	s.EndTurn()
	if len(s.Calls) != 0 || s.Grants["git.push"] != Session {
		t.Errorf("EndTurn 은 기록만 비우고 세션 허용은 남김: %+v %+v", s.Calls, s.Grants)
	}
}

func TestLoadExisting(t *testing.T) {
	home := t.TempDir()
	if _, ok := LoadExisting(home, "x"); ok {
		t.Error("없는 파일은 ok 가 거짓")
	}
	if _, ok := LoadExisting(home, ""); ok {
		t.Error("session_id 가 없으면 ok 가 거짓")
	}
	if err := Update(home, "x", func(s *State) { s.RecordCall("", "Bash: ls") }); err != nil {
		t.Fatal(err)
	}
	s, ok := LoadExisting(home, "x")
	if !ok || len(s.Calls[""]) != 1 {
		t.Errorf("읽어야 함: %v %+v", ok, s.Calls)
	}
	if err := os.WriteFile(Path(home, "x"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadExisting(home, "x"); ok {
		t.Error("깨진 파일은 ok 가 거짓")
	}
}
