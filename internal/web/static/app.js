'use strict';

/* ============================ состояние ============================ */
let cfg = null;
let st  = { drives: [], trail: [], scan: {}, posX: 0, posY: 0 };
let prevUdpSent = -1;
let lastUdpAt = 0;

const $  = s => document.querySelector(s);
const $$ = s => Array.from(document.querySelectorAll(s));

/* ============================ утилиты ============================ */
function getPath(o, p){
  return p.split('.').reduce((a,k)=> (a==null? a : a[k]), o);
}
function setPath(o, p, v){
  const ks = p.split('.'); const last = ks.pop();
  let t = o;
  for (const k of ks){
    if (t[k] == null) t[k] = (/^\d+$/.test(k) ? [] : {});
    t = t[k];
  }
  t[last] = v;
}
function readEl(el){
  if (el.type === 'checkbox') return el.checked;
  if (el.type === 'number' || el.dataset.num === '1'){
    const n = parseFloat(el.value);
    return isNaN(n) ? 0 : n;
  }
  return el.value;
}
function elFor(path){ return document.querySelector(`[data-path="${path}"]`); }

/* ============================ табы ============================ */
$$('.tab').forEach(t => t.addEventListener('click', () => {
  $$('.tab').forEach(x => x.classList.remove('active'));
  $$('.tabpage').forEach(x => x.classList.remove('active'));
  t.classList.add('active');
  $('#tab-' + t.dataset.tab).classList.add('active');
  if (t.dataset.tab === 'work') setTimeout(resizeCanvas, 30);
}));

/* ============================ конфигурация ============================ */
function bindInputs(){
  $$('[data-path]').forEach(el => {
    const p = el.dataset.path;
    const commit = () => {
      setPath(cfg, p, readEl(el));
      // синхронизация дублирующихся полей в другой вкладке
      $$(`[data-path="${p}"]`).forEach(other => {
        if (other === el) return;
        if (other.type === 'checkbox') other.checked = !!getPath(cfg, p);
        else if (document.activeElement !== other) other.value = getPath(cfg, p);
      });
      if (p === 'ui.theme') applyTheme();
    };
    el.addEventListener('input', commit);
    el.addEventListener('change', commit);
  });
}

function applyConfig(){
  $$('[data-path]').forEach(el => {
    const v = getPath(cfg, el.dataset.path);
    if (el.type === 'checkbox') el.checked = !!v;
    else el.value = (v == null ? '' : v);
  });
  applyTheme();
}

function applyTheme(){
  document.body.classList.toggle('light', cfg.ui && cfg.ui.theme === 'light');
}

async function loadConfig(){
  cfg = await (await fetch('/api/config')).json();
  applyConfig();
}

async function saveConfig(msgEl){
  const r = await fetch('/api/config', {
    method: 'POST',
    headers: {'Content-Type':'application/json'},
    body: JSON.stringify(cfg)
  });
  if (r.ok){
    cfg = await r.json();
    applyConfig();
    msgEl.textContent = 'Сохранено ' + new Date().toLocaleTimeString();
  } else {
    msgEl.textContent = 'Ошибка: ' + (await r.json()).error;
  }
  setTimeout(()=> msgEl.textContent = '', 4000);
}

$('#saveCfg').addEventListener('click', () => saveConfig($('#saveMsg')));
$('#saveTask').addEventListener('click', () => saveConfig($('#taskMsg')));

$('#routePreview').addEventListener('click', async () => {
  const pts = await (await fetch('/api/route')).json();
  const last = pts.length ? pts[pts.length-1] : {x:0,y:0};
  $('#routeInfo').textContent = `точек: ${pts.length}, конец (${last.x.toFixed(0)}; ${last.y.toFixed(0)})`;
});

/* ============================ SSE ============================ */
function connectSSE(){
  const es = new EventSource('/api/events');
  es.onmessage = e => {
    try { st = JSON.parse(e.data); } catch(_) { return; }
    updateUI();
  };
  es.onerror = () => { /* EventSource переподключается сам */ };
}

