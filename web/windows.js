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
      const [l, t] = clampPos(ol + (e.clientX - sx), ot + (e.clientY - sy));
      el.style.left = l + 'px';
      el.style.top = t + 'px';
    }
    function dragEnd(e) {
      if (e.pointerId !== dragId) return;
      try { handle.releasePointerCapture(dragId); } catch (_) {}
      dragId = null;
      setIframePE(el, true);
      handle.classList.remove('dragging');
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
      el.style.width = Math.max(MIN_W, ow + (e.clientX - rsx)) + 'px';
      el.style.height = Math.max(MIN_H, oh + (e.clientY - rsy)) + 'px';
    }
    function rzEnd(e) {
      if (e.pointerId !== rzId) return;
      try { grip.releasePointerCapture(rzId); } catch (_) {}
      rzId = null;
      setIframePE(el, true);
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
      },
    };
  }

  window.PrismWindows = { makeWindow };
})();
