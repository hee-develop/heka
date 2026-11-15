package index

import (
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// ErrNotFound 는 그 id 의 항목이 색인에 없다는 뜻이다.
var ErrNotFound = errors.New("항목이 색인에 없음")

// Index 는 읽기 전용으로 연 색인이다.
type Index struct{ db *sql.DB }

// Open 은 색인을 읽기 전용으로 연다. 파일이 없으면 오류다.
func Open(home string) (*Index, error) {
	p := Path(home)
	if _, err := os.Stat(p); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+p+"?mode=ro")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Index{db}, nil
}

// Close 는 색인을 닫는다.
func (x *Index) Close() error { return x.db.Close() }

// Record 는 색인에 든 항목 하나다.
type Record struct {
	ID, Text, Source, Quote, Checked, Expires, Confirm, CheckJSON, Body, Hash string
	Must                                                                      bool
	Scope, About                                                              []string
	When                                                                      map[string][]string // 종류 -> 값
}

// Item 은 id 로 항목을 읽는다. 없으면 ErrNotFound.
func (x *Index) Item(id string) (*Record, error) {
	r := &Record{When: map[string][]string{}}
	err := x.db.QueryRow(`SELECT id,text,must,source,quote,checked,expires,confirm,check_json,body,hash FROM item WHERE id=?`, id).
		Scan(&r.ID, &r.Text, &r.Must, &r.Source, &r.Quote, &r.Checked, &r.Expires, &r.Confirm, &r.CheckJSON, &r.Body, &r.Hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if r.Scope, err = x.strings(`SELECT scope FROM item_scope WHERE id=? ORDER BY rowid`, id); err != nil {
		return nil, err
	}
	if r.About, err = x.strings(`SELECT term FROM item_about WHERE id=? ORDER BY rowid`, id); err != nil {
		return nil, err
	}
	rows, err := x.db.Query(`SELECT kind,value FROM item_when WHERE id=? ORDER BY rowid`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		r.When[k] = append(r.When[k], v)
	}
	return r, rows.Err()
}

// ByAction 은 그 동작과 그 상위 동작에 조건이 걸린 항목 id 를 정렬해 돌려준다.
// 상위에 건 조건은 하위에도 걸리고, 하위에 건 조건은 상위 동작만으로는 걸리지 않는다.
func (x *Index) ByAction(action string) ([]string, error) {
	anc := map[string]bool{action: true}
	for i := strings.LastIndex(action, "."); i > 0; i = strings.LastIndex(action[:i], ".") {
		anc[action[:i]] = true
	}
	up, err := x.strings(`SELECT ancestor FROM action_in WHERE action=?`, action)
	if err != nil {
		return nil, err
	}
	for _, a := range up {
		anc[a] = true
	}
	return x.ByWhen("action", slices.Sorted(maps.Keys(anc))...)
}

// ByWhen 은 when 의 그 종류에 값 중 하나가 적힌 항목 id 를 정렬해 돌려준다.
func (x *Index) ByWhen(kind string, values ...string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	args := []any{kind}
	for _, v := range values {
		args = append(args, v)
	}
	return x.strings(`SELECT DISTINCT id FROM item_when WHERE kind=? AND value IN (?`+strings.Repeat(",?", len(values)-1)+`) ORDER BY id`, args...)
}

// AboutMatch 는 글에 주제어가 든 (항목 id, 주제어) 쌍이다.
type AboutMatch struct{ ID, Term string }

// AboutMatches 는 주제어가 글에 든 항목을 찾는다.
// 글과 주제어를 소문자로 바꾸고 공백을 지운 형태로 견주되, 주제어는 어절의 앞부분에서만 맞는다.
// 주제어가 영문·숫자로 끝나면 맞은 자리 뒤가 영문·숫자가 아니어야 한다.
func (x *Index) AboutMatches(s string) ([]AboutMatch, error) {
	orig := []rune(s)
	var norm []rune
	var pos []int // norm[i] 가 orig 의 몇 번째 글자인가
	for i, r := range orig {
		if !isSpace(r) {
			norm = append(norm, lower(r))
			pos = append(pos, i)
		}
	}
	rows, err := x.db.Query(`SELECT id,term,norm FROM item_about ORDER BY id,rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AboutMatch
	seen := map[AboutMatch]bool{}
	for rows.Next() {
		var id, term, nt string
		if err := rows.Scan(&id, &term, &nt); err != nil {
			return nil, err
		}
		t := []rune(nt)
		m := AboutMatch{id, term}
		if len(t) == 0 || seen[m] {
			continue
		}
		for i := 0; i+len(t) <= len(norm); i++ {
			if string(norm[i:i+len(t)]) != nt {
				continue
			}
			if p := pos[i]; p > 0 && (unicode.IsLetter(orig[p-1]) || unicode.IsDigit(orig[p-1])) {
				continue
			}
			if isASCIIAlnum(t[len(t)-1]) {
				if e := pos[i+len(t)-1] + 1; e < len(orig) && isASCIIAlnum(lower(orig[e])) {
					continue
				}
			}
			seen[m] = true
			out = append(out, m)
			break
		}
	}
	return out, rows.Err()
}

// Scored 는 낱말 점수를 받은 항목이다.
type Scored struct {
	ID    string
	Score float64
}

// Lexical 은 낱말 조각 점수로 상위 k 건을 돌려준다(점수 > 0, 점수 내림차순, 같으면 id 오름차순).
// 점수는 질의와 항목에 모두 있는 조각의 idf 합을 질의 조각 전체의 idf 합으로 나눈 값이다.
func (x *Index) Lexical(s string, k int) ([]Scored, error) {
	grams := Grams(s)
	if len(grams) == 0 || k <= 0 {
		return nil, nil
	}
	var n int
	if err := x.db.QueryRow(`SELECT count(*) FROM item`).Scan(&n); err != nil {
		return nil, err
	}
	var total float64
	sum := map[string]float64{}
	for g := range grams {
		var df int
		if err := x.db.QueryRow(`SELECT df FROM lex_df WHERE gram=?`, g).Scan(&df); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		w := idf(n, df)
		total += w
		if df == 0 {
			continue
		}
		ids, err := x.strings(`SELECT id FROM lex WHERE gram=?`, g)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			sum[id] += w
		}
	}
	out := make([]Scored, 0, len(sum))
	for id, v := range sum {
		out = append(out, Scored{id, v / total})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > k {
		out = out[:k]
	}
	return out, nil
}

func (x *Index) strings(q string, args ...any) ([]string, error) {
	rows, err := x.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("색인 조회 실패: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func isSpace(r rune) bool      { return unicode.IsSpace(r) }
func lower(r rune) rune        { return unicode.ToLower(r) }
func isASCIIAlnum(r rune) bool { return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' }
