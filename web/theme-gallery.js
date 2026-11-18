// Theme choices are ordinary radio inputs: the same selection is available by
// mouse, touch and keyboard. Previews use their own variables, not the active UI.
window.PrismThemeGallery = (() => {
  const GROUPS = [
    ['essential', 'Essentials', 'Everyday dark and light palettes.'],
    ['professional', 'Focus & work', 'Quiet surfaces and restrained accents.'],
    ['studio', 'Color & atmosphere', 'Warm tones, soft color and deep night palettes.'],
    ['playful', 'Geek & retro', 'Terminal green, neon and arcade color.'],
    ['custom', 'Your themes', 'Palettes saved in this browser.'],
  ]
  let instance = 0
  const preview = `<div class="theme-preview" aria-hidden="true">
    <div class="theme-preview-top"><i class="theme-preview-logo"></i><b>PRISM</b><span></span><i></i><i></i><i></i></div>
    <div class="theme-preview-body"><div class="theme-preview-rail"><i></i><i></i><i></i><i></i><i></i></div>
      <div class="theme-preview-work"><div class="theme-preview-heading"><b>My workspace</b><i></i></div>
        <div class="theme-preview-metrics"><div><span>Overview</span><b>24<span>+12%</span></b><svg viewBox="0 0 120 28" fill="none"><path d="M0 24 15 18 28 21 44 9 60 14 76 5 92 9 108 2 120 5"/></svg></div><div class="theme-preview-notes"><span>Today</span><i></i><i></i><i></i></div></div>
        <div class="theme-preview-activity"><span></span><i></i><i></i><i></i><b></b></div>
      </div><div class="theme-preview-chat"><b>Assistant</b><div class="theme-preview-question"></div><div class="theme-preview-answer"><i></i><i></i><i></i></div><div class="theme-preview-reply"><span></span><i></i></div></div>
    </div></div>`

  function mount(host, {select} = {}) {
    const T = window.PrismTheme, number = ++instance
    let filter = 'all', previewing = false
    host.className = 'theme-gallery'
    const toolbar = document.createElement('div'); toolbar.className = 'theme-gallery-toolbar'
    const filters = document.createElement('div'); filters.className = 'theme-gallery-filters'; filters.setAttribute('aria-label', 'Theme collections')
    const active = document.createElement('div'); active.className = 'theme-gallery-active'; active.setAttribute('role', 'status')
    toolbar.append(filters, active)
    const choices = document.createElement('div'); choices.setAttribute('role', 'radiogroup'); choices.setAttribute('aria-label', 'Interface theme')
    host.append(toolbar, choices)

    function sync() {
      const id = T.getThemeId(), all = T.getAllThemes()
      host.querySelectorAll('input[data-theme-id]').forEach(input => { input.checked = input.dataset.themeId === id })
      active.textContent = previewing ? 'Preview · unsaved palette' : 'Active · ' + (all[id]?.label || id)
      if (select) select.value = id
    }

    function render() {
      const all = T.getAllThemes(), entries = Object.entries(all)
      filters.replaceChildren()
      const available = GROUPS.filter(([group]) => entries.some(([, t]) => (t.custom ? 'custom' : t.group || 'essential') === group))
      if (filter !== 'all' && !available.some(([group]) => group === filter)) filter = 'all'
      for (const [key, label] of [['all', 'All themes'], ...available]) {
        const button = document.createElement('button'); button.type = 'button'; button.textContent = label
        button.setAttribute('aria-pressed', String(filter === key))
        button.dataset.collection = key
        button.onclick = () => { filter = key; render(); [...filters.children].find(b => b.dataset.collection === key)?.focus() }
        filters.append(button)
      }
      choices.replaceChildren()
      for (const [group, label, description] of GROUPS) {
        if (filter !== 'all' && filter !== group) continue
        const themes = entries.filter(([, t]) => (t.custom ? 'custom' : t.group || 'essential') === group)
        if (!themes.length) continue
        const section = document.createElement('section'); section.className = 'theme-collection'
        const heading = document.createElement('div'); heading.className = 'theme-collection-heading'
        const title = document.createElement('h3'); title.textContent = label
        const subtitle = document.createElement('p'); subtitle.textContent = description
        heading.append(title, subtitle); section.append(heading)
        const grid = document.createElement('div'); grid.className = 'theme-grid'; section.append(grid)
        for (const [id, theme] of themes) {
          const card = document.createElement('label'); card.className = 'theme-card'; card.dataset.theme = id; card.dataset.group = group
          const input = document.createElement('input'); input.type = 'radio'; input.name = 'prism-theme-choice-' + number; input.value = id; input.dataset.themeId = id
          input.setAttribute('aria-label', theme.label)
          const descriptionID = 'theme-description-' + number + '-' + grid.children.length + '-' + group
          input.setAttribute('aria-describedby', descriptionID)
          input.onchange = () => { if (input.checked) T.applyTheme(id) }
          const miniature = document.createElement('div'); miniature.innerHTML = preview
          for (const key of ['bg', 'bg1', 'bg2', 'bg3', 'bg4', 'border', 'border2', 'text', 'text2', 'text3', 'accent', 'accent-dim', 'green']) {
            const value = theme.vars['--' + key]
            if (value && CSS.supports('color', value)) miniature.style.setProperty('--preview-' + key, value)
          }
          if (theme.vars['--sans']) miniature.style.setProperty('--preview-font', theme.vars['--sans'])
          const radius = parseFloat(theme.vars['--radius'])
          if (Number.isFinite(radius)) miniature.style.setProperty('--preview-radius', Math.max(0, Math.min(6, radius / 2)) + 'px')
          miniature.className = 'theme-miniature'
          const meta = document.createElement('div'); meta.className = 'theme-card-meta'
          const name = document.createElement('strong'); name.textContent = theme.label
          const mode = document.createElement('span'); mode.className = 'theme-mode'; mode.textContent = theme.mode === 'light' ? 'Light' : 'Dark'
          const check = document.createElement('span'); check.className = 'theme-selected'; check.setAttribute('aria-hidden', 'true'); check.textContent = '✓'
          meta.append(name, mode, check)
          const detail = document.createElement('p'); detail.className = 'theme-card-description'; detail.id = descriptionID; detail.textContent = theme.description || (theme.custom ? 'Your saved palette.' : theme.label + ' palette.')
          card.append(input, miniature, meta, detail); grid.append(card)
        }
        choices.append(section)
      }
      sync()
    }

    const changes = new AbortController()
    function changed(event) {
      if (!host.isConnected) { changes.abort(); return }
      previewing = event.detail?.preview === true
      sync()
    }
    window.addEventListener('prism-theme-change', changed, {signal: changes.signal})
    window.addEventListener('prism-theme-catalog-change', () => {
      if (!host.isConnected) { changes.abort(); return }
      render()
    }, {signal: changes.signal})
    window.addEventListener('storage', e => {
      if (!host.isConnected) { changes.abort(); return }
      if (e.key === 'prism-custom-themes') render()
      else if (e.key === 'prism-theme') sync()
    }, {signal: changes.signal})
    render()
    return {refresh: render, sync, destroy: () => changes.abort()}
  }
  return {mount}
})()
