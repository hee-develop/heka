# heka

heka는 AI 에이전트가 일하는 각 순간에, 그 순간에 필요한 컨텍스트만 골라 행동 전에 전달하고, 그 전달이 맞았는지 기록으로 확인하는 개인용 컨텍스트 주입기입니다. Go로 만듭니다.

## 현재 상태

설계 단계입니다. 구현은 시작 전이며, 지금 동작하는 명령은 `heka version` 하나뿐입니다.

## 문서

- [docs/design.md](docs/design.md): 설계 초안(정의, 요구사항, 구조)

## 빌드와 검사

```sh
make build   # bin/heka 빌드
make check   # gofmt, go vet, go test, 푸시 전 검사 훅 시험
make hooks   # 이 저장소의 git 훅 경로를 .githooks 로 지정
```

지금은 Go 표준 라이브러리만 씁니다.

## 푸시 전 검사 훅

공개 저장소이므로 `.githooks/pre-push` 가 푸시될 커밋(작성자, 메시지, 파일 경로, 추가된 줄)에서 금지어와 비밀키 모양의 문자열을 찾고, 발견하면 푸시를 막습니다. `make hooks` 로 켭니다.

금지어 목록은 저장소 밖 파일입니다. 기본 경로는 `$HOME/.config/heka/push-denylist.txt` 이고, 환경 변수 `HEKA_PUSH_DENYLIST` 로 바꿀 수 있습니다. 한 줄에 확장 정규식 하나를 적으며, 빈 줄과 `#` 으로 시작하는 줄은 무시하고 대소문자는 구분하지 않습니다. 목록 파일이 없으면 푸시를 막습니다. 비밀키 검사만 하려면 `HEKA_PUSH_DENYLIST_OPTIONAL=1` 을 지정합니다.

이미 있는 커밋을 수동으로 검사하려면 `make scan` 을 씁니다.

## 라이선스

[Apache-2.0](LICENSE)
