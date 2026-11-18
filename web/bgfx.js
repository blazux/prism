// Canvas animated backgrounds, tinted from the active theme tokens. One effect
// per theme (light/dark keep their plain CSS gradient). Driven by the
// 'prism-theme-change' event from theme.js; intensity via --bgfx-opacity (#bg-fx).
//
//   cyberpunk → circuit   matrix → matrix     midnight → stars
//   nord      → snow       solarized-dark → bokeh   rose-pine → petals
//   tech      → network    synthwave → grid
//   espresso/dracula/bubblegum → bokeh     amber-terminal → amber glyphs
//   Professional palettes stay still; paper, dune and arcade use CSS textures.

(function () {
  'use strict';

  const EFFECTS = {
    cyberpunk: 'circuit', matrix: 'matrix',
    midnight: 'stars', nord: 'snow', 'solarized-dark': 'bokeh',
    'rose-pine': 'petals', tech: 'network', synthwave: 'grid',
    espresso: 'bokeh', dracula: 'bokeh', 'amber-terminal': 'matrix', bubblegum: 'bokeh',
  };

  let canvas, ctx, w = 0, h = 0;
  let traces = [], pads = [], chips = [], pulses = []; // circuit
  let circuitBoard, circuitTime = null;
  let drops = [], matrixSpeed = [];          // matrix
  let parts = [], shooters = [], gridPhase = 0; // generic particle effects
  let effect = null, raf = null, active = false;

  let colAccent = '#7c4dff', colCyan = '#21e6ff', colText = '#e8e8f0', colWarm = '#ff9e64';
  const FONT = 16;
  const GLYPHS = 'ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎ0123456789:.=*+<>';
  let glyphs = GLYPHS;
  let colRain = '#39ff14', colLead = '#d7ffd7', colBgFade = 'rgba(0,0,0,0.08)';

  const motion = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)');
  let reduceMotion = !!(motion && motion.matches);
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
    colLead = colText;
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
  // Route parallel buses between chip pins. All bends are 45 degrees, and the
  // horizontal/vertical corridors meet at components rather than random junctions.
  function circuitBus(a, b, vertical, count, side, terminals = 0) {
    const gap = b.u - a.u;
    if (gap <= 0) return;
    const bend = Math.min(52, gap * 0.16);
    const middle = (a.v + b.v) / 2 + side * bend;
    const diagonal = Math.abs(middle - a.v) + Math.abs(b.v - middle);
    const lead = Math.min(26, gap * 0.12, (gap - diagonal) / 2);
    if (lead <= 0) return;
    for (let lane = 0; lane < count; lane++) {
      const offset = (lane - (count - 1) / 2) * 8;
      const points = [
        [a.u, a.v], [a.u + lead, a.v],
        [a.u + lead + Math.abs(middle - a.v), middle],
        [b.u - lead - Math.abs(b.v - middle), middle],
        [b.u - lead, b.v], [b.u, b.v],
      ].map(([u, v]) => vertical ? { x: v + offset, y: u } : { x: u, y: v + offset });
      traces.push(points);
      if (terminals & 1) pads.push(points[0]);
      if (terminals & 2) pads.push(points[points.length - 1]);
    }
  }
  function genCircuit() {
    traces = []; pads = []; chips = []; pulses = []; circuitTime = null;
    const cols = Math.max(1, Math.min(6, Math.round(w / 480)));
    const rows = Math.max(1, Math.min(4, Math.round(h / 340)));
    const sx = w / cols, sy = h / rows;
    for (let r = 0; r < rows; r++) {
      const row = [];
      for (let c = 0; c < cols; c++) {
        const chip = {
          x: Math.round((c + 0.5) * sx + (r % 2 ? 24 : -24)),
          y: Math.round((r + 0.5) * sy + (c % 2 ? 18 : -18)),
        };
        chips.push(chip); row.push(chip);
      }
      for (let c = 0; c <= cols; c++) {
        const left = row[c - 1], right = row[c];
        circuitBus(
          { u: left ? left.x + 47 : 24, v: left ? left.y : right.y },
          { u: right ? right.x - 47 : w - 24, v: right ? right.y : left.y },
          false, 6, r % 2 ? -1 : 1, (left ? 0 : 1) | (right ? 0 : 2)
        );
      }
    }
    for (let c = 0; c < cols; c++) {
      for (let r = 0; r <= rows; r++) {
        const top = chips[(r - 1) * cols + c], bottom = chips[r * cols + c];
        circuitBus(
          { u: top ? top.y + 39 : 24, v: top ? top.x : bottom.x },
          { u: bottom ? bottom.y - 39 : h - 24, v: bottom ? bottom.x : top.x },
          true, 4, c % 2 ? 1 : -1, (top ? 0 : 1) | (bottom ? 0 : 2)
        );
      }
    }
    // Keep most of the board quiet; each signal has a pause between journeys.
    const routes = traces.map((_, i) => i);
    const count = Math.min(16, Math.max(4, Math.round(w * h / 85000)), routes.length);
    for (let i = 0; i < count; i++) {
      const ti = routes.splice(Math.floor(Math.random() * routes.length), 1)[0];
      const len = traceLen(traces[ti]), tail = rand(46, 78), cycle = len + tail + rand(140, 300);
      pulses.push({ ti, len, tail, cycle, dist: rand(0, cycle), speed: rand(65, 100), color: i % 4 ? colAccent : colCyan });
    }
    // Etched copper, pads and components are painted once, not on every frame.
    circuitBoard = document.createElement('canvas');
    circuitBoard.width = w; circuitBoard.height = h;
    paintCircuit(circuitBoard.getContext('2d'));
  }
  function paintCircuit(board) {
    board.lineJoin = 'round'; board.lineCap = 'round';
    board.strokeStyle = hexToRgba(colCyan, 0.22); board.lineWidth = 1;
    board.beginPath();
    for (const pts of traces) {
      board.moveTo(pts[0].x, pts[0].y);
      for (let i = 1; i < pts.length; i++) board.lineTo(pts[i].x, pts[i].y);
    }
    board.stroke();
    board.strokeStyle = hexToRgba(colCyan, 0.42); board.lineWidth = 1.2;
    for (const p of pads) { board.beginPath(); board.arc(p.x, p.y, 2.6, 0, Math.PI * 2); board.stroke(); }
    for (let i = 0; i < chips.length; i++) {
      const { x, y } = chips[i];
      board.strokeStyle = hexToRgba(colCyan, 0.36); board.lineWidth = 2;
      board.beginPath();
      for (let pin = -20; pin <= 20; pin += 8) {
        board.moveTo(x - 47, y + pin); board.lineTo(x - 40, y + pin);
        board.moveTo(x + 40, y + pin); board.lineTo(x + 47, y + pin);
      }
      for (let pin = -12; pin <= 12; pin += 8) {
        board.moveTo(x + pin, y - 39); board.lineTo(x + pin, y - 32);
        board.moveTo(x + pin, y + 32); board.lineTo(x + pin, y + 39);
      }
      board.stroke();
      board.fillStyle = hexToRgba(colCyan, 0.025); board.fillRect(x - 40, y - 32, 80, 64);
      board.strokeStyle = hexToRgba(colCyan, 0.26); board.lineWidth = 1;
      board.strokeRect(x - 40, y - 32, 80, 64);
      board.strokeStyle = hexToRgba(colCyan, 0.10); board.strokeRect(x - 27, y - 19, 54, 38);
      board.fillStyle = hexToRgba(colCyan, 0.38);
      board.beginPath(); board.arc(x - 33, y - 25, 1.5, 0, Math.PI * 2); board.fill();
      board.font = '9px monospace'; board.textAlign = 'center'; board.textBaseline = 'middle';
      board.fillText('U' + String(i + 1).padStart(2, '0'), x, y);
    }
  }
  function strokeTraceRange(pts, from, to) {
    if (to <= from) return;
    const start = posOnTrace(pts, from);
    ctx.beginPath(); ctx.moveTo(start.x, start.y);
    let distance = 0;
    for (let i = 1; i < pts.length; i++) {
      distance += Math.hypot(pts[i].x - pts[i - 1].x, pts[i].y - pts[i - 1].y);
      if (distance <= from) continue;
      if (distance >= to) { const end = posOnTrace(pts, to); ctx.lineTo(end.x, end.y); break; }
      ctx.lineTo(pts[i].x, pts[i].y);
    }
    ctx.stroke();
  }
  function drawPulse(pl) {
    if (pl.dist > pl.len + pl.tail) return;
    const pts = traces[pl.ti];
    ctx.shadowColor = pl.color; ctx.shadowBlur = 7;
    ctx.lineWidth = 2; ctx.lineJoin = 'round'; ctx.lineCap = 'round';
    // Draw the tail along the path itself so it stays on the copper at corners.
    for (let i = 0; i < 8; i++) {
      const from = Math.max(0, pl.dist - pl.tail + pl.tail * i / 8);
      const to = Math.min(pl.len, pl.dist - pl.tail + pl.tail * (i + 1) / 8);
      ctx.strokeStyle = hexToRgba(pl.color, 0.75 * ((i + 1) / 8) ** 2);
      strokeTraceRange(pts, from, to);
    }
    if (pl.dist <= pl.len) {
      const p = posOnTrace(pts, pl.dist);
      ctx.fillStyle = hexToRgba(colText, 0.9);
      ctx.beginPath(); ctx.arc(p.x, p.y, 1.4, 0, Math.PI * 2); ctx.fill();
    }
    ctx.shadowBlur = 0;
  }
  function staticCircuit() {
    if (!w || !h) return;
    ctx.clearRect(0, 0, w, h);
    ctx.drawImage(circuitBoard, 0, 0);
    for (const pl of pulses) drawPulse(pl);
  }
  function frameCircuit(time) {
    // Pixels per second, independent of a 60/120/144 Hz display. Clamp after a
    // stalled frame; hidden pages stop through the shared lifecycle below.
    const dt = circuitTime === null ? 0 : Math.min(0.05, Math.max(0, (time - circuitTime) / 1000));
    circuitTime = time;
    for (const pl of pulses) pl.dist = (pl.dist + pl.speed * dt) % pl.cycle;
    staticCircuit();
  }

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
      ctx.fillText(glyphs[(Math.random() * glyphs.length) | 0], i * FONT, y);
      if (y > h && Math.random() > 0.96) { drops[i] = 0; matrixSpeed[i] = 0.14 + Math.random() * 0.26; }
    }
  }
  function staticMatrix() {
    ctx.font = FONT + 'px monospace'; ctx.textBaseline = 'top'; ctx.fillStyle = colRain;
    for (let i = 0; i < drops.length; i++) ctx.fillText(glyphs[(Math.random() * glyphs.length) | 0], i * FONT, drops[i] * FONT);
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
    gridPhase = (gridPhase + 0.002) % 1;
    const rows = 18;
    for (let k = 0; k < rows; k++) {
      const t = (k / rows + gridPhase) % 1, y = horizon + (h - horizon) * (t * t);
      ctx.strokeStyle = hexToRgba(colCyan, 0.05 + t * 0.22);
      ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(w, y); ctx.stroke();
    }
  }

  // ─── Sparkline (slate) — a quiet monitoring chart scrolling along the bottom ─
  function sparkNext(s) {
    s.v = Math.max(-1, Math.min(1, s.v + rand(-0.5, 0.5)));
    s.y += s.v * 2.2;
    const lo = s.base - s.amp, hi = s.base + s.amp;
    if (s.y < lo) { s.y = lo; s.v = Math.abs(s.v); }
    if (s.y > hi) { s.y = hi; s.v = -Math.abs(s.v); }
    return s.y;
  }
  function spawnSparkline() {
    parts = [];
    const cols = [colAccent, colCyan, colWarm];
    for (let k = 0; k < 3; k++) {
      const s = { col: cols[k], base: h * (0.68 + 0.09 * k), amp: h * 0.045, step: 7, sp: 0.3 + 0.12 * k, off: 0, pts: [], y: 0, v: 0 };
      s.y = s.base;
      const m = Math.ceil(w / s.step) + 2;
      for (let i = 0; i < m; i++) s.pts.push(sparkNext(s));
      parts.push(s);
    }
  }
  function frameSparkline() {
    ctx.clearRect(0, 0, w, h);
    ctx.lineWidth = 1.2; ctx.lineJoin = 'round';
    for (const s of parts) {
      s.off += s.sp;
      while (s.off >= s.step) { s.off -= s.step; s.pts.shift(); s.pts.push(sparkNext(s)); }
      ctx.strokeStyle = hexToRgba(s.col, 0.13);
      ctx.beginPath();
      for (let i = 0; i < s.pts.length; i++) {
        const x = i * s.step - s.off;
        if (i === 0) ctx.moveTo(x, s.pts[i]); else ctx.lineTo(x, s.pts[i]);
      }
      ctx.stroke();
      // the "live" tip
      ctx.fillStyle = hexToRgba(s.col, 0.4);
      ctx.beginPath(); ctx.arc((s.pts.length - 1) * s.step - s.off, s.pts[s.pts.length - 1], 1.8, 0, Math.PI * 2); ctx.fill();
    }
  }

  // ─── Skyline (boardroom) — office towers at dusk, a few windows still lit ──
  function spawnSkyline() {
    parts = [];
    for (const layer of [1, 0]) {           // far first, so the near row draws on top
      let x = rand(-30, 0);
      while (x < w + 20) {
        const bw = rand(34, 90), bh = h * (layer ? rand(0.07, 0.15) : rand(0.1, 0.24));
        const b = { x, bw, bh, layer, wins: [] };
        if (!layer) {
          for (let wx = x + 6; wx < x + bw - 9; wx += 11)
            for (let wy = h - bh + 8; wy < h - 12; wy += 14)
              if (Math.random() < 0.5) b.wins.push({ x: wx, y: wy, on: Math.random() < 0.3 });
        }
        parts.push(b);
        x += bw + rand(4, 18);
      }
    }
  }
  function frameSkyline() {
    ctx.clearRect(0, 0, w, h);
    for (const b of parts) {
      ctx.fillStyle = hexToRgba(colText, b.layer ? 0.035 : 0.06);
      ctx.fillRect(b.x, h - b.bh, b.bw, b.bh);
      for (const wn of b.wins) {
        if (Math.random() < 0.0005) wn.on = !wn.on;   // someone calls it a day
        if (wn.on) { ctx.fillStyle = hexToRgba(colWarm, 0.35); ctx.fillRect(wn.x, wn.y, 5, 3); }
      }
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
    sparkline: { spawn: spawnSparkline, frame: frameSparkline },
    skyline: { spawn: spawnSkyline, frame: frameSkyline },
  };

  function spawn() { const e = effects[effect]; if (e && e.spawn) e.spawn(); }
  function drawStatic() {
    ctx.clearRect(0, 0, w, h);
    const e = effects[effect]; if (!e) return;
    (e.static || e.frame)();
  }
  function frame(time) {
    raf = null;
    if (!active) return;
    const e = effects[effect]; if (e && e.frame) e.frame(time);
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
    stop();
    document.documentElement.setAttribute('data-bgfx-paused', String(!!document.hidden));
    const id = document.documentElement.getAttribute('data-theme');
    const eff = EFFECTS[id] || null;
    const intensity = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--bgfx-opacity'));
    if (document.hidden || intensity === 0) return;
    glyphs = id === 'amber-terminal' ? '0123456789ABCDEF_/:.=>[]' : GLYPHS;
    if (eff && ensureCanvas()) { effect = eff; start(); }
  }

  window.addEventListener('prism-theme-change', update);
  window.addEventListener('prism-bgfx-change', update);
  document.addEventListener('visibilitychange', update);
  if (motion && motion.addEventListener) motion.addEventListener('change', e => { reduceMotion = e.matches; update(); });
  update();
})();
