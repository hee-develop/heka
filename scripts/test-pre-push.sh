#!/bin/bash
# .githooks/pre-push 훅의 동작 시험. 임시 저장소와 가짜 금지어 목록으로 돌리고,
# 하나라도 기대와 다르면 0 이 아닌 값으로 끝난다. 전역 git 설정은 건드리지 않는다.
# 사용법: bash scripts/test-pre-push.sh
set -u

HOOK="$(cd "$(dirname "$0")/.." && pwd)/.githooks/pre-push"
SELF="$(cd "$(dirname "$0")/.." && pwd)/scripts/test-pre-push.sh"
[ -f "$HOOK" ] || { echo "훅 파일이 없다: $HOOK" >&2; exit 1; }

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/heka-hook-test.XXXXXX")" || exit 1
trap 'rm -rf "$ROOT"' EXIT

BAD="zorb""lax-corp" # 지어낸 금지어(이 파일 자신이 걸리지 않게 이어 붙임)
DENY="$ROOT/deny.txt"
printf '# 시험용 금지어 목록\n\n%s\n' "$BAD" > "$DENY"
ZERO=0000000000000000000000000000000000000000

# 비밀키 모양 문자열은 이어 붙여서 만든다(이 파일에 그대로 적지 않기 위해)
fake_secret() {
  case "$1" in
    aws)    printf '%s%s%s' 'AK' 'IA' 'ABCDEFGHIJKLMNOP' ;;
    slack)  printf '%s%s' 'xo' 'xb-1234567890-abcdefghij' ;;
    github) printf '%s%s%s' 'gh' 'p_' 'abcdefghijklmnopqrstuvwxyz0123456789' ;;
    pem)    printf '%s%s' '-----BEGIN RSA PRIV' 'ATE KEY-----' ;;
    anth)   printf '%s%s' 'sk-' 'ant-api03-abcdefghij' ;;
    google) printf '%s%s%s' 'AI' 'za' 'abcdefghijklmnopqrstuvwxyz0123456789' ;;
  esac
}

PASS=0
FAILN=0
check() { # <이름> <기대 종료 코드> <실제 종료 코드>
  if [ "$2" -eq "$3" ]; then
    PASS=$((PASS + 1)); echo "ok   $1"
  else
    FAILN=$((FAILN + 1)); echo "FAIL $1 (기대 $2, 실제 $3)"
  fi
}

new_repo() { # <이름>: 빈 저장소를 만들고 경로를 REPO 에 담는다
  REPO="$ROOT/$1"
  git init -q "$REPO" || exit 1
}

commit() { # <메시지> [이메일]: REPO 에서 커밋한다
  git -C "$REPO" -c user.name=tester -c user.email="${2:-tester@example.com}" \
    -c commit.gpgsign=false commit -q -m "$1" || { echo "커밋 실패" >&2; exit 1; }
}

hook() { # 훅을 REPO 안에서 돌려 종료 코드를 돌려준다. 인자는 훅에 그대로 전달
  (cd "$REPO" && HEKA_PUSH_DENYLIST="${DENYFILE:-$DENY}" bash "$HOOK" "$@" > "$ROOT/out.txt" 2> "$ROOT/err.txt")
}

run_check() { # <이름> <기대> <훅 인자...>
  local name="$1" want="$2"
  shift 2
  hook "$@"
  check "$name" "$want" "$?"
}

# (a) 깨끗한 커밋은 통과
new_repo a
echo "hello" > "$REPO/a.txt"; git -C "$REPO" add a.txt; commit "첫 커밋"
run_check "(a) 깨끗한 커밋 --all" 0 --all
grep -q "문제 없음" "$ROOT/out.txt"; check "(a) 통과 메시지" 0 "$?"

# (b) 추가된 줄에 금지어
new_repo b
echo "회사는 $BAD 입니다" > "$REPO/b.txt"; git -C "$REPO" add b.txt; commit "메모 추가"
run_check "(b) 추가된 줄 금지어 --all" 1 --all
grep -q "추가된 줄" "$ROOT/err.txt"; check "(b) 출력에 위치 표시" 0 "$?"
grep -q "$BAD" "$ROOT/err.txt"; check "(b) 걸린 줄을 출력" 0 "$?"

# (c) 커밋 메시지에 금지어
new_repo c
echo x > "$REPO/c.txt"; git -C "$REPO" add c.txt; commit "feat: $BAD 연동"
run_check "(c) 커밋 메시지 금지어 --all" 1 --all

# (d) 작성자 이메일에 금지어
new_repo d
echo x > "$REPO/d.txt"; git -C "$REPO" add d.txt; commit "정상 메시지" "dev@$BAD.example"
run_check "(d) 작성자 이메일 금지어 --all" 1 --all

# (d2) 파일 경로에 금지어, 대소문자 무시
new_repo d2
UP="$(printf '%s' "$BAD" | tr a-z A-Z)"
echo x > "$REPO/$UP-notes.txt"; git -C "$REPO" add .; commit "경로 시험"
run_check "(d2) 파일 경로 금지어(대소문자 무시)" 1 --all

