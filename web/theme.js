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

  function getThemeId() {
    const id = localStorage.getItem(STORAGE_KEY);
    return THEMES[id] ? id : DEFAULT_THEME;
  }

  function activeVars() {
    return THEMES[getThemeId()].vars;
  }

  function varsToCSS(vars) {
    let s = ':root{';
    for (const k in vars) s += k + ':' + vars[k] + ';';
    return s + '}';
  }

  // Apply to the dashboard itself + broadcast to every widget iframe.
  function applyTheme(id, broadcast = true) {
    if (!THEMES[id]) id = DEFAULT_THEME;
    const vars = THEMES[id].vars;
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

  // Fill a <select> with the available themes and wire live switching.
  function populateSelect(sel) {
    if (!sel) return;
    sel.innerHTML = '';
    for (const id in THEMES) {
      const o = document.createElement('option');
      o.value = id;
      o.textContent = THEMES[id].label;
      sel.appendChild(o);
    }
    sel.value = getThemeId();
    sel.addEventListener('change', () => applyTheme(sel.value));
  }

  window.PrismTheme = {
    THEMES,
    getThemeId,
    activeVars,
    applyTheme,
    composeWidgetDoc,
    populateSelect,
  };

  // Apply the saved theme to the dashboard immediately (before app.js runs) so
  // there is no flash of the default palette.
  applyTheme(getThemeId(), false);
})();
