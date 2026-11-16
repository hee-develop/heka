package replay

import (
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/hee-develop/heka/internal/stat"
)

// Rate 는 분수 하나다. N 이 0 이면 잴 수 없어 Rate 가 null 이다.
type Rate struct {
	N    int      `json:"n"`
	OK   int      `json:"ok"`
	Rate *float64 `json:"rate"`
}

func rate(ok, n int) Rate {
	r := Rate{N: n, OK: ok}
	if n > 0 {
		v := float64(ok) / float64(n)
		r.Rate = &v
	}
	return r
}

// Precision 은 정밀도다. 맞음과 틀림만 분모에 든다. 정답이 없는 쌍과 모르는 것은 따로 센다.
type Precision struct {
	Correct   int      `json:"correct"`
	Wrong     int      `json:"wrong"`
	Unlabeled int      `json:"unlabeled"`
	Unsure    int      `json:"unsure"`
	Rate      *float64 `json:"rate"`
}

// Metrics 는 사례 묶음 하나의 성공 조건이다.
type Metrics struct {
	Cases        int       `json:"cases"`
	Errors       int       `json:"errors"`
	Delivery     Rate      `json:"delivery"`      // inject 가 있는 사례 가운데 모두 들어간 비율
	DeliveryMust Rate      `json:"delivery_must"` // 그중 필수 항목이 대상인 사례만
	Precision    Precision `json:"precision"`
	Silence      Rate      `json:"silence"` // silent 사례 가운데 아무것도 안 넣은 비율
}

// Timing 은 사례당 걸린 시간이다. 판정을 불렀던 사례와 아닌 사례를 나눈다.
type Timing struct {
	WithJudge    stat.Dist `json:"with_judge"`
	WithoutJudge stat.Dist `json:"without_judge"`
}

// Tokens 는 사례당 넣은 글의 토큰(어림)이다. 넣지 않은 사례는 0 으로 센다.
type Tokens struct {
	Mean float64 `json:"mean"`
	Max  int     `json:"max"`
}

// RunSummary 는 한 회차의 요약이다. Groups 는 사례의 confirmed·source 값별 묶음이다.
type RunSummary struct {
	Run     int                `json:"run"`
	Metrics                    // 전체
	Groups  map[string]Metrics `json:"groups,omitempty"`
	Timing  Timing             `json:"timing"`
	Tokens  Tokens             `json:"tokens"`
}

// Floor 는 회차들 가운데 가장 낮은 값이다. 잴 수 있는 회차가 없으면 null 이다.
type Floor struct {
	Delivery     *float64 `json:"delivery"`
	DeliveryMust *float64 `json:"delivery_must"`
	Precision    *float64 `json:"precision"`
	Silence      *float64 `json:"silence"`
}

// Report 는 재현의 전체 결과다.
type Report struct {
	Cases        int          `json:"cases"`
	Skipped      int          `json:"skipped"`
	SkippedLines []Skip       `json:"skipped_lines,omitempty"`
	Runs         []RunSummary `json:"runs"`
	Lowest       Floor        `json:"lowest"` // 회차 가운데 가장 낮은 값(합격 여부는 이것으로 본다)
}

func tally(rs []CaseResult) Metrics {
	m := Metrics{Cases: len(rs)}
	var dOK, dN, mOK, mN, sOK, sN int
	for _, r := range rs {
		if r.Error != "" {
			m.Errors++
		}
		if r.delivery {
			dN++
			ok := len(r.Missing) == 0 && r.Error == ""
			if ok {
				dOK++
			}
			if r.must {
				mN++
				if ok {
					mOK++
				}
			}
		}
		if r.silent {
			sN++
			if len(r.Injected) == 0 && r.Error == "" {
				sOK++
			}
		}
		m.Precision.Wrong += len(r.Wrong)
		m.Precision.Unlabeled += len(r.Unlabeled)
		m.Precision.Unsure += len(r.Unsure)
		m.Precision.Correct += len(r.Injected) - len(r.Wrong) - len(r.Unlabeled) - len(r.Unsure)
	}
	m.Delivery, m.DeliveryMust, m.Silence = rate(dOK, dN), rate(mOK, mN), rate(sOK, sN)
	if n := m.Precision.Correct + m.Precision.Wrong; n > 0 {
		v := float64(m.Precision.Correct) / float64(n)
		m.Precision.Rate = &v
	}
	return m
}

func summarize(run int, rs []CaseResult) RunSummary {
	s := RunSummary{Run: run, Metrics: tally(rs)}
	by := map[string][]CaseResult{}
	var with, without []int64
	var tokSum int
	for _, r := range rs {
		if r.Confirmed != nil {
			k := fmt.Sprintf("confirmed=%t", *r.Confirmed)
			by[k] = append(by[k], r)
		}
		if r.Source != "" {
			k := "source=" + r.Source
			by[k] = append(by[k], r)
		}
		if r.Judged {
			with = append(with, r.DurationMS)
		} else {
			without = append(without, r.DurationMS)
		}
		tokSum += r.Tokens
		s.Tokens.Max = max(s.Tokens.Max, r.Tokens)
	}
	if len(by) > 0 {
		s.Groups = map[string]Metrics{}
		for k, g := range by {
			s.Groups[k] = tally(g)
		}
	}
	s.Timing = Timing{stat.Of(with), stat.Of(without)}
	if len(rs) > 0 {
		s.Tokens.Mean = float64(tokSum) / float64(len(rs))
	}
	return s
}

