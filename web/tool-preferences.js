// Browser-local opt-outs. A former per-action opt-out keeps its combined tool off.
window.PrismToolPreferences = (() => {
  const key = 'agent-config'
  const combined = {
    add_widget: 'widget', remove_widget: 'widget',
    apt_install: 'install_packages', pip_install: 'install_packages',
    cron_list: 'cron', cron_add: 'cron', cron_remove: 'cron',
    rag_list: 'rag_manage',
  }
  function read() {
    let config
    try { config = JSON.parse(localStorage.getItem(key) || '{}') } catch { config = {} }
    if (!config || typeof config !== 'object' || Array.isArray(config)) config = {}
    const disabled = Array.isArray(config.disabled) ? config.disabled : []
    const notices = new Set(Array.isArray(config.combinedToolNotices) ? config.combinedToolNotices : [])
    let migrated = false
    config.disabled = [...new Set(disabled.filter(name => typeof name === 'string').map(name => {
      if (!Object.hasOwn(combined, name)) return name
      migrated = true
      notices.add(combined[name])
      return combined[name]
    }))]
    if (migrated) {
      config.combinedToolNotices = [...notices]
      write(config)
    }
    return config
  }
  function write(config) { localStorage.setItem(key, JSON.stringify(config)) }
  function set(name, enabled) {
    const config = read(), disabled = new Set(config.disabled)
    enabled ? disabled.delete(name) : disabled.add(name)
    config.disabled = [...disabled]
    config.combinedToolNotices = (config.combinedToolNotices || []).filter(item => item !== name)
    write(config)
  }
  function dismiss() { const config = read(); delete config.combinedToolNotices; write(config) }
  return { read, set, dismiss }
})()
