# heka

heka는 AI 에이전트가 일하는 각 순간에, 그 순간에 필요한 컨텍스트만 골라 행동 전에 전달하고, 그 전달이 맞았는지 기록으로 확인하는 개인용 컨텍스트 주입기입니다. Go로 만듭니다.

## 현재 상태

설계와 첫 구현이 함께 있습니다. 훅, 조회·등록, MCP 서버, 결과물 검사, 그림자 방식, 재현기(`heka replay`)가 구현돼 있습니다. 동작하는 명령은 다음과 같습니다.

- `heka index [--check]`: 항목 파일과 설정을 읽어 검증하고 색인(SQLite)을 만듭니다.
- `heka select`: 순간 하나에 넣을 항목을 골라 JSON 으로 보여 줍니다.
- `heka resolve`: 도구 호출 하나를 동작 이름으로 바꿔 보여 줍니다.
- `heka find`: 항목을 찾습니다(낱말 나열도, 작업을 설명한 문장도 됩니다).
- `heka add`: 항목을 등록합니다. 비슷한 항목이 있어도 바로 등록하고(같은 id 만 거절), 후보가 있으면 뒤에서 모델이 판정합니다.
- `heka judge-add <id>`: 등록한 항목 하나를 비슷한 후보와 견주어 판정합니다. 등록이 떨어진 프로세스로 띄우며 보통 사람이 부르지 않습니다.
- `heka review`: 판정이 올린 검토 목록을 보고 결정하며, `--json`·`apply`·`--notify` 가 있습니다(아래 "검토: heka review").
- `heka why`, `heka list`, `heka stats`, `heka retire`: 원장과 항목을 들여다보고 항목을 은퇴시킵니다(아래 "원장과 항목 보기").
- `heka judge-stats`: 원장에 남은 판정 시간을 모아 분포와 상한별로 잘릴 판정 수를 보입니다.
- `heka gui`: 원장과 항목을 보는 읽기 전용 화면을 로컬 서버로 엽니다(아래 "화면: heka gui").
- `heka replay`: 기록해 둔 순간들을 선별기에 다시 넣어 전달·정밀도·침묵을 잽니다(아래 "재현: heka replay").
- `heka mcp`: 조회·등록·표시를 위한 MCP 서버입니다(표준 입출력).
- `heka hook`: Claude Code 훅 연결부입니다. 표준 입력의 훅 JSON 을 처리해 표준 출력에 JSON 을 냅니다. `--print-settings` 는 설정 파일의 `hooks` 에 넣을 JSON 조각을 출력하며(`--shadow` 를 더하면 그림자 방식용), 설정 파일을 직접 고치지는 않습니다.
- `heka version`

설정 예시는 `config.example.yaml` 입니다. `judge.pick` 은 모델이 항목 목록에서 후보를 고르는 갈래를 기록 전용으로 켭니다(`mode: record`, 기본은 꺼짐). 고른 항목을 한 건씩 판정한 결과가 원장 줄의 `pick` 에 남고 `heka why` 가 "모델 후보" 로 보입니다. 무엇을 넣을지는 바꾸지 않으며, 프롬프트마다 대상 항목 전체를 모델에 읽히므로 그림자 방식에서 쓰는 것을 전제로 합니다. 데이터 폴더는 `$HEKA_HOME` 이고, 없으면 `$HOME/.heka` 입니다(잠정).

## Claude Code 훅

`heka hook` 은 `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `SubagentStart`, `Stop` 을 처리합니다. 작업 위치(`cwd`)는 설정의 `locations` 로 범위와 저장소 이름에 맞추고, 넣을 항목은 `additionalContext` 로 냅니다. 같은 세션에서 같은 항목은 한 번만 넣고(압축·`clear` 뒤에는 다시), 서브에이전트는 `agent_id` 별로 따로 셉니다. 위임 도구(`Agent`·`Task`)의 프롬프트는 세션 상태에 적어 두었다가 `SubagentStart` 에서 그 글로 항목을 고릅니다.

- 프롬프트가 시킨 동작("푸시해 줘")은 그 턴 동안의 허용으로 적힙니다. "이번 세션 동안" 같은 표현이 있으면 세션 동안 남고, "푸시하지 마" 는 허용을 거둡니다. 도구가 만든 프롬프트(`tool_prompt_prefixes` 로 시작하는 글)는 항목도 허용도 건드리지 않습니다.
- `confirm: true` 항목은 그 동작(또는 상위 동작)에 허용이 없으면 `PreToolUse` 에서 확인을 요구하고(`permissionDecision: "ask"`), `always` 는 허용이 있어도 요구합니다.
- 오류, 입력 해석 실패, 시간 초과(프롬프트·`SubagentStart` 25초, 나머지 5초. 그림자 방식은 60초와 10초)에서는 아무것도 출력하지 않고 종료 코드 0 으로 끝나며, 오류는 `$HEKA_HOME/ledger/errors.log` 에 한 줄 남습니다. 판단은 `$HEKA_HOME/ledger/YYYY-MM.jsonl` 에 한 줄씩 남습니다. 세션 상태는 `$HEKA_HOME/sessions/<session_id>.json` 이며, 등록할 때 인용을 대조하려고 훅 입력의 `transcript_path`(세션 기록 파일의 경로)만 거기에 남습니다. 프롬프트 글은 따로 복사해 두지 않습니다.
- 토큰 상한은 기본이 없습니다(설정의 `budgets` 에 순간별로 적으면 적용). 넣은 글의 토큰 어림은 원장 줄의 `tokens` 에 남으므로 "너무 많은지"는 그것으로 봅니다. 참고 항목은 한 번에 최대 3건입니다.

훅 등록은 직접 합니다. `heka hook --print-settings` 가 낸 JSON 을 Claude Code 설정 파일의 `hooks` 에 넣으세요.

## 조회 전용 항목: push

머리말에 `push: false` 를 쓰면 `heka find` 와 MCP 조회로만 찾고 heka 가 스스로 넣지 않는 항목이 됩니다. 세션 시작, 프롬프트, 도구 호출, 서브에이전트 시작 어디에서도 후보가 아니고, 원장에 탈락으로도 남지 않습니다. `push` 가 없으면 true 입니다. `must: true`, `when`, `confirm: true|always`, `check` 와는 함께 쓸 수 없고(검증 오류), 목록은 `heka list --find-only` 로 봅니다. 색인 꼴이 바뀌었으므로 옛 색인 파일은 다음 실행에서 자동으로 다시 만들어집니다.

```yaml
---
id: old-note-0001
text: 지어낸 옛 노트의 요약 한 줄.
about: [옛 노트]
scope: personal
source: doc
push: false
---
```

## 결과물 검사

항목에 `check` 를 붙이면 모델이 낸 결과물을 정규식과 견줍니다. 예: 커밋 메시지의 첫 줄을 50자 이내로 제한하는 항목입니다.

```yaml
---
id: commit-title-short
text: 커밋 메시지 첫 줄은 50자 이내로 쓴다.
must: true
when: {action: git.commit}
about: [커밋 메시지]
scope: [repo/sample-app]
source: user
quote: 지어낸 말
check:
  target: commit_message     # command · commit_message · pr_body · last_message · question
  pattern: "^.{1,50}(\n|$)"  # 맞아야 하는 형식. not_pattern 은 있으면 안 되는 표현
  mode: block                # block 은 막고, log 는 기록만 한다
  evidence: "(?i)--help"     # 선택. 이번 턴에 앞서 한 도구 호출이 이 식에 맞으면 통과시킨다
