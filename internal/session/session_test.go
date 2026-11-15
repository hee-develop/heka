package session

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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
