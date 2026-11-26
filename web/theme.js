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

  // Generated content may load this helper without access to account settings.
  // In that case keep theme choices in memory for the lifetime of the page.
  const transient = new Map();
  const preferences = window.PrismPreferences || {
    getItem: key => transient.get(key) ?? null,
    setItem: (key, value) => transient.set(key, String(value)),
    ready: Promise.resolve(),
  };
  let applyingPreferences = false;
  let preferencesReady = false;
  let renderedVars = null;
  function savePreference(key, value) {
    if (!applyingPreferences) preferences.setItem(key, String(value));
  }

  // Every preset defines a complete palette; derived controls and surfaces reset on switch.
  const COLOR_KEYS = ["--bg", "--bg1", "--bg2", "--bg3", "--bg4", "--border", "--border2", "--text", "--text2", "--text3", "--accent", "--accent-dim", "--green", "--red", "--yellow", "--orange"];
  const BASE_VARS = Object.fromEntries(COLOR_KEYS.map((key, i) => [key, ['#090c13','#10151f','#171e2b','#202a3a','#2b3749','#293549','#3c4c65','#eef2fb','#b9c3d6','#91a0b9','#9baeff','#293654','#72d6ad','#ff939d','#e9c783','#f1ae7f'][i]]));

  function luminance(hex) {
    const c = hex.slice(1).match(/../g).map(v => parseInt(v, 16) / 255).map(v => v <= .04045 ? v / 12.92 : ((v + .055) / 1.055) ** 2.4);
    return c[0] * .2126 + c[1] * .7152 + c[2] * .0722;
  }
  function contrast(a, b) { const x = luminance(a), y = luminance(b); return (Math.max(x, y) + .05) / (Math.min(x, y) + .05); }
  function mix(a, b, amount) {
    const rgb = value => value.slice(1).match(/../g).map(v => parseInt(v, 16));
    return '#' + rgb(a).map((v, i) => Math.round(v + (rgb(b)[i] - v) * amount).toString(16).padStart(2, '0')).join('');
  }
  function readable(color, backgrounds, minimum = 4.5) {
    const target = luminance(backgrounds[0]) < .4 ? '#ffffff' : '#000000';
    for (let step = 0; step <= 100; step++) {
      const candidate = mix(color, target, step / 100);
      if (backgrounds.every(bg => contrast(candidate, bg) >= minimum)) return candidate;
    }
    return target;
  }
  function onColor(color) {
    return contrast('#111820', color) >= 4.5 ? '#111820' : contrast('#ffffff', color) >= 4.5 ? '#ffffff' : '#000000';
  }
  function completeVars(input = {}, radius = '9px') {
    const vars = { ...BASE_VARS };
    for (const key of COLOR_KEYS) {
      let value = input[key];
      if (typeof value === 'string' && /^#[0-9a-f]{3}$/i.test(value)) value = '#' + [...value.slice(1)].map(c => c + c).join('');
      if (typeof value === 'string' && /^#[0-9a-f]{6}$/i.test(value)) vars[key] = value;
    }
    const dark = luminance(vars['--bg1']) < .4;
    vars['color-scheme'] = dark ? 'dark' : 'light';
    vars['--on-accent'] = onColor(vars['--accent']);
    vars['--on-danger'] = onColor(vars['--red']);
    vars['--on-success'] = onColor(vars['--green']);
    vars['--on-warning'] = onColor(vars['--yellow']);
    vars['--radius'] = typeof input['--radius'] === 'string' && /^(?:[0-9]|1[0-9]|2[0-4])px$/.test(input['--radius']) ? input['--radius'] : radius;
    vars['--shadow-color'] = dark ? '#00000066' : '#24344a1a';
    vars['--panel-shadow'] = dark ? '0 12px 36px #00000038' : '0 8px 28px #24344a12';
    vars['--popup-shadow'] = dark ? '0 16px 48px #00000080' : '0 14px 40px #24344a24';
    return vars;
  }
  function readablePalette(input, radius) {
    const vars = completeVars(input, radius);
    for (const key of ['--text', '--text2', '--text3']) vars[key] = readable(vars[key], [vars['--bg1'], vars['--bg2']]);
    for (const key of ['--accent', '--green', '--red', '--yellow', '--orange']) vars[key] = readable(vars[key], [vars['--bg1']]);
    return completeVars(vars, radius);
  }
  function preset(label, group, description, mode, colors, radius) {
    return { label, group, description, mode, vars: readablePalette(Object.fromEntries(COLOR_KEYS.map((key, i) => [key, '#' + colors.split(' ')[i]])), radius) };
  }

  const THEMES = {
    "prism": preset("Prism", "essential", "A glass prism separates white light into a quiet moving spectrum.", "dark", "0a0e18 121927 1b2536 26354b 354762 2d3e58 4c6484 f0f6ff c3d0e3 9bafcd b6b7ff 333454 8bdfc1 ffa1b4 efd98d f6bf91", "8px"),
    "prism-dark": preset("Prism Dark", "essential", "Balanced dark surfaces with a clear iris accent.", "dark", "090c13 10151f 171e2b 202a3a 2b3749 293549 3c4c65 eef2fb b9c3d6 91a0b9 9baeff 293654 72d6ad ff939d e9c783 f1ae7f", "9px"),
    "prism-light": preset("Prism Light", "essential", "Cool white, soft lavender and crisp blue.", "light", "f3f5fb ffffff eceff8 e2e7f2 d4ddeb d6deed bbc7df 20283b 4b5670 606b83 4657b8 e3e7fa 226c50 b1374f 815c13 9c5023", "9px"),
    "graphite": preset("Graphite", "professional", "Charcoal, silver and a restrained steel blue.", "dark", "101113 17191c 202327 292d32 353a40 34393f 4b535e f0f1f3 c1c6ce 9ba3af adbfda 303d50 8fc6a5 e49fa8 dac28f d6ac86", "6px"),
    "slate": preset("Slate", "professional", "Deep blue slate with a quiet mineral accent.", "dark", "111a23 18232e 212f3c 2c3d4c 394c5b 33495a 506777 eaf2f7 b6c9d5 95afc0 9cbfd3 2c4457 91c7b6 e4a2aa d9c093 d2ad91", "7px"),
    "paper": preset("Paper", "professional", "Warm ivory, olive ink and a paper canvas.", "light", "f1eee6 faf8f2 eae5d9 e0d9ca d1c8b7 d4cdbc b9b09a 302e25 5b584a 6c6756 67623e e6e1c9 456b42 a64040 7b5e14 96502b", "7px"),
    "boardroom": preset("Boardroom", "professional", "Porcelain white and precise navy blue.", "light", "eef2f6 ffffff e5ebf1 d9e2eb c8d5e1 ccd7e3 aabbd0 1c3047 455c74 5b6d82 2d557e dce8f4 286349 a83f4e 805d14 954a25", "5px"),
    "ink": preset("Ink", "professional", "A sharp black-and-white workspace.", "light", "f4f4f2 ffffff eaeae7 dfdfdb cfcfca d3d5cf b3b7ae 202220 4c4e4b 63655f 303633 e0e5df 316649 a53642 775b13 8b4c2c", "3px"),
    "spruce": preset("Spruce", "professional", "Fresh linen, evergreen and muted sage.", "light", "edf2ed f9fcf8 e1eae1 d5e2d5 c5d5c7 c9d8cb a9bfae 1e382c 435d4d 596f60 30664e d7e7dc 2b6743 a63c4c 7c5a15 97532d", "8px"),
    "midnight": preset("Midnight", "studio", "Inky blue and cool starlight.", "dark", "090e1d 10192b 19263d 243550 324665 2c3e5b 476184 edf3ff b5c9e6 94acd0 9dc6ff 283e61 81d4b5 ff9aa9 e8cd8c ecb48d", "10px"),
    "nord": preset("Nord", "studio", "Frosted blue-gray and soft arctic color.", "dark", "272e39 303947 3a4554 465264 536174 4c5b6d 657891 f1f5fa d4dfeb b2c2d4 a9d3dd 3c5260 bbd49f f3a0aa f1d99e ebbfa5", "8px"),
    "solarized-dark": preset("Solarized Dark", "studio", "Petrol blue, parchment text and amber details.", "dark", "052a34 0a3540 13434f 205460 2f6671 2a5862 42717a f4eedb c4d1c9 a3bbb8 8ac8d4 214e5c b9cf87 f3a39b ebcc80 efb18b", "8px"),
    "rose-pine": preset("Rosé Pine", "studio", "Smoky plum, lavender and rose.", "dark", "1c1928 252132 302b40 3d354e 4d435f 493d59 675477 f1eaf9 cfc1df b3a3c7 d5b9f3 473556 b6dacd f6a9c2 f1d3a5 eac1b9", "12px"),
    "espresso": preset("Espresso", "studio", "Roasted coffee, warm cream and copper.", "dark", "1a1513 241e1a 302823 3d332b 4c4036 4e4035 6b5846 f7eddf d7c5af bba58a e4ba8f 513c2c b7c994 e5a79d e8cb8b ddb091", "10px"),
    "dune": preset("Dune", "studio", "Sand, terracotta and sunlit cream.", "light", "f4eadb fff8ed efe0cc e5d2b8 d8bd9c dcc6aa c3a47f 443328 70513c 806047 995332 f1d7c4 516b3a ab3f42 7d5b15 9a5122", "11px"),
    "tech": preset("Tech", "playful", "Cold titanium, electric cyan and a network grid.", "dark", "09141c 101f2a 192d3b 243c4c 334f60 2b4b5d 41677d e7f5fc b1d4e6 8eb8cf 70d9ff 17475a 7ee3c3 ff9ba8 f0d17f f2b184", "5px"),
    "cyberpunk": preset("Cyberpunk", "playful", "Night-city blue, signal yellow and cyan circuitry.", "dark", "0a1019 101a25 172535 203146 2b3e55 2a4357 46647b edf6f8 b3c9d4 8fa8ba f6e75a 393817 74dbcd ff91b3 f6e75a f3af77", "3px"),
    "matrix": preset("Matrix", "playful", "Terminal green on black with digital rain.", "dark", "020804 07120b 0e1e14 172d1e 223d2a 24432d 3b6343 e0f3df add4af 87b88e 7ee787 163d24 87e8ac ff9d9d e4db87 edb387", "3px"),
    "synthwave": preset("Synthwave", "playful", "A striped sunset, wireframe mountains and a moving neon grid.", "dark", "190f2c 261637 351e49 47265e 5b3276 563265 774684 fceefe decce9 cfaccf ff91d5 5b2b55 93e3ec ffa8b6 f1db9a f4bd9a", "8px"),
    "dracula": preset("Dracula", "playful", "A moonlit castle, drifting mist and bats in a violet night.", "dark", "20212e 282a38 333646 414558 51566c 4d4e69 6b6c8b f4f2fc ccd1e7 aeb7d3 c7adff 4a3a67 a3e2b9 ffa3b8 f0db96 f5be93", "8px"),
    "amber-terminal": preset("Amber Terminal", "playful", "Amber phosphor, scrolling memory and a typing cursor.", "dark", "100c05 1a140a 282010 372c17 493b21 4d3d20 6c5730 f8edce ddcca0 bfaa77 f5c86a 463514 bed394 f2a18b f1d17c e8b184", "3px"),
    "arcade": preset("Arcade", "playful", "Pixel spacecraft and mint invaders in a violet starfield.", "dark", "19142d 251d3b 342749 45335e 594176 614377 82619c f9f0ff dcc7ef bda4d6 ffb087 593443 a8e9ca ff9fbc efd88c eeb583", "4px"),
    "bubblegum": preset("Bubblegum", "playful", "Pink, lilac and floating iridescent soap bubbles.", "light", "fceef5 fff9fc f3e0ed ebd1e3 dfbdd6 e0bfd6 c89dba 4b2a47 724768 855578 a23877 f2d4e9 366c57 b03759 815b13 a34d32", "14px"),
  };

  const DEFAULT_THEME = 'prism-dark';
  const STORAGE_KEY = 'prism-theme';
  const GLASS_THEMES = new Set(['prism', 'midnight', 'rose-pine', 'tech', 'cyberpunk', 'matrix', 'synthwave', 'dracula', 'amber-terminal', 'arcade']);

  // The shared widget stylesheet (the agent's class/token vocabulary) lives in
  // /widget-base.css and is pulled in via <link>, so the dashboard srcdoc path
  // and the server-side /plugins/*.html preview path share exactly one source.

  // The live-update bootstrap injected into each widget: listens for theme
  // broadcasts from the dashboard and updates :root in place (no reload).
  const WIDGET_BOOTSTRAP = `(function(){
    window.addEventListener('message',function(e){
      var d=e.data,o=window.PRISM_PARENT_ORIGIN;
      if(e.source!==window.parent||(o&&e.origin!==o)||!d||d.type!=='prism-theme'||!d.vars)return;
      var r=document.documentElement.style;for(var k in d.vars)r.setProperty(k,d.vars[k]);
      if(o){var keys=['--bg1','--text','--text2','--accent','--border','color-scheme'];
        window.parent.postMessage({type:'prism-widget-theme-ready',theme:keys.map(function(k){return r.getPropertyValue(k).trim();}).join('|')},o);}
    });
  })();`;

  // ─── Custom themes ───────────────────────────────────────────────────────
  const CUSTOM_KEY = 'prism-custom-themes';
  function readCustomArr() {
    try { const arr = JSON.parse(preferences.getItem(CUSTOM_KEY) || '[]'); return Array.isArray(arr) ? arr : []; } catch (_) { return []; }
  }
  function loadCustomThemes() {
    const out = {};
    for (const t of readCustomArr()) if (t && typeof t.id === 'string' && t.vars && !THEMES[t.id]) {
      const vars = completeVars(t.vars);
      out[t.id] = { label: t.label || t.id, vars, custom: true, group: 'custom', mode: vars['color-scheme'], description: 'Your saved palette.' };
    }
    return out;
  }
  function getAllThemes() { return { ...THEMES, ...loadCustomThemes() }; }
  function saveCustomTheme(id, label, vars) {
    const arr = readCustomArr();
    const i = arr.findIndex((t) => t.id === id);
    const entry = { id, label, vars: completeVars(vars) };
    if (i >= 0) arr[i] = entry; else arr.push(entry);
    savePreference(CUSTOM_KEY, JSON.stringify(arr));
    window.dispatchEvent(new CustomEvent('prism-theme-catalog-change'));
  }
  function deleteCustomTheme(id) {
    savePreference(CUSTOM_KEY, JSON.stringify(readCustomArr().filter((t) => t.id !== id)));
    if (preferences.getItem(STORAGE_KEY) === id) applyTheme(DEFAULT_THEME);
    window.dispatchEvent(new CustomEvent('prism-theme-catalog-change'));
  }

  function getThemeId() {
    const id = preferences.getItem(STORAGE_KEY);
    return Object.hasOwn(getAllThemes(), id) ? id : DEFAULT_THEME;
  }

  function activeVars() {
    const t = getAllThemes()[getThemeId()];
    return (t || THEMES[DEFAULT_THEME]).vars;
  }
  function currentVars() {
    const vars = { ...(renderedVars || activeVars()) };
    const font = document.documentElement.style.getPropertyValue?.('--sans');
    if (font) vars['--sans'] = font;
    return vars;
  }

  function inheritParentTheme() {
    try {
      if (window.parent === window || !window.parent?.PrismTheme?.currentVars) return false;
      const vars = window.parent.PrismTheme.currentVars();
      for (const [key, value] of Object.entries(vars)) document.documentElement.style.setProperty(key, value);
      renderedVars = { ...vars };
      const parentRoot = window.parent.document?.documentElement;
      if (parentRoot?.getAttribute) for (const key of ['data-theme','data-theme-group','data-theme-mode','data-chat-surface']) {
        const value = parentRoot.getAttribute(key);
        if (value !== null) document.documentElement.setAttribute(key, value);
      }
      return true;
    } catch (_) { return false; /* Generated widgets have a separate origin. */ }
  }

  function broadcastVars(vars) {
    const frames = [...document.querySelectorAll('.widget-body iframe')];
    const appFrame = document.getElementById('app-frame');
    if (appFrame) frames.push(appFrame);
    frames.forEach(frame => { try { frame.contentWindow.postMessage({type: 'prism-theme', vars}, '*'); } catch (_) {} });
  }

  function varsToCSS(vars) {
    let s = ':root{';
    for (const k in vars) s += k + ':' + vars[k] + ';';
    return s + '}';
  }

  // Apply to the dashboard itself + broadcast to every widget iframe.
  function applyTheme(id, broadcast = true) {
    const all = getAllThemes();
    if (!Object.hasOwn(all, id)) id = DEFAULT_THEME;
    const vars = all[id].vars;
    renderedVars = { ...vars };
    const root = document.documentElement.style;
    for (const k in vars) root.setProperty(k, vars[k]);
    // data-theme drives the per-theme animated background (see style.css #bg-fx).
    document.documentElement.setAttribute('data-theme', id);
    document.documentElement.setAttribute('data-theme-group', all[id].group || 'custom');
    document.documentElement.setAttribute('data-theme-mode', all[id].mode || vars['color-scheme']);
    document.documentElement.setAttribute('data-chat-surface', GLASS_THEMES.has(id) ? 'glass' : 'solid');
    savePreference(STORAGE_KEY, id);
    if (broadcast) broadcastVars(currentVars());
    // Let the animated background (bgfx.js) react: enable/disable + recolour.
    window.dispatchEvent(new CustomEvent('prism-theme-change', { detail: { id, vars } }));
  }

  // Live preview a token map without persisting it (used by the theme studio).
  function previewVars(vars) {
    vars = completeVars(vars);
    renderedVars = { ...vars };
    const root = document.documentElement.style;
    for (const k in vars) root.setProperty(k, vars[k]);
    broadcastVars(currentVars());
    document.documentElement.setAttribute('data-theme', 'custom-preview');
    document.documentElement.setAttribute('data-theme-group', 'custom');
    document.documentElement.setAttribute('data-theme-mode', vars['color-scheme']);
    document.documentElement.setAttribute('data-chat-surface', 'solid');
    window.dispatchEvent(new CustomEvent('prism-theme-change', { detail: { id: 'custom-preview', vars, preview: true } }));
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
    return readablePalette(v);
  }

  // ─── UI preferences (scale / density / animated-bg intensity) ─────────────
  const SCALE_KEY = 'prism-ui-scale', DENSITY_KEY = 'prism-density', BGFX_KEY = 'prism-bgfx';
  function getPref(k, d) { return preferences.getItem(k) || d; }
  function applyUIScale(v) {
    v = v || getPref(SCALE_KEY, '100'); savePreference(SCALE_KEY, v);
    document.documentElement.style.setProperty('--ui-scale', String(Number(v) / 100));
  }
  function applyDensity(v) {
    v = v || getPref(DENSITY_KEY, 'comfortable'); savePreference(DENSITY_KEY, v);
    const c = document.documentElement.classList;
    c.remove('density-compact', 'density-spacious');
    if (v !== 'comfortable') c.add('density-' + v);
  }
  function applyBgfx(v) {
    v = (v == null ? getPref(BGFX_KEY, '100') : String(v)); savePreference(BGFX_KEY, v);
    document.documentElement.style.setProperty('--bgfx-opacity', String(Number(v) / 100));
    document.documentElement.setAttribute('data-bgfx-disabled', String(Number(v) === 0));
    window.dispatchEvent(new CustomEvent('prism-bgfx-change'));
  }
  // ─── Interface font (overrides --sans) ────────────────────────────────────
  const FONT_KEY = 'prism-ui-font';
  function applyFont(v) {
    v = v == null ? getPref(FONT_KEY, '') : v;
    savePreference(FONT_KEY, v);
    const r = document.documentElement.style;
    if (v) r.setProperty('--sans', v); else r.removeProperty('--sans');
    if (renderedVars) { if (v) renderedVars['--sans'] = v; else delete renderedVars['--sans']; }
  }

  // ─── Layout (chat panel side) ─────────────────────────────────────────────
  const CHATSIDE_KEY = 'prism-chat-side';
  function applyChatSide(v) {
    v = v || getPref(CHATSIDE_KEY, 'right');
    if (v !== 'left' && v !== 'right') v = 'right';
    savePreference(CHATSIDE_KEY, v);
    document.documentElement.setAttribute('data-chat-side', v);
  }
  function applyPrefs(broadcast = false) {
    applyingPreferences = true;
    try {
      const inherited = inheritParentTheme();
      if (!inherited) applyTheme(getThemeId(), broadcast);
      applyUIScale(); applyDensity(); applyBgfx(); applyChatSide(); applyFont();
      if (inherited) {
        inheritParentTheme();
        if (broadcast) broadcastVars(currentVars());
      }
    }
    finally { applyingPreferences = false; }
  }

  // Wrap an agent-authored widget document with the active theme tokens, the
  // shared widget stylesheet and the live-update bootstrap.
  function composeWidgetDoc(content, sessionID) {
    const head =
      '<style id="prism-theme-vars">' + varsToCSS(currentVars()) + 'html,body{background:var(--bg1);color:var(--text)}</style>' +
      '<link rel="stylesheet" href="/widget-base.css">' +
      '<script>window.PRISM_SESSION=' + JSON.stringify(sessionID || '') + ';<\/script>' +
      '<script src="/prism-widget.js"><\/script>' +
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
    const labels = { essential: 'Essentials', professional: 'Focus & work', studio: 'Color & atmosphere', playful: 'Geek & retro', custom: 'Your themes' };
    const groups = {};
    for (const id in all) {
      const group = all[id].custom ? 'custom' : all[id].group;
      if (!groups[group]) { const g = document.createElement('optgroup'); g.label = labels[group] || 'Themes'; sel.appendChild(g); groups[group] = g; }
      const o = document.createElement('option');
      o.value = id;
      o.textContent = all[id].label + (all[id].custom ? ' ✦' : '');
      groups[group].appendChild(o);
    }
    sel.value = getThemeId();
    sel.onchange = () => applyTheme(sel.value);
  }

  window.PrismTheme = {
    THEMES, getAllThemes, getThemeId, activeVars, currentVars, syncFromParent: inheritParentTheme,
    applyTheme, previewVars, composeWidgetDoc, populateSelect,
    saveCustomTheme, deleteCustomTheme, generatePalette,
    applyUIScale, applyDensity, applyBgfx, applyChatSide, applyFont, getPref,
    KEYS: { THEME: STORAGE_KEY, SCALE: SCALE_KEY, DENSITY: DENSITY_KEY, BGFX: BGFX_KEY, CHATSIDE: CHATSIDE_KEY, FONT: FONT_KEY },
  };

  // Server preferences also refresh pages already open in another tab.
  window.addEventListener('prism-preferences-changed', (e) => {
    const keys = e.detail?.keys || [];
    if (preferencesReady && e.detail?.remote && keys.some(key => [STORAGE_KEY, CUSTOM_KEY, SCALE_KEY, DENSITY_KEY, BGFX_KEY, FONT_KEY, CHATSIDE_KEY].includes(key))) {
      applyPrefs(true);
      const sel = document.getElementById('theme-select');
      if (sel) populateSelect(sel);
      if (keys.includes(CUSTOM_KEY)) window.dispatchEvent(new CustomEvent('prism-theme-catalog-change'));
    }
  });

  // Paint a complete palette immediately, then apply the saved account choices.
  applyPrefs();
  preferences.ready.then(() => {
    preferencesReady = true;
    applyPrefs(true);
    const sel = document.getElementById('theme-select');
    if (sel) populateSelect(sel);
    window.dispatchEvent(new CustomEvent('prism-theme-catalog-change'));
  });
})();