func lowest(vals []*float64) *float64 {
	var out *float64
	for _, v := range vals {
		if v != nil && (out == nil || *v < *out) {
			out = v
		}
	}
	return out
}

// Summarize 는 회차별 사례 결과를 요약한다.
func Summarize(runs [][]CaseResult, skips []Skip) Report {
	rep := Report{Skipped: len(skips), SkippedLines: skips, Runs: []RunSummary{}}
	if len(runs) > 0 {
		rep.Cases = len(runs[0])
	}
	var d, dm, p, s []*float64
	for i, rs := range runs {
		sum := summarize(i+1, rs)
		rep.Runs = append(rep.Runs, sum)
		d, dm = append(d, sum.Delivery.Rate), append(dm, sum.DeliveryMust.Rate)
		p, s = append(p, sum.Precision.Rate), append(s, sum.Silence.Rate)
	}
	rep.Lowest = Floor{lowest(d), lowest(dm), lowest(p), lowest(s)}
	return rep
}

// Thresholds 는 합격선(0~1)이다. nil 이면 보지 않는다.
type Thresholds struct {
	Delivery, Precision, Silence *float64
}

// Check 는 가장 낮은 회차가 합격선에 못 미친 조건을 사람이 읽는 글로 돌려준다.
// 전달은 전체와 필수 대상 사례 모두에 적용한다. 잴 사례가 없는 조건은 판정하지 않고 notes 로 알린다.
func Check(rep Report, t Thresholds) (fails, notes []string) {
	one := func(name string, got, min *float64) {
		switch {
		case min == nil:
		case got == nil:
			notes = append(notes, name+": 잴 사례가 없어 합격선을 판정하지 않음")
		case *got < *min:
			fails = append(fails, fmt.Sprintf("%s: 가장 낮은 회차 %.1f%% < 합격선 %.1f%%", name, *got*100, *min*100))
		}
	}
	one("전달", rep.Lowest.Delivery, t.Delivery)
	one("전달(필수)", rep.Lowest.DeliveryMust, t.Delivery)
	one("정밀도", rep.Lowest.Precision, t.Precision)
	one("침묵", rep.Lowest.Silence, t.Silence)
	return fails, notes
}

func pct(r *float64) string {
	if r == nil {
		return "잴 수 없음"
	}
	return fmt.Sprintf("%.1f%%", *r*100)
}

func frac(r Rate) string {
	if r.N == 0 {
		return "해당 사례 없음"
	}
	return fmt.Sprintf("%d/%d (%s)", r.OK, r.N, pct(r.Rate))
}

func metricsText(w io.Writer, indent string, m Metrics) {
	fmt.Fprintf(w, "%s사례 %d건 (오류 %d건)\n", indent, m.Cases, m.Errors)
	fmt.Fprintf(w, "%s전달: %s\n", indent, frac(m.Delivery))
	fmt.Fprintf(w, "%s전달(필수 항목이 대상): %s\n", indent, frac(m.DeliveryMust))
	p := m.Precision
	fmt.Fprintf(w, "%s정밀도: 맞음 %d, 틀림 %d → %s (정답 없음 %d쌍, 모르는 것 %d쌍은 따로)\n", indent, p.Correct, p.Wrong, pct(p.Rate), p.Unlabeled, p.Unsure)
	fmt.Fprintf(w, "%s침묵: %s\n", indent, frac(m.Silence))
}

func distText(d stat.Dist) string {
	if d.N == 0 {
		return "사례 없음"
	}
	return fmt.Sprintf("%d건, 중앙값 %dms, 95%% %dms, 최댓값 %dms", d.N, d.Median, d.P95, d.Max)
}

// Text 는 사람이 읽는 요약이다.
func Text(w io.Writer, rep Report) {
	fmt.Fprintf(w, "재현: 사례 %d건, %d회", rep.Cases, len(rep.Runs))
	if rep.Skipped > 0 {
		fmt.Fprintf(w, ", 읽지 못해 건너뛴 줄 %d건", rep.Skipped)
	}
	fmt.Fprintln(w)
	for _, s := range rep.SkippedLines {
		fmt.Fprintf(w, "  건너뜀: %d번째 줄, %s\n", s.Line, s.Reason)
	}
	for _, r := range rep.Runs {
		fmt.Fprintf(w, "\n[회차 %d]\n", r.Run)
		metricsText(w, "  ", r.Metrics)
		for _, k := range slices.Sorted(maps.Keys(r.Groups)) {
			fmt.Fprintf(w, "  묶음 %s\n", k)
			metricsText(w, "    ", r.Groups[k])
		}
		fmt.Fprintf(w, "  걸린 시간(판정 있음): %s\n", distText(r.Timing.WithJudge))
		fmt.Fprintf(w, "  걸린 시간(판정 없음): %s\n", distText(r.Timing.WithoutJudge))
		fmt.Fprintf(w, "  넣은 글의 토큰: 사례당 평균 %.1f, 최댓값 %d\n", r.Tokens.Mean, r.Tokens.Max)
	}
	if len(rep.Runs) > 1 {
		l := rep.Lowest
		fmt.Fprintf(w, "\n[가장 낮은 회차] 전달 %s, 전달(필수) %s, 정밀도 %s, 침묵 %s\n", pct(l.Delivery), pct(l.DeliveryMust), pct(l.Precision), pct(l.Silence))
	}
}