---
```

- 정규식은 Go 표준 문법(`regexp`)입니다. `pattern` 은 맞아야, `not_pattern` 은 맞지 않아야 통과합니다.
- `command`, `commit_message`, `pr_body` 는 도구 호출 직전(`PreToolUse`)에 Bash 명령에서 뽑아 견줍니다. 검사할 항목은 그 호출의 동작과 `when` 이 맞고 범위·자격을 통과한 것이며, 이미 넣은 항목이어도 매번 검사합니다. 커밋 메시지는 `git commit` 의 `-m`(여럿이면 빈 줄로 이음), `-F -` 와 heredoc, `--amend -m` 에서, PR 본문은 `gh pr create|edit` 의 `--body`·`-b`, `--body-file -` 와 heredoc, `gh api … -f body=…` 에서 뽑습니다.
  - 파일로 넘긴 글(`git commit -F|--file <경로>`, `gh pr create|edit --body-file|-F <경로>`, `gh api … -F body=@<경로>`)도 풉니다. 경로 단어의 `$이름`·`${이름}` 은 같은 명령 안에서 앞서 나온 `이름=값` 대입(값이 글자 그대로일 때만. 환경 변수는 보지 않습니다)으로 풀고, `~/` 는 홈으로, 상대 경로는 훅 입력의 `cwd`(앞에 `cd <경로>` 가 있으면 그 디렉터리, `git -C <경로>` 가 붙은 commit 은 그 경로)를 기준으로 합니다. 같은 명령이 `cat > 경로 <<'EOF' … EOF` 로 그 파일을 쓰면 그 heredoc 본문이 값이고, 그렇지 않으면 파일을 읽습니다(보통 파일, 256KiB 이하, UTF-8 만. 읽기만 하고 어떤 명령도 실행하지 않습니다). 다른 구간이 그 경로나 변수를 앞서 언급하면(다른 명령의 인자, 리다이렉션 대상 등) 실제로 보낼 내용을 알 수 없으므로 뽑지 않습니다.
  - 글을 뽑지 못하면 그 검사는 하지 않고(원장에 `skip` 과 이유) 통과시킵니다.
- `mode: block` 이고 걸리면 `permissionDecision: "deny"` 로 막고, 사유에 그 항목의 글과 걸린 검사의 대상을 적어 모델이 고쳐서 다시 시도하게 합니다. 여러 항목이 걸리면 사유를 줄바꿈으로 이어 한 번에 냅니다. 같은 호출에 넣을 다른 항목은 `additionalContext` 로 함께 갑니다. 사람에게 묻지는 않습니다. `mode: log` 는 막지 않고 원장에만 남깁니다.
- `last_message` 는 턴이 끝날 때(`Stop`) 입력의 `last_assistant_message` 를 견줍니다. 이 검사의 항목은 범위·자격을 통과해야 하고, `when` 은 `repo`·`agent` 만 봅니다. 걸리면 `{"decision": "block", "reason": …}` 로 모델이 한 번 더 돌게 합니다. 세션당 3회까지만이고, 같은 항목이 연속으로 걸리면 두 번까지만 막으며, 그 뒤에는 원장에만 남깁니다.
- `question` 은 `AskUserQuestion` 호출 직전에 질문 글(`questions[].question` 과 `options[].label`·`options[].description` 을 줄바꿈으로 이은 글)을 견줍니다. 이 호출은 동작 이름을 내지 않으므로 `last_message` 처럼 범위·자격을 통과한 항목을 `when` 의 `repo`·`agent` 만 보고 고릅니다. `mode: block` 이면 호출을 막고 사유를 돌려줍니다.
- `evidence`(선택, Go 정규식)는 검사가 어긋났을 때 "이번 턴에 앞서 이런 도구 호출을 했으면 통과"로 바꾸는 조건이며, 모든 대상에 `pattern`·`not_pattern` 과 함께 쓸 수 있습니다. 예를 들어 `last_message` 에 `not_pattern: "(할|쓸) 수 없|지원하지 않"` 과 `evidence: "(?i)(--help|^WebFetch:|^WebSearch:)"` 를 두면 "할 수 없다"는 단정은 같은 턴에 `--help` 나 웹 조회를 한 뒤에만 통과합니다.
  - 증거는 훅이 모든 도구 호출 직전(`PreToolUse`)에 세션 상태에 쌓는 "턴의 도구 호출 기록"입니다. 한 줄이 `<도구 이름>: <요약>` 이고 Bash 는 명령, WebFetch 는 URL, WebSearch 는 검색어, Agent 는 `<subagent_type> <프롬프트 앞 200자>`, Skill 은 스킬 이름, Read 는 파일 경로이며 MCP 도구와 그 밖의 도구는 이름만 씁니다. 한 줄은 1000자, 한 턴은 64줄까지(넘으면 오래된 줄부터 버림)이고, 새 사용자 턴이 시작되면 비웁니다(도구가 만든 프롬프트는 같은 턴). 메인과 서브에이전트는 따로 쌓습니다.
  - 그 호출 자신의 검사를 먼저 하고 나서 쌓으므로 호출이 자기 자신의 증거가 되지 않으며, 막힌 호출은 쌓지 않습니다. 증거로 통과하면 원장 검사 기록의 `note` 에 맞은 기록의 앞 120자가 `증거: …` 로 남습니다.
  - 턴 기록을 읽지 못하면(`session_id` 없음, 세션 상태 파일이 없거나 깨짐) 통과시키고 `skip`(`턴 기록을 읽지 못함`)을 남깁니다.
- 식이 틀렸거나 글을 뽑다가 오류가 나면 통과시킵니다. 검사 때문에 막는 것은 위의 두 출력뿐이고, 훅이 실패해도 작업을 막지 않는다는 원칙은 그대로입니다.
- 원장 줄의 `checks` 에 항목 id, 대상, 결과(`pass`·`fail`·`skip`), 막았는지가 남습니다. `skip` 에는 이유 `note`(`대상 없음`, `변수를 풀지 못함`, `경로를 풀지 못함`, `같은 명령에서 파일을 고침`, `파일을 읽지 못함`, `식을 쓸 수 없음`, `턴 기록을 읽지 못함` 중 하나)가 붙고, 파일의 경로나 내용은 남기지 않습니다. `heka why` 는 이 이유를 `skip(이유)` 로 보입니다. `heka stats` 는 "검사에 걸린 횟수"를, `heka list --violated N` 은 검사에 걸린 횟수와 `flag` 횟수의 합이 N 이상인 항목을 보입니다.

## 그림자 방식

다른 도구와 같은 세션에서 나란히 돌려 판정만 견주기 위한 방식입니다. 환경 변수 `HEKA_SHADOW=1` 이나 설정 `claude_code.shadow: true` 로 켭니다. 그림자 방식의 훅은 평소와 똑같이 고르고 기록하지만(선별, 세션 상태 갱신, 검사 판정, 원장) 표준 출력에는 아무것도 쓰지 않고 종료 코드는 0 입니다. 즉 주입도, 확인 요구도, 검사로 막기도 없습니다. 원장 줄에는 `shadow: true` 가 붙고 `heka why` 는 `(그림자)` 를 붙입니다. 훅을 백그라운드로 돌리는 것이 전제라 처리 시간 상한이 넉넉합니다(프롬프트 60초).

```sh
heka hook --print-settings --shadow   # 명령이 `HEKA_SHADOW=1 heka hook` 이고 각 훅에 "async": true 가 붙은 설정 조각
```

그림자 방식의 `checks` 에서 `blocked` 는 실제로 막았다는 뜻이 아니라 막았을 판정이라는 뜻이고, 턴 종료 검사의 횟수 한도도 평소처럼 셉니다.

## 조회: heka find

```sh
heka find [--scope S]... [--budget N] [--limit K] [--json] <질의…>
```

모델 판정을 부르지 않고, 동작 조건이 붙은 항목도 대상에 넣습니다. 후보는 주제어(`about`)가 질의에 든 항목과 낱말 점수 상위 K건(기본 5, 점수 > 0)이고, 범위·자격(기한, 미확인 ai 등)은 프롬프트 선별과 같은 규칙으로 거릅니다. 순서는 주제어로 걸린 것이 먼저, 그다음 낱말 점수 내림차순, 같으면 id 오름차순입니다. 범위는 `--scope` 로 주고, 없으면 현재 작업 폴더를 설정의 `locations` 로 찾아 정합니다(`global_scopes` 는 늘 포함).

토큰 상한은 `--budget`, 없으면 설정의 `budgets.find`(기본 1500)입니다. 순서대로 넣다가 안 들어가는 항목은 본문을 빼고 주입문만으로 다시 시도하고(`body_omitted: true`), 그래도 안 들어가면 빠진 목록에 넣습니다. 조회는 원장에 한 줄(`event: find`) 남습니다.

`--json` 의 출력 모양입니다. 훅이나 MCP 를 쓰지 않는 도구는 이것을 부르면 됩니다.

```json
{
  "items": [
    {
      "id": "release-check",
      "text": "배포 점검표는 릴리스 전에 확인한다.",
      "body": "점검표는 팀 위키에 있다.",
      "must": false,
      "when": {"action": ["git.push"]},
      "why": "주제어 \"릴리스 점검\" 가 맞음, 낱말 점수 1.00",
      "score": 1.0,
      "body_omitted": true
    }
  ],
  "omitted": 1,
  "omitted_ids": ["other-rule"]
}
```

조회 전용 항목(아래 `push: false`)이면 글 출력의 메타에 `push=false` 가, JSON 에는 `"push": false` 가 붙습니다(`push` 가 true 인 항목에는 이 필드가 없습니다).

`when` 은 항목의 조건(`moment`, `action`, `path`, `repo`, `agent` 중 있는 것)이고, `body_omitted` 는 예산 때문에 본문을 뺀 항목에만 있습니다. `score` 는 낱말 점수(0~1)입니다.

## 등록: heka add

```sh
heka add --id release-check --text '배포 점검표는 릴리스 전에 확인한다.' \
  --about '릴리스 점검' --scope personal --source doc --body '점검표는 팀 위키에 있다.'
