// Existing widgets keep their browser data when moving to the content origin.
// Read only static keys present in that widget's source, never Prism's own
// preferences/history or a storage dump requested by widget code.
const reserved = /^(?:prism(?:[-_:]|$)|active-(?:workspace|session)$|agent-config$|chat-drawer-width$|history$|vox-supervision-session$)/i;
const migrationKey = (origin, key) => 'prism-widget-migrated:' + JSON.stringify([origin, key]);

function staticStorageKeys(source) {
  const keys = {local: new Set(), session: new Set()}, bindings = new Map();
  const literal = `(["'\x60])([^"'\x60\\\\\r\n]{1,200})\\1`;
  for (const match of source.matchAll(new RegExp('\\b(?:const|let|var)\\s+([A-Za-z_$][\\w$]*)\\s*=\\s*(["\'\x60])([^"\'\x60\\\\\r\n]{1,200})\\2', 'g'))) {
    if (!match[3].includes('${')) bindings.set(match[1], match[3]);
  }
  for (const [kind, name] of [['local', 'localStorage'], ['session', 'sessionStorage']]) {
    for (const match of source.matchAll(new RegExp('\\b' + name + '\\s*\\.\\s*(?:getItem|setItem|removeItem)\\s*\\(\\s*' + literal, 'g'))) {
      if (!match[2].includes('${')) keys[kind].add(match[2]);
    }
    for (const match of source.matchAll(new RegExp('\\b' + name + '\\s*\\.\\s*(?:getItem|setItem|removeItem)\\s*\\(\\s*([A-Za-z_$][\\w$]*)\\s*[,)]', 'g'))) {
      if (bindings.has(match[1])) keys[kind].add(bindings.get(match[1]));
    }
  }
  return keys;
}

export function legacyWidgetStorage(source, origin, stores = {local: localStorage, session: sessionStorage}) {
  const values = {local: Object.create(null), session: Object.create(null)};
  for (const [kind, keys] of Object.entries(staticStorageKeys(source))) {
    for (const key of keys) {
      if (reserved.test(key)) continue;
      try {
        if (stores[kind].getItem(migrationKey(origin, key)) === '1') continue;
        const value = stores[kind].getItem(key);
        if (value !== null) values[kind][key] = value;
      } catch (_) {}
    }
  }
  return values;
}

export function acknowledgeWidgetStorage(values, copied, origin, stores = {local: localStorage, session: sessionStorage}) {
  for (const kind of ['local', 'session']) {
    for (const key of Array.isArray(copied?.[kind]) ? copied[kind] : []) {
      if (typeof key !== 'string' || reserved.test(key) || !Object.hasOwn(values?.[kind] || {}, key)) continue;
      try { stores[kind].setItem(migrationKey(origin, key), '1'); } catch (_) {}
    }
  }
}
