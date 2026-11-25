'use strict';
/* heka 관찰 화면. 빌드 없이 돌아가는 바닐라 JS 다.
   데이터가 섞인 글은 모두 textContent 로만 넣는다(innerHTML 을 쓰지 않는다). */

// ---------- 구조 정의: 코드가 바뀌면 이 상수만 고친다 ----------
// 순서도의 갈래(순간)마다 노드(칸 col, 갈래 안 높이 y)와 간선을 적는다. 세션 카드의 작은 띠도 같은 노드(t, drops, k)에서 나온다.
//  P=처리(네모) D=판단(마름모) O=끝 기호(알약, 나가는 것) E=끝 표시(탈락·건너뜀) X=기록만 하는 점선 네모
//  exit: 판단의 아래쪽 출구 {label, lines(m), n(m)}, pass: 오른쪽 출구의 이름, note(m): 노드 아래 작은 숫자,
//  bus: 원장으로 모이는 선에 잇는가, t/drops/k: 카드 띠의 이름, 탈락 단계, 판정(judge)·확인(confirm)·검사(check)·사전(dict)
const n0 = v => (Number.isFinite(v) ? v : 0).toLocaleString('ko-KR');
const dr = (m, keys) => keys.reduce((a, k) => a + ((m.dropped || {})[k] || 0), 0);
const ms = v => `${n0(v)}ms`;
const P = (id, col, y, lines, o) => ({ kind: 'P', id, col, y, lines, ...o });
const D = (id, col, y, lines, o) => ({ kind: 'D', id, col, y, lines, ...o });
const O = (id, col, y, lines, o) => ({ kind: 'O', id, col, y, lines, bus: true, ...o });
const dropExit = (label, keys, extra) => ({ label, n: m => dr(m, keys), lines: m => [`탈락 ${n0(dr(m, keys))}건`, ...(extra ? [extra(m)] : [])] });
const SC = (id, col, y, note) => D(id, col, y, ['범위·자격', '통과?'], { t: '범위·자격', drops: ['범위', '자격'], pass: '예', note,
  d: '작업 위치로 정한 범위에 속하고, 기한이 지나지 않았고, 출처·확인 여부가 맞는가',
  exit: dropExit('아니오', ['범위', '자격'], m => `범위 ${n0(dr(m, ['범위']))}·자격 ${n0(dr(m, ['자격']))}`) });
const DEDUP = (id, col, y, note) => D(id, col, y, ['처음', '넣나?'], { t: '중복 제거', drops: ['중복'], pass: '예', note,
  d: '같은 항목은 세션에 한 번만 넣는다(서브에이전트는 따로 센다)', exit: dropExit('아니오', ['중복']) });
const JUDGE = (id, col, y) => D(id, col, y, ['직접', '답 되나?'], { t: '모델 판정', k: 'judge', drops: ['판정'], pass: '예',
  d: '작은 언어 모델이 "이 항목이 프롬프트가 묻거나 시키는 것에 직접 답하는가"를 한 건씩 판정한다',
  exit: dropExit('아니오', ['판정']),
  note: m => [`판정 ${n0(m.judges.n)}번·넣으라 ${n0(m.judges.inject)}번`, `시간 초과 ${n0(m.judges.timeout)}·오류 ${n0(m.judges.error)}`,
    `중앙 ${ms(m.judges.ms.median)}`, `95% ${ms(m.judges.ms.p95)}`] });
const PICK = (id, col, y) => ({ kind: 'X', id, col, y, w: 236, h: 44, show: m => m.pick.n > 0, bus: true,
  lines: m => ['모델 후보 (기록만 함, 주입에 안 쓰임)', `${n0(m.pick.n)}번 · 고른 ${n0(m.pick.picked)}건 중 판정 통과 ${n0(m.pick.picked_inject)}건`],
  d: '모델이 항목 목록에서 후보를 고른 기록. 원장에만 남고 무엇을 넣을지에는 쓰이지 않는다' });
const outLines = m => ['주입', `${n0(m.injected)}건`, `토큰 약 ${n0(m.tokens)}`];
const silent = m => `침묵 ${n0(m.rows_silent)}번`;