heka add --file <항목 파일> [--checked]
```

플래그는 `--id --text --must --moment --action --path --repo --agent --about --scope --source --quote --expires --confirm --body` 이고 목록 필드는 여러 번 쓸 수 있습니다. `--checked` 는 사람이 직접 확인했다는 뜻으로 오늘 날짜를 `checked` 에 넣습니다(MCP 로는 받지 않습니다). `--file` 로 읽은 파일의 `checked` 는 무시합니다.

- 검증은 `heka index --check` 와 같은 규칙입니다. error 가 있으면 등록하지 않고 문제 목록을 냅니다. `items/<id>.md` 가 이미 있으면 등록하지 않고 "같은 id 의 항목 <id> 가 이미 있음. 파일을 직접 고치거나 다른 id 를 쓰세요" 를 냅니다.
- `source: user` 이고 `quote` 가 있으면, 최근 24시간 안에 고쳐진 세션 상태 파일에 적힌 세션 기록 파일을 읽어 **사용자가 친 글**과 공백을 정규화해 대조합니다. 사용자가 친 글에는 일반 메시지 말고도 `AskUserQuestion` 에 직접 친 답과 메모, 작업 도중 보낸 메시지(`queued_command` 첨부 가운데 `origin.kind` 가 `human` 이고 `commandMode` 가 `prompt` 인 것)가 들어갑니다. AI 가 `answers`·`annotations` 를 미리 채워 부른 `AskUserQuestion` 의 결과와, 질문을 거절하며 친 글은 읽지 않습니다. AI 의 글, 고른 선택지의 라벨(AI 가 쓴 글), `AskUserQuestion` 이 아닌 도구의 결과(`tool_result`), 다른 세션·에이전트가 보낸 메시지, 도구가 만든 프롬프트(`tool_prompt_prefixes`), `origin.kind` 가 `human` 이 아니거나 `promptSource` 가 `typed`·`queued`·`sdk` 가 아닌 user 줄(에이전트 결과 알림, 제안된 문장을 수락한 것 등), `<local-command`·`<system-reminder>`·`<task-notification` 로 시작하는 글, `<bash-stdout`·`<bash-stderr`·`<artifact-content-authored-by-others` 가 나온 자리부터 끝, 슬래시 명령 줄에서 `<command-args>` 밖의 글, 사용자가 붙여넣은 글 블록(`<pasted_content …>`, 블록 앞뒤의 글은 이어 붙이지 않고 따로 대조합니다), 대화 압축 요약(`isCompactSummary`), `isMeta`·`isSidechain` 줄은 인용으로 인정하지 않습니다. 다만 `AskUserQuestion` 의 답을 훅이나 SDK 의 권한 콜백이 대신 채웠는지는 기록으로 가릴 수 없어, 그런 답도 사용자가 친 글로 봅니다. 기록 파일 읽기는 `internal/claudecode` 가 맡고(Claude Code 형식), 설정의 `claude_code.transcripts` 에 glob 목록을 주면 같은 형식의 다른 기록(최근 24시간 안에 고쳐진 것)도 봅니다. 기록 파일이 없거나 못 읽으면 그 파일은 건너뜁니다. 확인되지 않으면 `quote` 를 비우고 본문 끝에 `확인되지 않은 인용: "..."` 을 적어 등록하며, 사람이 확인(`checked`)하기 전에는 주입되지 않습니다.
- 그 밖에는 언제나 `items/<id>.md` 로 쓰고 색인을 다시 만듭니다. 비슷한 항목이 있어도 막지 않고, 대기함은 없습니다.
- 비슷한 후보는 (a) 범위와 `when.action` 이 함께 겹침, (b) 주제어가 같음, (c) 새 항목의 `text` 를 질의로 한 낱말 점수가 0.5 이상(잠정) 중 하나라도 맞는 기존 항목입니다. 범위·동작이 겹친 것을 먼저, 그다음 낱말 점수 내림차순, 같으면 id 순으로 최대 `register_judge.candidates`(기본 5)건입니다. 후보가 있으면 응답에 "비슷한 후보 N건은 뒤에서 판정합니다. 사용자에게 물을 필요는 없습니다." 와 후보 목록이 붙고, `heka judge-add <id>` 를 떨어진 프로세스(새 세션, 표준 입출력은 `/dev/null`)로 띄우며 기다리지 않습니다. 띄우기에 실패해도 등록은 성공이고 `errors.log` 에만 남습니다.
- 파일은 임시 파일에 쓴 뒤 이름을 바꿉니다. `$HEKA_HOME` 에 `.git` 이 있으면 쓴 뒤 `git add`·`git commit`(`add: <id>`)을 시도하고, 실패해도 등록은 성공입니다. 커밋에는 그 호출이 다룬 항목 파일(`items/<id>.md`)과 `review/` 에서 바뀐 파일만 담습니다. 손으로 고쳐 두고 커밋하지 않은 다른 항목은 섞이지 않습니다(판정은 `review/` 파일만, 합치기는 대상 항목과 `review/`).

## 등록 판정: heka judge-add

설정의 `register_judge.command` 에 새 항목과 후보를 표준 입력으로 넘기고 후보마다 `separate|merge|conflict|stale` 답을 받습니다. 입력은 `register_judge.instruction`(비면 코드 안의 기본 지시문), `[새 항목]`, `[후보 1]` … 블록을 빈 줄 하나로 이은 글이고, 블록은 `id`, 출처, 범위, 필수, 조건, 주제어, 주입문, 본문(1500자까지, 넘으면 `…(생략)`)입니다. 답은 `{"verdicts":[{"id","verdict","reason","merged":{"text","body","about":[]}}]}` 이고, 코드 울타리나 앞뒤 글이 섞여도 첫 `{` 부터 짝이 맞는 `}` 까지를 읽습니다.

- separate 는 원장에만 남깁니다. merge 는 검토 항목(합친 초안 포함), conflict 는 검토 항목을 만들고 곧바로 알림 명령을 부릅니다(한 번의 판정에서 모순이 여러 건이어도 알림은 한 번이고 건수를 적습니다).
- stale(낡음: 새 항목이 무엇을 없애거나 바꿨다는데 후보가 그 옛 상태를 지금도 유효한 것처럼 지시함)은 검토 항목(kind stale)을 만들고 옛 항목(후보)에 `heka_flag` 와 같은 표시(사유 `등록 판정: <새 항목> 기준으로 낡음 — <이유>`)를 원장에 남깁니다. 알리지 않습니다(2026-11-25 사용자 결정).
- merge 의 합친 초안은 기계로 검사합니다. 두 원본 본문의 `## ` 머리 줄이 초안 본문에 모두 있어야 하고, 초안 본문 길이(글자)가 두 원본 중 긴 쪽의 70% 이상이어야 합니다(두 본문이 다 비면 검사하지 않음). 어기면 검토 항목은 merge 로 만들되 초안을 비우고 `draft_problem`(원장의 판정 줄에도 남음)에 까닭을 적으며, 터미널의 `m` 은 보이지 않고 세션 스킬이 초안을 새로 씁니다.
- 명령이 없거나, 오류·시간 초과(`register_judge.timeout`, 기본 120초)·읽지 못한 답이면 검토 항목 하나(미판정, 후보 id 목록 포함)를 올립니다. 후보에 없는 id 와 모르는 verdict 는 버리고, 답이 없는 후보는 미판정입니다.
- 항목 파일은 고치거나 지우지 않습니다. 쓰는 것은 `review/` 와 원장(`event: register_judge`: 새 항목 id, 후보별 답과 이유, 걸린 시간, 오류·시간 초과)뿐이라, 검토 전에는 합치기·모순으로 판정된 두 항목이 함께 주입됩니다.

