# heka

heka는 AI 에이전트가 일하는 각 순간에, 그 순간에 필요한 컨텍스트만 골라 행동 전에 전달하고, 그 전달이 맞았는지 기록으로 확인하는 개인용 컨텍스트 주입기입니다. Go로 만듭니다.

## 현재 상태

설계 단계이며 구현은 일부만 있습니다. 동작하는 명령은 다음과 같습니다.

- `heka index [--check]`: 항목 파일과 설정을 읽어 검증하고 색인(SQLite)을 만듭니다.
- `heka select`: 순간 하나에 넣을 항목을 골라 JSON 으로 보여 줍니다.
- `heka resolve`: 도구 호출 하나를 동작 이름으로 바꿔 보여 줍니다.
- `heka find`: 항목을 찾습니다(낱말 나열도, 작업을 설명한 문장도 됩니다).
- `heka add`, `heka pending`: 항목을 등록하고, 비슷한 항목이 있어 대기함에 든 것을 결정합니다.
- `heka mcp`: 조회·등록·표시를 위한 MCP 서버입니다(표준 입출력).
- `heka hook`: Claude Code 훅 연결부입니다. 표준 입력의 훅 JSON 을 처리해 표준 출력에 JSON 을 냅니다. `--print-settings` 는 설정 파일의 `hooks` 에 넣을 JSON 조각을 출력하며, 설정 파일을 직접 고치지는 않습니다.
- `heka version`

설정 예시는 `config.example.yaml` 입니다. 결과물 검사(`check`)는 아직 없습니다. 데이터 폴더는 `$HEKA_HOME` 이고, 없으면 `$HOME/.heka` 입니다(잠정).

## Claude Code 훅

`heka hook` 은 `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `SubagentStart` 를 처리합니다. 작업 위치(`cwd`)는 설정의 `locations` 로 범위와 저장소 이름에 맞추고, 넣을 항목은 `additionalContext` 로 냅니다. 같은 세션에서 같은 항목은 한 번만 넣고(압축·`clear` 뒤에는 다시), 서브에이전트는 `agent_id` 별로 따로 셉니다. 위임 도구(`Agent`·`Task`)의 프롬프트는 세션 상태에 적어 두었다가 `SubagentStart` 에서 그 글로 항목을 고릅니다.

- 프롬프트가 시킨 동작("푸시해 줘")은 그 턴 동안의 허용으로 적힙니다. "이번 세션 동안" 같은 표현이 있으면 세션 동안 남고, "푸시하지 마" 는 허용을 거둡니다. 도구가 만든 프롬프트(`tool_prompt_prefixes` 로 시작하는 글)는 항목도 허용도 건드리지 않습니다.
- `confirm: true` 항목은 그 동작(또는 상위 동작)에 허용이 없으면 `PreToolUse` 에서 확인을 요구하고(`permissionDecision: "ask"`), `always` 는 허용이 있어도 요구합니다.
- 오류, 입력 해석 실패, 시간 초과(프롬프트·`SubagentStart` 25초, 나머지 5초)에서는 아무것도 출력하지 않고 종료 코드 0 으로 끝나며, 오류는 `$HEKA_HOME/ledger/errors.log` 에 한 줄 남습니다. 판단은 `$HEKA_HOME/ledger/YYYY-MM.jsonl` 에 한 줄씩 남습니다. 세션 상태는 `$HEKA_HOME/sessions/<session_id>.json` 이며, 등록할 때 인용을 대조하려고 사용자 프롬프트(세션마다 최근 50개)도 거기에 남습니다.

훅 등록은 직접 합니다. `heka hook --print-settings` 가 낸 JSON 을 Claude Code 설정 파일의 `hooks` 에 넣으세요.

## 조회: heka find

```sh
heka find [--scope S]... [--budget N] [--limit K] [--json] <질의…>
```

모델 판정을 부르지 않고, 동작 조건이 붙은 항목도 대상에 넣습니다. 후보는 주제어(`about`)가 질의에 든 항목과 낱말 점수 상위 K건(기본 10, 점수 > 0)이고, 범위·자격(기한, 미확인 ai 등)은 프롬프트 선별과 같은 규칙으로 거릅니다. 순서는 주제어로 걸린 것이 먼저, 그다음 낱말 점수 내림차순, 같으면 id 오름차순입니다. 범위는 `--scope` 로 주고, 없으면 현재 작업 폴더를 설정의 `locations` 로 찾아 정합니다(`global_scopes` 는 늘 포함).

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
- `source: user` 이고 `quote` 가 있으면, 훅이 세션 상태에 남긴 사용자 프롬프트(세션마다 최근 50개, 한 개 4,000자까지) 중 최근 24시간 안에 고쳐진 세션의 것과 공백을 정규화해 대조합니다. 확인되지 않으면 `quote` 를 비우고 본문 끝에 `확인되지 않은 인용: "..."` 을 적어 등록하며, 사람이 확인(`checked`)하기 전에는 주입되지 않습니다.
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