const LANES = [
  { id: 'session_start', short: '세션 시작', title: '세션 시작·압축 직후', event: 'SessionStart', h: 200,
    nodes: [
      P('a', 2, 66, ['작업 위치로', '범위 정하기'], { t: '범위 정하기', d: '세션을 연 작업 위치에서 항목의 범위를 정한다. 알 수 없으면 전역 항목만 본다' }),
      SC('b', 3, 66),
      D('c', 4, 66, ['시작 항목', '일치?'], { t: '시작 항목 일치', drops: ['일치'], pass: '예', exit: dropExit('아니오', ['일치']),
        d: 'when.moment 에 세션 시작이 적힌 항목인가. 동작 조건이 걸린 필수 항목은 여기서 넣지 않는다' }),
      DEDUP('d', 5, 66, m => ['압축·clear 뒤에는', '다시 넣음']),
      O('out', 8, 66, outLines, { note: m => [silent(m)], d: '시작 항목의 주입문을 넣는다' }),
    ],
    edges: [['a', 'b'], ['b', 'c'], ['c', 'd'], ['d', 'out']] },
  { id: 'prompt', short: '프롬프트', title: '사용자 프롬프트', event: 'UserPromptSubmit', h: 432,
    nodes: [
      D('tm', 2, 141, ['사용자가 쓴', '프롬프트?'], { chip: false, pass: '예', d: '도구가 만들어 넣은 프롬프트(하위 에이전트 완료 알림 등)는 두 갈래를 모두 건너뛴다',
        exit: { label: '아니오', n: m => m.tool_made, lines: m => [`건너뜀 ${n0(m.tool_made)}번`], skip: true } }),
      SC('sc', 3, 141, m => ['(두 갈래 공통,', '레인 합계)']),
      P('a1', 4, 66, ['동작 낱말', '사전으로', '시킨 동작'], { t: '동작 낱말 사전', k: 'dict', d: '"푸시해 줘" → git.push 처럼 시킨 동작을 알아채고, 부정·질문·조건이 섞이면 거절로 센다',
        note: m => [`시킨 동작 ${n0(m.asked)}건`, `거절 ${n0(m.refused)}건`] }),
      D('a2', 5, 66, ['걸린 필수', '항목?'], { t: '조건표(필수 항목)', pass: '예', d: '시킨 동작에 조건이 걸린 필수 항목을 조건표에서 찾는다. 찾으면 미리 넣고 허용을 기록한다',
        exit: { label: '아니오', lines: () => ['해당 없음'], skip: true } }),
      P('b1', 4, 216, ['조각내기·', '후보 잡기'], { t: '조각내기·후보 잡기', d: '어절과 두 글자 조각으로 나눠 주제어와 낱말 겹침 점수로 후보를 잡는다' }),
      D('b2', 5, 216, ['후보', '있나?'], { t: '일치', drops: ['일치'], pass: '예', d: '주제어가 프롬프트에 들었거나 낱말 겹침 점수가 높은 항목이 있는가',
        exit: dropExit('아니오', ['일치'], () => '(두 갈래 합계)') }),
      JUDGE('b3', 6, 216),
      DEDUP('dd', 7, 141),
      O('out', 8, 141, outLines, { note: m => ['필수 주입·허용 기록', '참고 주입 또는 침묵', silent(m)], d: '필수 항목과 참고 항목(최대 3건)의 주입문을 넣는다' }),
      PICK('pk', 4, 400),
    ],
    edges: [['tm', 'sc'], ['sc', 'a1'], ['sc', 'b1'], ['a1', 'a2'], ['b1', 'b2'], ['b2', 'b3'], ['a2', 'dd'], ['b3', 'dd'], ['dd', 'out'],
      ['b1', 'pk', { down: true, dot: true, label: '기록만' }]] },
  { id: 'tool', short: '도구 호출', title: '도구 호출 직전', event: 'PreToolUse', h: 218,
    nodes: [
      P('a', 2, 66, ['도구 호출을', '동작 이름으로', '해석'], { t: '동작 해석', d: '도구 호출을 동작 이름으로 바꾼다. 위임 도구(Agent·Task)면 위임 글을 세션 상태에 적어 둔다',
        note: m => ['위임 도구면', '위임 글 저장'] }),
      SC('b', 3, 66),
      D('c', 4, 66, ['조건 일치', '항목?'], { t: '조건표 일치', drops: ['일치'], pass: '예', d: '그 동작·경로·저장소에 조건이 맞는 항목이 조건표에 있는가',
        exit: dropExit('아니오', ['일치']) }),
      DEDUP('d', 5, 66, m => ['확인·검사 붙은', '항목은 다시 판정']),
      D('e', 6, 66, ['확인 불필요', '또는 허용?'], { t: '확인', k: 'confirm', pass: '예', d: '확인을 요구하는 항목이고 사용자가 그 동작을 시킨 허용이 없으면 사용자에게 확인을 요구한다',
        exit: { label: '아니오', n: m => m.confirm, lines: m => ['확인 요구', `${n0(m.confirm)}건`] },
        note: m => [`허용 사용 ${n0(m.grants_used)}번`] }),
      D('f', 7, 66, ['검사에', '맞나?'], { t: '검사', k: 'check', pass: '예', d: '결과물(명령·커밋 메시지·PR 본문)이 형식에 맞는가. 안 맞으면 막고 사유를 모델에게 돌려준다',
        exit: { label: '아니오', n: m => m.checks.blocked, lines: m => ['막고 사유', `돌려줌 ${n0(m.checks.blocked)}번`] },
        note: m => [`검사 통과 ${n0(m.checks.pass)}`, `걸림 ${n0(m.checks.fail)}·건너뜀 ${n0(m.checks.skip)}`] }),
      O('out', 8, 66, outLines, { note: m => ['주입문을 넣고', '진행', silent(m)], d: '조건이 맞는 항목의 주입문을 넣고 도구 호출을 진행시킨다' }),
    ],
    edges: [['a', 'b'], ['b', 'c'], ['c', 'd'], ['d', 'e'], ['e', 'f'], ['f', 'out']] },
  { id: 'subagent_start', short: '서브에이전트', title: '서브에이전트 시작', event: 'SubagentStart', h: 432,
    nodes: [
      P('a', 2, 141, ['세션 상태에서', '위임 글', '꺼내기'], { t: '위임 글 꺼내기', d: '위임 호출 직전에 적어 둔 같은 종류의 가장 오래된 글을 꺼낸다. 없으면 시작 항목만 고르고 참고 항목은 넣지 않는다' }),
      SC('b', 3, 141, m => ['(두 갈래 공통,', '레인 합계)']),
      D('c', 4, 66, ['시작 항목', '일치?'], { t: '시작 항목 일치', drops: ['일치'], pass: '예', exit: dropExit('아니오', ['일치']),
        d: 'when.moment 가 서브에이전트 시작이거나 when.agent 가 그 종류인 항목인가' }),
      P('d', 4, 216, ['후보 잡기', '(위임 글)'], { t: '후보 잡기', d: '위임 글로 주제어·낱말 겹침 점수를 매겨 후보를 잡는다(프롬프트와 같은 방식)' }),
      JUDGE('e', 5, 216),
      DEDUP('f', 6, 141),
      O('out', 8, 141, outLines, { note: m => [silent(m)], d: '시작 항목과 판정을 통과한 참고 항목의 주입문을 넣는다' }),
      PICK('pk', 4, 400),
    ],
    edges: [['a', 'b'], ['b', 'c'], ['b', 'd'], ['d', 'e'], ['c', 'f'], ['e', 'f'], ['f', 'out'],
      ['d', 'pk', { down: true, dot: true, label: '기록만' }]] },
  { id: 'stop', short: '턴 종료', title: '턴 종료', event: 'Stop', h: 276,
    nodes: [
      SC('a', 2, 66),
      D('b', 3, 66, ['형식에', '맞나?'], { t: '마지막 응답 검사', k: 'check', pass: '예', d: '마지막 응답이 검사의 형식에 맞는가(항목의 when 은 repo·agent 만 본다)',
        exit: { label: '아니오', lines: () => [], none: true }, noteRight: m => [`검사 통과 ${n0(m.checks.pass)}`, `걸림 ${n0(m.checks.fail)}·건너뜀 ${n0(m.checks.skip)}`] }),
      D('c', 3, 176, ['되돌림', '한도 안?'], { t: '되돌림 한도', pass: '예', d: '되돌림은 세션당 3회까지, 같은 항목이 연속으로 걸리면 2번까지만 한다',
        exit: { label: '아니오', lines: () => ['원장에만 남김'], skip: true, bus: true } }),
      O('back', 4, 176, m => ['되돌림', `${n0(m.checks.blocked)}번`], { d: '사유를 돌려주고 모델을 한 번 더 돌게 한다' }),
      O('out', 8, 66, () => ['통과', '(항목을', '넣지 않음)'], { d: '턴 종료는 항목을 넣지 않는다. 걸렸을 때만 사유를 돌려준다', dimWhen: m => !m.rows }),
    ],
    edges: [['a', 'b'], ['b', 'out'], ['b', 'c', { down: true, label: '아니오' }], ['c', 'back']] },
];
const LANE_BY = Object.fromEntries(LANES.map(l => [l.id, l]));
// 카드의 작은 띠에 쓰는 단계(순서도의 처리·판단 노드). chip:false 는 뺀다.
const flowNodes = lane => lane.nodes.filter(n => (n.kind === 'P' || n.kind === 'D') && n.chip !== false && n.t);
// 훅이 아닌 사건(모델·사람이 부르는 것)
const EVENTS = [['find', '조회', 'heka find'], ['add', '등록', 'heka add'], ['register_judge', '등록 판정', 'heka judge-add'],
  ['flag', '표시', 'flag'], ['retire', '은퇴', 'retire'], ['review', '검토 결정', 'heka review apply'], ['review_notify', '검토 알림', 'heka review --notify']];
const STAGES_ORDER = ['범위', '자격', '일치', '중복', '예산', '판정'];

