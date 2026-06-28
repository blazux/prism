// Canvas animated backgrounds, tinted from the active theme tokens. One effect
// per theme (light/dark keep their plain CSS gradient). Driven by the
// 'prism-theme-change' event from theme.js; intensity via --bgfx-opacity (#bg-fx).
//
//   cyberpunk → circuit   matrix → matrix     midnight → stars
//   nord      → snow       solarized-dark → bokeh   rose-pine → petals
//   tech      → network    synthwave → grid

(function () {
  'use strict';

  const GS = 32; // grid step for circuit traces
  const EFFECTS = {
    cyberpunk: 'circuit', matrix: 'matrix',
    midnight: 'stars', nord: 'snow', 'solarized-dark': 'bokeh',
    'rose-pine': 'petals', tech: 'network', synthwave: 'grid',
  };

  let canvas, ctx, w = 0, h = 0;
  let traces = [], pads = [], pulses = [];   // circuit
  let drops = [], matrixSpeed = [];          // matrix
  let parts = [], shooters = [], gridPhase = 0; // generic particle effects
  let effect = null, raf = null, active = false;

  let colAccent = '#7c4dff', colCyan = '#21e6ff', colText = '#e8e8f0', colWarm = '#ff9e64';
  const FONT = 16;
  const GLYPHS = 'ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎ0123456789:.=*+<>';
  let colRain = '#39ff14', colLead = '#d7ffd7', colBgFade = 'rgba(0,0,0,0.08)';

  const reduceMotion = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  function rand(a, b) { return a + Math.random() * (b - a); }

  function hexToRgba(hex, a) {
    hex = (hex || '').trim().replace('#', '');
    if (hex.length === 3) hex = hex.split('').map((c) => c + c).join('');
    const n = parseInt(hex, 16);
    if (isNaN(n)) return `rgba(124,77,255,${a})`;
    return `rgba(${(n >> 16) & 255},${(n >> 8) & 255},${n & 255},${a})`;
  }

  function ensureCanvas() {
    const host = document.getElementById('bg-fx');
    if (!host) return false;
    if (!canvas) {
      canvas = document.createElement('canvas');
      canvas.id = 'bgfx-canvas';
      host.appendChild(canvas);
      ctx = canvas.getContext('2d');
      window.addEventListener('resize', onResize);
    }
    return true;
  }

  function onResize() {
    if (!canvas || !active) return;
    sizeCanvas();
    spawn();
    if (reduceMotion) drawStatic();
  }

  function sizeCanvas() {
    const host = canvas.parentElement;
    w = canvas.width = host.clientWidth;
    h = canvas.height = host.clientHeight;
  }

  function readColors() {
    const cs = getComputedStyle(document.documentElement);
    colAccent = cs.getPropertyValue('--accent').trim() || '#7c4dff';
    colCyan = cs.getPropertyValue('--green').trim() || '#21e6ff';
    colText = cs.getPropertyValue('--text').trim() || '#e8e8f0';
    colWarm = cs.getPropertyValue('--orange').trim() || cs.getPropertyValue('--yellow').trim() || colAccent;
    // matrix uses the (green) accent for glyphs; trails fade toward the bg.
    colRain = colAccent;
    colLead = '#e9ffe9';
    colBgFade = hexToRgba(cs.getPropertyValue('--bg').trim() || '#000', 0.06);
  }

  // ─── Circuit (cyberpunk) ──────────────────────────────────────────────────
  function traceLen(pts) {
    let L = 0;
    for (let i = 0; i < pts.length - 1; i++) L += Math.hypot(pts[i + 1].x - pts[i].x, pts[i + 1].y - pts[i].y);
    return L;
  }
  function posOnTrace(pts, dist) {
    for (let i = 0; i < pts.length - 1; i++) {
      const a = pts[i], b = pts[i + 1];
      const seg = Math.hypot(b.x - a.x, b.y - a.y);
      if (dist <= seg) { const u = seg ? dist / seg : 0; return { x: a.x + (b.x - a.x) * u, y: a.y + (b.y - a.y) * u }; }
      dist -= seg;
    }
    return pts[pts.length - 1];
  }
  function makePulse(ti) {
    const len = traceLen(traces[ti]);
    return { ti, len, dist: Math.random() * len, speed: 0.9 + Math.random() * 1.8, color: Math.random() < 0.5 ? colAccent : colCyan };
  }
  function genCircuit() {
    traces = []; pads = [];
    const C = Math.max(2, Math.floor(w / GS)), R = Math.max(2, Math.floor(h / GS));
    const n = Math.max(10, Math.round((C * R) / 7));
    for (let k = 0; k < n; k++) {
      let cx = (Math.random() * C) | 0, cy = (Math.random() * R) | 0;
      let d = [[1, 0], [-1, 0], [0, 1], [0, -1]][(Math.random() * 4) | 0];
      const pts = [{ x: cx * GS, y: cy * GS }];
      const len = 3 + ((Math.random() * 8) | 0);
      for (let i = 0; i < len; i++) {
        if (Math.random() < 0.45) d = Math.random() < 0.5 ? [d[1], -d[0]] : [-d[1], d[0]];
        cx += d[0]; cy += d[1];
        if (cx < 0 || cx > C || cy < 0 || cy > R) break;
        pts.push({ x: cx * GS, y: cy * GS });
      }
      if (pts.length >= 2) {
        traces.push(pts);
        pads.push(pts[0], pts[pts.length - 1]);
        if (pts.length > 3 && Math.random() < 0.5) pads.push(pts[(pts.length / 2) | 0]);
      }
    }
    pulses = [];
    for (let i = 0; i < traces.length; i++) if (Math.random() < 0.55) pulses.push(makePulse(i));
  }
  function drawCircuit(alpha) {
    ctx.strokeStyle = hexToRgba(colAccent, alpha);
    ctx.lineWidth = 1.4; ctx.lineJoin = 'round'; ctx.lineCap = 'round';
    ctx.beginPath();
    for (const pts of traces) {
      ctx.moveTo(pts[0].x + 0.5, pts[0].y + 0.5);
      for (let i = 1; i < pts.length; i++) ctx.lineTo(pts[i].x + 0.5, pts[i].y + 0.5);
    }
    ctx.stroke();
    ctx.fillStyle = hexToRgba(colAccent, Math.min(1, alpha * 1.8));
    for (const p of pads) { ctx.beginPath(); ctx.arc(p.x, p.y, 2.4, 0, Math.PI * 2); ctx.fill(); }
  }
  function drawPulse(pl) {
    const p = posOnTrace(traces[pl.ti], pl.dist);
    ctx.shadowColor = pl.color; ctx.shadowBlur = 12; ctx.fillStyle = pl.color;
    ctx.beginPath(); ctx.arc(p.x, p.y, 2.0, 0, Math.PI * 2); ctx.fill();
  }
  function frameCircuit() {
    ctx.globalCompositeOperation = 'destination-out';
    ctx.fillStyle = 'rgba(0,0,0,0.12)';
    ctx.fillRect(0, 0, w, h);
    ctx.globalCompositeOperation = 'source-over';
    ctx.shadowBlur = 0;
    drawCircuit(0.03);
    for (const pl of pulses) {
      pl.dist += pl.speed;
      if (pl.dist > pl.len) { pl.dist = 0; pl.color = Math.random() < 0.5 ? colAccent : colCyan; }
      drawPulse(pl);
    }
    ctx.shadowBlur = 0;
  }
  function staticCircuit() { drawCircuit(0.16); ctx.shadowBlur = 0; for (const pl of pulses) drawPulse(pl); }

  // ─── Matrix ───────────────────────────────────────────────────────────────
  function spawnMatrix() {
    const c = Math.ceil(w / FONT);
    drops = new Array(c); matrixSpeed = new Array(c);
    for (let i = 0; i < c; i++) { drops[i] = Math.random() * (h / FONT); matrixSpeed[i] = 0.14 + Math.random() * 0.26; }
  }
  function frameMatrix() {
    ctx.fillStyle = colBgFade; ctx.fillRect(0, 0, w, h);
    ctx.font = FONT + 'px monospace'; ctx.textBaseline = 'top';
    for (let i = 0; i < drops.length; i++) {
      const prevRow = Math.floor(drops[i]);
      drops[i] += matrixSpeed[i];
      const row = Math.floor(drops[i]);
      if (row === prevRow) continue;
      const y = row * FONT;
      ctx.fillStyle = Math.random() < 0.12 ? colLead : colRain;
      ctx.fillText(GLYPHS[(Math.random() * GLYPHS.length) | 0], i * FONT, y);
      if (y > h && Math.random() > 0.96) { drops[i] = 0; matrixSpeed[i] = 0.14 + Math.random() * 0.26; }
    }
  }
  function staticMatrix() {
    ctx.font = FONT + 'px monospace'; ctx.textBaseline = 'top'; ctx.fillStyle = colRain;
    for (let i = 0; i < drops.length; i++) ctx.fillText(GLYPHS[(Math.random() * GLYPHS.length) | 0], i * FONT, drops[i] * FONT);
  }

  // ─── Stars (midnight) ──────────────────────────────────────────────────────
  function spawnStars() {
    const n = Math.round(w * h / 7000);
    parts = []; shooters = [];
    for (let i = 0; i < n; i++) parts.push({ x: Math.random() * w, y: Math.random() * h, r: rand(0.4, 1.7), tw: Math.random() * 6.28, sp: rand(0.01, 0.04), dx: rand(-0.04, 0.04) });
  }
  function frameStars() {
    ctx.clearRect(0, 0, w, h);
    for (const s of parts) {
      s.tw += s.sp; s.x += s.dx; if (s.x < 0) s.x += w; else if (s.x > w) s.x -= w;
      const a = 0.3 + 0.5 * (0.5 + 0.5 * Math.sin(s.tw));
      ctx.fillStyle = hexToRgba(colText, a);
      ctx.beginPath(); ctx.arc(s.x, s.y, s.r, 0, Math.PI * 2); ctx.fill();
    }
    if (shooters.length < 2 && Math.random() < 0.006) shooters.push({ x: rand(0, w), y: rand(0, h * 0.5), vx: rand(4, 7), vy: rand(2, 3.4) });
    for (let i = shooters.length - 1; i >= 0; i--) {
      const sh = shooters[i], tx = sh.x - sh.vx * 8, ty = sh.y - sh.vy * 8;
      const g = ctx.createLinearGradient(sh.x, sh.y, tx, ty);
      g.addColorStop(0, hexToRgba(colText, 0.85)); g.addColorStop(1, hexToRgba(colText, 0));
      ctx.strokeStyle = g; ctx.lineWidth = 2;
      ctx.beginPath(); ctx.moveTo(sh.x, sh.y); ctx.lineTo(tx, ty); ctx.stroke();
      sh.x += sh.vx; sh.y += sh.vy;
      if (sh.x > w || sh.y > h) shooters.splice(i, 1);
    }
  }

  // ─── Snow (nord) ───────────────────────────────────────────────────────────
  function spawnSnow() {
    const n = Math.round(w * h / 6000);
    parts = [];
    for (let i = 0; i < n; i++) parts.push({ x: Math.random() * w, y: Math.random() * h, r: rand(0.8, 2.6), sp: rand(0.3, 1.1), sway: rand(8, 22), ph: Math.random() * 6.28 });
  }
  function frameSnow() {
    ctx.clearRect(0, 0, w, h);
    ctx.fillStyle = hexToRgba(colText, 0.85);
    for (const p of parts) {
      p.y += p.sp; p.ph += 0.01;
      if (p.y > h + 4) { p.y = -4; p.x = Math.random() * w; }
      const x = p.x + Math.sin(p.ph) * p.sway;
      ctx.globalAlpha = 0.45 + p.r / 4;
      ctx.beginPath(); ctx.arc(x, p.y, p.r, 0, Math.PI * 2); ctx.fill();
    }
    ctx.globalAlpha = 1;
  }

  // ─── Bokeh (solarized-dark) ────────────────────────────────────────────────
  function spawnBokeh() {
    parts = [];
    const cols = [colAccent, colCyan, colWarm];
    for (let i = 0; i < 16; i++) parts.push({ x: Math.random() * w, y: Math.random() * h, r: rand(30, 95), vx: rand(-0.22, 0.22), vy: rand(-0.22, 0.22), col: cols[(Math.random() * cols.length) | 0], a: rand(0.05, 0.15), ph: Math.random() * 6.28 });
  }
  function frameBokeh() {
    ctx.clearRect(0, 0, w, h);
    ctx.globalCompositeOperation = 'lighter';
    for (const p of parts) {
      p.x += p.vx; p.y += p.vy; p.ph += 0.01;
      if (p.x < -p.r) p.x = w + p.r; else if (p.x > w + p.r) p.x = -p.r;
      if (p.y < -p.r) p.y = h + p.r; else if (p.y > h + p.r) p.y = -p.r;
      const a = p.a * (0.6 + 0.4 * Math.sin(p.ph));
      const g = ctx.createRadialGradient(p.x, p.y, 0, p.x, p.y, p.r);
      g.addColorStop(0, hexToRgba(p.col, a)); g.addColorStop(1, hexToRgba(p.col, 0));
      ctx.fillStyle = g; ctx.beginPath(); ctx.arc(p.x, p.y, p.r, 0, Math.PI * 2); ctx.fill();
    }
    ctx.globalCompositeOperation = 'source-over';
  }

  // ─── Petals (rose-pine) ────────────────────────────────────────────────────
  function spawnPetals() {
    const n = Math.round(w * h / 16000);
    parts = [];
    for (let i = 0; i < n; i++) parts.push({ x: Math.random() * w, y: Math.random() * h, sp: rand(0.4, 1.0), sway: rand(10, 26), ph: Math.random() * 6.28, rot: Math.random() * 6.28, rs: rand(-0.02, 0.02), s: rand(4, 8) });
  }
  function framePetals() {
    ctx.clearRect(0, 0, w, h);
    ctx.fillStyle = hexToRgba(colAccent, 0.5);
    for (const p of parts) {
      p.y += p.sp; p.ph += 0.012; p.rot += p.rs;
      if (p.y > h + 8) { p.y = -8; p.x = Math.random() * w; }
      const x = p.x + Math.sin(p.ph) * p.sway;
      ctx.save(); ctx.translate(x, p.y); ctx.rotate(p.rot);
      ctx.beginPath(); ctx.ellipse(0, 0, p.s, p.s * 0.5, 0, 0, Math.PI * 2); ctx.fill();
      ctx.restore();
    }
  }

  // ─── Network / constellation (tech) ────────────────────────────────────────
  function spawnNetwork() {
    const n = Math.min(90, Math.round(w * h / 16000));
    parts = [];
    for (let i = 0; i < n; i++) parts.push({ x: Math.random() * w, y: Math.random() * h, vx: rand(-0.3, 0.3), vy: rand(-0.3, 0.3) });
  }
  function frameNetwork() {
    ctx.clearRect(0, 0, w, h);
    for (const p of parts) { p.x += p.vx; p.y += p.vy; if (p.x < 0 || p.x > w) p.vx *= -1; if (p.y < 0 || p.y > h) p.vy *= -1; }
    const D2 = 130 * 130;
    ctx.lineWidth = 1;
    for (let i = 0; i < parts.length; i++) for (let j = i + 1; j < parts.length; j++) {
      const a = parts[i], b = parts[j], dx = a.x - b.x, dy = a.y - b.y, d2 = dx * dx + dy * dy;
      if (d2 < D2) { ctx.strokeStyle = hexToRgba(colAccent, (1 - d2 / D2) * 0.5); ctx.beginPath(); ctx.moveTo(a.x, a.y); ctx.lineTo(b.x, b.y); ctx.stroke(); }
    }
    ctx.fillStyle = hexToRgba(colAccent, 0.85);
    for (const p of parts) { ctx.beginPath(); ctx.arc(p.x, p.y, 1.6, 0, Math.PI * 2); ctx.fill(); }
  }

  // ─── Retro grid (synthwave) ────────────────────────────────────────────────
  function frameGrid() {
    ctx.clearRect(0, 0, w, h);
    const horizon = h * 0.46, cx = w / 2, sunR = Math.min(w, h) * 0.16;
    const sg = ctx.createLinearGradient(cx, horizon - sunR * 1.4, cx, horizon);
    sg.addColorStop(0, hexToRgba(colWarm, 0.5)); sg.addColorStop(1, hexToRgba(colAccent, 0.04));
    ctx.fillStyle = sg; ctx.beginPath(); ctx.arc(cx, horizon - sunR * 0.4, sunR, 0, Math.PI * 2); ctx.fill();
    ctx.lineWidth = 1.2;
    const cols = 14;
    for (let i = -cols; i <= cols; i++) {
      ctx.strokeStyle = hexToRgba(colAccent, 0.16);
      ctx.beginPath(); ctx.moveTo(cx, horizon); ctx.lineTo(cx + (i / cols) * w * 1.1, h); ctx.stroke();
    }
    gridPhase = (gridPhase + 0.006) % 1;
    const rows = 18;
    for (let k = 0; k < rows; k++) {
      const t = (k / rows + gridPhase) % 1, y = horizon + (h - horizon) * (t * t);
      ctx.strokeStyle = hexToRgba(colCyan, 0.05 + t * 0.22);
      ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(w, y); ctx.stroke();
    }
  }

  // ─── Effect registry + dispatch ────────────────────────────────────────────
  const effects = {
    circuit: { spawn: genCircuit, frame: frameCircuit, static: staticCircuit },
    matrix: { spawn: spawnMatrix, frame: frameMatrix, static: staticMatrix },
    stars: { spawn: spawnStars, frame: frameStars },
    snow: { spawn: spawnSnow, frame: frameSnow },
    bokeh: { spawn: spawnBokeh, frame: frameBokeh },
    petals: { spawn: spawnPetals, frame: framePetals },
    network: { spawn: spawnNetwork, frame: frameNetwork },
    grid: { spawn: function () { gridPhase = 0; }, frame: frameGrid },
  };

  function spawn() { const e = effects[effect]; if (e && e.spawn) e.spawn(); }
  function drawStatic() {
    ctx.clearRect(0, 0, w, h);
    const e = effects[effect]; if (!e) return;
    (e.static || e.frame)();
  }
  function frame() {
    if (!active) return;
    const e = effects[effect]; if (e && e.frame) e.frame();
    raf = requestAnimationFrame(frame);
  }

  function start() {
    if (active) return;
    active = true;
    canvas.style.display = 'block';
    readColors(); sizeCanvas(); spawn();
    if (reduceMotion) { drawStatic(); return; }
    raf = requestAnimationFrame(frame);
  }
  function stop() {
    active = false;
    if (raf) { cancelAnimationFrame(raf); raf = null; }
    if (canvas) { ctx.clearRect(0, 0, w, h); canvas.style.display = 'none'; }
  }

  function update() {
    const id = document.documentElement.getAttribute('data-theme');
    const eff = EFFECTS[id] || null;
    if (eff) {
      if (!ensureCanvas()) return;
      if (active) {
        effect = eff;
        ctx.clearRect(0, 0, w, h);
        readColors(); spawn();
        if (reduceMotion) drawStatic();
      } else { effect = eff; start(); }
    } else { stop(); }
  }

  window.addEventListener('prism-theme-change', update);
  update();
})();
