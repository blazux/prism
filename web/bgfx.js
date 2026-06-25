// Canvas animated backgrounds, tinted from the active theme tokens:
//   • cyberpunk → "circuit": a printed-circuit board (traces + pads) with glowing
//                  pulses travelling along the traces
//   • matrix    → "matrix": falling green code rain
// Active only for themes in EFFECTS (others keep their CSS effects). Driven by
// the 'prism-theme-change' event from theme.js.

(function () {
  'use strict';

  const GS = 32;         // grid step for circuit traces
  // theme id → effect name
  const EFFECTS = { cyberpunk: 'circuit', matrix: 'matrix' };

  let canvas, ctx, w = 0, h = 0;
  let traces = [], pads = [], pulses = [];  // circuit
  let drops = [];        // matrix: head row position (fractional) per column
  let matrixSpeed = [];  // matrix: rows advanced per frame, per column
  let effect = null;
  let raf = null;
  let active = false;
  let colAccent = '#7c4dff', colCyan = '#21e6ff';
  // matrix
  const FONT = 16;
  const GLYPHS = 'ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎ0123456789:.=*+<>';
  let colRain = '#39ff14', colLead = '#d7ffd7', colBgFade = 'rgba(0,0,0,0.08)';

  const reduceMotion = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

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
    colAccent = (cs.getPropertyValue('--accent').trim()) || '#7c4dff';
    colCyan = (cs.getPropertyValue('--green').trim()) || '#21e6ff';
    // matrix uses the (green) accent for glyphs; trails fade toward the bg.
    colRain = colAccent;
    colLead = '#e9ffe9';
    colBgFade = hexToRgba((cs.getPropertyValue('--bg').trim()) || '#000', 0.06);
  }

  // ─── Circuit board generation ─────────────────────────────────────────────
  // Each trace is a Manhattan-routed polyline on a grid (right-angle turns, no
  // reversals) ending in pads; pulses travel along the traces.
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
        if (Math.random() < 0.45) d = Math.random() < 0.5 ? [d[1], -d[0]] : [-d[1], d[0]]; // 90° turn
        cx += d[0]; cy += d[1];
        if (cx < 0 || cx > C || cy < 0 || cy > R) break;
        pts.push({ x: cx * GS, y: cy * GS });
      }
      if (pts.length >= 2) {
        traces.push(pts);
        pads.push(pts[0], pts[pts.length - 1]);
        if (pts.length > 3 && Math.random() < 0.5) pads.push(pts[(pts.length / 2) | 0]); // via
      }
    }
    pulses = [];
    for (let i = 0; i < traces.length; i++) if (Math.random() < 0.55) pulses.push(makePulse(i));
  }

  function spawn() {
    if (effect === 'matrix') {
      const c = Math.ceil(w / FONT);
      drops = new Array(c);
      matrixSpeed = new Array(c);
      for (let i = 0; i < c; i++) {
        drops[i] = Math.random() * (h / FONT);
        matrixSpeed[i] = 0.14 + Math.random() * 0.26;  // ~0.14–0.40 rows/frame
      }
      return;
    }
    genCircuit();
  }

  // Draw the static board (traces + pads) at the given alpha.
  function drawCircuit(alpha) {
    ctx.strokeStyle = hexToRgba(colAccent, alpha);
    ctx.lineWidth = 1.4;
    ctx.lineJoin = 'round';
    ctx.lineCap = 'round';
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
    ctx.shadowColor = pl.color;
    ctx.shadowBlur = 12;
    ctx.fillStyle = pl.color;
    ctx.beginPath();
    ctx.arc(p.x, p.y, 2.0, 0, Math.PI * 2);
    ctx.fill();
  }

  function drawStatic() {
    ctx.clearRect(0, 0, w, h);
    if (effect === 'matrix') {
      ctx.font = FONT + 'px monospace';
      ctx.textBaseline = 'top';
      ctx.fillStyle = colRain;
      for (let i = 0; i < drops.length; i++) {
        ctx.fillText(GLYPHS[(Math.random() * GLYPHS.length) | 0], i * FONT, drops[i] * FONT);
      }
      return;
    }
    // No frame accumulation here, so draw the board at a visible alpha.
    drawCircuit(0.16);
    ctx.shadowBlur = 0;
    for (const pl of pulses) drawPulse(pl);
  }

  function frameCircuit() {
    // Fade existing pixels (keeps the canvas otherwise transparent so the CSS
    // base gradient shows through) → glowing trails along the traces.
    ctx.globalCompositeOperation = 'destination-out';
    ctx.fillStyle = 'rgba(0,0,0,0.12)';
    ctx.fillRect(0, 0, w, h);
    ctx.globalCompositeOperation = 'source-over';

    ctx.shadowBlur = 0;
    drawCircuit(0.03);             // low alpha; accumulates to a soft board
    for (const pl of pulses) {
      pl.dist += pl.speed;
      if (pl.dist > pl.len) { pl.dist = 0; pl.color = Math.random() < 0.5 ? colAccent : colCyan; }
      drawPulse(pl);
    }
    ctx.shadowBlur = 0;
  }

  function frameMatrix() {
    // Trails fade toward the (near-black) theme background every frame.
    ctx.fillStyle = colBgFade;
    ctx.fillRect(0, 0, w, h);
    ctx.font = FONT + 'px monospace';
    ctx.textBaseline = 'top';
    for (let i = 0; i < drops.length; i++) {
      const prevRow = Math.floor(drops[i]);
      drops[i] += matrixSpeed[i];
      const row = Math.floor(drops[i]);
      if (row === prevRow) continue;  // only stamp a glyph when crossing a cell
      const y = row * FONT;
      // Bright leading glyph, occasionally a white sparkle.
      ctx.fillStyle = Math.random() < 0.12 ? colLead : colRain;
      ctx.fillText(GLYPHS[(Math.random() * GLYPHS.length) | 0], i * FONT, y);
      if (y > h && Math.random() > 0.96) {
        drops[i] = 0;
        matrixSpeed[i] = 0.14 + Math.random() * 0.26;
      }
    }
  }

  function frame() {
    if (!active) return;
    if (effect === 'matrix') frameMatrix();
    else frameCircuit();
    raf = requestAnimationFrame(frame);
  }

  function start() {
    if (active) return;
    active = true;
    canvas.style.display = 'block';
    readColors();
    sizeCanvas();
    spawn();
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
        ctx.clearRect(0, 0, w, h);   // drop the previous effect's pixels
        readColors();
        spawn();
        if (reduceMotion) drawStatic();
      } else {
        effect = eff;
        start();
      }
    } else {
      stop();
    }
  }

  window.addEventListener('prism-theme-change', update);
  // theme.js already applied the saved theme before this script ran, so sync now.
  update();
})();