// ---------- 작은 도구 ----------
function el(tag, props, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') e.className = v;
    else if (k === 'text') e.textContent = v;
    else if (k === 'on') for (const [ev, fn] of Object.entries(v)) e.addEventListener(ev, fn);
    else if (k === 'mc') e.style.setProperty('--mc', `var(--m-${v})`);
    else e.setAttribute(k, v === true ? '' : v);
  }
  for (const c of kids.flat(Infinity)) if (c != null && c !== false) e.append(c.nodeType ? c : document.createTextNode(String(c)));
  return e;
}
const arr = v => (Array.isArray(v) ? v : v ? [v] : []);
const clip = (s, n) => { s = String(s ?? '').replace(/\s+/g, ' ').trim(); return s.length > n ? s.slice(0, n) + '…' : s; };
const short8 = s => String(s || '').slice(0, 8);
function fmtTime(ts) {
  const d = new Date(ts);
  if (!ts || isNaN(d)) return ts || '';
  return d.toLocaleString('ko-KR', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
}
const link = (href, text, props) => el('a', { href, text, ...props });
const itemLink = id => link('#/items/' + encodeURIComponent(id), id, { class: 'mono' });

async function api(path) {
  let res;
  try { res = await fetch(path, { headers: { Accept: 'application/json' } }); }
  catch (e) { throw new Error('서버에 연결하지 못했습니다: ' + e.message); }
  let body = null;
  try { body = await res.json(); } catch (_) { /* 본문이 JSON 이 아님 */ }
  if (!res.ok) throw new Error((body && body.error) || `요청 실패 (${res.status})`);
  return body;
}

// ---------- 기간 ----------
const RANGES = { today: 0, '7d': 6, '30d': 29, all: null };
let rangeKey = '7d';
try { rangeKey = sessionStorage.getItem('heka.range') || '7d'; } catch (_) { /* 저장소 없음 */ }
if (!(rangeKey in RANGES)) rangeKey = '7d';
function rangeParams() {
  const back = RANGES[rangeKey];
  if (back == null) return { since: '', until: '' };
  const now = new Date();
  const day = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate() - back));
  return { since: day.toISOString().slice(0, 10), until: now.toISOString().slice(0, 10) };
}
function rangeQuery(extra) {
  const { since, until } = rangeParams();
  const q = new URLSearchParams(extra || {});
  if (since) q.set('since', since);
  if (until) q.set('until', until);
  const s = q.toString();
  return s ? '?' + s : '';
}
function rangeLabel() {
  const { since, until } = rangeParams();
  return since ? `${since} ~ ${until} (UTC)` : '전체 기간';
}

// ---------- 머리 ----------
async function renderHead() {
  try {
    const m = await api('/api/meta');
    document.getElementById('h-home').textContent = m.home;
    const box = document.getElementById('h-stats');
    box.replaceChildren(...[
      el('span', { class: 'st' }, '항목 ', el('b', { text: n0(m.items.total) }),
        el('span', { class: 'muted', text: `건 (넣는 것 ${n0(m.items.push)} · 조회 전용 ${n0(m.items.find_only)} · 필수 ${n0(m.items.must)} · 검토 대기 ${n0(m.items.review)})` })),
      el('span', { class: 'st' }, '원장 ', el('b', { text: n0(m.ledger.rows) }), '줄',
        m.ledger.first_ts ? el('span', { class: 'muted', text: ` (${fmtTime(m.ledger.first_ts)} ~ ${fmtTime(m.ledger.last_ts)})` }) : ''),
      m.errors > 0 ? el('span', { class: 'badge-warn', text: `훅 오류 ${n0(m.errors)}건` }) : el('span', { class: 'st muted', text: '훅 오류 0건' }),
      m.shadow ? el('span', { class: 'badge-shadow', text: '그림자 방식: 판단만 기록하고 내보내지 않음' }) : null,
      el('span', { class: 'st muted', text: m.version })].filter(Boolean));
  } catch (e) {
    document.getElementById('h-stats').replaceChildren(el('span', { class: 'badge-warn', text: '머리 정보를 읽지 못했습니다: ' + e.message }));
  }
}

function laneDetail(lane, m) {
  const d = el('div', { class: 'lane-detail' });
  const tab = rows => el('table', {}, rows.map(r => el('tr', {}, el('th', { text: r[0] }), el('td', { text: r[1] }))));
  d.append(el('h3', { text: '많이 넣은 항목' }));
  if (m.top_items.length) {
    d.append(el('ol', {}, m.top_items.map(t => el('li', {}, itemLink(t.id), ` ${n0(t.n)}번`))));
  } else d.append(el('p', { class: 'muted', text: '이 기간에 넣은 항목이 없습니다.' }));
  const rowsInfo = [
    ['처리 시간', `중앙 ${ms(m.duration_ms.median)} · 95% ${ms(m.duration_ms.p95)} · 최대 ${ms(m.duration_ms.max)}`],
    ['탈락(건)', STAGES_ORDER.map(s => `${s} ${n0((m.dropped || {})[s])}`).join(' · ')],
  ];
  if (m.judges.n) rowsInfo.push(['판정', `${n0(m.judges.n)}번 (넣으라 ${n0(m.judges.inject)} · 오류 ${n0(m.judges.error)} · 시간 초과 ${n0(m.judges.timeout)}) 시간 중앙 ${ms(m.judges.ms.median)} · 95% ${ms(m.judges.ms.p95)} · 최대 ${ms(m.judges.ms.max)}`]);
  const c = m.checks;
  if (c.pass + c.fail + c.skip) rowsInfo.push(['검사', `통과 ${n0(c.pass)} · 걸림 ${n0(c.fail)} · 건너뜀 ${n0(c.skip)} · 막음 ${n0(c.blocked)}`]);
  if (m.confirm || m.grants_used) rowsInfo.push(['확인', `요구 ${n0(m.confirm)}건 · 허용 사용 ${n0(m.grants_used)}번`]);
  if (m.asked || m.refused) rowsInfo.push(['시킨 동작', `시킨 ${n0(m.asked)}건 · 거절 ${n0(m.refused)}건`]);
  d.append(tab(rowsInfo), el('p', {}, link('#/sessions', '이 순간의 줄 보기 (세션 화면)'),
    el('span', { class: 'muted', text: ` — ${lane.short} 줄은 세션 상세의 카드에서 같은 색으로 보입니다.` })));
  return d;
}

function hashQuery() {
  const h = location.hash.slice(1), i = h.indexOf('?');
  return new URLSearchParams(i < 0 ? '' : h.slice(i + 1));
}
function setOpenInUrl(set) {
  const path = location.hash.slice(1).split('?')[0] || '/';
  const q = set.size ? '?open=' + [...set].join(',') : '';
  history.replaceState(null, '', '#' + path + q);
}

// ---------- 구조도: 좌우로 진행하는 순서도(SVG) ----------
const SVGNS = 'http://www.w3.org/2000/svg';
function sv(tag, attrs, ...kids) {
  const e = document.createElementNS(SVGNS, tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === 'text') e.textContent = v; else e.setAttribute(k, v);
  }
  for (const c of kids.flat(Infinity)) if (c) e.append(c);
  return e;
}
// 칸 배치: 0=시작, 1=어떤 이벤트인가, 2~8=갈래 안, 9=원장 기록, 10=끝
const COL_W = [84, 106, 106, 106, 106, 106, 106, 106, 106, 106, 44], GAP = 28, MARGIN = 10, NH = 50, DH = 74;
const colLeft = c => MARGIN + COL_W.slice(0, c).reduce((a, w) => a + w + GAP, 0);
const FLOW_W = colLeft(10) + COL_W[10] + MARGIN;
const BUS_X = colLeft(8) + COL_W[8] + 14, EBUS_X = colLeft(1) + COL_W[1] + 14;
const LH = 12.5; // 글줄 높이

// 글줄들을 (cx, cy) 가운데에 놓는다
function textLines(lines, cx, cy, cls) {
  const t = sv('text', { class: cls || 't', 'text-anchor': 'middle' });
  const y0 = cy - ((lines.length - 1) * LH) / 2 + 3.5;
  lines.forEach((s, i) => t.append(sv('tspan', { x: cx, y: y0 + i * LH, text: s })));
  return t;
}
function geom(l, cy, w, h) { return { l, r: l + w, cx: l + w / 2, cy, t: cy - h / 2, b: cy + h / 2, w, h }; }

