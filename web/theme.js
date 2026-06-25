// PRISM theming — a theme is just a set of values for the design tokens the
// whole UI is built on. The dashboard chrome already uses var(--…) everywhere,
// so re-skinning it is free. The hard part is the widgets: they are sandboxed
// iframes that do NOT inherit the parent's CSS variables, so this module also
// injects the active token block + a shared widget stylesheet into every widget
// document, and broadcasts live theme changes to them over postMessage.
//
// Exposed as window.PrismTheme (loaded as a classic script before app.js).

(function () {
  'use strict';

  // ─── Token sets ──────────────────────────────────────────────────────────
  // Every theme defines the FULL token set so switching is always complete.
  const THEMES = {
    'prism-dark': {
      label: 'Prism Dark',
      vars: {
        '--bg': '#080809', '--bg1': '#0e0e10', '--bg2': '#141416', '--bg3': '#1a1a1e', '--bg4': '#212126',
        '--border': '#232328', '--border2': '#2e2e35',
        '--text': '#e8e8f0', '--text2': '#9090a0', '--text3': '#55555f',
        '--accent': '#6b8afd', '--accent-dim': '#2a3566',
        '--green': '#4dba87', '--red': '#e06c75', '--yellow': '#e5c07b', '--orange': '#d19a66',
      },
    },
    'prism-light': {
      label: 'Prism Light',
      vars: {
        '--bg': '#f4f4f6', '--bg1': '#ffffff', '--bg2': '#ececef', '--bg3': '#e2e2e6', '--bg4': '#d6d6da',
        '--border': '#dcdce0', '--border2': '#c8c8d0',
        '--text': '#1c1c22', '--text2': '#55555f', '--text3': '#9090a0',
        '--accent': '#4f6ef0', '--accent-dim': '#d3dbfb',
        '--green': '#2e9e6b', '--red': '#d05661', '--yellow': '#b8862a', '--orange': '#c2772f',
      },
    },
    'midnight': {
      label: 'Midnight',
      vars: {
        '--bg': '#070b16', '--bg1': '#0b1120', '--bg2': '#111a2e', '--bg3': '#18233d', '--bg4': '#1f2d4d',
        '--border': '#1c2842', '--border2': '#26365a',
        '--text': '#e6ecf7', '--text2': '#8a99b8', '--text3': '#4d5a78',
        '--accent': '#5aa2ff', '--accent-dim': '#163059',
        '--green': '#3fb98a', '--red': '#e06c75', '--yellow': '#e5c07b', '--orange': '#d19a66',
      },
    },
    'nord': {
      label: 'Nord',
      vars: {
        '--bg': '#2e3440', '--bg1': '#2e3440', '--bg2': '#343b4a', '--bg3': '#3b4252', '--bg4': '#434c5e',
        '--border': '#3b4252', '--border2': '#4c566a',
        '--text': '#eceff4', '--text2': '#d8dee9', '--text3': '#7b8494',
        '--accent': '#88c0d0', '--accent-dim': '#3b4a52',
        '--green': '#a3be8c', '--red': '#bf616a', '--yellow': '#ebcb8b', '--orange': '#d08770',
      },
    },
    'solarized-dark': {
      label: 'Solarized Dark',
      vars: {
        '--bg': '#002b36', '--bg1': '#073642', '--bg2': '#08404f', '--bg3': '#0a4a5a', '--bg4': '#0d5263',
        '--border': '#0a4a5a', '--border2': '#11617a',
        '--text': '#eee8d5', '--text2': '#93a1a1', '--text3': '#586e75',
        '--accent': '#268bd2', '--accent-dim': '#0d3d52',
        '--green': '#859900', '--red': '#dc322f', '--yellow': '#b58900', '--orange': '#cb4b16',
      },
    },
    'rose-pine': {
      label: 'Rosé Pine',
      vars: {
        '--bg': '#191724', '--bg1': '#1f1d2e', '--bg2': '#26233a', '--bg3': '#2f2b45', '--bg4': '#393552',
        '--border': '#26233a', '--border2': '#403d5c',
        '--text': '#e0def4', '--text2': '#908caa', '--text3': '#6e6a86',
        '--accent': '#c4a7e7', '--accent-dim': '#2a2740',
        '--green': '#9ccfd8', '--red': '#eb6f92', '--yellow': '#f6c177', '--orange': '#ebbcba',
      },
    },
    'tech': {
      label: 'Tech',
      vars: {
        '--bg': '#0a0e14', '--bg1': '#0e131c', '--bg2': '#131a26', '--bg3': '#1a2433', '--bg4': '#223044',
        '--border': '#1c2738', '--border2': '#2a3a52',
        '--text': '#d6e4f0', '--text2': '#7d93ab', '--text3': '#455369',
        '--accent': '#18b3ff', '--accent-dim': '#0d3a52',
        '--green': '#2bd4a0', '--red': '#ff5d6c', '--yellow': '#ffcf5c', '--orange': '#ff9d4d',
      },
    },
    'cyberpunk': {
      label: 'Cyberpunk',
      vars: {
        '--bg': '#0a0420', '--bg1': '#0f0a33', '--bg2': '#161045', '--bg3': '#21185c', '--bg4': '#2e2278',
        '--border': '#241a60', '--border2': '#3b2c8f',
        '--text': '#e9e6ff', '--text2': '#9d8ad6', '--text3': '#6a5fa0',
        '--accent': '#7c4dff', '--accent-dim': '#2a1a5a',
        '--green': '#21e6ff', '--red': '#ff3c6e', '--yellow': '#fee440', '--orange': '#ff7b00',
      },
    },
    'matrix': {
      label: 'Matrix',
      vars: {
        '--bg': '#000000', '--bg1': '#020a02', '--bg2': '#041204', '--bg3': '#061a06', '--bg4': '#0a240a',
        '--border': '#0d3d0d', '--border2': '#135413',
        '--text': '#33ff66', '--text2': '#1fb84a', '--text3': '#0d6b2a',
        '--accent': '#39ff14', '--accent-dim': '#0a3d0a',
        '--green': '#39ff14', '--red': '#ff3333', '--yellow': '#ccff33', '--orange': '#ff9933',
      },
    },
    'synthwave': {
      label: 'Synthwave',
      vars: {
        '--bg': '#1a0b2e', '--bg1': '#240b36', '--bg2': '#2d1147', '--bg3': '#3a1659', '--bg4': '#491d70',
        '--border': '#3a1659', '--border2': '#5a2a85',
        '--text': '#ffe6ff', '--text2': '#d18ad6', '--text3': '#8a5a9e',
        '--accent': '#fe53bb', '--accent-dim': '#5a1a4d',
        '--green': '#08f7fe', '--red': '#ff5e5e', '--yellow': '#f5d300', '--orange': '#ff8b3d',
      },
    },
  };

  const DEFAULT_THEME = 'prism-dark';
  const STORAGE_KEY = 'prism-theme';

  // The shared widget stylesheet (the agent's class/token vocabulary) lives in
  // /widget-base.css and is pulled in via <link>, so the dashboard srcdoc path
  // and the server-side /plugins/*.html preview path share exactly one source.

  // The live-update bootstrap injected into each widget: listens for theme
  // broadcasts from the dashboard and updates :root in place (no reload).
  const WIDGET_BOOTSTRAP = `(function(){function a(v){var r=document.documentElement.style;for(var k in v)r.setProperty(k,v[k]);}window.addEventListener('message',function(e){var d=e.data;if(d&&d.type==='prism-theme'&&d.vars)a(d.vars);});})();`;

  // ─── Custom themes (localStorage) ─────────────────────────────────────────
  const CUSTOM_KEY = 'prism-custom-themes';
  function readCustomArr() {
    try { return JSON.parse(localStorage.getItem(CUSTOM_KEY) || '[]'); } catch (_) { return []; }
  }
  function loadCustomThemes() {
    const out = {};
    for (const t of readCustomArr()) if (t && t.id && t.vars) out[t.id] = { label: t.label || t.id, vars: t.vars, custom: true };
    return out;
  }
  function getAllThemes() { return Object.assign({}, THEMES, loadCustomThemes()); }
  function saveCustomTheme(id, label, vars) {
    const arr = readCustomArr();
    const i = arr.findIndex((t) => t.id === id);
    const entry = { id, label, vars };
    if (i >= 0) arr[i] = entry; else arr.push(entry);
    localStorage.setItem(CUSTOM_KEY, JSON.stringify(arr));
  }
  function deleteCustomTheme(id) {
    localStorage.setItem(CUSTOM_KEY, JSON.stringify(readCustomArr().filter((t) => t.id !== id)));
    if (localStorage.getItem(STORAGE_KEY) === id) applyTheme(DEFAULT_THEME);
  }

  function getThemeId() {
    const id = localStorage.getItem(STORAGE_KEY);
    return getAllThemes()[id] ? id : DEFAULT_THEME;
  }

  function activeVars() {
    const t = getAllThemes()[getThemeId()];
    return (t || THEMES[DEFAULT_THEME]).vars;
  }

  function varsToCSS(vars) {
    let s = ':root{';
    for (const k in vars) s += k + ':' + vars[k] + ';';
    return s + '}';
  }

  // Apply to the dashboard itself + broadcast to every widget iframe.
  function applyTheme(id, broadcast = true) {
    const all = getAllThemes();
    if (!all[id]) id = DEFAULT_THEME;
    const vars = all[id].vars;
    const root = document.documentElement.style;
    for (const k in vars) root.setProperty(k, vars[k]);
    // data-theme drives the per-theme animated background (see style.css #bg-fx).
    document.documentElement.setAttribute('data-theme', id);
    localStorage.setItem(STORAGE_KEY, id);
    if (broadcast) {
      // widgets (board) + the full-pane app iframe both get live updates
      const frames = [...document.querySelectorAll('.widget-body iframe')];
      const appFrame = document.getElementById('app-frame');
      if (appFrame) frames.push(appFrame);
      frames.forEach((f) => {
        try { f.contentWindow.postMessage({ type: 'prism-theme', vars }, '*'); } catch (_) {}
      });
    }
    // Let the animated background (bgfx.js) react: enable/disable + recolour.
    window.dispatchEvent(new CustomEvent('prism-theme-change', { detail: { id, vars } }));
  }

  // Live preview a token map without persisting it (used by the theme studio).
  function previewVars(vars) {
    const root = document.documentElement.style;
    for (const k in vars) root.setProperty(k, vars[k]);
    const frames = [...document.querySelectorAll('.widget-body iframe')];
    const appFrame = document.getElementById('app-frame');
    if (appFrame) frames.push(appFrame);
    frames.forEach((f) => { try { f.contentWindow.postMessage({ type: 'prism-theme', vars }, '*'); } catch (_) {} });
  }

  // ─── Palette generation: one accent → a coherent token set ────────────────
  function hexToHSL(hex) {
    hex = (hex || '').replace('#', '');
    if (hex.length === 3) hex = hex.split('').map((c) => c + c).join('');
    const r = parseInt(hex.slice(0, 2), 16) / 255, g = parseInt(hex.slice(2, 4), 16) / 255, b = parseInt(hex.slice(4, 6), 16) / 255;
    const max = Math.max(r, g, b), min = Math.min(r, g, b);
    let h = 0, s = 0; const l = (max + min) / 2;
    if (max !== min) {
      const d = max - min;
      s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
      if (max === r) h = (g - b) / d + (g < b ? 6 : 0);
      else if (max === g) h = (b - r) / d + 2;
      else h = (r - g) / d + 4;
      h *= 60;
    }
    return [h, s * 100, l * 100];
  }
  function hsl(h, s, l) {
    h = ((h % 360) + 360) % 360; s = Math.max(0, Math.min(100, s)) / 100; l = Math.max(0, Math.min(100, l)) / 100;
    const c = (1 - Math.abs(2 * l - 1)) * s, x = c * (1 - Math.abs((h / 60) % 2 - 1)), m = l - c / 2;
    let r = 0, g = 0, b = 0;
    if (h < 60) [r, g, b] = [c, x, 0]; else if (h < 120) [r, g, b] = [x, c, 0];
    else if (h < 180) [r, g, b] = [0, c, x]; else if (h < 240) [r, g, b] = [0, x, c];
    else if (h < 300) [r, g, b] = [x, 0, c]; else [r, g, b] = [c, 0, x];
    const hx = (n) => Math.round((n + m) * 255).toString(16).padStart(2, '0');
    return '#' + hx(r) + hx(g) + hx(b);
  }

  // generatePalette derives the full Prism token set from a single accent color.
  function generatePalette(accent, mode, harmony) {
    const [h, s] = hexToHSL(accent);
    const dark = mode !== 'light';
    let bgH = h, brH = h;
    if (harmony === 'complementary') { brH = (h + 180) % 360; }
    else if (harmony === 'analogous') { bgH = (h - 25 + 360) % 360; brH = (h + 25) % 360; }
    else if (harmony === 'triadic') { bgH = (h + 120) % 360; brH = (h + 240) % 360; }
    const t = Math.min(s, 26);                 // subtle surface tint
    const v = { '--accent': accent };
    if (dark) {
      v['--bg'] = hsl(bgH, t * 0.6, 6); v['--bg1'] = hsl(bgH, t * 0.6, 8);
      v['--bg2'] = hsl(bgH, t * 0.6, 11); v['--bg3'] = hsl(bgH, t * 0.6, 15); v['--bg4'] = hsl(bgH, t * 0.6, 20);
      v['--border'] = hsl(brH, t * 0.7, 18); v['--border2'] = hsl(brH, t * 0.7, 27);
      v['--text'] = hsl(h, Math.min(s, 14), 93); v['--text2'] = hsl(h, Math.min(s, 12), 62); v['--text3'] = hsl(h, Math.min(s, 10), 42);
      v['--accent-dim'] = hsl(h, Math.max(s, 40), 22);
    } else {
      v['--bg'] = hsl(bgH, t * 0.5, 96); v['--bg1'] = hsl(bgH, t * 0.4, 100);
      v['--bg2'] = hsl(bgH, t * 0.5, 93); v['--bg3'] = hsl(bgH, t * 0.5, 88); v['--bg4'] = hsl(bgH, t * 0.5, 82);
      v['--border'] = hsl(brH, t * 0.6, 86); v['--border2'] = hsl(brH, t * 0.6, 77);
      v['--text'] = hsl(h, Math.min(s, 25), 14); v['--text2'] = hsl(h, Math.min(s, 16), 38); v['--text3'] = hsl(h, Math.min(s, 12), 60);
      v['--accent-dim'] = hsl(h, Math.max(s, 35), 87);
    }
    v['--green'] = '#4dba87'; v['--red'] = '#e06c75'; v['--yellow'] = '#e5c07b'; v['--orange'] = '#d19a66';
    return v;
  }

  // ─── UI preferences (scale / density / animated-bg intensity) ─────────────
  const SCALE_KEY = 'prism-ui-scale', DENSITY_KEY = 'prism-density', BGFX_KEY = 'prism-bgfx';
  function getPref(k, d) { return localStorage.getItem(k) || d; }
  function applyUIScale(v) {
    v = v || getPref(SCALE_KEY, '100'); localStorage.setItem(SCALE_KEY, v);
    document.documentElement.style.setProperty('--ui-scale', String(Number(v) / 100));
  }
  function applyDensity(v) {
    v = v || getPref(DENSITY_KEY, 'comfortable'); localStorage.setItem(DENSITY_KEY, v);
    const c = document.documentElement.classList;
    c.remove('density-compact', 'density-spacious');
    if (v !== 'comfortable') c.add('density-' + v);
  }
  function applyBgfx(v) {
    v = (v == null ? getPref(BGFX_KEY, '100') : String(v)); localStorage.setItem(BGFX_KEY, v);
    document.documentElement.style.setProperty('--bgfx-opacity', String(Number(v) / 100));
  }
  function applyPrefs() { applyTheme(getThemeId(), false); applyUIScale(); applyDensity(); applyBgfx(); }

  // Wrap an agent-authored widget document with the active theme tokens, the
  // shared widget stylesheet and the live-update bootstrap.
  function composeWidgetDoc(content) {
    const head =
      '<style id="prism-theme-vars">' + varsToCSS(activeVars()) + '</style>' +
      '<link rel="stylesheet" href="/widget-base.css">' +
      '<script>' + WIDGET_BOOTSTRAP + '<\/script>';
    let m = content.match(/<head[^>]*>/i);
    if (m) { const i = m.index + m[0].length; return content.slice(0, i) + head + content.slice(i); }
    m = content.match(/<html[^>]*>/i);
    if (m) { const i = m.index + m[0].length; return content.slice(0, i) + '<head>' + head + '</head>' + content.slice(i); }
    return head + content;
  }

  // Fill a <select> with the available themes (presets + custom) and wire live
  // switching. Idempotent (uses onchange) so it can be re-called on updates.
  function populateSelect(sel) {
    if (!sel) return;
    const all = getAllThemes();
    sel.innerHTML = '';
    for (const id in all) {
      const o = document.createElement('option');
      o.value = id;
      o.textContent = all[id].label + (all[id].custom ? ' ✦' : '');
      sel.appendChild(o);
    }
    sel.value = getThemeId();
    sel.onchange = () => applyTheme(sel.value);
  }

  window.PrismTheme = {
    THEMES, getAllThemes, getThemeId, activeVars,
    applyTheme, previewVars, composeWidgetDoc, populateSelect,
    saveCustomTheme, deleteCustomTheme, generatePalette,
    applyUIScale, applyDensity, applyBgfx, getPref,
    KEYS: { THEME: STORAGE_KEY, SCALE: SCALE_KEY, DENSITY: DENSITY_KEY, BGFX: BGFX_KEY },
  };

  // Cross-tab: when settings change prefs/themes, re-apply + refresh the picker.
  window.addEventListener('storage', (e) => {
    if ([STORAGE_KEY, CUSTOM_KEY, SCALE_KEY, DENSITY_KEY, BGFX_KEY].includes(e.key)) {
      applyPrefs();
      const sel = document.getElementById('theme-select');
      if (sel) populateSelect(sel);
    }
  });

  // Apply everything immediately (before app.js runs) so there is no flash.
  applyPrefs();
})();
