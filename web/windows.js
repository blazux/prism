// Free-floating window manager — replaces GridStack. Each widget is an
// absolutely-positioned element that can be dragged by its header and resized
// from a bottom-right handle, with click-to-front z-ordering. Framework-free,
// pointer-event based. Exposed as window.PrismWindows (classic script).
//
// makeWindow(el, { handle, container, onChange, skip }) -> { destroy() }
//   el        : the .widget-window element (position:absolute)
//   handle    : drag handle (the widget header)
//   container : positioned scroll container (defaults to el.offsetParent)
//   onChange  : ({x,y,w,h}) => void, called after a drag/resize settles
//   skip      : selector of header descendants that must NOT start a drag

(function () {
  'use strict';

  const MIN_W = 200;
  const MIN_H = 120;
  const SNAP = 8; // magnetic snap threshold (px)
  // Snapping is on by default; users can turn it off (Settings → Appearance) or
  // hold Alt to place freely during a single drag.
  function snapEnabled() { try { return localStorage.getItem('prism-window-snap') !== '0'; } catch (_) { return true; } }
  // Best alignment within SNAP: cands = [[edgeValue, offsetFromBase], …].
  function snapAxis(cands, lines) {
    let best = null;
    for (const c of cands) for (const ln of lines) {
      const d = Math.abs(c[0] - ln);
      if (d <= SNAP && (!best || d < best.d)) best = { d: d, line: ln, off: c[1] };
    }
    return best;
  }
  // Widgets live in the band 20..Z_CAP. The left rail sits just above this band
  // (see #rail z-index in style.css) so its hover flyout is never hidden behind
  // a window. When the counter reaches the cap we compact every window's z-index
  // back down so the band can't creep into the rail/modal layers.
  let zTop = 20;
  const Z_CAP = 200;

  function renormalizeZ() {
    const wins = [...document.querySelectorAll('.widget-window')]
      .sort((a, b) => (parseInt(a.style.zIndex) || 0) - (parseInt(b.style.zIndex) || 0));
    zTop = 20;
    for (const el of wins) el.style.zIndex = String(++zTop);
  }

  function bringToFront(el) {
    if (zTop >= Z_CAP) renormalizeZ();
    el.style.zIndex = String(++zTop);
  }

  // Toggle iframe pointer-events so the cursor can't get "swallowed" by a
  // widget's own iframe mid-drag (it would otherwise eat pointermove events).
  function setIframePE(el, on) {
    el.querySelectorAll('iframe').forEach((f) => { f.style.pointerEvents = on ? '' : 'none'; });
  }

  function clamp(v, min, max) {
    return Math.max(min, Math.min(max, v));
  }

  function makeWindow(el, opts) {
    opts = opts || {};
    const handle = opts.handle;
    const container = opts.container || el.offsetParent || document.body;
    const onChange = opts.onChange || function () {};
    const skipSel = opts.skip || 'button, input, select, textarea, a';

    // Keep at least a sliver on-screen so windows can never be lost entirely.
    function clampPos(left, top) {
      const maxLeft = Math.max(0, container.clientWidth - 60);
      const maxTop = Math.max(0, container.clientHeight - 30);
      return [clamp(left, 0, maxLeft), clamp(top, 0, maxTop)];
    }

    // ── Snapping & alignment guides ───────────────────────────────────────
    let guideV = null, guideH = null;
    function guide(axis, pos) {
      let g = axis === 'v' ? guideV : guideH;
      if (!g) {
        g = document.createElement('div');
        g.className = 'snap-guide ' + axis;
        container.appendChild(g);
        if (axis === 'v') guideV = g; else guideH = g;
      }
      if (pos == null) { g.style.display = 'none'; return; }
      g.style.display = 'block';
      if (axis === 'v') g.style.left = pos + 'px'; else g.style.top = pos + 'px';
    }
    function clearGuides() { guide('v', null); guide('h', null); }
    // Snap lines from the container edges and every other visible window.
    function collectLines() {
      const vlines = [0, container.clientWidth], hlines = [0, container.clientHeight], widths = [], heights = [];
      document.querySelectorAll('.widget-window').forEach((w) => {
        if (w === el || w.offsetParent === null) return; // skip self + hidden/minimized
        const l = w.offsetLeft, t = w.offsetTop, ww = w.offsetWidth, hh = w.offsetHeight;
        vlines.push(l, l + ww, l + ww / 2);
        hlines.push(t, t + hh, t + hh / 2);
        widths.push(ww); heights.push(hh);
      });
      return { vlines: vlines, hlines: hlines, widths: widths, heights: heights };
    }

    // ── Raise on interaction ──────────────────────────────────────────────
    const onDown = () => bringToFront(el);
    el.addEventListener('pointerdown', onDown, true);

    // ── Drag (via header) ─────────────────────────────────────────────────
    let dragId = null, sx = 0, sy = 0, ol = 0, ot = 0;

    function dragStart(e) {
      if (e.button !== 0) return;
      if (e.target.closest(skipSel)) return;
      dragId = e.pointerId;
      sx = e.clientX; sy = e.clientY;
      ol = el.offsetLeft; ot = el.offsetTop;
      handle.setPointerCapture(dragId);
      setIframePE(el, false);
      handle.classList.add('dragging');
      e.preventDefault();
    }
    function dragMove(e) {
      if (e.pointerId !== dragId) return;
      let [l, t] = clampPos(ol + (e.clientX - sx), ot + (e.clientY - sy));
      if (snapEnabled() && !e.altKey) {
        const w = el.offsetWidth, h = el.offsetHeight;
        const lines = collectLines();
        const sX = snapAxis([[l, 0], [l + w / 2, w / 2], [l + w, w]], lines.vlines);
        const sY = snapAxis([[t, 0], [t + h / 2, h / 2], [t + h, h]], lines.hlines);
        if (sX) { l = sX.line - sX.off; guide('v', sX.line); } else guide('v', null);
        if (sY) { t = sY.line - sY.off; guide('h', sY.line); } else guide('h', null);
      } else clearGuides();
      el.style.left = l + 'px';
      el.style.top = t + 'px';
    }
    function dragEnd(e) {
      if (e.pointerId !== dragId) return;
      try { handle.releasePointerCapture(dragId); } catch (_) {}
      dragId = null;
      setIframePE(el, true);
      handle.classList.remove('dragging');
      clearGuides();
      emit();
    }
    handle.addEventListener('pointerdown', dragStart);
    handle.addEventListener('pointermove', dragMove);
    handle.addEventListener('pointerup', dragEnd);
    handle.addEventListener('pointercancel', dragEnd);

    // ── Resize (bottom-right handle) ──────────────────────────────────────
    const grip = document.createElement('div');
    grip.className = 'window-resize';
    el.appendChild(grip);

    let rzId = null, rsx = 0, rsy = 0, ow = 0, oh = 0;
    function rzStart(e) {
      if (e.button !== 0) return;
      rzId = e.pointerId;
      rsx = e.clientX; rsy = e.clientY;
      ow = el.offsetWidth; oh = el.offsetHeight;
      grip.setPointerCapture(rzId);
      setIframePE(el, false);
      bringToFront(el);
      e.preventDefault();
      e.stopPropagation();
    }
    function rzMove(e) {
      if (e.pointerId !== rzId) return;
      let w = Math.max(MIN_W, ow + (e.clientX - rsx));
      let h = Math.max(MIN_H, oh + (e.clientY - rsy));
      if (snapEnabled() && !e.altKey) {
        const left0 = el.offsetLeft, top0 = el.offsetTop;
        const lines = collectLines();
        // Right edge → a snap line, OR width → a neighbour's width.
        let bw = null;
        lines.vlines.forEach((ln) => { const cw = ln - left0, d = Math.abs((left0 + w) - ln); if (cw >= MIN_W && d <= SNAP && (!bw || d < bw.d)) bw = { d: d, v: cw, g: ln }; });
        lines.widths.forEach((ww) => { const d = Math.abs(w - ww); if (ww >= MIN_W && d <= SNAP && (!bw || d < bw.d)) bw = { d: d, v: ww, g: left0 + ww }; });
        let bh = null;
        lines.hlines.forEach((ln) => { const ch = ln - top0, d = Math.abs((top0 + h) - ln); if (ch >= MIN_H && d <= SNAP && (!bh || d < bh.d)) bh = { d: d, v: ch, g: ln }; });
        lines.heights.forEach((hh) => { const d = Math.abs(h - hh); if (hh >= MIN_H && d <= SNAP && (!bh || d < bh.d)) bh = { d: d, v: hh, g: top0 + hh }; });
        if (bw) { w = bw.v; guide('v', bw.g); } else guide('v', null);
        if (bh) { h = bh.v; guide('h', bh.g); } else guide('h', null);
      } else clearGuides();
      el.style.width = w + 'px';
      el.style.height = h + 'px';
    }
    function rzEnd(e) {
      if (e.pointerId !== rzId) return;
      try { grip.releasePointerCapture(rzId); } catch (_) {}
      rzId = null;
      setIframePE(el, true);
      clearGuides();
      emit();
    }
    grip.addEventListener('pointerdown', rzStart);
    grip.addEventListener('pointermove', rzMove);
    grip.addEventListener('pointerup', rzEnd);
    grip.addEventListener('pointercancel', rzEnd);

    function emit() {
      onChange({ x: el.offsetLeft, y: el.offsetTop, w: el.offsetWidth, h: el.offsetHeight });
    }

    bringToFront(el);

    return {
      focus: () => bringToFront(el),
      destroy() {
        el.removeEventListener('pointerdown', onDown, true);
        handle.removeEventListener('pointerdown', dragStart);
        handle.removeEventListener('pointermove', dragMove);
        handle.removeEventListener('pointerup', dragEnd);
        handle.removeEventListener('pointercancel', dragEnd);
        grip.remove();
        if (guideV) guideV.remove();
        if (guideH) guideH.remove();
      },
    };
  }

  window.PrismWindows = { makeWindow };
})();