function nodeGeom(n, top, m) {
  const w = n.w || COL_W[n.col];
  const lines = typeof n.lines === 'function' ? n.lines(m) : n.lines;
  const h = n.h || (n.kind === 'D' ? DH : n.kind === 'O' ? Math.max(NH, lines.length * LH + 16) : NH);
  return { ...geom(colLeft(n.col), top + n.y, w, h), lines };
}

// 갈래 하나를 그린다. 반환: 원장으로 모이는 y 들과 첫 노드 위치
function drawLane(lane, m, top, open, onToggle) {
  const g = sv('g', { class: 'lane-g' + (m.rows ? '' : ' dim'), style: `--mc: var(--m-${lane.id})`, role: 'group' },
    sv('title', { text: `${lane.title} (${lane.event}) 순서도` }));
  const x0 = colLeft(2) - 8, x1 = colLeft(8) + COL_W[8] + 8;
  const body = sv('g', { transform: `translate(0,0)` });
  g.append(sv('rect', { class: 'band', x: x0, y: top, width: x1 - x0, height: lane.h, rx: 6 }), body);
  // 머리줄(누르면 상세가 펼쳐진다)
  const parts = [`${n0(m.rows)}번`, `넣은 것 ${n0(m.rows_injected)}번`, `침묵 ${n0(m.rows_silent)}번`];
  if (m.shadow_rows) parts.push(`그림자 ${n0(m.shadow_rows)}번`);
  const caret = sv('tspan', { text: open ? '▼ ' : '▶ ' });
  const hd = sv('g', { class: 'lane-hd', tabindex: '0', role: 'button', 'aria-expanded': String(open) },
    sv('rect', { class: 'hd-bg', x: x0 + 2, y: top + 2, width: x1 - x0 - 4, height: 22, rx: 4 }),
    sv('text', { class: 'hd-t', x: x0 + 10, y: top + 17 }, caret, sv('tspan', { class: 'hd-title', text: lane.title }),
      sv('tspan', { class: 'hd-ev', dx: 8, text: lane.event }), sv('tspan', { dx: 14, text: parts.join(' · ') })),
    sv('text', { class: 'hd-time', x: x1 - 10, y: top + 17, 'text-anchor': 'end', text: `처리 시간 중앙 ${ms(m.duration_ms.median)} · 95% ${ms(m.duration_ms.p95)}` }));
  const toggle = () => { onToggle(); const o = hd.getAttribute('aria-expanded') !== 'true'; hd.setAttribute('aria-expanded', String(o)); caret.textContent = o ? '▼ ' : '▶ '; };
  hd.addEventListener('click', toggle);
  hd.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggle(); } });
  hd.append(sv('title', { text: '누르면 이 순간의 자세한 숫자가 아래에 펼쳐집니다' }));
  g.append(hd);

  const nodes = {}, shown = lane.nodes.filter(n => !n.show || n.show(m));
  for (const n of shown) nodes[n.id] = nodeGeom(n, top, m);
  const busYs = [];
  const labelDone = new Set();
  const edgeG = sv('g', {}), nodeG = sv('g', {});
  g.append(edgeG, nodeG);
  const line = (d, o) => edgeG.append(sv('path', { class: 'edge' + (o && o.dot ? ' dot' : ''), d, 'marker-end': o && o.noArrow ? null : 'url(#arr)' }));
  const elabel = (x, y, s, anchor) => edgeG.append(sv('text', { class: 'el', x, y, 'text-anchor': anchor || 'middle', text: s }));

  // 간선
  for (const [a, b, o = {}] of lane.edges) {
    const A = nodes[a], B = nodes[b];
    if (!A || !B) continue;
    const an = lane.nodes.find(n => n.id === a);
    if (o.down) {
      line(`M${A.cx},${A.b} V${B.t}`, o);
      if (o.label) elabel(A.cx + 6, (A.b + B.t) / 2 + 3, o.label, 'start');
      continue;
    }
    if (Math.abs(A.cy - B.cy) < 1) {
      line(`M${A.r},${A.cy} H${B.l}`, o);
      if (an.pass && an.kind === 'D' && !labelDone.has(a)) { labelDone.add(a); const gp = B.l - A.r; elabel(gp > 60 ? A.r + 20 : A.r + gp / 2, A.cy - 5, an.pass); }
    } else {
      const mid = B.l - 14;
      line(`M${A.r},${A.cy} H${mid} V${B.cy} H${B.l}`, o);
      if (an.pass && an.kind === 'D' && !labelDone.has(a) && mid - A.r >= 60) { labelDone.add(a); elabel(A.r + 20, A.cy - 5, an.pass); }
    }
  }

  // 노드와 출구
  for (const n of shown) {
    const G = nodes[n.id];
    const cls = n.kind === 'D' ? 'nd dec' : n.kind === 'O' ? 'nd out' : n.kind === 'X' ? 'nd rec dash' : 'nd proc';
    const dimmed = (n.kind === 'O' && (n.dimWhen ? n.dimWhen(m) : m.injected === 0 && n.id !== 'back')) || (n.id === 'back' && !m.checks.blocked);
    const ng = sv('g', { class: 'nodeg' + (dimmed ? ' dim' : '') });
    if (n.d) ng.append(sv('title', { text: `${n.t || n.id}: ${n.d}` }));
    if (n.kind === 'D') ng.append(sv('path', { class: cls, d: `M${G.cx},${G.t} L${G.r},${G.cy} L${G.cx},${G.b} L${G.l},${G.cy} Z` }));
    else ng.append(sv('rect', { class: cls, x: G.l, y: G.t, width: G.w, height: G.h, rx: n.kind === 'O' ? 18 : n.kind === 'X' ? 5 : 4 }));
    ng.append(textLines(G.lines, n.kind === 'X' ? G.cx : G.cx, G.cy, n.kind === 'D' ? 't dt' : 't'));
    nodeG.append(ng);
    if (n.bus) busYs.push(G.cy);
    // 아래 출구
    let bottom = G.b;
    if (n.exit && !n.exit.none) {
      const lines = n.exit.lines(m), ph = lines.length * 13 + 11;
      const pt = G.b + 28, P = geom(G.l, pt + ph / 2, G.w, ph);
      const zero = n.exit.n ? n.exit.n(m) === 0 : false;
      const eg = sv('g', { class: 'exitg' + (zero ? ' dim' : '') });
      eg.append(sv('path', { class: 'edge', d: `M${G.cx},${G.b} V${P.t}`, 'marker-end': 'url(#arr)' }),
        sv('text', { class: 'el', x: G.cx + 6, y: G.b + 18, 'text-anchor': 'start', text: n.exit.label }),
        sv('rect', { class: 'pill ' + (n.exit.skip ? 'skip' : 'drop'), x: P.l, y: P.t, width: P.w, height: P.h, rx: P.h / 2 > 14 ? 14 : P.h / 2 }),
        textLines(lines, P.cx, P.cy, 't pt'));
      edgeG.append(eg);
      if (n.exit.bus) { edgeG.append(sv('path', { class: 'edge', d: `M${P.r},${P.cy} H${BUS_X}` })); busYs.push(P.cy); }
      bottom = P.b;
    }
    if (n.note) {
      const lines = n.note(m);
      const joined = lines.join(''), zero = /\d/.test(joined) && !/[1-9]/.test(joined);
      nodeG.append(textLines(lines, G.cx, bottom + 8 + (lines.length * LH) / 2, 't tm' + (zero ? ' dim' : '')));
    }
    if (n.noteRight) {
      const lines = n.noteRight(m);
      const t = textLines(lines, G.cx + 46, G.b + 12 + (lines.length * LH) / 2, 't tm');
      t.setAttribute('text-anchor', 'start'); t.querySelectorAll('tspan').forEach(s => s.setAttribute('x', G.cx + 46));
      nodeG.append(t);
    }
    if (n.bus) edgeG.append(sv('path', { class: 'edge', d: `M${G.r},${G.cy} H${BUS_X}` }));
  }
  // 기록만 하는 점선 노드도 원장으로 잇는다(점선)
  for (const n of shown) if (n.kind === 'X') {
    const G = nodes[n.id];
    edgeG.append(sv('path', { class: 'edge dot', d: `M${G.r},${G.cy} H${BUS_X}` }));
  }
  const first = lane.nodes[0];
  return { g, firstY: top + first.y, firstL: colLeft(first.col), busYs };
}

