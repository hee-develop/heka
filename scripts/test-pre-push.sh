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
    openai) printf '%s%s%s' 'sk' '-proj-' 'abcdefghijklmnopqrstuvwxyz012345' ;;
    openai2) printf '%s%s' 'sk' '-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH' ;;
    stripe) printf '%s%s%s' 'sk' '_live_' 'abcdefghijklmnopqrstuvwx' ;;
    asia)   printf '%s%s%s' 'AS' 'IA' 'ABCDEFGHIJKLMNOP' ;;
    npm)    printf '%s%s%s' 'np' 'm_' 'abcdefghijklmnopqrstuvwxyz0123456789' ;;
    hook)   printf '%s%s%s' 'https://hooks' '.slack.com/services/T01234567/B01234567/' 'abcdefghijklmnopqrstuvwx' ;;
    atl)    printf '%s%s%s' 'AT' 'ATT3' 'abcdefghijklmnopqrstuvwxyz012345' ;;
  esac
}
secret_label() { # 훅이 출력하는 비밀키 종류 이름
  case "$1" in
    aws) echo 'AWS 액세스 키' ;; slack) echo 'Slack 토큰' ;; github) echo 'GitHub 토큰' ;;
    pem) echo 'PEM 개인키' ;; anth) echo 'Anthropic 키' ;; google) echo 'Google API 키' ;;
    openai) echo 'OpenAI 키' ;; openai2) echo 'OpenAI 키(구형)' ;; stripe) echo 'Stripe 라이브 키' ;;
    asia) echo 'AWS 임시 키' ;; npm) echo 'npm 토큰' ;; hook) echo 'Slack 웹훅 URL' ;;
    atl) echo 'Atlassian API 토큰' ;;
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

has() { # <이름> <파일> <고정 문자열>: 파일에 문자열이 있어야 한다
  grep -qF -- "$3" "$2"; check "$1" 0 "$?"
}
lacks() { # <이름> <파일> <고정 문자열>: 파일에 문자열이 없어야 한다
  grep -qF -- "$3" "$2"; check "$1" 1 "$?"
}
errhas() { has "$1" "$ROOT/err.txt" "$2"; }
noleak() { # <이름> <새는 문자열>: 출력(표준 출력·오류)에 원문이 없어야 한다
  lacks "$1 (오류 출력)" "$ROOT/err.txt" "$2"
  lacks "$1 (표준 출력)" "$ROOT/out.txt" "$2"
}
hookin() { # <표준 입력 내용> <훅 인자...>: 표준 입력을 넣어 훅을 돌린다
  local input="$1"
  shift
  printf '%s' "$input" | hook "$@"
}