/* ============================ обновление UI ============================ */
function updateUI(){
  // мини-индикаторы
  const miniConn = $('#miniConn');
  miniConn.textContent = 'связь: ' + (st.connected ? 'есть' : 'нет');
  miniConn.className = 'pill ' + (st.connected ? 'on' : 'off');

  const udpActive = st.udpOn;
  const miniUdp = $('#miniUdp');
  miniUdp.textContent = 'UDP: ' + (udpActive ? 'вкл' : 'выкл');
  miniUdp.className = 'pill ' + (udpActive ? 'on' : 'off');

  // подключение
  $('#dotServer').className = 'dot' + (st.serverUp ? ' ok' : '');
  $('#dotClient').className = 'dot' + (st.clientUp ? ' ok' : '');
  $('#txtServer').textContent = st.serverUp ? 'доступен' : 'недоступен';
  $('#txtClient').textContent = st.clientUp ? 'доступен' : 'недоступен';

  const sw = $('#connSwitch');
  if (sw.checked !== !!st.connected) sw.checked = !!st.connected;
  $('#connText').textContent = st.connected ? 'Подключено' : 'Отключено';
  $('#linkErr').textContent = st.linkErr || '';

  // UDP индикация
  const ind = $('#udpInd');
  ind.className = 'udpind' + (udpActive ? ' active' : '');
  if (st.udpSent !== prevUdpSent){
    prevUdpSent = st.udpSent;
    lastUdpAt = Date.now();
  }
  if (udpActive && Date.now() - lastUdpAt < 400) ind.classList.add('active');

  // сканирование
  const s = st.scan || {};
  $('#scanInfo').textContent = s.total
      ? `${s.running ? (s.paused ? 'пауза' : 'выполняется') : 'остановлено'} — ${s.index}/${s.total}`
      : '';

  // приводы
  (st.drives || []).forEach(d => {
    const a = (d.name || '').toUpperCase();
    if (a !== 'X' && a !== 'Y') return;
    const set = (id, v) => { const e = $(id); if (e) e.textContent = v; };
    set('#pos-' + a, (+d.position).toFixed(2));
    set('#spd-' + a, (+d.speed).toFixed(2));
    set('#cur-' + a, (+d.current).toFixed(0));
    set('#flt-' + a, '0x' + (d.fault>>>0).toString(16).padStart(4,'0'));
    set('#sts-' + a, '0x' + (d.status>>>0).toString(16).padStart(4,'0'));
    set('#unit-' + a, 'адрес ' + d.unitId + (d.online ? '' : ' • нет связи'));
    leds('#in-' + a, d.inputs);
    leds('#out-' + a, d.outputs);
  });
}

function leds(sel, value){
  const c = $(sel);
  if (!c) return;
  if (c.children.length !== 16){
    c.innerHTML = '';
    for (let i=0;i<16;i++){
      const s = document.createElement('span');
      s.className = 'led';
      s.textContent = i;
      s.dataset.bit = i;
      c.appendChild(s);
    }
    if (c.classList.contains('out')){
      c.addEventListener('click', e => {
        const t = e.target.closest('.led');
        if (!t) return;
        const axis = c.id.slice(4); // out-X -> X
        const bit = +t.dataset.bit;
        let mask = 0;
        const drv = (st.drives || []).find(d => (d.name||'').toUpperCase() === axis);
        if (drv) mask = drv.outputs >>> 0;
        mask ^= (1 << bit);
        manual({ axis, action:'outputs', outputs: mask });
      });
    }
  }
  const v = value >>> 0;
  Array.from(c.children).forEach((el,i) => el.classList.toggle('on', !!(v & (1<<i))));
}

/* ============================ команды ============================ */
async function manual(body){
  try {
    const r = await fetch('/api/manual', {
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body: JSON.stringify(body)
    });
    if (!r.ok) console.warn('manual:', (await r.json()).error);
  } catch(e){ console.warn(e); }
}

$('#connSwitch').addEventListener('change', async e => {
  const on = e.target.checked;
  const r = await fetch('/api/connect', {
    method:'POST',
    headers:{'Content-Type':'application/json'},
    body: JSON.stringify({on})
  });
  if (!r.ok){
    e.target.checked = false;
    $('#linkErr').textContent = (await r.json()).error || 'ошибка подключения';
  }
});

