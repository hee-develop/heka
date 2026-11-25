package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// 세션 기록 파일의 형식만 흉내 낸 지어낸 자료다.
func writeJSONL(t *testing.T, path string, lines ...any) {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		if s, ok := l.(string); ok {
			b.WriteString(s + "\n")
			continue
		}
		j, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func msg(typ string, content any, extra map[string]any) map[string]any {
	m := map[string]any{"type": typ, "message": map[string]any{"role": typ, "content": content}}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestUserTexts(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeJSONL(t, p,
		msg("user", "문자열 내용", nil),
		msg("user", []any{map[string]any{"type": "text", "text": "배열 내용 하나"}, map[string]any{"type": "text", "text": "배열 내용 둘"}}, nil),
		msg("assistant", []any{map[string]any{"type": "text", "text": "AI 글"}}, nil),
		msg("user", []any{map[string]any{"type": "tool_result", "tool_use_id": "x", "content": "도구 결과"}}, nil),
		msg("user", []any{map[string]any{"type": "tool_result", "content": "결과"}, map[string]any{"type": "text", "text": "결과 옆의 글"}}, nil),
		msg("user", "메타", map[string]any{"isMeta": true}),
		msg("user", "곁가지", map[string]any{"isSidechain": true}),
		msg("user", "<local-command-stdout>출력</local-command-stdout>", nil),
		msg("user", "<command-name>/foo</command-name>", nil),
		msg("user", "  <system-reminder>안내</system-reminder>", nil),
		msg("user", "<task-notification>설정 접두</task-notification>", nil),
		msg("user", []any{map[string]any{"type": "image", "source": "x"}, map[string]any{"type": "text", "text": "이미지 옆의 글"}}, nil),
		msg("summary", "요약", nil),
		"{깨진 줄 \"user\"",
		"",
		msg("user", "마지막 줄", nil),
	)
	// 마지막 줄 끝에 줄바꿈이 없어도 읽는다.
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"type":"user","message":{"content":"끝 줄"}}`)
	f.Close()
	got, err := UserTexts(p, []string{"<task-notification"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"문자열 내용", "배열 내용 하나", "배열 내용 둘", "이미지 옆의 글", "마지막 줄", "끝 줄"}
	if !slices.Equal(got, want) {
		t.Errorf("사용자 글 = %q\n기대 %q", got, want)
	}
	if _, err := UserTexts(filepath.Join(t.TempDir(), "없음"), nil); err == nil {
		t.Error("없는 파일은 오류")
	}
}

func askUse(id string) map[string]any {
	return msg("assistant", []any{map[string]any{"type": "tool_use", "id": id, "name": "AskUserQuestion", "input": map[string]any{}}}, nil)
}

func askResultLine(id string, questions []any, answers, annotations map[string]any) map[string]any {
	return msg("user", []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": "The user answered: ..."}},
		map[string]any{"toolUseResult": map[string]any{"questions": questions, "answers": answers, "annotations": annotations}})
}

func question(q string, multi bool, labels ...string) map[string]any {
	var opts []any
	for _, l := range labels {
		opts = append(opts, map[string]any{"label": l, "description": "설명"})
	}
	return map[string]any{"question": q, "header": "머리", "multiSelect": multi, "options": opts}
}

func queued(kind string, prompt any, extra map[string]any) map[string]any {
	m := map[string]any{"type": "attachment", "attachment": map[string]any{
		"type": "queued_command", "prompt": prompt, "commandMode": "prompt", "origin": map[string]any{"kind": kind}}}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// 사용자의 말이 일반 메시지 말고 질문 도구의 답과 작업 중 메시지로 남는 경우다.
// 여러 개를 고른 답은 Claude Code 가 남기는 모양을 따랐다. 고른 라벨 뒤에 직접 친 글이 오고, ", " 가 든 칸은 큰따옴표로 감싼다.
func TestUserTextsAskAndQueued(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeJSONL(t, p,
		askUse("ask1"),
		askResultLine("ask1",
			[]any{
				question("방향?", false, "가안 (추천)", "나안"),
				question("고른 라벨만?", false, "가안 (추천)", "나안"),
				question("라벨로 시작?", false, "예", "아니오"),
				question("여럿?", true, "하나, 둘", "셋", "넷"),
				question("라벨 든 직접 글?", true, "가", "나"),
				question("배열?", true, "셋", "넷"),
				question("안 답함?", false, "셋"),
			},
			map[string]any{
				"방향?":        "직접 친 답, 쉼표 포함",
				"고른 라벨만?":    "가안 (추천)",
				"라벨로 시작?":    "예, 그렇게 하되 테스트도",
				"여럿?":        `"하나, 둘", 셋, 직접 친 덧붙임`,
				"라벨 든 직접 글?": `나, "직접 글, 가, 그 뒤"`,
				"배열?":        []any{"넷", "배열 속 직접 친 글"},
			},
			map[string]any{"고른 라벨만?": map[string]any{"notes": "메모 글", "preview": "AI 미리보기"}, "여럿?": "모양이 다른 메모"},
		),
		// 질문 도구 결과가 든 줄에서는 답만 읽고 같은 줄의 다른 글은 읽지 않는다.
		askUse("ask2"),
		msg("user", []any{
			map[string]any{"type": "text", "text": "결과 앞의 글"},
			map[string]any{"type": "tool_result", "tool_use_id": "ask2", "content": "The user answered: ..."},
		}, map[string]any{"toolUseResult": map[string]any{"questions": []any{question("둘째?", false, "셋")}, "answers": map[string]any{"둘째?": "둘째 답"}}}),
		// 질문 글이 겹치면 다른 질문의 라벨도 라벨로 보고 답은 한 번만 읽는다. multiSelect 가 없으면 읽지 않는다.
		askUse("ask3"),
		askResultLine("ask3",
			[]any{question("겹침?", false, "가"), question("겹침?", false, "규칙: 늘 X 한다"), map[string]any{"question": "키 없음?", "options": []any{map[string]any{"label": "라"}}}},
			map[string]any{"겹침?": "규칙: 늘 X 한다", "키 없음?": "라, 키 없는 질문의 직접 글"}, nil),
		askUse("ask5"),
		askResultLine("ask5", []any{question("겹친 직접 글?", false, "가"), question("겹친 직접 글?", false, "나")},
			map[string]any{"겹친 직접 글?": "겹친 질문의 직접 글"}, map[string]any{"겹친 직접 글?": map[string]any{"notes": "겹친 질문의 메모"}}),
		// AI 가 답을 미리 채워 부른 질문 도구의 결과는 읽지 않는다.
		msg("assistant", []any{map[string]any{"type": "tool_use", "id": "pre1", "name": "AskUserQuestion",
			"input": map[string]any{"questions": []any{}, "answers": map[string]any{"미리?": "AI 가 채운 답"}}}}, nil),
		askResultLine("pre1", []any{question("미리?", false, "가")}, map[string]any{"미리?": "AI 가 채운 답"}, nil),
		msg("assistant", []any{map[string]any{"type": "tool_use", "id": "pre2", "name": "AskUserQuestion",
			"input": map[string]any{"questions": []any{}, "annotations": map[string]any{"미리 메모?": map[string]any{"notes": "AI 가 채운 메모"}}}}}, nil),
		askResultLine("pre2", []any{question("미리 메모?", false, "가")}, map[string]any{"미리 메모?": "미리 메모 호출의 답"}, nil),
		msg("assistant", []any{map[string]any{"type": "tool_use", "id": "pre3", "name": "AskUserQuestion",
			"input": map[string]any{"questions": []any{}, "answers": map[string]any{}, "annotations": nil}}}, nil),
		askResultLine("pre3", []any{question("빈 미리?", false, "가")}, map[string]any{"빈 미리?": "빈 값으로 부른 답"}, nil),
		// 질문 도구 결과와 다른 도구의 결과가 한 줄에 섞이면 읽지 않는다.
		askUse("ask4"),
		msg("user", []any{
			map[string]any{"type": "tool_result", "tool_use_id": "ask4", "content": "The user answered: ..."},
			map[string]any{"type": "tool_result", "tool_use_id": "bash0", "content": "출력"},
		}, map[string]any{"toolUseResult": map[string]any{"questions": []any{question("섞임?", false, "셋")}, "answers": map[string]any{"섞임?": "섞인 줄의 답"}}}),
		// 질문 도구가 아닌 도구의 결과는 같은 모양이어도 읽지 않는다.
		msg("assistant", []any{map[string]any{"type": "tool_use", "id": "bash1", "name": "Bash", "input": map[string]any{}}}, nil),
		askResultLine("bash1", []any{question("가짜?", false, "셋")}, map[string]any{"가짜?": "AI 가 만든 출력"}, nil),
		// id 를 모르는 결과도 읽지 않는다.
		askResultLine("unknown", []any{question("모름?", false, "셋")}, map[string]any{"모름?": "모르는 id 의 답"}, nil),
		// 곁가지의 질문 도구 호출은 기억하지 않는다.
		msg("assistant", []any{map[string]any{"type": "tool_use", "id": "side1", "name": "AskUserQuestion"}}, map[string]any{"isSidechain": true}),
		askResultLine("side1", []any{question("곁?", false, "셋")}, map[string]any{"곁?": "곁가지 답"}, nil),
		queued("human", "작업 중 보낸 말", nil),
		queued("human", []any{map[string]any{"type": "text", "text": "이미지와 보낸 말"}, map[string]any{"type": "image", "source": map[string]any{}}}, nil),
		queued("human", "<system-reminder>안내</system-reminder>", nil),
		queued("peer", "다른 세션의 말", nil),
		queued("coordinator", "조정자의 말", nil),
		queued("human", "곁가지 대기 말", map[string]any{"isSidechain": true}),
		map[string]any{"type": "attachment", "attachment": map[string]any{"type": "queued_command", "prompt": "알림 모드의 글",
			"commandMode": "task-notification", "origin": map[string]any{"kind": "human"}}},
		map[string]any{"type": "attachment", "attachment": map[string]any{"type": "task_reminder", "prompt": "다른 첨부"}},
		map[string]any{"type": "queue-operation", "operation": "enqueue", "content": "대기열 사본"},
	)
	got, err := UserTexts(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"직접 친 답, 쉼표 포함", "메모 글", "예, 그렇게 하되 테스트도", "직접 친 덧붙임", "직접 글, 가, 그 뒤", "배열 속 직접 친 글",
		"둘째 답", "겹친 질문의 직접 글", "겹친 질문의 메모", "빈 값으로 부른 답",
		"작업 중 보낸 말", "이미지와 보낸 말",
	}
	if !slices.Equal(got, want) {
		t.Errorf("사용자 글 = %q\n기대 %q", got, want)
	}
}

func TestTypedAnswer(t *testing.T) {
	labels := map[string]bool{"가": true, "가, 나": true, "다 (추천)": true, `항상 "테스트", 먼저`: true,
		"a": true, "b, 항상 테스트를 먼저 작성한다": true, `a, "b`: true}
	for _, c := range []struct {
		ans   string
		multi bool
		want  string
	}{
		{"가", false, ""},
		{"가, 나", false, ""},
		{"가, 직접", false, "가, 직접"},
		{"직접", false, "직접"},
		{"가", true, ""},
		{"가, 다 (추천)", true, ""},
		{`"가, 나", 다 (추천)`, true, ""},
		{"가, 나", true, ""},          // 답 전체가 라벨이면 먼저 뺀다
		{"가, 다 (추천), 나", true, "나"}, // 라벨 둘을 고르고 "나" 를 직접 쳤다
		{"다 (추천), 직접", true, "직접"},
		{`다 (추천), "직접, 가, 그 뒤"`, true, "직접, 가, 그 뒤"},
		{`"항상 ""테스트"", 먼저"`, true, ""},
		{`a, "b, 항상 테스트를 먼저 작성한다"`, true, ""}, // 라벨이 칸 경계를 넘어 맞지 않는다
		{`"따옴표 ""안"" 글, 끝"`, true, `따옴표 "안" 글, 끝`},
		{"직접, 가", true, ""},   // 직접 친 글은 맨 뒤에만 온다
		{"직접, 더", true, ""},   // 남은 칸이 여럿이면 가릴 수 없다
		{`"닫히지 않음`, true, ""}, // 감싼 칸이 닫히지 않았다
		{`"감쌈"뒤`, true, ""},
		{"가, ", true, ""},
		{"가나", true, "가나"},
		{"", true, ""},
	} {
		if got := typedAnswer(c.ans, labels, c.multi); got != c.want {
			t.Errorf("typedAnswer(%q, multi=%v) = %q, 기대 %q", c.ans, c.multi, got, c.want)
		}
	}
}

func TestTranscriptsGlob(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string, age time.Duration) string {
		p := filepath.Join(root, rel)
		writeJSONL(t, p, msg("user", "x", nil))
		ts := time.Now().Add(-age)
		os.Chtimes(p, ts, ts)
		return p
	}
	a := mk("sessions/2026/11/a.jsonl", time.Hour)
	b := mk("sessions/b.jsonl", time.Hour)
	mk("sessions/old.jsonl", 48*time.Hour)
	mk("sessions/c.txt", time.Hour)
	mk("other/d.jsonl", time.Hour)
	e := mk("flat/e1.jsonl", time.Hour)
	since := time.Now().Add(-24 * time.Hour)

	got := Transcripts([]string{filepath.Join(root, "sessions", "**", "*.jsonl")}, since)
	slices.Sort(got)
	if want := []string{a, b}; !slices.Equal(got, want) {
		t.Errorf("** = %v\n기대 %v", got, want)
	}
	got = Transcripts([]string{filepath.Join(root, "sessions", "*.jsonl")}, since)
	if !slices.Equal(got, []string{b}) {
		t.Errorf("* 는 폴더를 넘지 않음: %v", got)
	}
	got = Transcripts([]string{filepath.Join(root, "flat", "e?.jsonl"), filepath.Join(root, "nothing", "**", "*.jsonl"), filepath.Join(root, "flat", "e1.jsonl")}, since)
	if !slices.Equal(got, []string{e}) {
		t.Errorf("? 와 없는 폴더, 중복: %v", got)
	}
	if got := Transcripts(nil, since); len(got) != 0 {
		t.Errorf("패턴이 없으면 없음: %v", got)
	}
	t.Setenv("HOME", root)
	got = Transcripts([]string{"~/flat/*.jsonl"}, since)
	if !slices.Equal(got, []string{e}) {
		t.Errorf("~ 는 홈으로: %v", got)
	}
}