## 검토: heka review

```sh
heka review --json [--all]       # 검토 목록(한 번에 review.batch 건, --all 이면 전부). 양쪽 항목의 필드를 함께 냅니다
heka review                      # 터미널에서 한 건씩 보고 키로 결정합니다(터미널이 아니면 --json 을 안내하고 끝)
heka review apply <entry-id> <merge|keep|drop-new|drop-old> [--reason 글] [--text 글] [--body-file 파일] [--about a,b]
heka review --notify             # 주 1회 표본을 올리고, 기준을 넘으면 알림 명령을 부릅니다
```

`--json` 의 각 건에는 두 항목에서 값이 다른 머리말 필드와 양쪽 값(`header_diff`)이 함께 나오고 터미널 화면에도 한 줄로 보입니다(합치면 새 항목 쪽 값이 사라지므로).

검토 항목은 `$HEKA_HOME/review/<entry-id>.json` 입니다(`<새>--<대상>` 또는 `<새>--unjudged`). 순서는 모순, 낡음, 합치기, 미판정, 표본이고 같으면 오래된 것이 먼저입니다. 새 항목이나 대상 항목 파일이 이미 없는 항목은 목록에서 빼고 지우며 원장에 남깁니다.

- `merge`: 대상 항목(id 유지)을 다시 씁니다. 초안의 본문이 비고 `--body-file` 도 없으면 대상 본문과 새 항목 본문을 이어 쓰고, 초안 주제어가 비면 두 항목 주제어의 합집합을 씁니다. 검사(`check`)는 대상 것을 쓰되 대상에 없고 새 항목에 있으면 새 항목 것을 쓰며, 둘 다 있고 다르면 합치기를 거절합니다. 주입문·본문·주제어는 초안(플래그가 있으면 플래그 값), 조건·필수·범위·확인·검사·기한·`checked` 는 대상 항목 것입니다. 출처와 인용은 두 항목 중 더 믿을 만한 쪽(`user` > `doc` > `ai`, 같으면 대상 쪽)을 쓰고, 다른 쪽의 출처와 인용은 본문 끝에 `합친 항목 <id>: 출처 <s>, 인용 "<q>"` 로 남깁니다. 합친 결과가 검증을 통과하지 못하면 아무것도 바꾸지 않고 오류이며, 통과하면 새 항목은 은퇴합니다(사유 `합침: <대상> 에 합침`).
- `keep` 은 검토 항목만 지우고 원장에 사유(기본 "따로 두기")를 남깁니다. `drop-new`·`drop-old` 는 새 항목·대상 항목을 `heka retire` 와 같은 처리로 은퇴시킵니다. 은퇴한 항목을 가리키는 다른 검토 항목도 지웁니다. 결정마다 원장에 `event: review` 한 줄이 남습니다.
- 터미널 키: 낡음은 `o` 옛 것 버리기, `k`, `n`, `s`, `q`(합치기는 거절합니다). 모순·합치기·표본은 `m` 합치기(초안이 있을 때만), `k` 따로 두기, `n` 새 것 버리기, `o` 옛 것 버리기, `s` 건너뛰기, `q` 끝. 미판정은 `j` 다시 판정(그 자리에서 `judge-add` 를 동기로 실행), `k`, `n`, `s`, `q`.
- `--notify`: 마지막 표본이 7일을 넘었거나 없으면, 지난 7일 원장에서 `separate` 로 판정된 쌍 중 두 항목이 다 있고 아직 검토 항목이 아닌 것을 `review.sample`(기본 3)건 무작위로 올립니다. 검토 대기가 `review.notify_min`(기본 5)건 이상이거나 가장 오래된 것이 `review.notify_days`(기본 7)일 이상이면 `review.notify_command` 에 알림 글을 마지막 인자로 덧붙여 부르고, 명령이 비어 있으면 표준 출력에만 씁니다.