$$('[data-man]').forEach(btn => {
  btn.addEventListener('click', () => {
    const axis = btn.dataset.axis;
    const act  = btn.dataset.man;
    const drv  = (st.drives || []).find(d => (d.name||'').toUpperCase() === axis) || {};
    const conf = (cfg.drives || []).find(d => (d.name||'').toUpperCase() === axis) || {};

    switch (act){
      case 'speed-min':
        manual({axis, action:'speed', value: +(conf.minSpeed||1)});
        break;
      case 'speed-max':
        manual({axis, action:'speed', value: +(conf.maxSpeed||100)});
        break;
      case 'speed-set':
        manual({axis, action:'speed', value: parseFloat($('#spdset-'+axis).value) || 0});
        break;
      case 'move-to':
        manual({axis, action:'move-to', value: parseFloat($('#posset-'+axis).value) || 0});
        break;
      case 'home':
        manual({axis, action:'home', value: parseFloat($('#posset-'+axis).value) || 0});
        break;
      case 'stop':
        manual({axis, action:'stop'});
        break;
      case 'enable':
        manual({axis, action:'enable'});
        break;
      case 'disable':
        manual({axis, action:'disable'});
        break;
    }
  });
});

async function scanAction(action){
  await fetch('/api/scan', {
    method:'POST',
    headers:{'Content-Type':'application/json'},
    body: JSON.stringify({action})
  });
}
$('#btnStart').addEventListener('click', () => scanAction('start'));
$('#btnStop').addEventListener('click',  () => scanAction('stop'));
$('#btnReset').addEventListener('click', () => scanAction('reset'));

/* ============================ 3D ============================ */
const canvas = $('#view3d');
const ctx = canvas.getContext('2d');
let dpr = window.devicePixelRatio || 1;

const view = {
  angle: 0,          // вращение вокруг оси цилиндра, рад
  pos: 0,            // смещение вдоль оси, мм (от центра трубы)
  dist: 0,           // расстояние камеры от оси; 0 = авто
  fov: 55 * Math.PI/180,
  follow: false
};
let dragging = false, lastX = 0, lastY = 0;

$('#camFollow').addEventListener('change', e => view.follow = e.target.checked);

function resizeCanvas(){
  const w = canvas.clientWidth, h = canvas.clientHeight;
  if (w === 0 || h === 0) return;
  dpr = window.devicePixelRatio || 1;
  if (canvas.width !== Math.round(w*dpr) || canvas.height !== Math.round(h*dpr)){
    canvas.width  = Math.round(w*dpr);
    canvas.height = Math.round(h*dpr);
  }
  ctx.setTransform(dpr,0,0,dpr,0,0);
}
window.addEventListener('resize', resizeCanvas);

/* векторная алгебра */
const V = {
  add:(a,b)=>[a[0]+b[0],a[1]+b[1],a[2]+b[2]],
  sub:(a,b)=>[a[0]-b[0],a[1]-b[1],a[2]-b[2]],
  mul:(a,s)=>[a[0]*s,a[1]*s,a[2]*s],
  dot:(a,b)=>a[0]*b[0]+a[1]*b[1]+a[2]*b[2],
  cross:(a,b)=>[a[1]*b[2]-a[2]*b[1], a[2]*b[0]-a[0]*b[2], a[0]*b[1]-a[1]*b[0]],
  len:a=>Math.hypot(a[0],a[1],a[2]),
  norm:a=>{const l=V.len(a)||1;return [a[0]/l,a[1]/l,a[2]/l];}
};

function sceneParams(){
  const S = (cfg && cfg.scan) || {};
  const L = +S.pipeLength || 1000;
  const D = +S.pipeDiameter || 500;
  const R = D/2;
  const C = Math.PI * D;
  const vertical = (S.orientation === 'vertical');
  const A  = vertical ? [0,0,1] : [1,0,0];
  const E1 = vertical ? [1,0,0] : [0,1,0];
  const E2 = vertical ? [0,1,0] : [0,0,1];
  return {L, D, R, C, vertical, A, E1, E2};
}

// точка на поверхности: u — мм от начала трубы, theta — угол
function surf(P, u, theta){
  const s = u - P.L/2;
  const c = Math.cos(theta), sn = Math.sin(theta);
  return [
    P.A[0]*s + P.E1[0]*P.R*c + P.E2[0]*P.R*sn,
    P.A[1]*s + P.E1[1]*P.R*c + P.E2[1]*P.R*sn,
    P.A[2]*s + P.E1[2]*P.R*c + P.E2[2]*P.R*sn
  ];
}

function axisPoint(P, u){
  const s = u - P.L/2;
  return [P.A[0]*s, P.A[1]*s, P.A[2]*s];
}