function legendEl() {
  const g = sv('g', { class: 'legend' });
  let x = MARGIN;
  const item = (shape, label, w) => { g.append(shape(x), sv('text', { class: 't tm', x: x + 46, y: 15, text: label })); x += w; };
  item(x0 => sv('rect', { class: 'nd out lg', x: x0, y: 4, width: 38, height: 18, rx: 9 }), '시작·끝(나가는 것)', 130);
  item(x0 => sv('rect', { class: 'nd proc lg', x: x0, y: 4, width: 38, height: 18, rx: 3 }), '처리', 80);
  item(x0 => sv('path', { class: 'nd dec lg', d: `M${x0 + 19},2 L${x0 + 38},13 L${x0 + 19},24 L${x0},13 Z` }), '판단 (위쪽은 오른쪽으로, 아래쪽은 탈락·건너뜀)', 300);
  item(x0 => sv('rect', { class: 'pill drop lg', x: x0, y: 5, width: 38, height: 16, rx: 8 }), '탈락 끝 표시', 120);
  item(x0 => sv('rect', { class: 'pill skip lg', x: x0, y: 5, width: 38, height: 16, rx: 8 }), '건너뜀·침묵·남김', 130);
  item(x0 => sv('rect', { class: 'nd rec dash lg', x: x0, y: 4, width: 38, height: 18, rx: 3 }), '기록만 하고 주입에는 안 쓰임', 200);
  g.append(sv('text', { class: 't tm', x: x + 6, y: 15, text: '"번"은 훅이 불린 횟수, "건"은 항목 수' }));
  return g;
}

function overviewSvg(ov, open, onToggle) {
  const byId = Object.fromEntries(ov.moments.map(m => [m.moment, m]));
  const LEG = 30, GAP_L = 12;
  let top = LEG;
  const lanes = [];
  for (const lane of LANES) {
    const m = { id: lane.id, ...byId[lane.id] };
    const h = lane.h - (lane.nodes.some(n => n.kind === 'X') && !(m.pick && m.pick.n > 0) ? 52 : 0);
    lanes.push({ lane, m, top, h });
    top += h + GAP_L;
  }
  const totalH = top;
  const svg = sv('svg', { class: 'flow', width: FLOW_W, height: totalH, viewBox: `0 0 ${FLOW_W} ${totalH}`, role: 'img', 'aria-label': 'AI 도구의 이벤트마다 heka 가 하는 일을 왼쪽에서 오른쪽으로 보인 순서도' });
  svg.append(sv('defs', {}, sv('marker', { id: 'arr', viewBox: '0 0 8 8', refX: '7', refY: '4', markerWidth: '7', markerHeight: '7', orient: 'auto' }, sv('path', { class: 'arrowhead', d: 'M0,0 L8,4 L0,8 Z' }))));
  svg.append(legendEl());
  const drawn = [];
  const allBus = [];
  for (const L of lanes) {
    L.lane = { ...L.lane, h: L.h };
    const r = drawLane(L.lane, L.m, L.top, open.has(L.lane.id), () => onToggle(L.lane.id));
    drawn.push(r); svg.append(r.g); allBus.push(...r.busYs);
  }
  // 시작 → 어떤 이벤트인가 → 다섯 갈래
  const ys = drawn.map(d => d.firstY), cy = (Math.min(...ys) + Math.max(...ys)) / 2;
  const S = geom(colLeft(0), cy, COL_W[0], 74), EV = geom(colLeft(1), cy, COL_W[1], DH);
  const gl = sv('g', { class: 'glob' });
  gl.append(sv('path', { class: 'edge', d: `M${S.r},${cy} H${EV.l}`, 'marker-end': 'url(#arr)' }));
  for (const d of drawn) gl.append(sv('path', { class: 'edge', d: `M${EV.r},${cy} H${EBUS_X} V${d.firstY} H${d.firstL}`, 'marker-end': 'url(#arr)' }));
  gl.append(sv('rect', { class: 'nd gstart', x: S.l, y: S.t, width: S.w, height: S.h, rx: 18 }), textLines(['AI 도구에서', '이벤트', '발생'], S.cx, cy, 't'),
    sv('path', { class: 'nd dec gdec', d: `M${EV.cx},${EV.t} L${EV.r},${EV.cy} L${EV.cx},${EV.b} L${EV.l},${EV.cy} Z` }), textLines(['어떤', '이벤트?'], EV.cx, cy, 't dt'));
  // 원장으로 모임 → 끝
  const minY = Math.min(...allBus), maxY = Math.max(...allBus), recY = (minY + maxY) / 2;
  const R = geom(colLeft(9), recY, COL_W[9], 112), E = geom(colLeft(10), recY, COL_W[10], 40);
  gl.append(sv('path', { class: 'edge', d: `M${BUS_X},${minY} V${maxY}` }),
    sv('path', { class: 'edge', d: `M${BUS_X},${recY} H${R.l}`, 'marker-end': 'url(#arr)' }),
    sv('path', { class: 'edge', d: `M${R.r},${recY} H${E.l}`, 'marker-end': 'url(#arr)' }),
    sv('rect', { class: 'nd rec', x: R.l, y: R.t, width: R.w, height: R.h, rx: 4 }),
    textLines(['모든 판단을', '원장에', '한 줄 기록', `이 기간`, `${n0(ov.range.rows)}줄`], R.cx, recY, 't'),
    sv('rect', { class: 'nd gstart', x: E.l, y: E.t, width: E.w, height: E.h, rx: 18 }), textLines(['끝'], E.cx, recY, 't'));
  svg.append(gl);
  return svg;
}