## 원장과 항목 보기: heka why, list, stats, retire

원장(`$HEKA_HOME/ledger/YYYY-MM.jsonl`)은 여러 달 파일을 모두 읽고, 깨진 줄은 건너뜁니다.

```sh
heka why [--session <id>] [--last N] [--id <항목 id>] [--json]
heka judge-stats [--json] [--since YYYY-MM-DD]
heka list [--json] [--agent <종류>] [--must] [--flagged] [--find-only] [--violated N]
heka stats <id> [--json]
heka retire <id> --reason <글>
```

- `why`: 최근 줄(기본 20)을 한 줄씩 보입니다. 시각(UTC), 이벤트, 동작, 넣은 항목과 이유, 빠진 항목(`id[단계: 사유]`), 판정(`판정: <id> 넣음 1234ms`. 안 넣음·오류·시간 초과도 같은 꼴), 모델 후보(`모델 후보(14건 중, 2850ms): a 바로·판정 넣음, b 후보·판정 넣지 않음`. 오류면 `모델 후보: 오류`), 토큰. `--id` 를 주면 그 항목이 넣어졌거나 빠진 줄만 봅니다. `--json` 은 원장 줄을 JSON 배열로 냅니다.
- `judge-stats`: 프롬프트·서브에이전트 시작의 주제 갈래가 판정 명령을 부를 때마다 원장 줄의 `judges` 에 항목 id, 답(`inject`), 걸린 시간(`ms`), 오류(`error`)와 시간 초과(`timeout`)가 남습니다(`votes: 2` 면 호출마다 한 건). 이 명령은 그 기록을 모아 판정 수, 걸린 시간의 중앙값·95%·최댓값, 그리고 판정 시간 상한을 3·4·5·6·8·10·15초로 줄였다면 잘렸을 판정(걸린 시간이 상한을 넘은 것)의 수와 그중 넣으라는 답의 수를 표로 보입니다. 시간 초과·오류로 끝난 호출은 답이 없어 넣으라는 답에 세지 않습니다. `--since` 는 그 날짜(UTC) 이후의 줄만 셉니다.
- `list`: 항목의 id, 필수 여부, 조건(`when`), 범위, 확인 날짜, 기한. `--agent` 는 `when.agent` 에 그 종류가 걸린 항목, `--flagged` 는 원장에 `flag` 가 남은 항목만, `--violated N` 은 검사에 걸린 횟수와 `flag` 횟수의 합이 N 이상인 항목만 보입니다. 검증에 실패한 항목은 빼고 알립니다.
- `stats`: 주입 횟수, 마지막 주입 시각, 예산·중복·판정으로 빠진 횟수, 결과물 검사에 걸린 횟수, `flag` 횟수와 마지막 사유. 훅의 줄만 셉니다(조회·등록 줄은 세지 않습니다).
- `retire`: 항목 파일을 지우고 색인을 다시 만들고 원장에 `retire` 한 줄(사유 포함)을 남깁니다. `$HEKA_HOME` 에 `.git` 이 있으면 사유를 메시지로 커밋을 시도하며, 실패해도 성공입니다.