function autoDist(P){
  const W = canvas.clientWidth || 800, H = canvas.clientHeight || 400;
  const fov = view.fov;
  const need1 = (P.L/2 + P.R*0.5) / Math.max(1e-6, (W/H) * Math.tan(fov/2));
  const need2 = (P.R * 1.35) / Math.tan(fov/2);
  return Math.max(need1, need2, P.R*1.6);
}

function makeCam(P){
  const posAlong = view.follow ? (st.posX || 0) - P.L/2 : view.pos;
  const center = V.mul(P.A, posAlong);
  const phi = view.angle;
  const radial = V.add(V.mul(P.E1, Math.cos(phi)), V.mul(P.E2, Math.sin(phi)));
  const d = view.dist > 0 ? view.dist : autoDist(P);
  const camPos = V.add(center, V.mul(radial, d));
  const f = V.norm(V.sub(center, camPos));

  const upHint = P.vertical
      ? P.A
      : V.add(V.mul(P.E1, -Math.sin(phi)), V.mul(P.E2, Math.cos(phi)));

  const r = V.norm(V.cross(f, upHint));
  const u = V.cross(r, f);
  const focal = (canvas.clientHeight/2) / Math.tan(view.fov/2);
  return {camPos, f, r, u, focal, d};
}

function project(p, cam){
  const d = V.sub(p, cam.camPos);
  const z = V.dot(d, cam.f);
  if (z < 1) return null;
  const x = V.dot(d, cam.r);
  const y = V.dot(d, cam.u);
  return [
    canvas.clientWidth/2  + x*cam.focal/z,
    canvas.clientHeight/2 - y*cam.focal/z,
    z
  ];
}

function visible(P, p, camPos){
  const ap = V.mul(P.A, V.dot(p, P.A));
  const n = V.norm(V.sub(p, ap));
  return V.dot(n, V.sub(camPos, p)) > 0;
}

function uThetaToMachine(P, u, theta){
  const t = ((theta % (2*Math.PI)) + 2*Math.PI) % (2*Math.PI);
  return { x: u, y: t * P.D / 2 };
}
function machineToUTheta(P, x, y){
  return { u: x, theta: (2*y / P.D) };
}

/* ---------- отрисовка ---------- */
function draw3D(){
  resizeCanvas();
  const W = canvas.clientWidth, H = canvas.clientHeight;
  if (!W || !H || !cfg) return;

  const P = sceneParams();
  const cam = makeCam(P);

  ctx.clearRect(0, 0, W, H);

  // фон-сетка «пола» не нужен — рисуем торцы
  const gridStep = niceStep(P.L / 12);
  const angStep  = Math.PI / 6;   // 30°

  // --- сетка на поверхности ---
  ctx.lineWidth = 1;
  ctx.strokeStyle = 'rgba(120,160,200,0.28)';

  // кольца
  for (let u = 0; u <= P.L + 1e-6; u += gridStep){
    strokeArc(P, cam, u, 64);
  }
  // продольные линии
  for (let k = 0; k < 12; k++){
    const th = k * angStep;
    const segs = 48;
    ctx.beginPath();
    let started = false;
    for (let i = 0; i <= segs; i++){
      const u = P.L * i / segs;
      const p = surf(P, u, th);
      if (!visible(P, p, cam.camPos)){ started = false; continue; }
      const s = project(p, cam);
      if (!s){ started = false; continue; }
      if (!started){ ctx.moveTo(s[0], s[1]); started = true; }
      else ctx.lineTo(s[0], s[1]);
    }
    ctx.stroke();
  }

  // --- контур трубы (торцы) ---
  ctx.strokeStyle = 'rgba(150,190,230,0.75)';
  ctx.lineWidth = 1.6;
  strokeArc(P, cam, 0, 96);
  strokeArc(P, cam, P.L, 96);

  // --- ось ---
  const a0 = project(axisPoint(P, 0), cam);
  const a1 = project(axisPoint(P, P.L), cam);
  if (a0 && a1){
    ctx.strokeStyle = 'rgba(120,160,200,0.35)';
    ctx.setLineDash([6,6]);
    ctx.beginPath(); ctx.moveTo(a0[0],a0[1]); ctx.lineTo(a1[0],a1[1]); ctx.stroke();
    ctx.setLineDash([]);
  }

  // --- маршрут сканирования ---
  drawRoute(P, cam, false);
  // --- пройденный путь ---
  drawRoute(P, cam, true);

  // --- тележка ---
  const mp = machineToUTheta(P, st.posX || 0, st.posY || 0);
  drawMarker(P, cam, mp.u, mp.theta);

  // рамка-индикация UDP
  if (st.udpOn){
    ctx.strokeStyle = 'rgba(40,199,111,0.6)';
    ctx.lineWidth = 2;
    ctx.strokeRect(1,1,W-2,H-2);
  }
}

