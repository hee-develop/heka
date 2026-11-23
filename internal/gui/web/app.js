'use strict';
/* heka 관찰 화면. 빌드 없이 돌아가는 바닐라 JS 다.
   데이터가 섞인 글은 모두 textContent 로만 넣는다(innerHTML 을 쓰지 않는다). */

// ---------- 구조 정의: 코드가 바뀌면 이 상수만 고친다 ----------
// 단계(stage): k=그리는 방식, t=이름, d=설명, drops=원장 탈락 단계 이름들.
// cols: 가로로 이어지는 열이고, 열 안의 단계는 세로로 쌓인 갈래다(한 칸짜리 열은 모든 갈래에 공통이다).
const S = (k, t, d, drops) => ({ k, t, d: d || '', drops: drops || [] });
const SCOPE = S('plain', '범위·자격', '위치 범위, 기한·확인 여부', ['범위', '자격']);
const DEDUP = S('plain', '중복 제거', '세션에 한 번만', ['중복']);
const LANES = [
  { id: 'session_start', short: '세션 시작', title: '세션 시작·압축 직후', event: 'SessionStart',
    inDesc: '새 세션이 열리거나 압축·clear 뒤 이어질 때',
    cols: [[S('plain', '범위', '작업 위치로 정한 범위', ['범위'])], [S('plain', '자격', '기한·확인 여부', ['자격'])],
      [S('plain', '시작 항목 고르기', 'when.moment 가 세션 시작', ['일치'])], [DEDUP]] },
  { id: 'prompt', short: '프롬프트', title: '사용자 프롬프트', event: 'UserPromptSubmit',
    inDesc: '사용자가 프롬프트를 보냈을 때', pre: 'tool_made',
    cols: [[S('dict', '동작 낱말 사전', '"푸시해 줘" → git.push'), S('plain', '조각내기', '어절·두 글자 조각')],
      [SCOPE],
      [S('plain', '조건표', '시킨 동작에 걸린 필수 항목'), S('plain', '후보 잡기', '주제어·낱말 겹침 (조건표 탈락 포함)', ['일치'])],
      [null, S('judge', '모델 판정', '직접 답이 되는가', ['판정'])],
      [DEDUP]],
    pickLane: true },
  { id: 'tool', short: '도구 호출', title: '도구 호출 직전', event: 'PreToolUse',
    inDesc: '도구(Bash, Edit 등)를 쓰기 직전',
    cols: [[S('plain', '동작 해석', '도구 호출 → 동작 이름 (위임 도구면 위임 글을 적어 둠)')], [SCOPE],
      [S('plain', '조건표 일치', '동작·경로·저장소', ['일치'])], [DEDUP], [S('check', '확인·검사', '확인 요구, 결과물 검사')]] },
  { id: 'subagent_start', short: '서브에이전트', title: '서브에이전트 시작', event: 'SubagentStart',
    inDesc: '서브에이전트가 시작될 때',
    cols: [[S('plain', '위임 글 꺼내기', '세션 상태에서 같은 종류의 글')], [SCOPE],
      [S('plain', '시작 항목', 'when.moment·when.agent', ['일치']), S('plain', '후보 잡기', '위임 글로 주제어·낱말')],
      [null, S('judge', '모델 판정', '직접 답이 되는가', ['판정'])], [DEDUP]],
    pickLane: true },
  { id: 'stop', short: '턴 종료', title: '턴 종료', event: 'Stop',
    inDesc: 'AI 가 응답을 끝냈을 때',
    cols: [[SCOPE], [S('check', '마지막 응답 검사', '형식에 맞는가')]] },
];
const LANE_BY = Object.fromEntries(LANES.map(l => [l.id, l]));
// 훅이 아닌 사건(모델·사람이 부르는 것)
const EVENTS = [['find', '조회', 'heka find'], ['add', '등록', 'heka add'], ['pending', '대기함 결정', 'pending'],
  ['flag', '표시', 'flag'], ['retire', '은퇴', 'retire']];
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
const n0 = v => (Number.isFinite(v) ? v : 0).toLocaleString('ko-KR');
const arr = v => (Array.isArray(v) ? v : v ? [v] : []);
const clip = (s, n) => { s = String(s ?? '').replace(/\s+/g, ' ').trim(); return s.length > n ? s.slice(0, n) + '…' : s; };
const short8 = s => String(s || '').slice(0, 8);
function fmtTime(ts) {
  const d = new Date(ts);
  if (!ts || isNaN(d)) return ts || '';
  return d.toLocaleString('ko-KR', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
}
const ms = v => `${n0(v)}ms`;
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
        el('span', { class: 'muted', text: `건 (넣는 것 ${n0(m.items.push)} · 조회 전용 ${n0(m.items.find_only)} · 필수 ${n0(m.items.must)} · 대기함 ${n0(m.items.pending)})` })),
      el('span', { class: 'st' }, '원장 ', el('b', { text: n0(m.ledger.rows) }), '줄',
        m.ledger.first_ts ? el('span', { class: 'muted', text: ` (${fmtTime(m.ledger.first_ts)} ~ ${fmtTime(m.ledger.last_ts)})` }) : ''),
      m.errors > 0 ? el('span', { class: 'badge-warn', text: `훅 오류 ${n0(m.errors)}건` }) : el('span', { class: 'st muted', text: '훅 오류 0건' }),
      m.shadow ? el('span', { class: 'badge-shadow', text: '그림자 방식: 판단만 기록하고 내보내지 않음' }) : null,
      el('span', { class: 'st muted', text: m.version })].filter(Boolean));
  } catch (e) {
    document.getElementById('h-stats').replaceChildren(el('span', { class: 'badge-warn', text: '머리 정보를 읽지 못했습니다: ' + e.message }));
  }
}