# (e) 비밀키 패턴
for kind in aws slack github pem anth google; do
  new_repo "e-$kind"
  fake_secret "$kind" > "$REPO/key.txt"; git -C "$REPO" add key.txt; commit "키 추가"
  run_check "(e) 비밀키 $kind" 1 --all
done
grep -q "비밀키" "$ROOT/err.txt"; check "(e) 출력에 종류 표시" 0 "$?"

# (f) 금지어 목록 파일이 없으면 막힘
new_repo f
echo x > "$REPO/f.txt"; git -C "$REPO" add f.txt; commit "깨끗함"
DENYFILE="$ROOT/없는-파일.txt" run_check "(f) 목록 없음 --all" 1 --all
grep -q "만드는 방법" "$ROOT/err.txt"; check "(f) 만드는 방법 안내" 0 "$?"

# (g) OPTIONAL=1 이면 목록이 없어도 깨끗한 커밋은 통과, 비밀키는 여전히 막힘
DENYFILE="$ROOT/없는-파일.txt" HEKA_PUSH_DENYLIST_OPTIONAL=1 run_check "(g) OPTIONAL=1 깨끗한 커밋" 0 --all
new_repo g2
fake_secret aws > "$REPO/k.txt"; git -C "$REPO" add k.txt; commit "키"
DENYFILE="$ROOT/없는-파일.txt" HEKA_PUSH_DENYLIST_OPTIONAL=1 run_check "(g) OPTIONAL=1 비밀키는 막힘" 1 --all

# (h) 삭제된 줄에만 금지어가 있으면 통과(--range 로 두 번째 커밋만 검사)
new_repo h
printf 'keep\n%s\n' "$BAD" > "$REPO/h.txt"; git -C "$REPO" add h.txt
# 금지어가 있는 첫 커밋은 검사 범위 밖에 둔다
commit "초기"
FIRST="$(git -C "$REPO" rev-parse HEAD)"
printf 'keep\n' > "$REPO/h.txt"; git -C "$REPO" add h.txt; commit "줄 삭제"
run_check "(h) 삭제된 줄만 금지어 --range" 0 --range "$FIRST..HEAD"
run_check "(h) 대조: 금지어가 추가된 첫 커밋은 막힘" 1 --all

# (i) 일반 모드(표준 입력 형식)로 새 브랜치 푸시 흉내
new_repo i
echo x > "$REPO/i.txt"; git -C "$REPO" add i.txt; commit "깨끗함"
CLEAN="$(git -C "$REPO" rev-parse HEAD)"
echo "$BAD" > "$REPO/i2.txt"; git -C "$REPO" add i2.txt; commit "금지어 추가"
DIRTY="$(git -C "$REPO" rev-parse HEAD)"
printf 'refs/heads/topic %s refs/heads/topic %s\n' "$CLEAN" "$ZERO" | hook origin
check "(i) 일반 모드 새 브랜치, 깨끗함" 0 "$?"
printf 'refs/heads/topic %s refs/heads/topic %s\n' "$DIRTY" "$ZERO" | hook origin
check "(i) 일반 모드 새 브랜치, 금지어" 1 "$?"
printf 'refs/heads/topic %s refs/heads/topic %s\n' "$DIRTY" "$CLEAN" | hook origin
check "(i) 일반 모드 기존 브랜치, 금지어" 1 "$?"
printf 'refs/heads/topic %s refs/heads/topic %s\n' "$CLEAN" "$CLEAN" | hook origin
check "(i) 일반 모드 변경 없음(빈 범위)" 0 "$?"
printf '(삭제) %s refs/heads/topic %s\n' "$ZERO" "$DIRTY" | hook origin
check "(i) 일반 모드 브랜치 삭제는 건너뜀" 0 "$?"

# (j) 커밋이 0개이면 검사할 커밋 없음으로 통과
new_repo j
run_check "(j) 커밋 0개 --all" 0 --all
grep -q "검사할 커밋 없음" "$ROOT/out.txt"; check "(j) 안내 메시지" 0 "$?"

# (k) 내부 오류는 막는다: 잘못된 범위, 잘못된 정규식
run_check "(k) 잘못된 rev 범위는 막힘" 1 --range "없는-ref..HEAD"
printf '(\n' > "$ROOT/badre.txt"
DENYFILE="$ROOT/badre.txt" run_check "(k) 잘못된 정규식은 막힘" 1 --all

# (l) 훅 파일과 이 시험 스크립트가 자기 자신의 패턴에 걸리지 않는다
new_repo l
mkdir -p "$REPO/.githooks" "$REPO/scripts"
cp "$HOOK" "$REPO/.githooks/pre-push"; cp "$SELF" "$REPO/scripts/test-pre-push.sh"
git -C "$REPO" add .; commit "훅과 시험 스크립트"
run_check "(l) 훅·시험 스크립트가 비밀키 패턴에 안 걸림" 0 --all

echo "---"
echo "통과 $PASS, 실패 $FAILN"
[ "$FAILN" -eq 0 ]