async function viewOverview(root, token) {
  const ov = await api('/api/overview' + rangeQuery());
  if (token !== routeToken) return;
  const open = new Set((hashQuery().get('open') || '').split(',').filter(x => LANE_BY[x]));
  const byId = Object.fromEntries(ov.moments.map(m => [m.moment, m]));
  const details = el('div', { class: 'details' });
  const drawDetails = () => {
    details.replaceChildren(...LANES.filter(l => open.has(l.id)).map(l => el('section', { class: 'lane-detail-box', mc: l.id },
      el('h3', {}, el('span', { class: 'mbadge', text: l.short }), ' ', l.title), laneDetail(l, { id: l.id, ...byId[l.id] }))));
  };
  const onToggle = id => { if (open.has(id)) open.delete(id); else open.add(id); setOpenInUrl(open); drawDetails(); };
  const view = el('div', {});
  view.append(el('p', { class: 'muted', text: `${rangeLabel()} · 줄 ${n0(ov.range.rows)}개 · 세션 ${n0(ov.range.sessions)}개. 갈래 머리줄을 누르면 아래에 자세한 숫자가 펼쳐집니다. 탈락 건수는 그 순간 전체 합계입니다.` }));
  if (!ov.range.rows) view.append(el('div', { class: 'empty', text: '이 기간에는 기록이 없습니다' }));
  const wrap = el('div', { class: 'flow-wrap' });
  wrap.append(overviewSvg(ov, open, onToggle));
  view.append(wrap, details);
  drawDetails();
  view.append(el('div', { class: 'band' }, el('h3', { text: '모델·사람이 부르는 것 (훅이 아닌 사건)' }),
    el('div', { class: 'chips' }, EVENTS.map(([k, label, cmd]) => {
      const v = ov.events[k] || 0;
      return el('span', { class: 'chip' + (v ? '' : ' zero') }, label, ' ', el('b', { text: n0(v) + '번' }), ' ', el('small', { text: cmd }));
    })),
    el('p', { class: 'muted', text: '조회는 조회 전용 항목까지 찾고, 등록·표시·은퇴·검토 결정은 항목 파일을 바꿉니다.' })));
  view.append(el('div', { class: 'band' }, el('h3', { text: '구조: 항목이 어디에서 읽히고 판단이 어디에 남는가' }),
    el('div', { class: 'struct' }, el('span', { class: 'box', text: '항목 파일 (items/*.md)' }), el('span', { class: 'muted', text: '→' }),
      el('span', { class: 'box', text: '색인' }), el('span', { class: 'muted', text: '→ 위의 모든 판단이 읽음 →' }),
      el('span', { class: 'box', text: '모든 판단' }), el('span', { class: 'muted', text: '→' }),
      el('span', { class: 'box ledger', text: `원장: 이 기간 ${n0(ov.range.rows)}줄` }))));
  root.replaceChildren(view);
}

// ---------- 줄 카드 (세션 상세, 항목 상세에서 공유) ----------
function stageChips(row) {
  const lane = LANE_BY[row.moment];
  const chips = [];
  if (!lane) return chips;
  if (row.tool_made) return [el('span', { class: 'pc skip', text: '도구가 만든 프롬프트: 건너뜀' })];
  const dropBy = {};
  for (const d of row.dropped || []) dropBy[d.stage] = (dropBy[d.stage] || 0) + 1;
  const seen = new Set();
  const cs = row.checks || [], cf = row.confirm || [];
  for (const st of flowNodes(lane)) {
    if (seen.has(st.t)) continue;
    seen.add(st.t);
    let cls = 'pc', txt = st.t;
    const dn = (st.drops || []).reduce((a, k) => a + (dropBy[k] || 0), 0);
    if (dn) { cls += ' drop'; txt += ` 탈락 ${dn}건`; }
    if (st.k === 'judge' && (row.judges || []).length) {
      const j = row.judges;
      cls += ' judge'; txt += ` ${j.length}번`;
      if (j.some(x => x.error || x.timeout)) txt += ' (오류·초과)';
    }
    if (st.k === 'confirm' && cf.length) { cls += ' check'; txt += ` 요구 ${cf.length}건`; }
    if (st.k === 'check' && cs.length) {
      cls += cs.some(c => c.blocked) ? ' block' : ' check';
      txt += ` ${cs.length}건${cs.some(c => c.result === 'fail') ? ' 걸림' : ''}`;
    }
    if (st.k === 'dict' && ((row.asked || []).length || (row.refused || []).length)) { cls += ' judge'; txt += ` 시킨 ${(row.asked || []).length}·거절 ${(row.refused || []).length}`; }
    chips.push(el('span', { class: cls, text: txt }), el('span', { class: 'pa', text: '›' }));
  }
  chips.pop();
  const inj = (row.injected || []).length;
  chips.push(el('span', { class: 'pa', text: '›' }), el('span', { class: 'pc ' + (inj ? 'inject' : 'skip'), text: inj ? `넣음 ${inj}건` : '침묵' }));
  return chips;
}
function section(label, ...kids) { return el('div', { class: 'sec' }, el('span', { class: 'lbl', text: label }), kids); }

function dropDetails(drops) {
  const groups = {};
  for (const d of drops) (groups[d.stage] ||= []).push(d);
  const names = Object.keys(groups).sort((a, b) => STAGES_ORDER.indexOf(a) - STAGES_ORDER.indexOf(b));
  const wrap = el('div', { class: 'sec' }, el('span', { class: 'lbl', text: '탈락' }));
  for (const name of names) {
    const det = el('details', { class: 'drops' }, el('summary', { text: `${name} ${groups[name].length}건` }));
    let built = false;
    det.addEventListener('toggle', () => { // 펼칠 때 만든다
      if (!det.open || built) return;
      built = true;
      det.append(el('ul', {}, groups[name].map(d => el('li', {}, itemLink(d.id), el('span', { class: 'muted', text: ' — ' + d.reason })))));
    });
    wrap.append(det);
  }
  return wrap;
}

function rowCard(row, refs, opts) {
  opts = opts || {};
  refs = refs || {};
  const hook = row.IsHook !== undefined ? row.IsHook : !!LANE_BY[row.moment] && ['SessionStart', 'UserPromptSubmit', 'PreToolUse', 'SubagentStart', 'Stop'].includes(row.event);
  const lane = hook ? LANE_BY[row.moment] : null;
  const top = el('div', { class: 'card-top' },
    el('span', { class: 'muted mono', text: fmtTime(row.ts) }),
    lane ? el('span', { class: 'mbadge', text: lane.short }) : null,
    el('b', { text: row.event }),
    arr(row.actions).map(a => el('span', { class: 'tag', text: a })),
    row.agent ? el('span', { class: 'tag', text: 'agent ' + short8(row.agent) }) : null,
    row.shadow ? el('span', { class: 'tag shadow', text: '그림자' }) : null,
    row.duration_ms ? el('span', { class: 'muted', text: ms(row.duration_ms) }) : null,
    row.tokens ? el('span', { class: 'muted', text: `토큰 약 ${n0(row.tokens)}` }) : null,
    opts.sessionLink ? link('#/sessions/' + encodeURIComponent(row.session), '세션 ' + short8(row.session)) : null);
  if (!hook) {
    const c = el('div', { class: 'card plain' + (row.agent ? ' sub' : '') }, top);
    if (row.prompt) c.append(el('div', { class: 'card-prompt', text: clip(row.prompt, 200) }));
    if (row.reason) c.append(section('사유', row.reason));
    return c;
  }
  const c = el('div', { class: 'card' + (row.agent ? ' sub' : ''), mc: row.moment }, top);
  if (row.prompt) c.append(el('div', { class: 'card-prompt', text: clip(row.prompt, 200) }));
  if (row.tool_made) c.append(el('div', { class: 'card-prompt', text: '(도구가 만든 프롬프트라 건너뜀)' }));
  c.append(el('div', { class: 'pipe' }, stageChips(row)));
  if ((row.asked || []).length) c.append(section('시킨 동작', row.asked.join(', ')));
  if ((row.refused || []).length) c.append(section('거절', row.refused.join(', ')));
  if ((row.grants_used || []).length) c.append(section('허용 사용', row.grants_used.join(', ')));
  if ((row.confirm || []).length) c.append(section('확인 요구', row.confirm.map(id => [itemLink(id), ' '])));
  if ((row.injected || []).length) {
    const box = el('div', { class: 'sec' }, el('span', { class: 'lbl', text: `넣은 항목 ${row.injected.length}건` }));
    for (const i of row.injected) {
      const ref = refs[i.id];
      box.append(el('div', { class: 'inj' }, itemLink(i.id), ' ',
        ref && ref.must ? el('span', { class: 'tag must', text: '필수' }) : null,
        el('span', { class: 'muted', text: ' 이유: ' + (i.why || '-') }),
        el('div', { class: 'txt', text: !ref ? '' : ref.exists ? ref.text : '지금은 없는 항목' })));
    }
    c.append(box);
  }
  if ((row.dropped || []).length) c.append(dropDetails(row.dropped));
  if ((row.judges || []).length) {
    c.append(section('판정', row.judges.map(j => el('div', {}, itemLink(j.id),
      el('span', { class: 'muted', text: ` 답 ${j.inject ? '넣으라' : '넣지 말라'} · ${ms(j.ms)}${j.timeout ? ' · 시간 초과' : j.error ? ' · 오류' : ''}` })))));
  }
  if ((row.checks || []).length) {
    c.append(section('검사', row.checks.map(k => el('div', {}, itemLink(k.id),
      el('span', { class: 'muted', text: ` 대상 ${k.target} · ` }),
      el('span', { class: 'tag ' + (k.result === 'fail' ? 'warn' : ''), text: k.result }),
      k.blocked ? el('span', { class: 'tag warn', text: '막음' }) : null,
      k.note ? el('span', { class: 'muted', text: ' ' + k.note }) : null))));
  }
  if (row.pick) {
    const p = row.pick;
    c.append(section('모델 후보(기록만 함)', `목록 ${n0(p.listed)}건 · 토큰 약 ${n0(p.list_tokens)} · ${ms(p.ms)}${p.timeout ? ' · 시간 초과' : p.error ? ' · 오류' : ''} · 고른 ${n0((p.items || []).length)}건`,
      (p.items || []).map(x => el('div', {}, itemLink(x.id), el('span', { class: 'muted', text: ` ${x.direct ? '바로 넣으라' : '따져 볼 만함'}${x.judged ? ` · 판정 ${x.inject ? '넣으라' : '넣지 말라'}` : ''}` })))));
  }
  return c;
}

