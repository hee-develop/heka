# heka

heka는 AI 에이전트가 일하는 각 순간에, 그 순간에 필요한 컨텍스트만 골라 행동 전에 전달하고, 그 전달이 맞았는지 기록으로 확인하는 개인용 컨텍스트 주입기입니다. Go로 만듭니다.

## 현재 상태

설계 단계이며 구현은 일부만 있습니다. 동작하는 명령은 다음과 같습니다.

- `heka index [--check]`: 항목 파일과 설정을 읽어 검증하고 색인(SQLite)을 만듭니다.
- `heka select`: 순간 하나에 넣을 항목을 골라 JSON 으로 보여 줍니다.
- `heka resolve`: 도구 호출 하나를 동작 이름으로 바꿔 보여 줍니다.
- `heka find`: 항목을 찾습니다(낱말 나열도, 작업을 설명한 문장도 됩니다).
- `heka add`, `heka pending`: 항목을 등록하고, 비슷한 항목이 있어 대기함에 든 것을 결정합니다.
- `heka why`, `heka list`, `heka stats`, `heka retire`: 원장과 항목을 들여다보고 항목을 은퇴시킵니다(아래 "원장과 항목 보기").
- `heka judge-stats`: 원장에 남은 판정 시간을 모아 분포와 상한별로 잘릴 판정 수를 보입니다.
- `heka replay`: 기록해 둔 순간들을 선별기에 다시 넣어 전달·정밀도·침묵을 잽니다(아래 "재현: heka replay").
- `heka mcp`: 조회·등록·표시를 위한 MCP 서버입니다(표준 입출력).
- `heka hook`: Claude Code 훅 연결부입니다. 표준 입력의 훅 JSON 을 처리해 표준 출력에 JSON 을 냅니다. `--print-settings` 는 설정 파일의 `hooks` 에 넣을 JSON 조각을 출력하며(`--shadow` 를 더하면 그림자 방식용), 설정 파일을 직접 고치지는 않습니다.
- `heka version`

설정 예시는 `config.example.yaml` 입니다. 데이터 폴더는 `$HEKA_HOME` 이고, 없으면 `$HOME/.heka` 입니다(잠정).

## Claude Code 훅

`heka hook` 은 `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `SubagentStart`, `Stop` 을 처리합니다. 작업 위치(`cwd`)는 설정의 `locations` 로 범위와 저장소 이름에 맞추고, 넣을 항목은 `additionalContext` 로 냅니다. 같은 세션에서 같은 항목은 한 번만 넣고(압축·`clear` 뒤에는 다시), 서브에이전트는 `agent_id` 별로 따로 셉니다. 위임 도구(`Agent`·`Task`)의 프롬프트는 세션 상태에 적어 두었다가 `SubagentStart` 에서 그 글로 항목을 고릅니다.

- 프롬프트가 시킨 동작("푸시해 줘")은 그 턴 동안의 허용으로 적힙니다. "이번 세션 동안" 같은 표현이 있으면 세션 동안 남고, "푸시하지 마" 는 허용을 거둡니다. 도구가 만든 프롬프트(`tool_prompt_prefixes` 로 시작하는 글)는 항목도 허용도 건드리지 않습니다.
- `confirm: true` 항목은 그 동작(또는 상위 동작)에 허용이 없으면 `PreToolUse` 에서 확인을 요구하고(`permissionDecision: "ask"`), `always` 는 허용이 있어도 요구합니다.
- 오류, 입력 해석 실패, 시간 초과(프롬프트·`SubagentStart` 25초, 나머지 5초. 그림자 방식은 60초와 10초)에서는 아무것도 출력하지 않고 종료 코드 0 으로 끝나며, 오류는 `$HEKA_HOME/ledger/errors.log` 에 한 줄 남습니다. 판단은 `$HEKA_HOME/ledger/YYYY-MM.jsonl` 에 한 줄씩 남습니다. 세션 상태는 `$HEKA_HOME/sessions/<session_id>.json` 이며, 등록할 때 인용을 대조하려고 훅 입력의 `transcript_path`(세션 기록 파일의 경로)만 거기에 남습니다. 프롬프트 글은 따로 복사해 두지 않습니다.
- 토큰 상한은 기본이 없습니다(설정의 `budgets` 에 순간별로 적으면 적용). 넣은 글의 토큰 어림은 원장 줄의 `tokens` 에 남으므로 "너무 많은지"는 그것으로 봅니다. 참고 항목은 한 번에 최대 3건입니다.

훅 등록은 직접 합니다. `heka hook --print-settings` 가 낸 JSON 을 Claude Code 설정 파일의 `hooks` 에 넣으세요.

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
  target: commit_message     # command · commit_message · pr_body · last_message
  pattern: "^.{1,50}(\n|$)"  # 맞아야 하는 형식. not_pattern 은 있으면 안 되는 표현
  mode: block                # block 은 막고, log 는 기록만 한다
---
```

