package index

import (
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	tokenRe   = regexp.MustCompile(`[a-z0-9][a-z0-9_\-./]*[a-z0-9]|[가-힣]+`)
	hangulRe  = regexp.MustCompile(`^[가-힣]+$`)
	splitRune = func(r rune) bool { return strings.ContainsRune("_-./", r) }
)

// Grams 는 글에서 낱말 조각 집합을 뽑는다.
// 한글 토큰은 연속한 두 글자 조각 전부, 그 밖의 토큰은 토큰 자체와 [_\-./] 로 나눈 길이 2 이상의 부분이다.
func Grams(s string) map[string]bool {
	set := map[string]bool{}
	for _, tok := range tokenRe.FindAllString(strings.ToLower(s), -1) {
		if hangulRe.MatchString(tok) {
			r := []rune(tok)
			for i := 0; i+1 < len(r); i++ {
				set[string(r[i:i+2])] = true
			}
			continue
		}
		if utf8.RuneCountInString(tok) >= 2 {
			set[tok] = true
		}
		for _, part := range strings.FieldsFunc(tok, splitRune) {
			if utf8.RuneCountInString(part) >= 2 {
				set[part] = true
			}
		}
	}
	return set
}

// idf 는 N 개 항목 중 df 개에 든 조각의 희소도다.
func idf(n, df int) float64 {
	return math.Log(1 + (float64(n)-float64(df)+0.5)/(float64(df)+0.5))
}
