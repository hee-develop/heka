// Package stat 는 걸린 시간 같은 정수 값 목록의 분포를 잰다.
package stat

import (
	"math"
	"slices"
)

// Dist 는 값 목록의 분포다. 비어 있으면 모두 0 이다.
type Dist struct {
	N      int   `json:"n"`
	Median int64 `json:"median_ms"`
	P95    int64 `json:"p95_ms"`
	Max    int64 `json:"max_ms"`
}

// Of 는 값 목록(밀리초)의 분포를 돌려준다. 받은 목록은 바꾸지 않는다.
func Of(vals []int64) Dist {
	s := slices.Clone(vals)
	slices.Sort(s)
	d := Dist{N: len(s)}
	if len(s) == 0 {
		return d
	}
	d.Median, d.P95, d.Max = Median(s), Percentile(s, 0.95), s[len(s)-1]
	return d
}

// Percentile 은 오름차순으로 정렬된 값에서 가까운 순위(nearest-rank) 방식으로 p(0~1) 백분위를 고른다.
func Percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	k := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[min(max(k, 0), len(sorted)-1)]
}

// Median 은 오름차순으로 정렬된 값의 중앙값이다. 개수가 짝수면 가운데 둘의 평균(내림)이다.
func Median(sorted []int64) int64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}