function niceStep(v){
  if (!(v > 0)) return 50;
  const e = Math.pow(10, Math.floor(Math.log10(v)));
  const m = v / e;
  let s;
  if (m < 1.5) s = 1;
  else if (m < 3.5) s = 2;
  else if (m < 7.5) s = 5;
  else s = 10;
  return s * e;
}

function strokeArc(P, cam, u, segs){
  ctx.beginPath();
  let started = false;
  for (let i = 0; i <= segs; i++){
    const th = 2*Math.PI*i/segs;
    const p = surf(P, u, th);
    if (!visible(P, p, cam.camPos)){ started = false; continue; }
    const s = project(p, cam);
    if (!s){ started = false; continue; }
    if (!started){ ctx.moveTo(s[0], s[1]); started = true; }
    else ctx.lineTo(s[0], s[1]);
  }
  ctx.stroke();
}

function drawRoute(P, cam, trail){
  const pts = trail ? (st.trail || []) : routeCache;
  if (!pts || !pts.length) return;

  ctx.strokeStyle = trail ? 'rgba(255,176,32,0.95)' : 'rgba(63,167,255,0.85)';
  ctx.lineWidth = trail ? 2.2 : 1.4;
  if (!trail) ctx.setLineDash([5,4]);

  ctx.beginPath();
  let started = false;
  for (const q of pts){
    const mt = machineToUTheta(P, q.x, q.y);
    const p = surf(P, mt.u, mt.theta);
    if (!visible(P, p, cam.camPos)){ started = false; continue; }
    const s = project(p, cam);
    if (!s){ started = false; continue; }
    if (!started){ ctx.moveTo(s[0], s[1]); started = true; }
    else ctx.lineTo(s[0], s[1]);
  }
  ctx.stroke();
  ctx.setLineDash([]);
}

function drawMarker(P, cam, u, theta){
  const p = surf(P, u, theta);
  const s = project(p, cam);
  if (!s) return;
  const near = visible(P, p, cam.camPos);

  ctx.beginPath();
  ctx.arc(s[0], s[1], near ? 7 : 4, 0, Math.PI*2);
  ctx.fillStyle = near ? '#28c76f' : 'rgba(40,199,111,0.35)';
  ctx.fill();
  ctx.lineWidth = 2;
  ctx.strokeStyle = '#0b0f14';
  ctx.stroke();

  // крестик-выноска
  ctx.beginPath();
  ctx.moveTo(s[0]-14, s[1]); ctx.lineTo(s[0]-8, s[1]);
  ctx.moveTo(s[0]+8, s[1]);  ctx.lineTo(s[0]+14, s[1]);
  ctx.moveTo(s[0], s[1]-14); ctx.lineTo(s[0], s[1]-8);
  ctx.moveTo(s[0], s[1]+8);  ctx.lineTo(s[0], s[1]+14);
  ctx.strokeStyle = '#28c76f';
  ctx.lineWidth = 1.5;
  ctx.stroke();
}

let routeCache = [];
async function refreshRoute(){
  try { routeCache = await (await fetch('/api/route')).json(); } catch(_){}
}
setInterval(refreshRoute, 3000);

function renderLoop(){
  draw3D();
  requestAnimationFrame(renderLoop);
}

/* ---------- управление камерой ---------- */
canvas.addEventListener('mousedown', e => {
  dragging = true; lastX = e.clientX; lastY = e.clientY;
});
window.addEventListener('mouseup', () => dragging = false);
window.addEventListener('mousemove', e => {
  if (!dragging) return;
  const dx = e.clientX - lastX, dy = e.clientY - lastY;
  lastX = e.clientX; lastY = e.clientY;
  view.angle -= dx * 0.008;
  const P = sceneParams();
  view.follow = false;
  $('#camFollow').checked = false;
  view.pos += dy * (P.L / 500);
  const lim = P.L * 0.75;
  view.pos = Math.max(-lim, Math.min(lim, view.pos));
});