// ---------- 구조도 ----------
function stageFlat(lane) { return lane.cols.flat().filter(Boolean); }
const sumDrops = (m, keys) => keys.reduce((a, k) => a + ((m.dropped || {})[k] || 0), 0);

// 단계 상자 안에 쓸 숫자 줄들
function stageLines(st, m) {
  if (st.k === 'dict') return [`시킨 동작 ${n0(m.asked)}건`, `거절 ${n0(m.refused)}건`];
  if (st.k === 'judge') {
    const j = m.judges;
    if (!j.n) return ['판정 0번'];
    return [`판정 ${n0(j.n)}번 · 넣으라 ${n0(j.inject)}번`, `시간 초과 ${n0(j.timeout)}번 · 오류 ${n0(j.error)}번`, `중앙 ${ms(j.ms.median)} · 95% ${ms(j.ms.p95)}`];
  }
  if (st.k === 'check') {
    const c = m.checks;
    const out = [];
    if (m.id !== 'stop') out.push(`확인 요구 ${n0(m.confirm)}건 · 허용 사용 ${n0(m.grants_used)}번`);
    out.push(`검사 통과 ${n0(c.pass)} · 걸림 ${n0(c.fail)} · 건너뜀 ${n0(c.skip)}`);
    return out;
  }
  return [];
}
function stageActive(st, m) {
  if (st.drops.length && sumDrops(m, st.drops) > 0) return true;
  if (st.k === 'dict') return m.asked + m.refused > 0;
  if (st.k === 'judge') return m.judges.n > 0;
  if (st.k === 'check') return m.confirm + m.grants_used + m.checks.pass + m.checks.fail + m.checks.skip > 0;
  return m.rows > 0;
}
function stageBox(st, m) {
  const drop = st.drops.length ? sumDrops(m, st.drops) : null;
  const nums = stageLines(st, m);
  return el('div', { class: 'stage' + (stageActive(st, m) ? '' : ' zero') },
    el('div', { class: 'st-t', text: st.t }),
    st.d ? el('div', { class: 'st-d', text: st.d }) : null,
    nums.map(t => el('div', { class: 'st-n', text: t })),
    drop == null ? null : el('div', { class: 'drop' + (drop ? '' : ' z'), text: `탈락 ${n0(drop)}건` }));
}
function flowEl(lane, m) {
  const parts = [];
  lane.cols.forEach((col, i) => {
    if (i) parts.push(el('div', { class: 'arrow', 'aria-hidden': 'true' }));
    parts.push(el('div', { class: 'col' }, col.map(st => st ? stageBox(st, m) : el('div', { class: 'ghost' }))));
  });
  const wrap = el('div', { class: 'flow-wrap' });
  if (lane.pre === 'tool_made') {
    wrap.append(el('div', { class: 'skipbox' + (m.tool_made ? '' : ' dim'), text: `도구가 만든 프롬프트는 건너뜀: ${n0(m.tool_made)}번 (아래 두 갈래를 모두 지나지 않음)` }));
  }
  wrap.append(el('div', { class: 'flow' }, parts));
  if (lane.pickLane && m.pick.n > 0) {
    wrap.append(el('div', { class: 'pickbox' }, '모델 후보(기록만 함, 주입에는 쓰이지 않음) ',
      el('b', { text: `${n0(m.pick.n)}번` }), ` · 고른 ${n0(m.pick.picked)}건 중 판정 통과 ${n0(m.pick.picked_inject)}건`,
      (m.pick.error || m.pick.timeout) ? ` · 오류 ${n0(m.pick.error)} · 시간 초과 ${n0(m.pick.timeout)}` : ''));
  }
  return wrap;
}
function outEl(lane, m) {
  const o = el('div', { class: 'zone-out' });
  if (lane.id === 'stop') {
    o.append(el('div', { class: 'big', text: `되돌림 ${n0(m.checks.blocked)}번` }), el('div', { class: 'muted', text: '걸리면 사유를 돌려주고 한 번 더 돌게 함. 항목을 넣지는 않음' }));
    return o;
  }
  o.append(el('div', { class: 'big' + (m.injected ? '' : ' dim'), text: `넣은 항목 ${n0(m.injected)}건` }),
    el('div', { class: m.tokens ? '' : 'dim', text: `토큰 약 ${n0(m.tokens)}` }));
  if (lane.id === 'prompt') {
    o.append(el('div', { class: 'out-item muted', text: '필수 항목 주입 · 허용 기록 · 참고 항목 주입 또는 침묵' }),
      el('div', { class: 'out-item' + (m.rows_silent ? '' : ' dim'), text: `침묵 ${n0(m.rows_silent)}번` }));
  } else if (lane.id === 'tool') {
    o.append(el('div', { class: 'out-item' + (m.confirm ? '' : ' dim'), text: `확인 요구 ${n0(m.confirm)}건` }),
      el('div', { class: 'out-item' + (m.checks.blocked ? '' : ' dim'), text: `막기 ${n0(m.checks.blocked)}번` }));
  }
  return o;
}
function laneHead(lane, m, open) {
  const parts = [`${n0(m.rows)}번`, `그중 넣은 것 ${n0(m.rows_injected)}번 · 침묵 ${n0(m.rows_silent)}번`];
  if (m.shadow_rows) parts.push(`그림자 ${n0(m.shadow_rows)}번`);
  return el('button', { type: 'button', class: 'lane-head', 'aria-expanded': String(open) },
    el('span', { class: 'lane-caret', text: open ? '▼' : '▶' }),
    el('span', { class: 'lane-title', text: lane.title }),
    el('span', { class: 'lane-ev', text: lane.event }),
    el('span', { class: 'lane-count' + (m.rows ? '' : ' dim'), text: parts.join(' · ') }),
    el('span', { class: 'lane-time' + (m.rows ? '' : ' dim'), text: `처리 시간 중앙 ${ms(m.duration_ms.median)} · 95% ${ms(m.duration_ms.p95)}` }));
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

async function viewOverview(root, token) {
  const ov = await api('/api/overview' + rangeQuery());
  if (token !== routeToken) return;
  const open = new Set((hashQuery().get('open') || '').split(',').filter(x => LANE_BY[x]));
  const view = el('div', {});
  view.append(el('p', { class: 'muted', text: `${rangeLabel()} · 줄 ${n0(ov.range.rows)}개 · 세션 ${n0(ov.range.sessions)}개. "번"은 훅이 불린 횟수, "건"은 항목 수입니다.` }));
  if (!ov.range.rows) { view.append(el('div', { class: 'empty', text: '이 기간에는 기록이 없습니다' })); }
  view.append(el('div', { class: 'zones' }, el('div', { text: 'AI 도구의 이벤트' }), el('div', { text: 'heka 가 하는 일' }), el('div', { text: '나가는 것' })));
  const byId = Object.fromEntries(ov.moments.map(m => [m.moment, m]));
  for (const lane of LANES) {
    const m = { id: lane.id, ...(byId[lane.id]) };
    if (!byId[lane.id]) continue;
    const box = el('section', { class: 'lane', mc: lane.id });
    const draw = () => {
      const isOpen = open.has(lane.id);
      const body = el('div', { class: 'lane-body' },
        el('div', { class: 'zone-in' }, el('div', { class: 'ev', text: lane.event }), el('div', { class: 'muted', text: lane.inDesc })),
        flowEl(lane, m), outEl(lane, m));
      const head = laneHead(lane, m, isOpen);
      head.addEventListener('click', () => { if (open.has(lane.id)) open.delete(lane.id); else open.add(lane.id); setOpenInUrl(open); draw(); });
      box.replaceChildren(...[head, body, isOpen ? laneDetail(lane, m) : null].filter(Boolean));
    };
    draw();
    view.append(box);
  }
  // 훅이 아닌 사건
  view.append(el('div', { class: 'band' }, el('h3', { text: '모델·사람이 부르는 것 (훅이 아닌 사건)' }),
    el('div', { class: 'chips' }, EVENTS.map(([k, label, cmd]) => {
      const v = ov.events[k] || 0;
      return el('span', { class: 'chip' + (v ? '' : ' zero') }, label, ' ', el('b', { text: n0(v) + '번' }), ' ', el('small', { text: cmd }));
    })),
    el('p', { class: 'muted', text: '조회는 조회 전용 항목까지 찾고, 등록·표시·은퇴·대기함 결정은 항목 파일을 바꿉니다.' })));
  const total = ov.range.rows;
  view.append(el('div', { class: 'band' }, el('h3', { text: '구조: 항목이 어디에서 읽히고 판단이 어디에 남는가' }),
    el('div', { class: 'struct' }, el('span', { class: 'box', text: '항목 파일 (items/*.md)' }), el('span', { class: 'muted', text: '→' }),
      el('span', { class: 'box', text: '색인' }), el('span', { class: 'muted', text: '→ 위의 모든 판단이 읽음 →' }),
      el('span', { class: 'box', text: '모든 판단' }), el('span', { class: 'muted', text: '→' }),
      el('span', { class: 'box ledger', text: `원장: 이 기간 ${n0(total)}줄` }))));
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
  for (const st of stageFlat(lane)) {
    if (seen.has(st.t)) continue;
    seen.add(st.t);
    let cls = 'pc', txt = st.t;
    const dn = st.drops.reduce((a, k) => a + (dropBy[k] || 0), 0);
    if (dn) { cls += ' drop'; txt += ` 탈락 ${dn}건`; }
    if (st.k === 'judge' && (row.judges || []).length) {
      const j = row.judges;
      cls += ' judge'; txt += ` ${j.length}번`;
      if (j.some(x => x.error || x.timeout)) txt += ' (오류·초과)';
    }
    if (st.k === 'check') {
      const cs = row.checks || [];
      if (cs.length || (row.confirm || []).length) {
        cls += cs.some(c => c.blocked) ? ' block' : ' check';
        const parts = [];
        if ((row.confirm || []).length) parts.push(`확인 ${row.confirm.length}건`);
        if (cs.length) parts.push(`검사 ${cs.length}건${cs.some(c => c.result === 'fail') ? ' 걸림' : ''}`);
        txt += ' ' + parts.join(' · ');
      }
    }
    if (st.k === 'dict' && ((row.asked || []).length || (row.refused || []).length)) { cls += ' judge'; txt += ` 시킨 ${(row.asked || []).length}·거절 ${(row.refused || []).length}`; }
    chips.push(el('span', { class: cls, text: txt }));
    chips.push(el('span', { class: 'pa', text: '›' }));
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