// ---------- 세션 ----------
async function viewSessions(root, token) {
  const list = await api('/api/sessions' + rangeQuery({ limit: 500 }));
  if (token !== routeToken) return;
  if (!list.length) { root.replaceChildren(el('div', { class: 'empty', text: '이 기간에는 기록이 없습니다' })); return; }
  const head = ['마지막 시각', '줄(번)', '넣은(건)', '탈락(건)', '서브에이전트', '막음/확인', '그림자', '첫 프롬프트', 'id'];
  const tb = el('tbody', {});
  for (const s of list) {
    tb.append(el('tr', { class: 'click', on: { click: () => { location.hash = '#/sessions/' + encodeURIComponent(s.id); } } },
      el('td', { text: fmtTime(s.last_ts) }), el('td', { class: 'num', text: n0(s.rows) }), el('td', { class: 'num', text: n0(s.injected) }),
      el('td', { class: 'num', text: n0(s.dropped) }), el('td', { class: 'num', text: n0(s.agents) }),
      el('td', { class: 'num' + (s.blocked ? '' : ''), text: `${n0(s.blocked)} / ${n0(s.confirm)}` }),
      el('td', {}, s.shadow ? el('span', { class: 'tag shadow', text: '그림자' }) : ''),
      el('td', { class: 'clip', text: clip(s.prompt, 80) }), el('td', { class: 'mono', text: short8(s.id) })));
  }
  root.replaceChildren(el('p', { class: 'muted', text: `${rangeLabel()} · 세션 ${n0(list.length)}개 (최근 순, 최대 500)` }),
    el('div', { class: 'tbl-wrap' }, el('table', { class: 'list' }, el('thead', {}, el('tr', {}, head.map(h => el('th', { text: h })))), tb)));
}

async function viewSession(root, token, id) {
  const data = await api('/api/sessions/' + encodeURIComponent(id));
  if (token !== routeToken) return;
  const f = { on: new Set([...LANES.map(l => l.id), 'other']), inj: false, act: false };
  const tl = el('div', { class: 'timeline' });
  const count = el('span', { class: 'muted' });
  const draw = () => {
    const frag = document.createDocumentFragment();
    let n = 0;
    for (const r of data.rows) {
      const key = LANE_BY[r.moment] && ['SessionStart', 'UserPromptSubmit', 'PreToolUse', 'SubagentStart', 'Stop'].includes(r.event) ? r.moment : 'other';
      if (!f.on.has(key)) continue;
      if (f.inj && !(r.injected || []).length) continue;
      if (f.act && !((r.dropped || []).length || (r.judges || []).length || (r.checks || []).length)) continue;
      frag.append(rowCard(r, data.items));
      n++;
    }
    tl.replaceChildren(frag);
    if (!n) tl.append(el('div', { class: 'empty', text: '조건에 맞는 줄이 없습니다' }));
    count.textContent = `${n0(n)} / ${n0(data.rows.length)}줄`;
  };
  const bar = el('div', { class: 'toolbar' });
  for (const l of [...LANES, { id: 'other', short: '그 밖(조회·등록 등)' }]) {
    const b = el('button', { type: 'button', class: 'on', text: l.short });
    if (l.id !== 'other') b.style.setProperty('border-left', `4px solid var(--m-${l.id})`);
    b.addEventListener('click', () => { if (f.on.has(l.id)) f.on.delete(l.id); else f.on.add(l.id); b.classList.toggle('on'); draw(); });
    bar.append(b);
  }
  const cb = (label, key) => {
    const i = el('input', { type: 'checkbox' });
    i.addEventListener('change', () => { f[key] = i.checked; draw(); });
    return el('label', {}, i, ' ', label);
  };
  bar.append(cb('넣은 줄만', 'inj'), cb('탈락·판정·검사가 있는 줄만', 'act'), count);
  const first = data.rows[0], last = data.rows[data.rows.length - 1];
  root.replaceChildren(el('div', { class: 'crumb' }, link('#/sessions', '← 세션 목록')),
    el('h2', {}, '세션 ', el('span', { class: 'mono', text: data.id })),
    el('p', { class: 'muted', text: `${fmtTime(first.ts)} ~ ${fmtTime(last.ts)} · 줄 ${n0(data.rows.length)}개` }), bar, tl);
  draw();
}

// ---------- 항목 ----------
function whenSummary(w) {
  w = w || {};
  const parts = [];
  for (const [k, label] of [['moment', '순간'], ['action', '동작'], ['path', '경로'], ['repo', '저장소'], ['agent', '에이전트']]) {
    const v = arr(w[k]);
    if (v.length) parts.push(`${label} ${v.join(',')}`);
  }
  return parts.join(' · ') || '(조건 없음)';
}
function itemKind(it) { return !it.push ? 'find' : it.must ? 'must' : 'ref'; }