## 화면: heka gui

```sh
heka gui [--port N] [--open]
```

원장과 항목을 브라우저에서 보는 읽기 전용 화면입니다. 시작하면 표준 출력에 `http://127.0.0.1:<포트>/` 한 줄을 내고, `Ctrl-C`(SIGINT)나 SIGTERM 에는 정상 종료합니다. 포트를 잡지 못하면 사유를 표준 오류에 내고 종료 코드 1, 잘못된 플래그는 2 입니다.

- `--port`: 기본 7788 입니다. 0 이면 빈 포트를 고릅니다. 주소는 늘 `127.0.0.1` 이고, 다른 주소에 묶는 옵션은 없습니다. 인증이 없는 화면이라서입니다.
- `--open`: 브라우저로 엽니다(macOS 는 `open`, 그 밖은 `xdg-open`). 열지 못해도 서버는 계속 돕니다.

읽기 전용입니다. GET·HEAD 만 받고 그 밖의 메서드는 405 이며, 원장·항목·설정 어디에도 쓰지 않습니다. `Host` 머리말이 `127.0.0.1`, `localhost`, `[::1]` 가 아니면 403 으로 막고(DNS 리바인딩 방지), CORS 머리말은 내지 않습니다. 요청마다 원장과 항목을 새로 읽으며(캐시 없음), 파일이 없거나 깨져 있으면 빈 값으로 답합니다. 화면 자산은 실행 파일에 들어 있습니다.

보여 주는 것은 JSON API 로 가져옵니다(전부 `application/json; charset=utf-8`). `since`·`until` 은 UTC 날짜 `YYYY-MM-DD` 이며 양끝을 포함하고, 틀리면 400 입니다.

- `GET /api/meta`: 버전, 데이터 폴더, 항목 수(전체·넣는 것·조회 전용·필수)와 검토 대기 건수(`items.review`, 읽기만 하며 지워진 항목을 가리키는 것은 세지 않음), 원장 줄 수와 처음·마지막 시각, `errors.log` 줄 수, 그림자 방식 여부.
- `GET /api/overview?since=&until=`: 훅의 줄을 순간(세션 시작·프롬프트·도구 호출·서브에이전트 시작·턴 종료)별로 모은 것. 줄 수, 넣은 항목 수와 토큰, 단계별 탈락 수, 시킨·하지 말라 한 동작, 확인 요구, 검사 결과, 판정과 모델 후보 요약, 걸린 시간 분포, 많이 넣은 항목 5건. 훅이 아닌 줄(조회·등록·표시 등)은 이벤트별로 셉니다.
- `GET /api/sessions?since=&until=&limit=`: 세션 목록(마지막 시각 내림차순, `limit` 기본 100·최대 1000). `GET /api/sessions/<id>`: 그 세션의 원장 줄(시각 오름차순)과 줄에 나온 항목의 지금 글.
- `GET /api/items`: 항목 목록과 원장에서 센 횟수(넣음·탈락·검사에 걸림·표시). `GET /api/items/<id>`: 본문, 단계별 탈락 수, 이 항목이 나온 최근 줄 30건. 횟수를 세는 규칙은 `heka stats` 와 같습니다(훅의 줄만, 탈락은 예산·중복·판정만).

## 재현: heka replay

```sh
heka replay <사례.jsonl> [--runs N] [--parallel N] [--no-judge] [--json] [--out <파일>] \
  [--min-delivery X] [--min-precision X] [--min-silence X]
```

기록해 둔 순간들을 선별기에 다시 넣어 성공 조건을 재는 명령입니다. 실사용 경로(훅)와 무관하고 세션 상태와 원장을 쓰지 않습니다(색인이 낡았으면 다시 만들 뿐입니다). 사례마다 세션은 새로 시작한 것으로 봅니다(이미 넣은 항목 없음).

