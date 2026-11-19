// Canvas animated backgrounds, tinted from the active theme tokens. One effect
// per theme (light/dark keep their plain CSS gradient). Driven by the
// 'prism-theme-change' event from theme.js; intensity via --bgfx-opacity (#bg-fx).
//
//   prism → refracted light   cyberpunk → circuit   matrix → digital rain   amber-terminal → phosphor terminal
//   tech → drifting constellation   synthwave → neon landscape   dracula → gothic night
//   arcade → pixel shooter   bubblegum → iridescent bubbles
//   Professional palettes stay still; the studio collection keeps its own effects.

(function () {
  'use strict';

  const EFFECTS = {
    prism: 'prism', cyberpunk: 'circuit', matrix: 'matrix',
    midnight: 'stars', nord: 'snow', 'solarized-dark': 'bokeh',
    'rose-pine': 'petals', tech: 'network', synthwave: 'synthwave',
    espresso: 'bokeh', dracula: 'dracula', 'amber-terminal': 'terminal',
    arcade: 'arcade', bubblegum: 'bubbles',
  };

  let canvas, ctx, w = 0, h = 0;
  let traces = [], pads = [], chips = [], pulses = []; // circuit
  let circuitBoard, circuitTime = null;
  let parts = [], shooters = []; // particle effects
  let effect = null, raf = null, active = false;

  let colAccent = '#7c4dff', colCyan = '#21e6ff', colText = '#e8e8f0', colWarm = '#ff9e64';
  const GLYPHS = 'ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎ0123456789:.=*+<>';

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
  }

  // Scene clocks advance in seconds and pause with the shared lifecycle. Static
  // illustrations use the same renderer without scheduling animation frames.
  function sceneEffect(build, paint) {
    let scene, elapsed = 0, previous = null;
    function draw() {
      if (!w || !h) return;
      ctx.clearRect(0, 0, w, h);
      paint(scene, Math.round(elapsed * 1e9) / 1e9);
    }
    return {
      spawn() { elapsed = 0; previous = null; scene = w && h ? build() : null; },
      frame(time) {
        if (Number.isFinite(time)) {
          if (previous !== null) elapsed += Math.min(0.05, Math.max(0, (time - previous) / 1000));
          previous = time;
        }
        draw();
      },
      static: draw,
      dispose() { scene = null; previous = null; },
    };
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
    const cols = Math.max(1, Math.min(8, Math.round(w / 360)));
    const rows = Math.max(1, Math.min(6, Math.round(h / 260)));
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
          false, 8, r % 2 ? -1 : 1, (left ? 0 : 1) | (right ? 0 : 2)
        );
      }
    }
    for (let c = 0; c < cols; c++) {
      for (let r = 0; r <= rows; r++) {
        const top = chips[(r - 1) * cols + c], bottom = chips[r * cols + c];
        circuitBus(
          { u: top ? top.y + 39 : 24, v: top ? top.x : bottom.x },
          { u: bottom ? bottom.y - 39 : h - 24, v: bottom ? bottom.x : top.x },
          true, 6, c % 2 ? 1 : -1, (top ? 0 : 1) | (bottom ? 0 : 2)
        );
      }
    }
    // Independent signals keep the buses busy, with traffic in both directions.
    const routes = traces.map((_, i) => i);
    const count = Math.min(72, Math.max(6, Math.round(w * h / 26000)), routes.length);
    for (let i = 0; i < count; i++) {
      const ti = routes.splice(Math.floor(Math.random() * routes.length), 1)[0];
      const path = i % 3 ? traces[ti] : traces[ti].slice().reverse();
      const len = traceLen(path), tail = rand(64, 104), cycle = len + tail + rand(30, 90);
      pulses.push({ ti, path, len, tail, cycle, dist: rand(0, cycle), speed: rand(220, 360), color: i % 3 ? colAccent : colCyan });
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
      for (let pin = -28; pin <= 28; pin += 8) {
        board.moveTo(x - 47, y + pin); board.lineTo(x - 40, y + pin);
        board.moveTo(x + 40, y + pin); board.lineTo(x + 47, y + pin);
      }
      for (let pin = -20; pin <= 20; pin += 8) {
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
    const pts = pl.path;
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

  // ─── Matrix: cached phosphor trails at different depths and speeds ─────────
  function spawnDigitalRain() {
    const columns = Math.max(8, Math.min(150, Math.ceil(w / 20))), spacing = w / columns;
    const fontSize = Math.max(12, Math.min(17, Math.floor(spacing * 0.84))), lineHeight = fontSize + 3;
    return Array.from({ length: columns }, (_, i) => {
      const count = Math.floor(rand(10, 25)), image = document.createElement('canvas');
      image.width = fontSize + 5; image.height = lineHeight * (count + 1);
      const paint = image.getContext('2d'); paint.font = fontSize + 'px monospace'; paint.textBaseline = 'top';
      const depth = rand(0.25, 0.9);
      for (let row = 0; row < count; row++) {
        const lead = row === count - 1;
        paint.fillStyle = hexToRgba(lead ? colText : colAccent, lead ? depth : depth * (0.06 + 0.73 * (row / count) ** 1.7));
        paint.fillText(GLYPHS[Math.floor(rand(0, GLYPHS.length))], 1, row * lineHeight);
      }
      return { image, x: Math.round(i * spacing + (spacing - fontSize) / 2),
        offset: rand(0, h + image.height + 90), speed: rand(35, 95) * (0.7 + depth * 0.5) };
    });
  }
  function drawDigitalRain(scene, seconds) {
    for (const stream of scene) {
      const y = (stream.offset + seconds * stream.speed) % (h + stream.image.height + 90) - stream.image.height;
      ctx.drawImage(stream.image, stream.x, Math.round(y));
    }
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

  // ─── Amber Terminal: scrolling phosphor memory, with a typing cursor ──────
  function spawnTerminal() {
    const fontSize = w < 600 ? 11 : 13, lineHeight = fontSize + 12;
    const columns = Math.max(1, Math.min(3, Math.floor(w / 660)));
    const columnWidth = w / columns, count = Math.ceil(h / lineHeight) + 3;
    const bytes = Math.max(4, Math.min(16, Math.floor((columnWidth - 105) / (fontSize * 1.84))));
    const streams = [];
    for (let column = 0; column < columns; column++) {
      const lines = [];
      for (let row = 0; row < count * 2; row++) {
        let address = (0xA000 + row * bytes + column * 0x4000).toString(16).toUpperCase().padStart(6, '0');
        const data = Array.from({ length: bytes }, () => Math.floor(rand(0, 256)).toString(16).toUpperCase().padStart(2, '0'));
        lines.push(address + '  ' + data.join(' ') + '  ' + data.map(v => {
          const n = parseInt(v, 16); return n >= 48 && n <= 90 ? String.fromCharCode(n) : '·';
        }).join(''));
      }
      streams.push({ x: Math.max(15, columnWidth * 0.05) + column * columnWidth, lines });
    }
    return { fontSize, lineHeight, count, streams };
  }
  function drawTerminal(scene, seconds) {
    const progress = seconds * 0.38, step = Math.floor(progress), fraction = progress - step;
    ctx.font = scene.fontSize + 'px ui-monospace, SFMono-Regular, Consolas, monospace';
    ctx.textBaseline = 'top';
    for (const stream of scene.streams) for (let row = 0; row < scene.count; row++) {
      const y = (row - fraction) * scene.lineHeight;
      if (y < -scene.lineHeight || y > h) continue;
      const line = stream.lines[(row + step) % stream.lines.length];
      const lastRow = Math.max(0, Math.floor(h / scene.lineHeight) - 1);
      if (row > lastRow) continue;
      const newest = row === lastRow;
      const visible = newest ? Math.floor(line.length * fraction) : line.length;
      const alpha = 0.07 + Math.max(0, y / h) * 0.18;
      ctx.fillStyle = hexToRgba(colAccent, alpha);
      ctx.fillText(line.slice(0, visible), stream.x, y);
      if (newest) {
        ctx.fillStyle = hexToRgba(colAccent, 0.38);
        ctx.fillRect(stream.x + ctx.measureText(line.slice(0, visible)).width + 2, y, scene.fontSize * 0.58, scene.fontSize + 1);
      }
    }
  }

  // ─── Arcade: an autonomous pixel shooter ────
  function spawnArcade() {
    const unit = Math.max(2, Math.min(4, Math.floor(Math.min(w / 330, h / 235))));
    const patterns = [
      ['001000100', '000101000', '011111110', '110101011', '111111111', '001101100', '010000010'],
      ['001000100', '000101000', '011111110', '110101011', '111111111', '010000010', '100000001'],
      ['000111000', '001111100', '011010110', '111111111', '010111010', '001000100', '010101010'],
      ['000111000', '001111100', '011010110', '111111111', '010111010', '010000010', '001101100'],
    ];
    function sprite(pattern, color) {
      const image = document.createElement('canvas'); image.width = 9 * unit; image.height = 7 * unit;
      const paint = image.getContext('2d'); paint.fillStyle = color;
      pattern.forEach((line, y) => [...line].forEach((bit, x) => { if (bit === '1') paint.fillRect(x * unit, y * unit, unit, unit); }));
      return image;
    }
    const aliens = patterns.map((pattern, i) => sprite(pattern, hexToRgba(i < 2 ? colCyan : colAccent, 0.5)));
    const ship = sprite(['000010000', '000111000', '000111000', '010111010', '111111111', '111111111', '100101001'], hexToRgba(colText, 0.6));
    const columns = Math.max(4, Math.min(12, Math.floor(w / 105))), rows = h < 400 ? 2 : 4;
    const stars = Array.from({ length: Math.min(120, Math.max(24, Math.round(w * h / 16000))) }, () =>
      ({ x: rand(0, w), y: rand(0, h), speed: rand(5, 19), size: Math.random() < 0.15 ? 2 : 1 }));
    return { unit, aliens, ship, columns, rows, stars, gapX: w * 0.76 / columns,
      gapY: Math.max(30, Math.min(60, h * 0.067)), top: h * 0.17, shipY: h * 0.86 };
  }
  function drawArcade(scene, seconds) {
    ctx.save(); ctx.imageSmoothingEnabled = false;
    for (const star of scene.stars) {
      ctx.fillStyle = hexToRgba(star.size === 1 ? colText : colCyan, star.size === 1 ? 0.19 : 0.28);
      ctx.fillRect(Math.floor(star.x), Math.floor((star.y + seconds * star.speed) % h), star.size, star.size);
    }
    const cycle = Math.floor(seconds / 3.2), phase = seconds / 3.2 - cycle;
    const target = cycle * 7 % (scene.columns * scene.rows), previous = ((cycle - 1) * 7 % (scene.columns * scene.rows) + scene.columns * scene.rows) % (scene.columns * scene.rows);
    const sway = Math.sin(seconds * 0.45) * Math.min(28, w * 0.025);
    const alienX = index => w * 0.12 + ((index % scene.columns) + 0.5) * scene.gapX + sway;
    const alienY = index => scene.top + Math.floor(index / scene.columns) * scene.gapY;
    const leg = Math.floor(seconds * 2.5) % 2;
    for (let index = 0; index < scene.columns * scene.rows; index++) {
      if (index === target && phase > 0.82) continue;
      const sprite = scene.aliens[(Math.floor(index / scene.columns) % 2) * 2 + leg];
      ctx.drawImage(sprite, Math.round(alienX(index) - sprite.width / 2), Math.round(alienY(index)));
    }
    const blend = Math.min(1, phase / 0.32), ease = blend * blend * (3 - 2 * blend);
    const shipX = alienX(previous) * (1 - ease) + alienX(target) * ease;
    ctx.drawImage(scene.ship, Math.round(shipX - scene.ship.width / 2), Math.round(scene.shipY));
    if (phase >= 0.35 && phase < 0.82) {
      const travel = (phase - 0.35) / 0.47, y = scene.shipY + (alienY(target) + scene.unit * 4 - scene.shipY) * travel;
      ctx.fillStyle = hexToRgba(colAccent, 0.72); ctx.fillRect(Math.round(alienX(target) - scene.unit / 2), Math.round(y), scene.unit, scene.unit * 4);
    }
    if (phase >= 0.82) {
      const spread = (phase - 0.82) / 0.18;
      ctx.fillStyle = hexToRgba(colWarm, (1 - spread) * 0.65);
      for (let i = 0; i < 8; i++) {
        const angle = i * Math.PI / 4, radius = scene.unit * (4 + spread * 9);
        ctx.fillRect(Math.round(alienX(target) + Math.cos(angle) * radius), Math.round(alienY(target) + scene.unit * 3 + Math.sin(angle) * radius), scene.unit, scene.unit);
      }
    }
    ctx.fillStyle = hexToRgba(colAccent, 0.08);
    for (let x = 0; x < w; x += scene.unit * 12) ctx.fillRect(x, h * 0.94, scene.unit * 8, scene.unit);
    ctx.restore();
  }

  // ─── Synthwave: striped sunset above a low-poly neon landscape ────────────
  function spawnSynthwave() {
    const horizon = h * 0.43;
    const cx = w * (w < 650 ? 0.52 : 0.60);
    const radius = Math.min(w * 0.27, h * 0.17, 230);
    const cy = horizon - radius * 0.79;
    const backdrop = document.createElement('canvas');
    backdrop.width = w; backdrop.height = h;
    const paint = backdrop.getContext('2d');
    if (!w || !h) return { backdrop, horizon, cx, rows: 22 };

    // The sunset, stars, mountain mesh and converging grid are cached. Only
    // horizontal grid lines move, so the foreground never flashes or sparkles.
    const haze = paint.createRadialGradient(cx, cy, radius * 0.2, cx, cy, Math.max(radius * 2.7, w * 0.3));
    haze.addColorStop(0, hexToRgba(colAccent, 0.14));
    haze.addColorStop(0.48, hexToRgba(colAccent, 0.055));
    haze.addColorStop(1, hexToRgba(colAccent, 0));
    paint.fillStyle = haze; paint.fillRect(0, 0, w, horizon + 40);

    const stars = Math.min(200, Math.max(35, Math.round(w * horizon / 6500)));
    for (let i = 0; i < stars; i++) {
      const x = rand(0, w), y = rand(18, Math.max(20, horizon - 20));
      if (Math.hypot(x - cx, y - cy) < radius * 1.13) continue;
      const size = i % 11 === 0 ? 1.5 : rand(0.6, 1.15);
      paint.fillStyle = hexToRgba(i % 3 ? colText : colCyan, rand(0.18, 0.48));
      paint.fillRect(Math.round(x), Math.round(y), size, size);
      if (i % 11 === 0) {
        paint.strokeStyle = hexToRgba(colCyan, 0.2); paint.lineWidth = 0.8;
        paint.beginPath(); paint.moveTo(x - 3, y + 0.5); paint.lineTo(x + 4, y + 0.5);
        paint.moveTo(x + 0.5, y - 3); paint.lineTo(x + 0.5, y + 4); paint.stroke();
      }
    }

    const halo = paint.createRadialGradient(cx, cy, radius * 0.7, cx, cy, radius * 1.6);
    halo.addColorStop(0, hexToRgba(colWarm, 0.09));
    halo.addColorStop(1, hexToRgba(colAccent, 0));
    paint.fillStyle = halo; paint.fillRect(cx - radius * 1.6, cy - radius * 1.6, radius * 3.2, radius * 3.2);
    const sunset = paint.createLinearGradient(0, cy - radius, 0, cy + radius);
    sunset.addColorStop(0, hexToRgba(colWarm, 0.74));
    sunset.addColorStop(0.48, hexToRgba(colAccent, 0.66));
    sunset.addColorStop(1, hexToRgba(colAccent, 0.30));
    paint.save();
    paint.beginPath(); paint.arc(cx, cy, radius, 0, Math.PI * 2); paint.clip();
    paint.fillStyle = sunset;
    paint.fillRect(cx - radius, cy - radius, radius * 2, radius * 0.9);
    const stripe = radius * 1.1 / 10;
    for (let i = 0; i < 10; i++) {
      const gap = radius * (0.012 + i * 0.004);
      paint.fillRect(cx - radius, cy - radius * 0.1 + i * stripe + gap, radius * 2, stripe - gap);
    }
    paint.restore();

    // Three irregular, triangulated ridges make a landscape, rather than a
    // decorative zigzag. A valley keeps the striped sun readable in the centre.
    const step = Math.max(38, Math.min(100, w / 14));
    for (let layer = 0; layer < 3; layer++) {
      const ridge = [];
      const base = horizon + (layer - 1) * h * 0.018;
      for (let x = -step; x < w + step * 1.6; x += step * rand(0.72, 1.28)) {
        const edge = Math.min(1, Math.abs(x - cx) / Math.max(w * 0.38, 1));
        const amplitude = h * (0.035 + edge * 0.095) * (1 - layer * 0.19);
        ridge.push({ x, y: base - amplitude * rand(0.25, 1.0) });
      }
      const feet = ridge.map(point => ({ x: point.x + rand(-step * 0.25, step * 0.25), y: horizon + h * (0.015 + layer * 0.022) }));
      paint.fillStyle = layer === 0 ? 'rgba(31,17,53,0.84)' : layer === 1 ? 'rgba(24,14,43,0.94)' : 'rgba(19,12,34,0.98)';
      paint.beginPath(); paint.moveTo(ridge[0].x, feet[0].y);
      for (const point of ridge) paint.lineTo(point.x, point.y);
      paint.lineTo(ridge[ridge.length - 1].x, feet[feet.length - 1].y); paint.closePath(); paint.fill();
      paint.strokeStyle = hexToRgba(layer === 2 ? colCyan : colAccent, layer === 2 ? 0.22 : 0.18);
      paint.lineWidth = layer === 2 ? 1.1 : 0.8;
      paint.beginPath();
      for (let i = 0; i < ridge.length; i++) {
        const point = ridge[i];
        if (i) paint.lineTo(point.x, point.y); else paint.moveTo(point.x, point.y);
      }
      paint.stroke();
      paint.strokeStyle = hexToRgba(layer === 2 ? colCyan : colAccent, layer === 2 ? 0.085 : 0.065);
      paint.lineWidth = 0.8; paint.beginPath();
      for (let i = 0; i < ridge.length - 1; i++) {
        paint.moveTo(ridge[i].x, ridge[i].y); paint.lineTo(feet[i].x, feet[i].y);
        paint.lineTo(ridge[i + 1].x, ridge[i + 1].y);
        paint.moveTo(feet[i].x, feet[i].y); paint.lineTo(feet[i + 1].x, feet[i + 1].y);
      }
      paint.stroke();
    }

    const ground = paint.createLinearGradient(0, horizon, 0, h);
    ground.addColorStop(0, 'rgba(19,12,34,0)');
    ground.addColorStop(0.2, 'rgba(19,12,34,0.30)');
    ground.addColorStop(1, 'rgba(12,9,26,0.72)');
    paint.fillStyle = ground; paint.fillRect(0, horizon, w, h - horizon);
    paint.lineWidth = 1;
    const columns = w < 650 ? 7 : 12;
    for (let i = -columns; i <= columns; i++) {
      paint.strokeStyle = hexToRgba(i % 4 === 0 ? colCyan : colAccent, i % 4 === 0 ? 0.26 : 0.19);
      paint.beginPath(); paint.moveTo(cx, horizon); paint.lineTo(cx + i / columns * w * 1.5, h); paint.stroke();
    }
    const glow = paint.createLinearGradient(0, horizon - 7, 0, horizon + 25);
    glow.addColorStop(0, hexToRgba(colAccent, 0));
    glow.addColorStop(0.2, hexToRgba(colAccent, 0.14));
    glow.addColorStop(1, hexToRgba(colAccent, 0));
    paint.fillStyle = glow; paint.fillRect(0, horizon - 7, w, 32);
    paint.strokeStyle = hexToRgba(colCyan, 0.21); paint.lineWidth = 1;
    paint.beginPath(); paint.moveTo(0, horizon + 0.5); paint.lineTo(w, horizon + 0.5); paint.stroke();
    return { backdrop, horizon, cx, rows: w < 650 ? 19 : 22 };
  }

  function drawSynthwave(scene, seconds) {
    ctx.save(); ctx.globalAlpha = 1; ctx.globalCompositeOperation = 'source-over'; ctx.shadowBlur = 0;
    ctx.drawImage(scene.backdrop, 0, 0);
    const { horizon, rows } = scene;
    const phase = seconds * 2.05;
    ctx.lineWidth = 1.1;
    for (let i = 0; i < rows; i++) {
      const distance = ((i + phase) % rows) / rows;
      const y = horizon + (h - horizon) * distance * distance;
      const fade = Math.min(1, distance * 7) * Math.min(1, (1 - distance) * 15);
      ctx.strokeStyle = hexToRgba(i % 4 ? colCyan : colAccent, (0.055 + distance * 0.25) * fade);
      ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(w, y); ctx.stroke();
    }
    ctx.restore();
  }

  // Dracula: a moonlit silhouette, actual flapping bats and low drifting mist.
  function spawnDracula() {
    const width = Math.max(1, w), height = Math.max(1, h);
    const art = document.createElement('canvas');
    art.width = width; art.height = height;
    const c = art.getContext('2d');
    const radius = Math.max(18, Math.min(86, Math.min(width, height) * 0.115));
    const moonX = width * 0.64, moonY = height * 0.23;
    const glow = c.createRadialGradient(moonX, moonY, radius * 0.5, moonX, moonY, radius * 2.9);
    glow.addColorStop(0, hexToRgba(colAccent, 0.095));
    glow.addColorStop(0.55, hexToRgba(colCyan, 0.022));
    glow.addColorStop(1, hexToRgba(colAccent, 0));
    c.fillStyle = glow; c.fillRect(moonX - radius * 3, moonY - radius * 3, radius * 6, radius * 6);
    c.save();
    c.beginPath(); c.arc(moonX, moonY, radius, 0, Math.PI * 2); c.clip();
    const face = c.createRadialGradient(moonX - radius * 0.34, moonY - radius * 0.38, 0, moonX + radius * 0.18, moonY + radius * 0.15, radius * 1.5);
    face.addColorStop(0, hexToRgba(colCyan, 0.43));
    face.addColorStop(0.45, hexToRgba(colAccent, 0.37));
    face.addColorStop(1, hexToRgba(colAccent, 0.18));
    c.fillStyle = face; c.fillRect(moonX - radius, moonY - radius, radius * 2, radius * 2);
    c.fillStyle='rgba(34,29,57,.065)';
    c.beginPath();c.moveTo(moonX-radius*.64,moonY-radius*.18);
    c.bezierCurveTo(moonX-radius*.48,moonY-radius*.73,moonX+radius*.03,moonY-radius*.49,moonX+radius*.19,moonY-radius*.24);
    c.bezierCurveTo(moonX+radius*.49,moonY+radius*.06,moonX-radius*.04,moonY+radius*.42,moonX-radius*.41,moonY+radius*.24);
    c.bezierCurveTo(moonX-radius*.69,moonY+radius*.1,moonX-radius*.49,moonY-radius*.04,moonX-radius*.64,moonY-radius*.18);c.fill();
    // Fine cached grain adds surface texture without any per-frame particle work.
    for(let i=0;i<180;i++){
      const angle=rand(0,Math.PI*2),distance=Math.sqrt(rand(0,1))*radius;
      c.fillStyle=hexToRgba(i%3?colCyan:colAccent,rand(.018,.06));
      c.beginPath();c.arc(moonX+Math.cos(angle)*distance,moonY+Math.sin(angle)*distance,rand(.25,.85),0,Math.PI*2);c.fill();
    }
    // Small relief marks follow the lunar disc, rather than floating bokeh spots.
    for (const [x, y, r] of [[-.36,-.18,.17],[.24,-.38,.11],[.42,.12,.23],[-.18,.42,.14],[-.57,.42,.08],[.05,.02,.095],[.25,.58,.075],[-.40,-.63,.055],[.66,-.35,.065]]) {
      const px = moonX + x * radius, py = moonY + y * radius, pr = r * radius;
      const crater = c.createRadialGradient(px - pr * .25, py - pr * .25, pr * .1, px, py, pr);
      crater.addColorStop(0, 'rgba(25,20,45,0.15)'); crater.addColorStop(.7, 'rgba(25,20,45,0.045)'); crater.addColorStop(1, 'rgba(25,20,45,0)');
      c.fillStyle = crater; c.beginPath(); c.arc(px, py, pr, 0, Math.PI * 2); c.fill();
      c.strokeStyle = hexToRgba(colCyan, .075); c.lineWidth = .8;
      c.beginPath(); c.arc(px, py, pr * .84, Math.PI * .82, Math.PI * 1.6); c.stroke();
    }
    c.restore();
    c.strokeStyle = hexToRgba(colCyan, .24); c.lineWidth = 1;
    c.beginPath(); c.arc(moonX, moonY, radius - .5, 0, Math.PI * 2); c.stroke();

    // A low hillside supports asymmetrical roofs and recognisable narrow towers.
    const base = height * .91, s = Math.max(.32, Math.min(1.1, width / 1100, height / 760));
    const left = width * .065;
    // Two receding Carpathian ridges stay below the open central sky.
    for(const [ridge,color] of [
      [[[0,.91],[.06,.81],[.13,.85],[.21,.69],[.28,.79],[.34,.75],[.43,.86],[.51,.82],[.60,.9],[.72,.83],[.80,.88],[.89,.77],[1,.88]],hexToRgba(colAccent,.045)],
      [[[0,.94],[.08,.88],[.18,.91],[.27,.82],[.34,.87],[.43,.84],[.55,.94],[.66,.89],[.75,.94],[.87,.85],[1,.91]],'rgba(10,10,23,.26)'],
    ]) {
      c.fillStyle=color;c.beginPath();c.moveTo(0,height);
      for(const [x,y] of ridge)c.lineTo(x*width,y*height);
      c.lineTo(width,height);c.closePath();c.fill();
    }
    c.fillStyle = 'rgba(11,10,22,0.68)';
    c.beginPath(); c.moveTo(0, height); c.lineTo(0, base + 20 * s);
    c.bezierCurveTo(width * .10, base - 38 * s, width * .25, base - 20 * s, width * .4, base + 25 * s);
    c.bezierCurveTo(width * .65, height * .98, width * .84, height * .92, width, height * .94);
    c.lineTo(width, height); c.closePath(); c.fill();
    // Uneven tiered branches and exposed trunks create a quiet foreground forest.
    for(const [position,size,opacity] of [[.015,82,.48],[.036,119,.6],[.32,65,.38],[.37,88,.48],[.79,69,.4],[.84,103,.52],[.9,128,.63],[.95,78,.53],[.983,109,.59]]) {
      const x=width*position,treeHeight=size*s,foot=height*(.967+Math.sin(position*24)*.012),top=foot-treeHeight,half=treeHeight*.19;
      c.fillStyle=`rgba(10,12,23,${opacity})`;c.fillRect(x-1.15*s,foot-treeHeight*.3,2.3*s,treeHeight*.34);
      c.beginPath();c.moveTo(x,top);
      for(let branch=1;branch<=6;branch++){
        const y=top+treeHeight*(.09+branch*.12),reach=half*(.2+branch*.12);
        c.lineTo(x-reach*.64,y-treeHeight*.045);c.lineTo(x-reach,y+treeHeight*.045);c.lineTo(x-reach*.31,y+treeHeight*.018);
      }
      c.lineTo(x,foot-treeHeight*.12);
      for(let branch=6;branch>=1;branch--){
        const y=top+treeHeight*(.09+branch*.12),reach=half*(.2+branch*.12);
        c.lineTo(x+reach*.31,y+treeHeight*.018);c.lineTo(x+reach,y+treeHeight*.045);c.lineTo(x+reach*.64,y-treeHeight*.045);
      }
      c.closePath();c.fill();
    }
    c.save(); c.translate(left, base); c.scale(s, s);
    const masonry = '#11101e';
    c.fillStyle = masonry; c.strokeStyle = hexToRgba(colAccent, .15); c.lineWidth = 1;
    const block = (x, y, bw, bh) => { c.fillRect(x, y, bw, bh); c.strokeRect(x + .5, y + .5, bw - 1, bh - 1); };
    const roof = (x, y, rw, rh) => {
      c.beginPath(); c.moveTo(x - rw / 2, y); c.lineTo(x, y - rh); c.lineTo(x + rw / 2, y); c.closePath(); c.fill(); c.stroke();
      c.save();c.fillStyle=hexToRgba(colAccent,.045);c.beginPath();c.moveTo(x,y-rh);c.lineTo(x+rw/2,y);c.lineTo(x+rw*.08,y);c.closePath();c.fill();
      c.strokeStyle=hexToRgba(colAccent,.06);c.lineWidth=.6;
      for(let tile=1;tile<5;tile++){
        const yy=y-rh+rh*tile/5,spread=rw*.5*tile/5;c.beginPath();c.moveTo(x-spread,yy);c.lineTo(x+spread,yy);c.stroke();
      }
      c.restore();
    };
    block(30, -142, 42, 148); roof(51, -142, 56, 60);
    block(72, -105, 116, 112);
    block(104, -183, 42, 190); roof(125, -183, 56, 74);
    block(181, -121, 43, 129); roof(202.5, -121, 59, 63);
    // Fine courses and staggered mortar joints remain part of the cached masonry.
    c.save();c.strokeStyle=hexToRgba(colAccent,.07);c.lineWidth=.65;c.beginPath();
    for(const [x,y,bw,bh] of [[31,-140,39,143],[74,-103,107,107],[106,-181,38,185],[183,-119,39,123]]){
      let row=0;for(let yy=y+12;yy<y+bh;yy+=14){
        c.moveTo(x,yy);c.lineTo(x+bw,yy);
        for(let xx=x+8+(row%2)*10;xx<x+bw-3;xx+=20){c.moveTo(xx,yy);c.lineTo(xx,Math.min(yy+14,y+bh))}row++;
      }
    }
    c.stroke();c.restore();
    // Projecting, stepped buttresses make the keep read as stone architecture.
    for(const x of [75,96,157,177]){
      c.fillStyle='#191626';c.strokeStyle=hexToRgba(colAccent,.11);c.lineWidth=.7;
      c.beginPath();c.moveTo(x,-89);c.lineTo(x+5,-89);c.lineTo(x+5,-49);c.lineTo(x+9,-43);c.lineTo(x+9,5);c.lineTo(x-3,5);c.lineTo(x-3,-41);c.lineTo(x,-48);c.closePath();c.fill();c.stroke();
      c.fillStyle='rgba(0,0,0,.13)';c.fillRect(x+5,-42,4,47);
    }
    // Crenellations and a recessed arch distinguish the keep from a skyline.
    for (let x = 74; x < 182; x += 18) { c.fillRect(x, -115, 10, 14); c.strokeRect(x + .5, -114.5, 9, 13); }
    c.fillStyle = 'rgba(4,3,12,.55)';
    c.beginPath(); c.moveTo(117, 8); c.lineTo(117, -25); c.quadraticCurveTo(118,-39,132,-48);c.quadraticCurveTo(146,-39,147,-25);c.lineTo(147,8);c.closePath();c.fill();
    c.strokeStyle=hexToRgba(colAccent,.17);c.lineWidth=2;c.stroke();
    c.strokeStyle=hexToRgba(colAccent,.065);c.lineWidth=.7;c.beginPath();
    for(const x of [122,127,132,137,142]){c.moveTo(x,6);c.lineTo(x,-25)}
    c.moveTo(113,7);c.lineTo(151,7);c.moveTo(110,11);c.lineTo(154,11);c.stroke();
    for (const [x,y] of [[46,-111],[46,-77],[119,-151],[119,-113],[196,-88],[94,-70],[161,-70]]) {
      c.fillStyle = hexToRgba(colCyan, .23);
      c.beginPath();c.moveTo(x,y+13);c.lineTo(x,y+5);c.quadraticCurveTo(x+1,y,x+5,y-4);c.quadraticCurveTo(x+9,y,x+10,y+5);c.lineTo(x+10,y+13);c.closePath();c.fill();
      c.strokeStyle=hexToRgba(colAccent,.11);c.lineWidth=.8;c.stroke();
      c.strokeStyle=masonry;c.lineWidth=1.3;c.beginPath();c.moveTo(x+5,y-1);c.lineTo(x+5,y+13);c.moveTo(x+1,y+7);c.lineTo(x+9,y+7);c.stroke();
    }
    c.strokeStyle=hexToRgba(colCyan,.14);c.lineWidth=1;c.beginPath();c.arc(132,-77,9,0,Math.PI*2);c.stroke();
    c.beginPath();for(let spoke=0;spoke<8;spoke++){const a=spoke*Math.PI/4;c.moveTo(132,-77);c.lineTo(132+Math.cos(a)*8,-77+Math.sin(a)*8)}c.stroke();
    // A tiny vane keeps the tallest roof's outline sharply Gothic.
    c.strokeStyle = hexToRgba(colAccent, .23); c.beginPath(); c.moveTo(125,-257); c.lineTo(125,-274); c.moveTo(119,-268); c.lineTo(131,-268); c.stroke();
    c.restore();

    const fog = document.createElement('canvas');
    fog.width = Math.max(320, Math.min(1500, Math.ceil(width * 1.3))); fog.height = 150;
    const f = fog.getContext('2d');
    for (const [x,y,rx,ry,alpha] of [[.15,.55,.32,.34,.055],[.5,.48,.42,.22,.045],[.9,.58,.35,.31,.06]]) {
      f.save(); f.translate(fog.width * x, fog.height * y); f.scale(fog.width * rx, fog.height * ry);
      const mist = f.createRadialGradient(0,0,.1,0,0,1);
      mist.addColorStop(0,hexToRgba(colCyan,alpha)); mist.addColorStop(.55,hexToRgba(colAccent,alpha*.45)); mist.addColorStop(1,hexToRgba(colAccent,0));
      f.fillStyle=mist; f.fillRect(-1,-1,2,2); f.restore();
    }
    const bats = Array.from({length: Math.max(3, Math.min(7, Math.round(width / 240)))}, (_,i) => ({
      x: rand(.12,.92) * width, y: rand(.11,.42) * height,
      size: rand(8,15) * Math.max(.65, Math.min(1.2,width/1000)),
      speed: rand(5,11) * (i%2 ? -1 : 1), phase: rand(0,Math.PI*2),
    }));
    return {art, fog, bats, width, height};
  }

  function drawDracula(scene, seconds) {
    const t = Number.isFinite(seconds) ? seconds : 0;
    ctx.save(); ctx.globalCompositeOperation = 'source-over';
    ctx.drawImage(scene.art, 0, 0);
    for (const bat of scene.bats) {
      const span=scene.width+80, x=((bat.x+t*bat.speed+40)%span+span)%span-40;
      const y=bat.y+Math.sin(t*.45+bat.phase)*8;
      const flap=Math.sin(t*5.5+bat.phase), s=bat.size;
      ctx.save();ctx.translate(x,y);ctx.rotate(Math.sin(t*.22+bat.phase)*.09);
      ctx.fillStyle='rgba(10,8,20,.78)';ctx.strokeStyle=hexToRgba(colAccent,.18);ctx.lineWidth=.65;
      ctx.beginPath();ctx.moveTo(0,-s*.1);
      for(const side of [-1,1]) {
        ctx.quadraticCurveTo(side*s*.43,-s*(.34+.18*flap),side*s,-s*(.32+.36*flap));
        ctx.quadraticCurveTo(side*s*.91,s*.08,side*s*.70,s*.19);
        ctx.quadraticCurveTo(side*s*.50,-s*.02,side*s*.34,s*.23);
        ctx.quadraticCurveTo(side*s*.15,s*.05,0,s*.35);
        ctx.lineTo(0,-s*.1);
      }
      ctx.fill();ctx.stroke();
      ctx.beginPath();ctx.moveTo(-s*.12,s*.05);ctx.lineTo(-s*.14,-s*.28);ctx.lineTo(0,-s*.17);ctx.lineTo(s*.14,-s*.28);ctx.lineTo(s*.12,s*.1);ctx.closePath();ctx.fill();
      ctx.restore();
    }
    const bandWidth=scene.width*1.55, bandHeight=Math.max(70,scene.height*.20);
    for(let i=0;i<3;i++) {
      const offset=((t*(3+i*2)+i*scene.width*.31)%bandWidth+bandWidth)%bandWidth;
      const y=scene.height*(.67+i*.105)+Math.sin(t*.10+i)*scene.height*.009;
      ctx.globalAlpha=.58+i*.12;
      ctx.drawImage(scene.fog,offset-bandWidth,y,bandWidth,bandHeight);
      ctx.drawImage(scene.fog,offset,y,bandWidth,bandHeight);
    }
    ctx.restore();
  }

  // Bubblegum: translucent soap membranes, coloured rims and curved reflections.
  function spawnBubblegum() {
    const width=Math.max(1,w),height=Math.max(1,h), textures=[];
    for(let variant=0;variant<3;variant++) {
      const image=document.createElement('canvas');image.width=image.height=256;
      const c=image.getContext('2d'),r=104,x=128,y=128;
      // Artwork colours are candy pink/lilac/cyan, independent of UI success/warning inks.
      const spectrum=['#e35aaf','#79bdea','#b2a3ee'];
      const colors=variant===0?spectrum:variant===1?[spectrum[1],spectrum[2],spectrum[0]]:[spectrum[2],spectrum[0],spectrum[1]];
      const wash=c.createRadialGradient(x-r*.2,y-r*.25,r*.1,x,y,r);
      wash.addColorStop(0,hexToRgba(colors[0],0));wash.addColorStop(.72,hexToRgba(colors[1],.012));wash.addColorStop(.93,hexToRgba(colors[0],.055));wash.addColorStop(1,hexToRgba(colors[2],.11));
      c.fillStyle=wash;c.beginPath();c.arc(x,y,r,0,Math.PI*2);c.fill();
      const rim=c.createLinearGradient(x-r,y-r,x+r,y+r);
      rim.addColorStop(0,hexToRgba(colors[0],.68));rim.addColorStop(.30,hexToRgba(colors[1],.58));rim.addColorStop(.62,hexToRgba(colors[2],.57));rim.addColorStop(1,hexToRgba(colors[0],.64));
      c.strokeStyle=rim;c.lineWidth=1.6;c.beginPath();c.arc(x,y,r,0,Math.PI*2);c.stroke();
      // Unequal arc thickness suggests a thin, iridescent liquid membrane.
      for(const [a,b,index,line] of [[.04,.42,0,2.7],[.62,1.05,1,1.9],[1.18,1.54,2,2.9],[1.64,1.92,0,1.5]]) {
        c.strokeStyle=hexToRgba(colors[index],.42);c.lineWidth=line;c.beginPath();c.arc(x,y,r-2,a*Math.PI,b*Math.PI);c.stroke();
      }
      // Curved highlights sit inside the rim; the centre remains transparent.
      c.strokeStyle='rgba(255,255,255,.96)';c.lineWidth=5.5;c.lineCap='round';
      c.beginPath();c.arc(x,y,r*.84,Math.PI*1.10,Math.PI*1.49);c.stroke();
      c.strokeStyle=hexToRgba(colors[0],.18);c.lineWidth=1.2;
      c.beginPath();c.arc(x,y,r*.78,Math.PI*1.12,Math.PI*1.43);c.stroke();
      c.fillStyle='rgba(255,255,255,.97)';c.beginPath();c.ellipse(x-r*.33,y-r*.70,r*.16,r*.047,-.47,0,Math.PI*2);c.fill();
      c.strokeStyle='rgba(255,255,255,.52)';c.lineWidth=2;
      c.beginPath();c.arc(x,y,r*.89,Math.PI*.15,Math.PI*.30);c.stroke();
      textures.push(image);
    }
    const count=Math.max(7,Math.min(18,Math.round(width*height/78000)));
    const period=Math.max(35,Math.min(80,height/18));
    const bubbles=Array.from({length:count},(_,i)=>{
      const depth=rand(0,1),r=16+depth*43,baseY=(i+.5+rand(-.22,.22))*height/count;
      const xFraction=((i*.61803398875+.21+rand(-.09,.09))%1+1)%1;
      return {r,depth,x:r+xFraction*Math.max(0,width-r*2),phase:height+r*2-baseY,speed:(height+r*4)/period,wobble:rand(0,Math.PI*2),texture:i%textures.length};
    }).sort((a,b)=>a.depth-b.depth);
    return {textures,bubbles,width,height};
  }

  function drawBubblegum(scene,seconds) {
    const t=Number.isFinite(seconds)?seconds:0;
    ctx.save();ctx.globalCompositeOperation='source-over';
    for(const bubble of scene.bubbles) {
      const span=scene.height+bubble.r*4;
      const y=scene.height+bubble.r*2-((bubble.phase+t*bubble.speed)%span+span)%span;
      const x=bubble.x+Math.sin(t*.32+bubble.wobble)*Math.min(25,scene.width*.035)+Math.sin(t*.11+bubble.wobble*2)*7;
      const wobble=Math.sin(t*.83+bubble.wobble);
      ctx.save();ctx.translate(x,y);ctx.scale(1+wobble*.022,1-wobble*.014);
      ctx.globalAlpha=.46+bubble.depth*.40;
      const size=bubble.r*2*256/208;
      ctx.drawImage(scene.textures[bubble.texture],-size/2,-size/2,size,size);
      ctx.restore();
    }
    ctx.restore();
  }

  // ─── Prism: glass facets separate a white beam into visible wavelengths ───
  function spawnPrism() {
    const radius = Math.max(70, Math.min(150, w * 0.23, h * 0.20));
    const cx = w * 0.48, cy = h * 0.46;
    const art = document.createElement('canvas'); art.width = w; art.height = h;
    const paint = art.getContext('2d');
    if (!w || !h) return { art, fronts: [] };
    const a = { x: cx, y: cy - radius * 0.92 };
    const b = { x: cx - radius * 0.77, y: cy + radius * 0.63 };
    const c = { x: cx + radius * 0.77, y: cy + radius * 0.63 };
    const dx = radius * 0.18, dy = -radius * 0.12;
    const back = point => ({ x: point.x + dx, y: point.y + dy });
    const polygon = points => {
      paint.beginPath(); points.forEach((point, i) => i ? paint.lineTo(point.x, point.y) : paint.moveTo(point.x, point.y));
      paint.closePath();
    };
    const entry = { x: cx - radius * 0.77 * 0.79 / 1.55, y: cy - radius * 0.13 };
    const normal = Math.atan(0.77 / 1.55);
    const colors = ['#f38a81', '#efb273', '#eadb99', '#8ccda7', '#78cbdc', '#88a9e7', '#ad9ce2'];
    const indices = [1.47, 1.481, 1.492, 1.504, 1.516, 1.528, 1.54];
    // Trace both refractions through the triangular section with Snell's law.
    // Higher indices bend towards the base more, so red remains above violet.
    const rays = indices.map((index, i) => {
      const internal = normal - Math.asin(Math.sin(normal) / index);
      const slope = Math.tan(internal), k = 0.77 / 1.55;
      const localX = k * (entry.y - a.y - slope * (entry.x - cx)) / (1 - k * slope);
      const exit = { x: cx + localX, y: entry.y + slope * (cx + localX - entry.x) };
      const angle = Math.asin(index * Math.sin(internal + normal)) - normal;
      return { exit, angle, color: colors[i] };
    });
    const fanWidth = Math.max(12, Math.min(radius * 3.7, w - rays[6].exit.x + 22,
      (h * 0.94 - rays[6].exit.y) / Math.tan(rays[6].angle)));
    for (const ray of rays) ray.end = { x: ray.exit.x + fanWidth, y: ray.exit.y + fanWidth * Math.tan(ray.angle) };

    const glow = paint.createRadialGradient(cx, cy, radius * 0.2, cx, cy, radius * 2.7);
    glow.addColorStop(0, 'rgba(115,172,224,0.045)');
    glow.addColorStop(0.55, 'rgba(91,143,211,0.018)');
    glow.addColorStop(1, 'rgba(91,143,211,0)');
    paint.fillStyle = glow; paint.fillRect(cx - radius * 2.7, cy - radius * 2.7, radius * 5.4, radius * 5.4);

    // Faint back edges and differently lit side planes give the glass depth.
    paint.strokeStyle = 'rgba(151,198,235,0.12)'; paint.lineWidth = 0.8;
    polygon([back(a), back(b), back(c)]); paint.stroke();
    const side = paint.createLinearGradient(c.x, a.y, c.x + dx, b.y);
    side.addColorStop(0, 'rgba(151,198,235,0.10)');
    side.addColorStop(0.6, 'rgba(116,174,218,0.025)');
    side.addColorStop(1, 'rgba(154,206,242,0.13)');
    paint.fillStyle = side; polygon([a, back(a), back(c), c]); paint.fill(); paint.stroke();
    paint.fillStyle = 'rgba(153,195,231,0.027)'; polygon([b, c, back(c), back(b)]); paint.fill(); paint.stroke();

    const input = { start: { x: 0, y: entry.y }, end: entry, color: '#e8f2ff' };
    const beam = (from, to, color, alpha, width) => {
      const gradient = paint.createLinearGradient(from.x, from.y, to.x, to.y);
      gradient.addColorStop(0, hexToRgba(color, alpha * 0.42));
      gradient.addColorStop(0.12, hexToRgba(color, alpha));
      gradient.addColorStop(0.73, hexToRgba(color, alpha));
      gradient.addColorStop(1, hexToRgba(color, alpha * 0.08));
      paint.strokeStyle = gradient; paint.lineWidth = width;
      paint.beginPath(); paint.moveTo(from.x, from.y); paint.lineTo(to.x, to.y); paint.stroke();
    };
    beam(input.start, input.end, input.color, 0.065, 7);
    // The incoming beam remains white up to the first glass boundary.
    const white = paint.createLinearGradient(0, entry.y, entry.x, entry.y);
    white.addColorStop(0, 'rgba(224,238,255,0.08)'); white.addColorStop(0.16, 'rgba(224,238,255,0.40)'); white.addColorStop(1, 'rgba(244,248,255,0.62)');
    paint.strokeStyle = white; paint.lineWidth = 1.3;
    paint.beginPath(); paint.moveTo(0, entry.y); paint.lineTo(entry.x, entry.y); paint.stroke();
    for (const ray of rays) {
      beam(ray.exit, ray.end, ray.color, 0.045, 7);
      beam(ray.exit, ray.end, ray.color, 0.40, 1.3);
      paint.strokeStyle = hexToRgba(ray.color, 0.12); paint.lineWidth = 1;
      paint.beginPath(); paint.moveTo(entry.x, entry.y); paint.lineTo(ray.exit.x, ray.exit.y); paint.stroke();
    }

    // The transparent front face adds a cool sheen without obscuring the beam.
    const glass = paint.createLinearGradient(b.x, b.y, c.x + radius * 0.1, a.y);
    glass.addColorStop(0, 'rgba(137,186,226,0.065)');
    glass.addColorStop(0.25, 'rgba(194,224,246,0.018)');
    glass.addColorStop(0.6, 'rgba(132,186,226,0.025)');
    glass.addColorStop(1, 'rgba(177,215,243,0.13)');
    paint.fillStyle = glass; polygon([a, b, c]); paint.fill();
    paint.strokeStyle = 'rgba(172,211,241,0.43)'; paint.lineWidth = 1.1; paint.stroke();
    paint.strokeStyle = 'rgba(221,240,253,0.48)'; paint.lineWidth = 1.2;
    paint.beginPath(); paint.moveTo(a.x, a.y); paint.lineTo(b.x, b.y); paint.stroke();
    paint.strokeStyle = 'rgba(143,194,233,0.30)'; paint.lineWidth = 0.7;
    paint.beginPath(); paint.moveTo(a.x + dx, a.y + dy); paint.lineTo(c.x + dx, c.y + dy); paint.lineTo(c.x, c.y); paint.stroke();
    // Narrow reflected streaks taper along the face, rather than filling it flat.
    paint.save(); polygon([a, b, c]); paint.clip();
    paint.fillStyle = 'rgba(219,237,252,0.045)';
    polygon([{x:a.x-radius*.10,y:a.y+radius*.08},{x:a.x+radius*.03,y:a.y+radius*.08},
      {x:b.x+radius*.28,y:b.y-radius*.06},{x:b.x+radius*.13,y:b.y-radius*.06}]); paint.fill();
    paint.strokeStyle = 'rgba(177,218,247,0.09)'; paint.lineWidth = 0.8;
    paint.beginPath(); paint.moveTo(cx-radius*.10,cy-radius*.64); paint.lineTo(cx+radius*.51,cy+radius*.47); paint.stroke();
    paint.restore();
    const entryGlow = paint.createRadialGradient(entry.x, entry.y, 0, entry.x, entry.y, radius * 0.12);
    entryGlow.addColorStop(0, 'rgba(229,244,255,0.23)'); entryGlow.addColorStop(1, 'rgba(229,244,255,0)');
    paint.fillStyle = entryGlow; paint.fillRect(entry.x - radius*.12,entry.y - radius*.12,radius*.24,radius*.24);

    return { art, fronts: [
      { ...input, phase: 0.34, rate: 0.035, size: 25 },
      { start: rays[3].exit, end: rays[3].end, color: rays[3].color, phase: 0.26, rate: 0.027, size: 19 },
      { start: rays[5].exit, end: rays[5].end, color: rays[5].color, phase: 0.70, rate: 0.023, size: 16 },
    ] };
  }

  function drawPrism(scene, seconds) {
    ctx.save(); ctx.globalAlpha = 1; ctx.globalCompositeOperation = 'source-over'; ctx.shadowBlur = 0;
    ctx.drawImage(scene.art, 0, 0);
    for (const front of scene.fronts) {
      const dx = front.end.x - front.start.x, dy = front.end.y - front.start.y;
      const length = Math.hypot(dx, dy);
      if (length < 1) continue;
      const progress = (seconds * front.rate + front.phase) % 1;
      const fade = Math.min(1, progress * 8) * Math.min(1, (1 - progress) * 8);
      const distance = progress * length;
      ctx.lineCap = 'round'; ctx.lineWidth = 1.5;
      for (let segment = 0; segment < 4; segment++) {
        const from = Math.max(0, distance - front.size + front.size * segment / 4);
        const to = Math.max(0, distance - front.size + front.size * (segment + 1) / 4);
        ctx.strokeStyle = hexToRgba(front.color, (0.12 + segment * 0.10) * fade);
        ctx.beginPath(); ctx.moveTo(front.start.x + dx * from / length, front.start.y + dy * from / length);
        ctx.lineTo(front.start.x + dx * to / length, front.start.y + dy * to / length); ctx.stroke();
      }
      ctx.fillStyle = hexToRgba(front.color, 0.40 * fade);
      ctx.beginPath(); ctx.arc(front.start.x + dx * progress, front.start.y + dy * progress, 1.15, 0, Math.PI * 2); ctx.fill();
    }
    ctx.restore();
  }

  // ─── Effect registry + dispatch ────────────────────────────────────────────
  const effects = {
    prism: sceneEffect(spawnPrism, drawPrism),
    circuit: { spawn: genCircuit, frame: frameCircuit, static: staticCircuit,
      dispose() { circuitBoard = null; traces = []; pads = []; chips = []; pulses = []; } },
    matrix: sceneEffect(spawnDigitalRain, drawDigitalRain),
    stars: { spawn: spawnStars, frame: frameStars },
    snow: { spawn: spawnSnow, frame: frameSnow },
    bokeh: { spawn: spawnBokeh, frame: frameBokeh },
    petals: { spawn: spawnPetals, frame: framePetals },
    network: { spawn: spawnNetwork, frame: frameNetwork },
    synthwave: sceneEffect(spawnSynthwave, drawSynthwave),
    dracula: sceneEffect(spawnDracula, drawDracula),
    terminal: sceneEffect(spawnTerminal, drawTerminal),
    arcade: sceneEffect(spawnArcade, drawArcade),
    bubbles: sceneEffect(spawnBubblegum, drawBubblegum),
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
    const e = effects[effect]; if (e && e.dispose) e.dispose();
    if (canvas) { ctx.clearRect(0, 0, w, h); canvas.style.display = 'none'; }
  }

  function update() {
    stop();
    document.documentElement.setAttribute('data-bgfx-paused', String(!!document.hidden));
    const id = document.documentElement.getAttribute('data-theme');
    const eff = EFFECTS[id] || null;
    const intensity = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--bgfx-opacity'));
    if (document.hidden || intensity === 0) return;
    if (eff && ensureCanvas()) { effect = eff; start(); }
  }

  window.addEventListener('prism-theme-change', update);
  window.addEventListener('prism-bgfx-change', update);
  document.addEventListener('visibilitychange', update);
  if (motion && motion.addEventListener) motion.addEventListener('change', e => { reduceMotion = e.matches; update(); });
  update();
})();
