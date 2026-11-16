package main

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/hee-develop/heka/internal/action"
	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/index"
	"github.com/hee-develop/heka/internal/item"
)

// runIndex 는 `heka index [--check]` 를 실행한다.
// 항목과 설정을 읽어 검증하고, --check 가 아니면 색인을 만든다. error 문제가 있으면 종료 코드 1 이다.
func runIndex(args []string, stdout, stderr io.Writer) int {
	check := false
	for _, a := range args {
		if a != "--check" {
			fmt.Fprintf(stderr, "heka index: 알 수 없는 인자 %q\n", a)
			return 2
		}
		check = true
	}
	home, err := config.Home()
	if err != nil {
		fmt.Fprintln(stderr, "heka index:", err)
		return 1
	}
	cfg, err := config.Load(home)
	if err != nil {
		fmt.Fprintln(stderr, "heka index:", err)
		return 1
	}
	fp, items, err := loadItems(home, cfg)
	if err != nil {
		fmt.Fprintln(stderr, "heka index:", err)
		return 1
	}
	valid, excluded, hasError := 0, 0, false
	for _, it := range items {
		if it.Valid() {
			valid++
		} else {
			excluded++
			hasError = true
		}
	}
	if check {
		fmt.Fprintf(stdout, "항목 %d건 유효, 제외 %d건\n", valid, excluded)
	} else {
		n, err := index.Build(home, items, cfg, fp)
		if err != nil {
			fmt.Fprintln(stderr, "heka index: 색인을 만들지 못함:", err)
			return 1
		}
		fmt.Fprintf(stdout, "항목 %d건 색인, 제외 %d건\n", n, excluded)
	}
	for _, it := range items {
		for _, p := range it.Problems {
			fmt.Fprintln(stdout, p)
		}
	}
	if hasError {
		return 1
	}
	return 0
}

// loadItems 는 항목을 읽고, 읽기 전에 잰 지문을 함께 돌려준다.
// 지문을 먼저 재야, 읽는 도중 고쳐진 파일이 있으면 다음에 낡은 색인으로 드러난다.
func loadItems(home string, cfg *config.Config) (string, []*item.Item, error) {
	fp, err := index.Fingerprint(home)
	if err != nil {
		return "", nil, err
	}
	items, err := item.LoadDir(filepath.Join(home, "items"), action.Vocab(cfg))
	return fp, items, err
}