- 정규식은 Go 표준 문법(`regexp`)입니다. `pattern` 은 맞아야, `not_pattern` 은 맞지 않아야 통과합니다.
- `command`, `commit_message`, `pr_body` 는 도구 호출 직전(`PreToolUse`)에 Bash 명령에서 뽑아 견줍니다. 검사할 항목은 그 호출의 동작과 `when` 이 맞고 범위·자격을 통과한 것이며, 이미 넣은 항목이어도 매번 검사합니다. 커밋 메시지는 `git commit` 의 `-m`(여럿이면 빈 줄로 이음), `-F -` 와 heredoc, `--amend -m` 에서, PR 본문은 `gh pr create|edit` 의 `--body`·`-b`, `--body-file -` 와 heredoc, `gh api … -f body=…` 에서 뽑습니다. 파일로 넘겨서 뽑을 수 없으면 그 검사는 하지 않고(원장에 `skip`) 통과시킵니다.
- `mode: block` 이고 걸리면 `permissionDecision: "deny"` 로 막고, 사유에 그 항목의 글과 걸린 검사의 대상을 적어 모델이 고쳐서 다시 시도하게 합니다. 여러 항목이 걸리면 사유를 줄바꿈으로 이어 한 번에 냅니다. 같은 호출에 넣을 다른 항목은 `additionalContext` 로 함께 갑니다. 사람에게 묻지는 않습니다. `mode: log` 는 막지 않고 원장에만 남깁니다.
- `last_message` 는 턴이 끝날 때(`Stop`) 입력의 `last_assistant_message` 를 견줍니다. 이 검사의 항목은 범위·자격을 통과해야 하고, `when` 은 `repo`·`agent` 만 봅니다. 걸리면 `{"decision": "block", "reason": …}` 로 모델이 한 번 더 돌게 합니다. 세션당 3회까지만이고, 같은 항목이 연속으로 걸리면 두 번까지만 막으며, 그 뒤에는 원장에만 남깁니다.
- 식이 틀렸거나 글을 뽑다가 오류가 나면 통과시킵니다. 검사 때문에 막는 것은 위의 두 출력뿐이고, 훅이 실패해도 작업을 막지 않는다는 원칙은 그대로입니다.
- 원장 줄의 `checks` 에 항목 id, 대상, 결과(`pass`·`fail`·`skip`), 막았는지가 남습니다. `heka stats` 는 "검사에 걸린 횟수"를, `heka list --violated N` 은 검사에 걸린 횟수와 `flag` 횟수의 합이 N 이상인 항목을 보입니다.

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

`when` 은 항목의 조건(`moment`, `action`, `path`, `repo`, `agent` 중 있는 것)이고, `body_omitted` 는 예산 때문에 본문을 뺀 항목에만 있습니다. `score` 는 낱말 점수(0~1)입니다.

## 등록: heka add, heka pending

```sh
heka add --id release-check --text '배포 점검표는 릴리스 전에 확인한다.' \
  --about '릴리스 점검' --scope personal --source doc --body '점검표는 팀 위키에 있다.'
heka add --file <항목 파일> [--checked]
```

플래그는 `--id --text --must --moment --action --path --repo --agent --about --scope --source --quote --expires --confirm --body` 이고 목록 필드는 여러 번 쓸 수 있습니다. `--checked` 는 사람이 직접 확인했다는 뜻으로 오늘 날짜를 `checked` 에 넣습니다(MCP 로는 받지 않습니다). `--file` 로 읽은 파일의 `checked` 는 무시합니다.

- 검증은 `heka index --check` 와 같은 규칙입니다. error 가 있으면 등록하지 않고 문제 목록을 냅니다.
- `source: user` 이고 `quote` 가 있으면, 최근 24시간 안에 고쳐진 세션 상태 파일에 적힌 세션 기록 파일을 읽어 **사용자가 친 글**과 공백을 정규화해 대조합니다. AI 의 글, 도구 결과(`tool_result`), 도구가 만든 프롬프트(`tool_prompt_prefixes`), `<local-command`·`<command-name>`·`<system-reminder>` 로 시작하는 글, `isMeta`·`isSidechain` 줄은 인용으로 인정하지 않습니다. 기록 파일 읽기는 `internal/claudecode` 가 맡고(Claude Code 형식), 설정의 `claude_code.transcripts` 에 glob 목록을 주면 같은 형식의 다른 기록(최근 24시간 안에 고쳐진 것)도 봅니다. 기록 파일이 없거나 못 읽으면 그 파일은 건너뜁니다. 확인되지 않으면 `quote` 를 비우고 본문 끝에 `확인되지 않은 인용: "..."` 을 적어 등록하며, 사람이 확인(`checked`)하기 전에는 주입되지 않습니다.
- 비슷한 항목이 있으면 `items/_pending/<id>.md` 에 두고 후보를 알립니다. 비슷함은 (a) 범위와 `when.action` 이 겹침, (b) 주제어가 같음, (c) 새 항목의 `text` 를 질의로 한 낱말 점수가 0.5 이상(잠정), (d) 같은 id 입니다. 대기함의 항목은 주입되지 않습니다. 없으면 `items/<id>.md` 로 쓰고 색인을 다시 만듭니다.
- 파일은 임시 파일에 쓴 뒤 이름을 바꿉니다. `$HEKA_HOME` 에 `.git` 이 있으면 쓴 뒤 `git add`·`git commit`(`add: <id>`)을 시도하고, 실패해도 등록은 성공입니다.