입력은 사례가 한 줄에 하나씩 든 JSONL 입니다. 깨진 줄, `id` 가 없거나 겹치는 줄, 모르는 `moment.kind` 의 줄은 건너뛰고 그 수를 알립니다. 모르는 키는 무시합니다.

```json
{"id":"c1","moment":{"kind":"prompt","text":"푸시해 줘","scope":["repo/sample-app"],"repo":"sample-app"},"expect":{"inject":["no-force-push"],"not_inject":[],"silent":false}}
{"id":"c2","moment":{"kind":"tool","tool":{"name":"Bash","input":{"command":"git push --force"}},"scope":["repo/sample-app"],"repo":"sample-app"},"expect":{"inject":["no-force-push"]}}
{"id":"c3","moment":{"kind":"prompt","text":"오늘 날씨 어때"},"expect":{"silent":true}}
```

- `moment.kind`: `prompt`, `tool`, `session_start`, `subagent_start`. 범위는 `scope` 와 `scopes` 둘 다 받고(문자열 하나도 됩니다), 설정의 `claude_code.scopes` 가 더해집니다. `tool` 은 `tool`(이름과 입력)을 주면 `internal/action` 으로 동작을 해석하고, `action` 을 직접 줘도 됩니다. `agent`, `path`, `repo`, `cwd` 도 받습니다. 프롬프트가 `claude_code.tool_prompt_prefixes` 로 시작하면 도구가 만든 것으로 보고 건너뜁니다.
- `expect`: `inject`(넣어야 하는 항목), `not_inject`(넣으면 안 되는 항목), `silent`(아무것도 넣으면 안 됨), 덧붙인 정답 `also_inject`·`also_not_inject`, 모르는 것으로 세는 `unsure`.
- 사례에 `confirmed`(bool)와 `source` 가 있으면 그 값별로 묶음을 나눠 셉니다.

재는 것입니다.

- 전달: `inject` 가 있는 사례 가운데 그 항목이 모두 들어간 비율. 필수(`must`) 항목이 대상인 사례와 전체를 나눠서 냅니다.
- 정밀도: 넣은 (사례, 항목) 쌍 가운데 맞은 비율. 맞음은 `inject`·`also_inject` 에 있는 것, 틀림은 `not_inject`·`also_not_inject` 에 있거나 `silent` 사례에서 넣은 것입니다. 정답이 없는 쌍과 `unsure` 는 분모에 넣지 않고 따로 셉니다.
- 침묵: `silent` 사례 가운데 아무것도 안 넣은 비율.
- 걸린 시간(사례당 중앙값·95%·최댓값, 판정을 불렀던 사례와 아닌 사례를 나눠서)과 넣은 글의 토큰(사례당 평균·최댓값).

`--runs N`(기본 1)은 전체를 N 번 돌립니다. 판정 명령이 있으면 회차마다 답이 달라질 수 있어, 조건별 점수를 회차별로 내고 합격 여부는 가장 낮은 회차로 봅니다. `--no-judge` 는 판정 명령을 쓰지 않아 참고 항목(주제 갈래)은 프롬프트에서 넣지 않습니다. `--parallel N`(기본 4)은 사례를 동시에 돌립니다. `--json` 은 같은 내용을 JSON 으로, `--out <파일>` 은 사례별 결과(넣은 항목과 이유, 탈락과 사유, 빠진·틀린·모르는·정답 없는 항목, 맞았는지)를 회차마다 JSONL 로 씁니다.

`--min-delivery`, `--min-precision`, `--min-silence`(0~1)를 주면 가장 낮은 회차가 그 값에 못 미칠 때 종료 코드 1 로 끝납니다(`--min-delivery` 는 전체와 필수 대상 모두에 적용). 주지 않으면 재기만 하고 0 입니다. 잴 사례가 없는 조건은 판정하지 않고 알립니다. 시험용 사례와 항목은 `internal/replay/testdata/` 에 있습니다(`config.yaml`, `items/`, `cases.jsonl`).

```sh
mkdir -p /tmp/heka-replay && cp -r internal/replay/testdata/items /tmp/heka-replay/ && cp internal/replay/testdata/config.yaml /tmp/heka-replay/
HEKA_HOME=/tmp/heka-replay heka replay internal/replay/testdata/cases.jsonl --no-judge --min-silence 1
```

## MCP 서버: heka mcp

표준 입출력으로 JSON-RPC 2.0 메시지를 한 줄에 하나씩 주고받습니다. 도구는 셋입니다. 검토 도구는 두지 않습니다(검토는 사람이 시작하는 일입니다).

| 도구 | 쓰임 |
|---|---|
| `heka_find` | `query`(필수), `scopes`, `budget`. 저장소·팀·개인의 규칙이나 용어를 모를 때 답하기 전에 찾습니다. |
| `heka_add` | 등록 입력 필드(`checked` 제외). 등록됨(비슷한 후보가 있으면 후보 목록과 "뒤에서 판정하니 사용자에게 물을 필요는 없다"는 안내), 검증 문제(같은 id 포함)를 돌려줍니다. |
| `heka_flag` | `id`, `reason`. 틀렸거나 낡았다는 표시를 원장에 남깁니다(`event: flag`). 항목 파일은 고치지 않습니다. |

도구 호출이 실패하면 서버는 죽지 않고 `isError: true` 결과를 돌려줍니다. 요청마다 색인이 낡았는지 보고 다시 엽니다. 서버의 작업 폴더가 `heka_find` 의 기본 범위를 정합니다.

Claude Code 에 등록하려면 다음을 직접 실행하세요(이 저장소의 작업이 대신 등록하지 않습니다).

```sh
claude mcp add --scope user heka -- /path/to/heka mcp
# 데이터 폴더를 지정하려면:
claude mcp add --scope user -e HEKA_HOME=/path/to/data heka -- /path/to/heka mcp
```

서버는 `initialize` 로 시작하는 방식의 판(2025-11-25, 2025-06-18, 2025-03-26, 2024-11-05)을 말합니다.

## 도구 호출을 동작 이름으로

