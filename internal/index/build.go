// Package index 는 항목과 설정에서 SQLite 색인 파일을 만들고 읽는다.
package index

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/item"

	_ "modernc.org/sqlite" // 순수 Go SQLite 드라이버
)

const schemaVersion = "1"

const ddl = `
CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE item(id TEXT PRIMARY KEY, text TEXT, must INTEGER, source TEXT, quote TEXT, checked TEXT, expires TEXT, confirm TEXT, check_json TEXT, body TEXT, hash TEXT);
CREATE TABLE item_scope(id TEXT, scope TEXT);
CREATE TABLE item_when(id TEXT, kind TEXT, value TEXT);
CREATE TABLE item_about(id TEXT, term TEXT, norm TEXT);
CREATE TABLE scope_in(scope TEXT, ancestor TEXT);
CREATE TABLE action_in(action TEXT, ancestor TEXT);
CREATE TABLE action_phrase(action TEXT, phrase TEXT);
CREATE TABLE lex(gram TEXT, id TEXT, PRIMARY KEY(gram, id)) WITHOUT ROWID;
CREATE TABLE lex_df(gram TEXT PRIMARY KEY, df INTEGER);
CREATE INDEX item_when_idx ON item_when(kind, value);
CREATE INDEX action_in_idx ON action_in(action);
`

// Path 는 home 아래의 색인 파일 경로다.
func Path(home string) string { return filepath.Join(home, "index", "index.db") }

// Norm 은 주제어 견줌에 쓰는 형태다. 소문자로 바꾸고 공백을 지운다.
func Norm(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !isSpace(r) {
			b.WriteRune(lower(r))
		}
	}
	return b.String()
}

// Build 는 유효한 항목(error 없는 것)으로 색인을 만들어 같은 폴더의 임시 파일에서 한 번에 교체한다.
// fingerprint 는 항목을 읽기 전에 잰 값을 넘긴다.
func Build(home string, items []*item.Item, cfg *config.Config, fingerprint string) (n int, err error) {
	dir := filepath.Dir(Path(home))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	tmp := filepath.Join(dir, fmt.Sprintf("index.db.tmp-%d-%d", os.Getpid(), time.Now().UnixNano()))
	_ = os.Remove(tmp)
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	db, err := sql.Open("sqlite", tmp)
	if err != nil {
		return 0, err
	}
	n, err = fill(db, items, cfg, fingerprint)
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, err
	}
	return n, os.Rename(tmp, Path(home))
}

func fill(db *sql.DB, items []*item.Item, cfg *config.Config, fingerprint string) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(ddl); err != nil {
		return 0, err
	}
	exec := func(q string, a ...any) {
		if err == nil {
			_, err = tx.Exec(q, a...)
		}
	}

	scopes := map[string]bool{}
	for _, s := range cfg.ScopeNames() {
		scopes[s] = true
	}
	actions := map[string]bool{}
	for a := range cfg.Actions {
		actions[a] = true
	}
	df := map[string]int{}
	count := 0
	for _, it := range items {
		if !it.Valid() {
			continue
		}
		count++
		chk := ""
		if it.Check != nil {
			b, _ := json.Marshal(it.Check)
			chk = string(b)
		}
		exec(`INSERT INTO item VALUES(?,?,?,?,?,?,?,?,?,?,?)`, it.ID, it.Text, it.Must, it.Source, it.Quote,
			string(it.Checked), string(it.Expires), it.ConfirmValue(), chk, it.Body, it.Hash)
		for _, s := range it.Scope {
			scopes[s] = true
			exec(`INSERT INTO item_scope VALUES(?,?)`, it.ID, s)
		}
		for _, w := range it.When.Kinds() {
			if w[0] == "action" {
				actions[w[1]] = true
			}
			exec(`INSERT INTO item_when VALUES(?,?,?)`, it.ID, w[0], w[1])
		}
		for _, t := range it.About {
			exec(`INSERT INTO item_about VALUES(?,?,?)`, it.ID, t, Norm(t))
		}
		for g := range Grams(it.Text + " " + strings.Join(it.About, " ")) {
			exec(`INSERT INTO lex VALUES(?,?)`, g, it.ID)
			df[g]++
		}
	}
	for g, d := range df {
		exec(`INSERT INTO lex_df VALUES(?,?)`, g, d)
	}
	for _, s := range slices.Sorted(maps.Keys(scopes)) {
		for _, a := range cfg.ScopeClosure(s) {
			exec(`INSERT INTO scope_in VALUES(?,?)`, s, a)
		}
	}
	for _, a := range slices.Sorted(maps.Keys(actions)) {
		for _, up := range cfg.ActionClosure(a) {
			exec(`INSERT INTO action_in VALUES(?,?)`, a, up)
		}
	}
	for _, p := range cfg.Phrases() {
		exec(`INSERT INTO action_phrase VALUES(?,?)`, p[0], p[1])
	}
	for k, v := range map[string]string{"schema": schemaVersion, "built_at": time.Now().UTC().Format(time.RFC3339),
		"fingerprint": fingerprint, "item_count": strconv.Itoa(count)} {
		exec(`INSERT INTO meta VALUES(?,?)`, k, v)
	}
	if err != nil {
		return 0, err
	}
	return count, tx.Commit()
}

// Fingerprint 는 items/*.md 와 config.yaml 의 (상대 경로, 크기, 수정 시각)을 정렬해 해시한 값이다.
func Fingerprint(home string) (string, error) {
	paths, err := filepath.Glob(filepath.Join(home, "items", "*.md"))
	if err != nil {
		return "", err
	}
	paths = append(paths, filepath.Join(home, "config.yaml"))
	var lines []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		if st.IsDir() {
			continue
		}
		rel, _ := filepath.Rel(home, p)
		lines = append(lines, fmt.Sprintf("%s\t%d\t%d", filepath.ToSlash(rel), st.Size(), st.ModTime().UnixNano()))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

// Stale 은 색인이 없거나 열 수 없거나 meta.fingerprint 가 지금 값과 다르면 true 다.
func Stale(home string) bool {
	cur, err := Fingerprint(home)
	if err != nil {
		return true
	}
	ix, err := Open(home)
	if err != nil {
		return true
	}
	defer ix.Close()
	var fp string
	if err := ix.db.QueryRow(`SELECT value FROM meta WHERE key='fingerprint'`).Scan(&fp); err != nil {
		return true
	}
	return fp != cur
}

// Ensure 는 색인이 낡았으면 항목을 다시 읽어 만든다.
func Ensure(home string, cfg *config.Config) error {
	if !Stale(home) {
		return nil
	}
	fp, err := Fingerprint(home)
	if err != nil {
		return err
	}
	items, err := item.LoadDir(filepath.Join(home, "items"), cfg.ActionVocab())
	if err != nil {
		return err
	}
	if _, err := Build(home, items, cfg, fp); err != nil {
		return fmt.Errorf("색인을 만들지 못함: %w", err)
	}
	return nil
}