```sh
heka pending                                  # 대기함 목록과 비슷한 항목
heka pending fix <대기 id> <기존 id>          # 기존 항목의 내용을 새 내용으로 고침(id 는 기존 것)
heka pending replace <대기 id> <기존 id>      # 기존 항목 파일을 지우고 새 항목을 등록
heka pending keep <대기 id> --reason <글>     # 다른 항목이라는 이유를 본문 끝에 적고 등록
heka pending drop <대기 id>                   # 대기함에서 지움
```

결정마다 색인을 다시 만들고 원장에 한 줄 남깁니다.

## 원장과 항목 보기: heka why, list, stats, retire

원장(`$HEKA_HOME/ledger/YYYY-MM.jsonl`)은 여러 달 파일을 모두 읽고, 깨진 줄은 건너뜁니다.

```sh
heka why [--session <id>] [--last N] [--id <항목 id>] [--json]
heka judge-stats [--json] [--since YYYY-MM-DD]
heka list [--json] [--agent <종류>] [--must] [--flagged] [--violated N]
heka stats <id> [--json]
heka retire <id> --reason <글>
```

- `why`: 최근 줄(기본 20)을 한 줄씩 보입니다. 시각(UTC), 이벤트, 동작, 넣은 항목과 이유, 빠진 항목(`id[단계: 사유]`), 판정(`판정: <id> 넣음 1234ms`. 안 넣음·오류·시간 초과도 같은 꼴), 토큰. `--id` 를 주면 그 항목이 넣어졌거나 빠진 줄만 봅니다. `--json` 은 원장 줄을 JSON 배열로 냅니다.
- `judge-stats`: 프롬프트·서브에이전트 시작의 주제 갈래가 판정 명령을 부를 때마다 원장 줄의 `judges` 에 항목 id, 답(`inject`), 걸린 시간(`ms`), 오류(`error`)와 시간 초과(`timeout`)가 남습니다(`votes: 2` 면 호출마다 한 건). 이 명령은 그 기록을 모아 판정 수, 걸린 시간의 중앙값·95%·최댓값, 그리고 판정 시간 상한을 3·4·5·6·8·10·15초로 줄였다면 잘렸을 판정(걸린 시간이 상한을 넘은 것)의 수와 그중 넣으라는 답의 수를 표로 보입니다. 시간 초과·오류로 끝난 호출은 답이 없어 넣으라는 답에 세지 않습니다. `--since` 는 그 날짜(UTC) 이후의 줄만 셉니다.
- `list`: 항목의 id, 필수 여부, 조건(`when`), 범위, 확인 날짜, 기한. `--agent` 는 `when.agent` 에 그 종류가 걸린 항목, `--flagged` 는 원장에 `flag` 가 남은 항목만, `--violated N` 은 검사에 걸린 횟수와 `flag` 횟수의 합이 N 이상인 항목만 보입니다. 검증에 실패한 항목은 빼고 알립니다.
- `stats`: 주입 횟수, 마지막 주입 시각, 예산·중복·판정으로 빠진 횟수, 결과물 검사에 걸린 횟수, `flag` 횟수와 마지막 사유. 훅의 줄만 셉니다(조회·등록 줄은 세지 않습니다).
- `retire`: 항목 파일을 지우고 색인을 다시 만들고 원장에 `retire` 한 줄(사유 포함)을 남깁니다. `$HEKA_HOME` 에 `.git` 이 있으면 사유를 메시지로 커밋을 시도하며, 실패해도 성공입니다.

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

표준 입출력으로 JSON-RPC 2.0 메시지를 한 줄에 하나씩 주고받습니다. 도구는 넷입니다.

| 도구 | 쓰임 |
|---|---|
| `heka_find` | `query`(필수), `scopes`, `budget`. 저장소·팀·개인의 규칙이나 용어를 모를 때 답하기 전에 찾습니다. |
| `heka_add` | 등록 입력 필드(`checked` 제외). 등록됨, 대기함(후보와 "사용자에게 물어 달라"는 안내), 검증 문제를 돌려줍니다. |
| `heka_pending` | `action`(list\|fix\|replace\|keep\|drop), `id`, `target`, `reason`. |
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

## 문서

- [docs/design.md](docs/design.md): 설계 초안(정의, 요구사항, 구조)

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
