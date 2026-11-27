package mcpserver

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func strs(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}
func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

// tools 의 설명은 모델이 언제 부를지 알 수 있게 쓴다.
var tools = []tool{
	{"heka_find", "저장소·팀·개인의 규칙이나 용어를 모를 때, 답하거나 일하기 전에 찾는다. 낱말이든 하려는 일을 설명한 문장이든 된다. 항목마다 id, 주입문, 본문, 조건, 걸린 이유를 돌려주고 예산 때문에 빠진 항목도 알린다.",
		obj(map[string]any{
			"query":  str("찾을 낱말 또는 작업을 설명하는 문장"),
			"scopes": strs("범위. 없으면 서버의 작업 폴더에서 정한다"),
			"budget": map[string]any{"type": "integer", "description": "돌려줄 글의 토큰 상한. 빠진 항목이 있으면 늘려 다시 부른다"},
		}, "query")},
	{"heka_add", "사용자가 규칙·교정·용어·결정을 말했을 때 등록한다. source 는 사용자가 한 말이면 user, 읽은 문서에서 뽑았으면 doc, 추측이면 ai. user 면 quote 에 사용자의 말을 그대로 적는다(세션 기록과 대조해 확인한다). 비슷한 항목이 있어도 등록되며(같은 id 만 거절), 중복·모순 판정은 뒤에서 돌고 사람이 나중에 검토한다. 사용자에게 물을 필요는 없다.",
		obj(map[string]any{
			"id":   str("영문 소문자·숫자·하이픈으로 된 id"),
			"text": str("주입문. 한두 문장이고 이것만 읽어도 행동할 수 있어야 한다"),
			"must": map[string]any{"type": "boolean", "description": "반드시 지킬 규칙인가. true 면 when 에 조건이 있어야 한다"},
			"when": obj(map[string]any{"moment": strs("session_start|prompt|tool|subagent_start"), "action": strs("동작 이름(예: git.commit)"),
				"path": strs("경로 글롭"), "repo": strs("저장소 이름"), "agent": strs("에이전트 종류")}),
			"about":   strs("주제어(1개 이상)"),
			"scope":   strs("주입해도 되는 범위(1개 이상)"),
			"source":  str("user|doc|ai"),
			"quote":   str("source 가 user 일 때 근거가 되는 사용자의 말"),
			"expires": str("유효기한 YYYY-MM-DD"),
			"confirm": str("false|true|always"),
			"check": obj(map[string]any{"target": str("command|commit_message|pr_body|review_body|last_message"), "pattern": str("정규식"),
				"not_pattern": str("정규식"), "mode": str("block|log")}),
			"body": str("배경과 예시. 조회할 때만 읽힌다"),
		}, "id", "text", "about", "scope", "source")},
	{"heka_flag", "heka 항목이 틀렸거나 낡았거나 쓸모없다는 것을 알았을 때 표시를 남긴다. 항목 파일은 고치지 않고 기록만 하며, 사람이 점검할 때 본다.",
		obj(map[string]any{"id": str("항목 id"), "reason": str("무엇이 틀렸는지 또는 왜 낡았는지")}, "id", "reason")},
}