canvas.addEventListener('wheel', e => {
  e.preventDefault();
  const P = sceneParams();
  const base = view.dist > 0 ? view.dist : autoDist(P);
  view.dist = Math.max(P.R*1.2, Math.min(base * (e.deltaY > 0 ? 1.12 : 0.89), P.L * 6));
}, {passive:false});

window.addEventListener('keydown', e => {
  const P = sceneParams();
  const step = P.L / 60;
  switch (e.key){
    case 'ArrowLeft':  view.follow=false; $('#camFollow').checked=false; view.pos -= step; break;
    case 'ArrowRight': view.follow=false; $('#camFollow').checked=false; view.pos += step; break;
    case 'ArrowUp':    view.angle += 0.06; break;
    case 'ArrowDown':  view.angle -= 0.06; break;
    default: return;
  }
  e.preventDefault();
});

/* ---------- выбор точки на цилиндре ---------- */
function pickPoint(sx, sy){
  const P = sceneParams();
  const cam = makeCam(P);

  const x = (sx - canvas.clientWidth/2)  / cam.focal;
  const y = -(sy - canvas.clientHeight/2) / cam.focal;
  const dir = V.norm(V.add(V.add(V.mul(cam.r, x), V.mul(cam.u, y)), cam.f));

  const o = cam.camPos;
  const oA = V.dot(o, P.A);
  const dA = V.dot(dir, P.A);
  const oP = V.sub(o, V.mul(P.A, oA));
  const dP = V.sub(dir, V.mul(P.A, dA));

  const a = V.dot(dP, dP);
  const b = 2 * V.dot(oP, dP);
  const c = V.dot(oP, oP) - P.R*P.R;
  const disc = b*b - 4*a*c;
  if (a < 1e-9 || disc < 0) return null;

  const sq = Math.sqrt(disc);
  const t1 = (-b - sq) / (2*a);
  const t2 = (-b + sq) / (2*a);
  const t = (t1 > 0.01) ? t1 : t2;
  if (t <= 0.01) return null;

  const hit = V.add(o, V.mul(dir, t));
  const u = V.dot(hit, P.A) + P.L/2;
  if (u < 0 || u > P.L) return null;

  const rel = V.sub(hit, V.mul(P.A, V.dot(hit, P.A)));
  const th = Math.atan2(V.dot(rel, P.E2), V.dot(rel, P.E1));
  return uThetaToMachine(P, u, th);
}

canvas.addEventListener('click', e => {
  if (Math.abs(e.clientX - lastX) > 3) return;
  const r = canvas.getBoundingClientRect();
  const hit = pickPoint(e.clientX - r.left, e.clientY - r.top);
  if (!hit) return;
  showPopup(e.clientX - r.left, e.clientY - r.top, hit);
});

function showPopup(px, py, pt){
  const pop = $('#pickPopup');
  pop.innerHTML = `
    <h5>Точка на трубе</h5>
    <div>X = <b>${pt.x.toFixed(1)}</b> мм</div>
    <div>Y = <b>${pt.y.toFixed(1)}</b> мм</div>
    <div class="row">
      <button id="pkGo">Ехать к точке</button>
      <button id="pkHome">Привязка</button>
      <button id="pkClose">Отмена</button>
    </div>`;
  pop.style.left = Math.min(px + 10, canvas.clientWidth - 210) + 'px';
  pop.style.top  = Math.min(py + 10, canvas.clientHeight - 140) + 'px';
  pop.classList.remove('hidden');

  $('#pkGo').onclick = () => {
    manual({axis:'X', action:'move-to', value: pt.x});
    manual({axis:'Y', action:'move-to', value: pt.y});
    pop.classList.add('hidden');
  };
  $('#pkHome').onclick = () => {
    manual({axis:'X', action:'home', value: pt.x});
    manual({axis:'Y', action:'home', value: pt.y});
    pop.classList.add('hidden');
  };
  $('#pkClose').onclick = () => pop.classList.add('hidden');
}

/* ============================ запуск ============================ */
(async function init(){
  await loadConfig();
  bindInputs();
  refreshRoute();
  connectSSE();
  resizeCanvas();
  renderLoop();
})();