`heka resolve --tool Bash --command 'git commit -m x && git push -f'` 처럼 도구 호출 하나를 동작 이름(`git.commit`, `git.push.force`, `code.edit` 등)으로 해석해 JSON 으로 출력합니다. Bash 명령은 `internal/shell` 이 구간과 단어로 나누고 접두(`sudo`, `env`, `exec` 등)와 git 전역 옵션을 벗긴 뒤 읽습니다. Bash 가 아닌 호출 중 도구 이름이나 스킬 이름으로만 알 수 있는 동작은 설정의 `tool_actions` 로 잇습니다(`config.example.yaml` 참고).

항목의 `when.agent` 는 에이전트 종류(`subagent_type`)와 견줍니다. `main` 은 서브에이전트가 아닌 본체를 가리키므로, 본체에만 줄 항목은 `when: {agent: [main]}` 으로 적습니다. 서브에이전트 안의 호출인데 종류가 비어 오면 `subagent` 로 봅니다.

항목의 `when.action` 에 쓴 이름이 설정의 `actions`·`tool_actions` 에도, 해석기가 스스로 내는 이름(`code.edit`, `git.stash`, `git.destructive`, `git.worktree`, `git.create_branch`, `doc.edit`, `test.run`, `agent.delegate` 등)에도 없으면 검증이 경고합니다. 해석기가 내는 이름은 설정에 적지 않아도 되고, `git.push.force` 가 `git.push` 에 속하듯 점으로 이어진 이름의 위쪽도 아는 이름으로 칩니다. 설정에 동작 이름이 하나도 없으면 이 경고는 하지 않습니다.

## 문서

- [docs/design.md](docs/design.md): 설계(정의, 요구사항, 구조, 성공 조건)

## 빌드와 검사

```sh
make build   # bin/heka 빌드
make check   # gofmt, go vet, go test, 푸시 전 검사 훅 시험
make hooks   # 이 저장소의 git 훅 경로를 .githooks 로 지정
```

직접 의존하는 외부 모듈은 둘입니다: 순수 Go SQLite 드라이버 `modernc.org/sqlite` 와 YAML 라이브러리 `go.yaml.in/yaml/v3`. cgo 는 쓰지 않습니다.

## 푸시 전 검사 훅

공개 저장소이므로 `.githooks/pre-push` 가 푸시될 것에서 금지어와 비밀키 모양의 문자열을 찾고, 발견하면 푸시를 막습니다. 검사 대상은 다음과 같습니다.

- 커밋: 작성자·커미터 이름과 이메일, 메시지(트레일러 포함), 병합 커밋에 든 태그, 바뀐 파일 경로, 추가된 줄. 바이너리로 판정된 파일도 내용을 봅니다. 삭제된 줄은 보지 않습니다.
- 주석 태그: 태그 메시지와 태거 이름·이메일.
- ref 이름: 푸시하는 브랜치·태그의 로컬 이름과 원격 이름.

비밀키는 PEM 개인키, Slack 토큰과 웹훅 URL, GitHub 토큰, Anthropic·OpenAI 키, AWS 액세스 키와 임시 키, Google API 키, Stripe 라이브 키, npm 토큰, Atlassian API 토큰의 모양을 찾습니다.

걸리면 어느 커밋의 어디(파일, 메시지, 작성자, ref 이름)에 어떤 종류가 걸렸는지만 알려 주고, 걸린 줄의 원문과 금지어 패턴은 출력하지 않습니다. 훅은 사람에게 묻지 않습니다. 걸린 것이 있거나 내부 오류일 때만 막고, 그 밖에는 조용히 지나갑니다.

`make hooks` 로 켭니다. 이것은 클론마다 한 번 해야 하는 로컬 설정(`core.hooksPath`)입니다. 새로 클론하면 꺼져 있습니다.

금지어 목록은 저장소 밖 파일입니다. 기본 경로는 `$HOME/.config/heka/push-denylist.txt` 이고, 환경 변수 `HEKA_PUSH_DENYLIST` 로 바꿀 수 있습니다. 한 줄에 확장 정규식 하나를 적으며, 줄의 앞뒤 공백은 지우고, 빈 줄과 `#` 으로 시작하는 줄은 무시하고, 대소문자는 구분하지 않습니다. 목록 파일이 없거나, 읽을 수 없거나, 유효한 패턴이 하나도 없으면 푸시를 막습니다. 파일이 없거나 패턴이 하나도 없을 때 `HEKA_PUSH_DENYLIST_OPTIONAL=1` 을 지정하면 경고를 내고 비밀키 검사만 합니다. 읽을 수 없는 목록은 이 설정으로도 막습니다.

이미 있는 커밋을 수동으로 검사하려면 `make scan` 을 씁니다. 로컬의 모든 브랜치, 태그, 원격 추적 ref(와 HEAD)에서 닿는 커밋과 주석 태그, ref 이름을 봅니다. 범위를 정해 검사하려면 `.githooks/pre-push --range origin/main..main` 처럼 씁니다. 모르는 `-` 인자는 사용법을 내고 오류로 끝납니다.

### 이 훅이 검사하지 못하는 경우

- `git push --no-verify` 는 훅을 건너뜁니다.
- 훅 설정(`make hooks`)은 클론마다 한 번 해야 합니다. 하지 않은 클론에서는 훅이 돌지 않습니다.
- 훅 파일이 없는 옛 커밋을 체크아웃한 채 푸시하면, 그 작업 트리에는 `.githooks/pre-push` 가 없으므로 훅 없이 나갑니다.
- GitHub 웹 편집기, GitHub API, 다른 도구와 다른 머신의 클론은 이 훅을 거치지 않습니다.
- 이미 원격에 있는 커밋은 다시 보지 않습니다. 이미 올라간 것은 `make scan` 이나 `--range` 로 따로 확인해야 합니다.
- 서버 쪽 보완책으로 GitHub 의 push protection(비밀키 전용)을 켜 둡니다. 금지어는 서버에서 막아 주지 않습니다.

## 라이선스

[Apache-2.0](LICENSE)