async function viewItems(root, token) {
  const items = await api('/api/items');
  if (token !== routeToken) return;
  if (!items.length) { root.replaceChildren(el('div', { class: 'empty', text: '항목이 없습니다' })); return; }
  const st = { q: '', kinds: new Set(['must', 'ref', 'find']), sort: 'injected' };
  const tb = el('tbody', {});
  const count = el('span', { class: 'muted' });
  const draw = () => {
    const q = st.q.trim().toLowerCase();
    let list = items.filter(it => st.kinds.has(itemKind(it)) &&
      (!q || it.id.toLowerCase().includes(q) || (it.text || '').toLowerCase().includes(q) || arr(it.about).some(a => String(a).toLowerCase().includes(q))));
    list = list.slice().sort(st.sort === 'id' ? (a, b) => a.id.localeCompare(b.id)
      : (a, b) => (b.stats.injected - a.stats.injected) || a.id.localeCompare(b.id));
    const frag = document.createDocumentFragment();
    for (const it of list) {
      frag.append(el('tr', { class: 'click', on: { click: () => { location.hash = '#/items/' + encodeURIComponent(it.id); } } },
        el('td', { class: 'mono', text: it.id }), el('td', { class: 'clip', text: clip(it.text, 90) }),
        el('td', { text: whenSummary(it.when) }), el('td', { text: arr(it.scope).join(', ') || '전역' }),
        el('td', { class: 'num', text: n0(it.stats.injected) }), el('td', { text: it.stats.last_injected ? fmtTime(it.stats.last_injected) : '-' }),
        el('td', { class: 'num', text: n0(it.stats.check_fail) }),
        el('td', { class: 'tags' }, it.must ? el('span', { class: 'tag must', text: '필수' }) : null, !it.push ? el('span', { class: 'tag', text: '조회 전용' }) : null,
          it.confirm ? el('span', { class: 'tag', text: '확인' }) : null, it.has_check ? el('span', { class: 'tag', text: '검사' }) : null,
          it.stats.flagged ? el('span', { class: 'tag warn', text: `표시 ${it.stats.flagged}` }) : null)));
    }
    tb.replaceChildren(frag);
    count.textContent = `${n0(list.length)} / ${n0(items.length)}건`;
  };
  const search = el('input', { type: 'search', placeholder: 'id·주입문·주제어 검색' });
  let timer;
  search.addEventListener('input', () => { clearTimeout(timer); timer = setTimeout(() => { st.q = search.value; draw(); }, 120); });
  const sort = el('select', {}, el('option', { value: 'injected', text: '넣은 횟수 많은 순' }), el('option', { value: 'id', text: 'id 순' }));
  sort.addEventListener('change', () => { st.sort = sort.value; draw(); });
  const bar = el('div', { class: 'toolbar' }, search);
  for (const [k, label] of [['must', '필수'], ['ref', '넣는 참고'], ['find', '조회 전용']]) {
    const i = el('input', { type: 'checkbox', checked: true });
    i.addEventListener('change', () => { if (i.checked) st.kinds.add(k); else st.kinds.delete(k); draw(); });
    bar.append(el('label', {}, i, ' ', label));
  }
  bar.append(sort, count);
  const head = ['id', '주입문', '조건(when)', '범위', '넣은(번)', '마지막 주입', '검사 걸림', '표시'];
  root.replaceChildren(el('p', { class: 'muted', text: '넣은 횟수·마지막 주입·검사 걸림은 원장 전체 기준이고, 위의 기간 선택과 무관합니다.' }), bar,
    el('div', { class: 'tbl-wrap' }, el('table', { class: 'list' }, el('thead', {}, el('tr', {}, head.map(h => el('th', { text: h })))), tb)));
  draw();
}

async function viewItem(root, token, id) {
  const it = await api('/api/items/' + encodeURIComponent(id));
  if (token !== routeToken) return;
  const kv = [['id', it.id], ['종류', itemKind(it) === 'must' ? '필수' : itemKind(it) === 'ref' ? '넣는 참고' : '조회 전용'],
    ['주입문', it.text], ['조건(when)', whenSummary(it.when)], ['범위', arr(it.scope).join(', ') || '전역'],
    ['주제어', arr(it.about).join(', ') || '-'], ['출처', it.source || '-'], ['확인한 날', it.checked || '-'], ['기한', it.expires || '-'],
    ['확인 요구', it.confirm || '-'], ['검사', it.has_check ? '있음' : '없음']];
  const dl = el('dl', { class: 'kv' });
  for (const [k, v] of kv) dl.append(el('dt', { text: k }), el('dd', { text: v }));
  const s = it.stats;
  const by = s.dropped_by_stage || {};
  const stats = el('dl', { class: 'kv' },
    el('dt', { text: '넣은 횟수' }), el('dd', { text: `${n0(s.injected)}번` }),
    el('dt', { text: '마지막 주입' }), el('dd', { text: s.last_injected ? fmtTime(s.last_injected) : '-' }),
    el('dt', { text: '탈락' }), el('dd', { text: `${n0(s.dropped)}건` + (Object.keys(by).length ? ' (' + STAGES_ORDER.filter(k => by[k]).concat(Object.keys(by).filter(k => !STAGES_ORDER.includes(k))).map(k => `${k} ${n0(by[k])}`).join(' · ') + ')' : '') }),
    el('dt', { text: '검사 걸림' }), el('dd', { text: `${n0(s.check_fail)}건` }),
    el('dt', { text: '표시(flag)' }), el('dd', { text: `${n0(s.flagged)}번` }));
  const recent = el('div', { class: 'timeline' });
  const rows = (it.recent || []).slice().sort((a, b) => (a.ts < b.ts ? 1 : -1));
  for (const r of rows) recent.append(rowCard(r, { [it.id]: { text: it.text, must: it.must, exists: true } }, { sessionLink: true }));
  root.replaceChildren(el('div', { class: 'crumb' }, link('#/items', '← 항목 목록')), el('h2', {}, el('span', { class: 'mono', text: it.id })), dl,
    el('h3', { text: '본문 (마크다운 원문)' }), el('pre', { class: 'body', text: it.body || '(본문 없음)' }),
    el('h3', { text: '통계 (원장 전체)' }), stats,
    el('h3', { text: `최근 줄 (${rows.length}개, 최근 것이 위)` }), rows.length ? recent : el('div', { class: 'empty', text: '이 항목이 나온 줄이 없습니다' }));
}

// ---------- 라우터 ----------
let routeToken = 0;
async function route() {
  const token = ++routeToken;
  const path = (location.hash.slice(1).split('?')[0] || '/').replace(/\/+$/, '') || '/';
  const root = document.getElementById('view');
  const seg = path.split('/').filter(Boolean);
  const tab = seg[0] === 'sessions' ? 'sessions' : seg[0] === 'items' ? 'items' : 'overview';
  document.querySelectorAll('#tabs a').forEach(a => a.classList.toggle('on', a.dataset.tab === tab));
  document.querySelectorAll('#range button[data-r]').forEach(b => b.classList.toggle('on', b.dataset.r === rangeKey));
  const usesRange = tab !== 'items' || false;
  document.getElementById('range').classList.toggle('dim', !usesRange);
  root.replaceChildren(el('p', { class: 'muted', text: '불러오는 중...' }));
  try {
    const id = seg[1] ? decodeURIComponent(seg.slice(1).join('/')) : '';
    if (tab === 'sessions') await (id ? viewSession(root, token, id) : viewSessions(root, token));
    else if (tab === 'items') await (id ? viewItem(root, token, id) : viewItems(root, token));
    else await viewOverview(root, token);
  } catch (e) {
    if (token === routeToken) root.replaceChildren(el('div', { class: 'errbox', text: '화면을 그리지 못했습니다: ' + e.message }));
  }
  if (token === routeToken) window.scrollTo(0, 0);
}

document.querySelectorAll('#range button[data-r]').forEach(b => b.addEventListener('click', () => {
  rangeKey = b.dataset.r;
  try { sessionStorage.setItem('heka.range', rangeKey); } catch (_) { /* 저장소 없음 */ }
  route();
}));
document.getElementById('refresh').addEventListener('click', () => { renderHead(); route(); });
window.addEventListener('hashchange', route);
renderHead();
route();
