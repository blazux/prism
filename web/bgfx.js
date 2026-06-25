// Canvas animated backgrounds, tinted from the active theme tokens:
//   • cyberpunk → "tron": glow points travelling a grid, turning at nodes
//   • matrix    → "matrix": falling green code rain
// Active only for themes in EFFECTS (others keep their CSS effects). Driven by
// the 'prism-theme-change' event from theme.js.

(function () {
  'use strict';

  const SPACING = 46;
  // theme id → effect name
  const EFFECTS = { cyberpunk: 'tron', matrix: 'matrix' };

  let canvas, ctx, w = 0, h = 0;
  let runners = [];      // tron
  let drops = [];        // matrix: head row position (fractional) per column
  let matrixSpeed = [];  // matrix: rows advanced per frame, per column
  let effect = null;
  let raf = null;
  let active = false;
  // colGrid alpha is low because the grid is redrawn over the fading trail
  // buffer every frame, so its on-screen brightness accumulates ~6×.
  let colAccent = '#7c4dff', colCyan = '#21e6ff', colGrid = 'rgba(124,77,255,0.02)';
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
    colGrid = hexToRgba(colAccent, 0.02);
    // matrix uses the (green) accent for glyphs; trails fade toward the bg.
    colRain = colAccent;
    colLead = '#e9ffe9';
    colBgFade = hexToRgba((cs.getPropertyValue('--bg').trim()) || '#000', 0.06);
  }

  const cols = () => Math.floor(w / SPACING) + 2;
  const rows = () => Math.floor(h / SPACING) + 2;

  function randDir() {
    const d = [[1, 0], [-1, 0], [0, 1], [0, -1]][(Math.random() * 4) | 0];
    return { dx: d[0], dy: d[1] };
  }

  function makeRunner() {
    const { dx, dy } = randDir();
    return {
      gx: (Math.random() * cols()) | 0,
      gy: (Math.random() * rows()) | 0,
      dx, dy,
      t: Math.random(),
      speed: 0.013 + Math.random() * 0.022,
      color: Math.random() < 0.5 ? colAccent : colCyan,
    };
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
    const n = Math.min(70, Math.max(12, Math.round((w * h) / 26000)));
    runners = [];
    for (let i = 0; i < n; i++) runners.push(makeRunner());
  }

  function step(r) {
    r.t += r.speed;
    if (r.t < 1) return;
    r.t -= 1;
    r.gx += r.dx;
    r.gy += r.dy;
    // Mostly carry straight; sometimes take a 90° turn (never reverse).
    const turn = Math.random();
    if (turn < 0.22) { const dx = r.dx; r.dx = r.dy; r.dy = -dx; }       // left
    else if (turn < 0.44) { const dx = r.dx; r.dx = -r.dy; r.dy = dx; }  // right
    if (r.gx < -1 || r.gx > cols() || r.gy < -1 || r.gy > rows()) {
      Object.assign(r, makeRunner());
    }
  }

  function drawGrid() {
    ctx.strokeStyle = colGrid;
    ctx.lineWidth = 1;
    ctx.beginPath();
    for (let x = 0; x <= w; x += SPACING) { ctx.moveTo(x + 0.5, 0); ctx.lineTo(x + 0.5, h); }
    for (let y = 0; y <= h; y += SPACING) { ctx.moveTo(0, y + 0.5); ctx.lineTo(w, y + 0.5); }
    ctx.stroke();
  }

  function drawHead(r) {
    const x = (r.gx + r.dx * r.t) * SPACING;
    const y = (r.gy + r.dy * r.t) * SPACING;
    ctx.shadowColor = r.color;
    ctx.shadowBlur = 14;
    ctx.fillStyle = r.color;
    ctx.beginPath();
    ctx.arc(x, y, 1.9, 0, Math.PI * 2);
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
    // No frame accumulation here, so use a visible grid alpha.
    const savedGrid = colGrid;
    colGrid = hexToRgba(colAccent, 0.12);
    drawGrid();
    colGrid = savedGrid;
    ctx.shadowBlur = 0;
    for (const r of runners) {
      const x = (r.gx + r.dx * r.t) * SPACING;
      const y = (r.gy + r.dy * r.t) * SPACING;
      ctx.fillStyle = r.color;
      ctx.beginPath();
      ctx.arc(x, y, 1.9, 0, Math.PI * 2);
      ctx.fill();
    }
  }

  function frameTron() {
    // Fade existing pixels (keeps the canvas otherwise transparent so the CSS
    // base gradient shows through) → glowing trails along the grid lines.
    ctx.globalCompositeOperation = 'destination-out';
    ctx.fillStyle = 'rgba(0,0,0,0.10)';
    ctx.fillRect(0, 0, w, h);
    ctx.globalCompositeOperation = 'source-over';

    ctx.shadowBlur = 0;
    drawGrid();
    for (const r of runners) { step(r); drawHead(r); }
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
    else frameTron();
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