new_remote_repo() { # <이름>: REPO(복제본)와 bare 원격. 깨끗한 첫 커밋을 main 으로 푸시해 원격 추적 ref 가 있다
  REMOTE_DIR="$ROOT/$1.git"
  git init -q --bare "$REMOTE_DIR" || exit 1
  new_repo "$1"
  git -C "$REPO" remote add origin "$REMOTE_DIR"
  echo base > "$REPO/base.txt"; git -C "$REPO" add base.txt; commit "기반"
  git -C "$REPO" branch -M main
  git -C "$REPO" -c core.hooksPath=/dev/null push -q origin main 2> /dev/null || { echo "원격 준비 실패" >&2; exit 1; }
  git -C "$REPO" fetch -q origin
  BASE="$(git -C "$REPO" rev-parse HEAD)"
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
errhas "(b) 출력에 금지어 종류 표시" "금지어 걸림"
errhas "(b) 출력에 파일 이름 표시(11)" "b.txt"
errhas "(b) 출력에 커밋 표시" "[$(git -C "$REPO" rev-parse --short HEAD)]"
noleak "(b) 금지어 원문을 출력하지 않음(10)" "$BAD"

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
for kind in aws slack github pem anth google openai openai2 stripe asia npm hook atl; do
  new_repo "e-$kind"
  { printf 'KEY='; fake_secret "$kind"; echo; } > "$REPO/key.txt"; git -C "$REPO" add key.txt; commit "키 추가"
  run_check "(e) 비밀키 $kind" 1 --all
  errhas "(e) $kind 종류 이름 표시(6)" "비밀키($(secret_label "$kind"))"
  noleak "(e) $kind 비밀키 원문을 출력하지 않음(10)" "$(fake_secret "$kind")"
  errhas "(e) $kind 파일 이름 표시(11)" "key.txt"
done
# 비밀키처럼 보이지만 아닌 글은 통과(오탐 확인)
new_repo e-neg
printf 'ask-me-anything-about-task-force-management-things\nsk-short\nhttps://hooks.slack.com/docs\nnpm_install_something\n' > "$REPO/neg.txt"
git -C "$REPO" add neg.txt; commit "오탐 확인"
run_check "(e) 비밀키와 비슷한 일반 글은 통과" 0 --all

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

# ======== 아래는 푸시 전 검사 강화 후 추가한 시험 ========

# (m) [1] 금지어 목록을 읽지 못하면 막는다(빈 목록으로 보고 통과시키지 않는다)
new_repo m
echo "$BAD" > "$REPO/m.txt"; git -C "$REPO" add m.txt; commit "금지어"
cp "$DENY" "$ROOT/unread.txt"; chmod 000 "$ROOT/unread.txt"
if [ -r "$ROOT/unread.txt" ]; then
  echo "skip (m) 읽기 권한 시험: 이 계정은 권한과 무관하게 읽을 수 있다"
else
  DENYFILE="$ROOT/unread.txt" run_check "(m1) 읽을 수 없는 목록은 막힘" 1 --all
  errhas "(m1) 읽을 수 없다고 알림" "읽을 수 없다"
  DENYFILE="$ROOT/unread.txt" HEKA_PUSH_DENYLIST_OPTIONAL=1 run_check "(m1) OPTIONAL=1 이어도 읽을 수 없는 목록은 막힘" 1 --all
fi
chmod 600 "$ROOT/unread.txt"
mkdir -p "$ROOT/dirlist"
DENYFILE="$ROOT/dirlist" run_check "(m1) 목록 경로가 디렉터리면 막힘" 1 --all
mkdir -p "$ROOT/noexec"; cp "$DENY" "$ROOT/noexec/list.txt"; chmod 000 "$ROOT/noexec"
if [ -x "$ROOT/noexec" ]; then
  echo "skip (m) 디렉터리 권한 시험"
else
  DENYFILE="$ROOT/noexec/list.txt" HEKA_PUSH_DENYLIST_OPTIONAL=1 run_check "(m1) 목록이 든 디렉터리에 못 들어가면 막힘" 1 --all
fi
chmod 700 "$ROOT/noexec"

# (n) [2] 유효한 패턴이 없으면 막는다. OPTIONAL=1 일 때만 경고하고 비밀키 검사만으로 진행
printf '# 주석뿐\n\n   \n\t# 들여쓴 주석\n' > "$ROOT/empty.txt"
: > "$ROOT/zero.txt"
DENYFILE="$ROOT/empty.txt" run_check "(n) 주석뿐인 목록은 막힘" 1 --all
errhas "(n) 패턴이 없다고 알림" "유효한 패턴이 하나도 없다"
DENYFILE="$ROOT/zero.txt" run_check "(n) 빈 파일 목록은 막힘" 1 --all
DENYFILE="$ROOT/empty.txt" HEKA_PUSH_DENYLIST_OPTIONAL=1 run_check "(n) OPTIONAL=1 금지어 커밋은 검사 생략으로 통과" 0 --all
errhas "(n) OPTIONAL=1 경고를 냄" "건너뛴다"
new_repo n2
fake_secret aws > "$REPO/k.txt"; git -C "$REPO" add k.txt; commit "키"
DENYFILE="$ROOT/empty.txt" HEKA_PUSH_DENYLIST_OPTIONAL=1 run_check "(n) OPTIONAL=1 비밀키는 막힘" 1 --all
printf 'x*\n' > "$ROOT/emptymatch.txt"
DENYFILE="$ROOT/emptymatch.txt" run_check "(n) 빈 문자열과 일치하는 패턴은 목록 오류로 막힘" 1 --all
errhas "(n) 빈 문자열 패턴 안내" "빈 문자열과 일치하는 패턴"

# (o) [3] 바이너리로 판정된 파일의 내용도 검사한다
new_repo o1
printf 'x\0y %s\n' "$BAD" > "$REPO/bin.dat"; git -C "$REPO" add bin.dat; commit "NUL 포함"
run_check "(o) 앞쪽에 NUL 이 든 파일 안의 금지어" 1 --all
errhas "(o) 위치 표시" "추가된 줄"
errhas "(o) 파일 이름 표시" "bin.dat"
new_repo o2
printf 'x\0y %s\n' "$(fake_secret aws)" > "$REPO/bin.dat"; git -C "$REPO" add bin.dat; commit "NUL 포함 키"
run_check "(o) NUL 이 든 파일 안의 비밀키" 1 --all
errhas "(o) 비밀키 종류 표시" "비밀키(AWS 액세스 키)"
new_repo o3
printf '*.md binary\n' > "$REPO/.gitattributes"; echo "$BAD" > "$REPO/n.md"
git -C "$REPO" add .gitattributes n.md; commit "binary 속성"
run_check "(o) .gitattributes 로 binary 표시한 파일의 금지어" 1 --all
new_repo o4
printf '*.md binary\n' > "$REPO/.gitattributes"; fake_secret github > "$REPO/n.md"
git -C "$REPO" add .gitattributes n.md; commit "binary 속성 키"
run_check "(o) .gitattributes 로 binary 표시한 파일의 비밀키" 1 --all
new_repo o5
printf '\377\376\0\200abc\377 %s \377\n' "$BAD" > "$REPO/bad-utf8.bin"; git -C "$REPO" add .; commit "깨진 UTF-8"
run_check "(o) 깨진 UTF-8 바이트가 섞인 파일의 금지어" 1 --all
new_repo o6
head -c 20000 /dev/urandom > "$REPO/rand.bin"; git -C "$REPO" add .; commit "무작위 바이너리"
run_check "(o) 무작위 바이너리는 오류 없이 통과" 0 --all

# (p) [4] 주석 태그의 메시지와 태거
new_remote_repo p
git -C "$REPO" -c user.name=tester -c user.email=t@example.com -c tag.gpgsign=false tag -a v1 -m "릴리스 $BAD" "$BASE"
T1="$(git -C "$REPO" rev-parse v1)"
hookin "refs/tags/v1 $T1 refs/tags/v1 $ZERO
" origin
check "(p) 주석 태그 메시지 금지어(대상 커밋은 원격에 있음)" 1 "$?"
errhas "(p) 태그 위치 표시" "태그 메시지·태거"
noleak "(p) 태그 메시지 원문을 출력하지 않음" "$BAD"
git -C "$REPO" -c user.name=tester -c user.email="me@$BAD.example" -c tag.gpgsign=false tag -a v2 -m "정상" "$BASE"
T2="$(git -C "$REPO" rev-parse v2)"
hookin "refs/tags/v2 $T2 refs/tags/v2 $ZERO
" origin
check "(p) 태거 이메일 금지어" 1 "$?"
git -C "$REPO" -c user.name=tester -c user.email=t@example.com -c tag.gpgsign=false tag -a v3 -m "정상 메시지" "$BASE"
T3="$(git -C "$REPO" rev-parse v3)"
hookin "refs/tags/v3 $T3 refs/tags/v3 $ZERO
" origin
check "(p) 깨끗한 주석 태그는 통과" 0 "$?"
[ ! -s "$ROOT/err.txt" ]; check "(p) 깨끗한 주석 태그는 조용히 통과" 0 "$?"
git -C "$REPO" -c user.name=tester -c user.email=t@example.com -c tag.gpgsign=false tag -a v4 -m "키 $(fake_secret aws)" "$BASE"
T4="$(git -C "$REPO" rev-parse v4)"
hookin "refs/tags/v4 $T4 refs/tags/v4 $ZERO
" origin
check "(p) 태그 메시지의 비밀키" 1 "$?"
# 태그를 가리키는 태그
git -C "$REPO" -c user.name=tester -c user.email=t@example.com -c tag.gpgsign=false -c advice.nestedTag=false tag -a v5 -m "바깥 $BAD" "$T3"
T5="$(git -C "$REPO" rev-parse v5)"
hookin "refs/tags/v5 $T5 refs/tags/v5 $ZERO
" origin
check "(p) 태그가 태그를 가리켜도 바깥 태그를 봄" 1 "$?"
run_check "(p) --all 은 모든 주석 태그를 봄" 1 --all

# (q) [5] 푸시되는 ref 이름(로컬·원격, 브랜치·태그)
hookin "refs/heads/$BAD-x $BASE refs/heads/ok $ZERO
" origin
check "(q) 로컬 브랜치 이름 금지어(커밋은 원격에 있음)" 1 "$?"
errhas "(q) ref 이름 위치 표시" "ref 이름"
noleak "(q) ref 이름 원문을 출력하지 않음" "$BAD"
hookin "refs/heads/ok $BASE refs/heads/$BAD-x $ZERO
" origin
check "(q) 원격 브랜치 이름 금지어" 1 "$?"
hookin "refs/tags/rel-$BAD $BASE refs/tags/rel-$BAD $ZERO
" origin
check "(q) 태그 이름 금지어" 1 "$?"
hookin "refs/heads/ok $BASE refs/heads/ok $ZERO
" origin
check "(q) 평범한 ref 이름은 통과" 0 "$?"
git -C "$REPO" branch "feat-$BAD" "$BASE"
run_check "(q) --all 은 로컬 ref 이름도 봄" 1 --all
git -C "$REPO" branch -D "feat-$BAD" -q

# (r) [6] 새 비밀키 패턴은 (e) 에서 종류별로 확인했다. 훅 파일 자신이 걸리지 않는지는 (l).

# (s) [7] --all 은 HEAD 에서 닿는 커밋뿐 아니라 모든 브랜치·태그·원격 추적 ref 를 본다
new_remote_repo s1
git -C "$REPO" checkout -q -b other
echo "$BAD" > "$REPO/o.txt"; git -C "$REPO" add o.txt; commit "다른 브랜치"
git -C "$REPO" checkout -q main
run_check "(s) 다른 브랜치의 금지어" 1 --all
new_remote_repo s2
git -C "$REPO" checkout -q -b tmp
echo "$BAD" > "$REPO/o.txt"; git -C "$REPO" add o.txt; commit "태그로만 남는 커밋"
git -C "$REPO" tag lw-tag
git -C "$REPO" checkout -q main; git -C "$REPO" branch -D tmp -q
run_check "(s) 가벼운 태그로만 닿는 커밋의 금지어" 1 --all
new_remote_repo s3
git -C "$REPO" checkout -q -b tmp
echo "$BAD" > "$REPO/o.txt"; git -C "$REPO" add o.txt; commit "추적 ref 로만 남는 커밋"
git -C "$REPO" update-ref refs/remotes/origin/feat HEAD
git -C "$REPO" checkout -q main; git -C "$REPO" branch -D tmp -q
run_check "(s) 원격 추적 ref 로만 닿는 커밋의 금지어" 1 --all
new_remote_repo s4
run_check "(s) 깨끗한 저장소의 --all 은 통과" 0 --all
[ ! -s "$ROOT/err.txt" ]; check "(s) 깨끗한 --all 은 조용히 통과" 0 "$?"

# (t) [8] 모르는 '-' 인자는 사용법을 내고 0 이 아닌 코드로 끝난다
new_repo t
echo "$BAD" > "$REPO/t.txt"; git -C "$REPO" add t.txt; commit "금지어"
run_check "(t) --range=main 오타는 사용법과 코드 2" 2 --range=main
errhas "(t) 사용법 출력" "사용법"
run_check "(t) 모르는 옵션 -x" 2 -x
run_check "(t) 모르는 옵션 --foo" 2 --foo
run_check "(t) --range 값 없음" 2 --range
run_check "(t) --range 값이 옵션처럼 생김" 2 --range --all
run_check "(t) --help 는 사용법을 내고 0" 0 --help

# (u) [9] 패턴 줄 앞뒤 공백·탭·CR 은 지운다, BOM 도 지운다
new_repo u
echo "$BAD" > "$REPO/u.txt"; git -C "$REPO" add u.txt; commit "금지어"
printf '  \t%s \t \r\n' "$BAD" > "$ROOT/ws.txt"
DENYFILE="$ROOT/ws.txt" run_check "(u) 앞뒤 공백이 있는 패턴" 1 --all
printf '\357\273\277%s\n' "$BAD" > "$ROOT/bom.txt"
DENYFILE="$ROOT/bom.txt" run_check "(u) BOM 으로 시작하는 목록의 첫 패턴" 1 --all

# (v) [10, 11] 출력에 원문 대신 위치·종류만. 파일 경로·작성자·메시지 위치도 확인
new_repo v
echo x > "$REPO/v.txt"; git -C "$REPO" add v.txt; commit "메시지 $BAD" "dev@$BAD.example"
run_check "(v) 메시지·작성자 금지어" 1 --all
errhas "(v) 커밋 메시지 위치 표시" "커밋 메시지"
errhas "(v) 작성자·커미터 위치 표시" "작성자·커미터"
noleak "(v) 메시지·작성자 원문을 출력하지 않음" "$BAD"
new_repo v2
echo x > "$REPO/dir-$BAD.txt"; git -C "$REPO" add .; commit "경로"
run_check "(v) 파일 경로 금지어" 1 --all
errhas "(v) 파일 경로 위치 표시" "파일 경로"
noleak "(v) 금지어가 든 경로 이름을 출력하지 않음" "$BAD"
new_repo v3
echo "내용 $BAD" > "$REPO/dir-$BAD.txt"; git -C "$REPO" add .; commit "경로와 내용"
run_check "(v) 이름과 내용 모두 금지어" 1 --all
errhas "(v) 이름이 걸린 파일은 이름을 가린다" "이름이 걸려 가림"
noleak "(v) 이름과 내용 모두 금지어: 원문 없음" "$BAD"
new_repo v4
mkdir -p "$REPO/sub"; echo "$BAD" > "$REPO/sub/deep file.txt"; echo "$BAD" > "$REPO/other.txt"
git -C "$REPO" add .; commit "여러 파일"
run_check "(v) 여러 파일의 추가된 줄" 1 --all
errhas "(v) 공백이 든 파일 이름 표시" "deep file.txt"
errhas "(v) 다른 파일 이름도 표시" "other.txt"

# (w) [12] 강제 푸시에서 원격의 옛 커밋을 로컬이 모르면 막고 fetch 를 안내한다
new_remote_repo w
hookin "refs/heads/main $BASE refs/heads/main 1234567890123456789012345678901234567890
" origin
check "(w) 원격 옛 커밋을 모르면 막힘" 1 "$?"
errhas "(w) git fetch 안내" "git fetch"

# (x) 원격 추적 ref 가 있는 저장소에서 새 브랜치 푸시
new_remote_repo x
echo "$BAD" > "$REPO/old.txt"; git -C "$REPO" add old.txt; commit "이미 원격에 있는 금지어 커밋"
git -C "$REPO" -c core.hooksPath=/dev/null push -q origin main 2> /dev/null; git -C "$REPO" fetch -q origin
echo ok > "$REPO/new.txt"; git -C "$REPO" add new.txt; commit "새 브랜치의 깨끗한 커밋"
NEWC="$(git -C "$REPO" rev-parse HEAD)"
hookin "refs/heads/topic $NEWC refs/heads/topic $ZERO
" origin
check "(x) 새 브랜치: 이미 원격에 있는 커밋은 제외" 0 "$?"
hookin "refs/heads/topic $NEWC refs/heads/topic $ZERO
" 다른원격
check "(x) 대조: 추적 ref 가 없는 원격 이름이면 옛 금지어 커밋도 검사" 1 "$?"
echo "$BAD" > "$REPO/new2.txt"; git -C "$REPO" add new2.txt; commit "새 브랜치의 금지어 커밋"
NEWD="$(git -C "$REPO" rev-parse HEAD)"
hookin "refs/heads/topic $NEWD refs/heads/topic $ZERO
" origin
check "(x) 새 브랜치의 새 금지어 커밋은 막힘" 1 "$?"

# (y) 병합 커밋에서만 들어온 금지어
new_remote_repo y
git -C "$REPO" checkout -q -b m1; echo one > "$REPO/m1.txt"; git -C "$REPO" add m1.txt; commit "m1"
git -C "$REPO" checkout -q -b m2 "$BASE"; echo two > "$REPO/m2.txt"; git -C "$REPO" add m2.txt; commit "m2"
git -C "$REPO" -c user.name=tester -c user.email=t@example.com -c commit.gpgsign=false merge -q --no-commit --no-ff m1 > /dev/null 2>&1
echo "$BAD" > "$REPO/evil.txt"; git -C "$REPO" add evil.txt; commit "병합"
MC="$(git -C "$REPO" rev-parse HEAD)"
hookin "refs/heads/m2 $MC refs/heads/m2 $ZERO
" origin
check "(y) 병합 커밋에서만 들어온 금지어" 1 "$?"
errhas "(y) 병합 커밋의 추가된 줄 표시" "추가된 줄"

# (z) 이름 바꾼 파일, 여러 ref 중 두 번째만, 트레일러, 커미터만, 줄바꿈 파일 이름, 긴 한 줄
new_remote_repo z1
git -C "$REPO" mv base.txt "$BAD.txt"; commit "이름 바꾸기"
hookin "refs/heads/main $(git -C "$REPO" rev-parse HEAD) refs/heads/main $BASE
" origin
check "(z) 금지어 이름으로 바꾼 파일" 1 "$?"
errhas "(z) 파일 경로 위치 표시" "파일 경로"

new_remote_repo z2
git -C "$REPO" checkout -q -b ok1; echo ok > "$REPO/ok.txt"; git -C "$REPO" add ok.txt; commit "ok"
OK1="$(git -C "$REPO" rev-parse HEAD)"
git -C "$REPO" checkout -q -b bad2 "$BASE"; echo "$BAD" > "$REPO/bad.txt"; git -C "$REPO" add bad.txt; commit "bad"
BAD2="$(git -C "$REPO" rev-parse HEAD)"
hookin "refs/heads/ok1 $OK1 refs/heads/ok1 $ZERO
refs/heads/bad2 $BAD2 refs/heads/bad2 $ZERO
" origin
check "(z) 여러 ref 동시 푸시 중 두 번째만 금지어" 1 "$?"
hookin "refs/heads/ok1 $OK1 refs/heads/ok1 $ZERO
refs/heads/bad2 $BAD2 refs/heads/bad2 $ZERO" origin
check "(z) 마지막 줄에 줄바꿈이 없어도 검사" 1 "$?"
hookin "refs/heads/ok1 $OK1 refs/heads/ok1 $ZERO
" origin
check "(z) 대조: 첫 번째 ref 만 푸시하면 통과" 0 "$?"

new_remote_repo z3
echo t > "$REPO/t.txt"; git -C "$REPO" add t.txt
git -C "$REPO" -c user.name=tester -c user.email=t@example.com -c commit.gpgsign=false commit -q -m "제목" -m "Co-Authored-By: x <x@$BAD.example>"
hookin "refs/heads/main $(git -C "$REPO" rev-parse HEAD) refs/heads/main $BASE
" origin
check "(z) 커밋 트레일러의 금지어" 1 "$?"
errhas "(z) 커밋 메시지 위치 표시" "커밋 메시지"

new_remote_repo z4
echo u > "$REPO/u.txt"; git -C "$REPO" add u.txt
GIT_COMMITTER_EMAIL="c@$BAD.example" commit "커미터만 다름"
hookin "refs/heads/main $(git -C "$REPO" rev-parse HEAD) refs/heads/main $BASE
" origin
check "(z) 커미터만 금지어" 1 "$?"
errhas "(z) 작성자·커미터 위치 표시" "작성자·커미터"

new_remote_repo z5
NLNAME="$(printf 'a\n%s.txt' "$BAD")"
echo x > "$REPO/$NLNAME"; git -C "$REPO" add -A; commit "줄바꿈 이름"
hookin "refs/heads/main $(git -C "$REPO" rev-parse HEAD) refs/heads/main $BASE
" origin
check "(z) 줄바꿈이 든 파일 이름의 금지어" 1 "$?"
noleak "(z) 줄바꿈 파일 이름 원문을 출력하지 않음" "$BAD"

new_remote_repo z6
{ head -c 2000000 /dev/zero | tr '\0' a; printf ' %s\n' "$BAD"; } > "$REPO/long.txt"
git -C "$REPO" add long.txt; commit "아주 긴 한 줄"
hookin "refs/heads/main $(git -C "$REPO" rev-parse HEAD) refs/heads/main $BASE
" origin
check "(z) 2MB 한 줄 끝의 금지어" 1 "$?"
noleak "(z) 긴 줄 원문을 출력하지 않음" "aaaaaaaaaa"

# 병합 커밋에 든 태그(mergetag): git 이 만든 것과 같은 꼴의 헤더를 직접 넣은 커밋으로 시험한다
new_remote_repo z7
git -C "$REPO" checkout -q -b side; echo s > "$REPO/s.txt"; git -C "$REPO" add s.txt; commit "side"
SIDE="$(git -C "$REPO" rev-parse HEAD)"
git -C "$REPO" checkout -q main; echo m > "$REPO/m.txt"; git -C "$REPO" add m.txt; commit "main 쪽"
MAIN2="$(git -C "$REPO" rev-parse HEAD)"
TREE="$(git -C "$REPO" rev-parse 'HEAD^{tree}')"
MT="$(printf 'tree %s\nparent %s\nparent %s\nauthor t <t@example.com> 1700000000 +0900\ncommitter t <t@example.com> 1700000000 +0900\nmergetag object %s\n type commit\n tag side-tag\n tagger t <t@example.com> 1700000000 +0900\n \n 태그 %s\n\n병합\n' \
  "$TREE" "$MAIN2" "$SIDE" "$SIDE" "$BAD" | git -C "$REPO" hash-object -t commit -w --stdin)"
run_check "(z) 병합 커밋의 mergetag 에 든 금지어" 1 --range "$BASE..$MT"
errhas "(z) mergetag 위치 표시" "병합 커밋의 태그"
noleak "(z) mergetag 원문을 출력하지 않음" "$BAD"

# UTF-16 BOM 으로 시작하는 파일이 같은 커밋의 다른 파일 검사를 가리지 못한다(맥 grep 의 BOM 오판 방지)
new_repo bom
printf '\377\376a\0b\0\n' > "$REPO/a-utf16.txt"
echo "$BAD" > "$REPO/z-plain.txt"
git -C "$REPO" add .; commit "UTF-16 파일과 일반 파일"
run_check "(z) UTF-16 BOM 파일이 있어도 다른 파일의 금지어를 봄" 1 --all
new_repo bom2
printf '\377\376'> "$REPO/u16.txt"; printf '%s\n' "$BAD" | iconv -f UTF-8 -t UTF-16LE >> "$REPO/u16.txt"
git -C "$REPO" add .; commit "UTF-16LE 로 저장된 금지어"
run_check "(z) UTF-16LE 로 저장된 금지어" 1 --all
errhas "(z) NUL 제거 후 대조 표시" "NUL 제거 후"
new_repo bom3
printf '\377\376%s\n' "$BAD" > "$REPO/msgbom.txt"; git -C "$REPO" add .
git -C "$REPO" -c user.name=tester -c user.email=t@example.com -c commit.gpgsign=false commit -q -m "$(printf '\377\376%s' "$BAD")" 2> /dev/null || commit "대체 메시지 $BAD"
run_check "(z) 내용이 BOM 바이트로 시작해도 금지어를 봄" 1 --all

# 한글 문자 범위 같은 로캘 의존 정규식도 오류 없이 동작한다
new_repo ko
printf '비밀 프로젝트-푸른하늘\n' > "$REPO/ko.txt"; git -C "$REPO" add ko.txt; commit "한글 내용"
printf '푸른[가-힣]+\n' > "$ROOT/ko-list.txt"
DENYFILE="$ROOT/ko-list.txt" run_check "(z) 한글 문자 범위 패턴이 한글 내용에 걸림" 1 --all
printf '푸른하늘\n' > "$ROOT/ko-list2.txt"
DENYFILE="$ROOT/ko-list2.txt" run_check "(z) 한글 리터럴 패턴이 한글 내용에 걸림" 1 --all
printf '없는말[가-힣]+\n' > "$ROOT/ko-list3.txt"
DENYFILE="$ROOT/ko-list3.txt" run_check "(z) 한글 범위 패턴, 걸릴 것이 없으면 통과" 0 --all

# 평범한 푸시는 조용히 통과
new_remote_repo ok1
echo fine > "$REPO/fine.txt"; git -C "$REPO" add fine.txt; commit "평범한 변경"
hookin "refs/heads/main $(git -C "$REPO" rev-parse HEAD) refs/heads/main $BASE
" origin
check "(ok) 평범한 푸시는 통과" 0 "$?"
[ ! -s "$ROOT/err.txt" ]; check "(ok) 평범한 푸시는 오류 출력 없이 조용히 통과" 0 "$?"

echo "---"
echo "통과 $PASS, 실패 $FAILN"
[ "$FAILN" -eq 0 ]
