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

func queued(kind, prompt string, extra map[string]any) map[string]any {
	m := map[string]any{"type": "attachment", "attachment": map[string]any{
		"type": "queued_command", "prompt": prompt, "commandMode": "prompt", "origin": map[string]any{"kind": kind}}}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// 사용자의 말이 일반 메시지 말고 질문 도구의 답과 작업 중 메시지로 남는 경우다.
func TestUserTextsAskAndQueued(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeJSONL(t, p,
		askUse("ask1"),
		askResultLine("ask1",
			[]any{
				question("방향?", false, "가안 (추천)", "나안"),
				question("고른 라벨만?", false, "가안 (추천)", "나안"),
				question("여럿?", true, "하나, 둘", "셋", "넷"),
				question("배열?", true, "셋", "넷"),
				question("안 답함?", false, "셋"),
			},
			map[string]any{
				"방향?":     "직접 친 답, 쉼표 포함",
				"고른 라벨만?": "가안 (추천)",
				"여럿?":     "하나, 둘, 직접 친 덧붙임, 셋",
				"배열?":     []any{"넷", "배열 속 직접 친 글"},
			},
			map[string]any{"고른 라벨만?": map[string]any{"notes": "메모 글", "preview": "AI 미리보기"}, "여럿?": "모양이 다른 메모"},
		),
		// 질문 도구가 아닌 도구의 결과는 같은 모양이어도 읽지 않는다.
		msg("assistant", []any{map[string]any{"type": "tool_use", "id": "bash1", "name": "Bash", "input": map[string]any{}}}, nil),
		askResultLine("bash1", []any{question("가짜?", false, "셋")}, map[string]any{"가짜?": "AI 가 만든 출력"}, nil),
		// id 를 모르는 결과도 읽지 않는다.
		askResultLine("unknown", []any{question("모름?", false, "셋")}, map[string]any{"모름?": "모르는 id 의 답"}, nil),
		// 곁가지의 질문 도구 호출은 기억하지 않는다.
		msg("assistant", []any{map[string]any{"type": "tool_use", "id": "side1", "name": "AskUserQuestion"}}, map[string]any{"isSidechain": true}),
		askResultLine("side1", []any{question("곁?", false, "셋")}, map[string]any{"곁?": "곁가지 답"}, nil),
		queued("human", "작업 중 보낸 말", nil),
		queued("human", "<system-reminder>안내</system-reminder>", nil),
		queued("peer", "다른 세션의 말", nil),
		queued("coordinator", "조정자의 말", nil),
		queued("human", "곁가지 대기 말", map[string]any{"isSidechain": true}),
		map[string]any{"type": "attachment", "attachment": map[string]any{"type": "task_reminder", "prompt": "다른 첨부"}},
		map[string]any{"type": "queue-operation", "operation": "enqueue", "content": "대기열 사본"},
	)
	got, err := UserTexts(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"직접 친 답, 쉼표 포함", "메모 글", "직접 친 덧붙임", "배열 속 직접 친 글", "작업 중 보낸 말"}
	if !slices.Equal(got, want) {
		t.Errorf("사용자 글 = %q\n기대 %q", got, want)
	}
}

func TestTypedAnswer(t *testing.T) {
	labels := []string{"가", "가, 나", "다 (추천)"}
	for ans, want := range map[string]string{
		"가":             "",
		"가, 나":          "",
		"가, 나, 다 (추천)":  "",
		"다 (추천), 직접":    "직접",
		"직접, 가":         "직접",
		"직접 글, 그 뒤":     "직접 글, 그 뒤",
		"가나":            "가나",
		"":              "",
		"다 (추천), 가, 끝말": "끝말",
	} {
		if got := typedAnswer(ans, labels); got != want {
			t.Errorf("typedAnswer(%q) = %q, 기대 %q", ans, got, want)
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
