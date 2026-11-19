// The file editor is optional during normal chat use. Keep its dependencies
// local, loading them once when a file is opened and retrying failed resources.
;(function () {
  const resources = new Map()
  let pending = null

  function resource(kind, path) {
    if (resources.has(path)) return resources.get(path)
    const element = document.createElement(kind === 'script' ? 'script' : 'link')
    if (kind === 'script') element.src = path
    else { element.rel = 'stylesheet'; element.href = path }
    const promise = new Promise((resolve, reject) => {
      element.onload = () => resolve()
      element.onerror = () => {
        resources.delete(path)
        element.remove()
        reject(new Error('Could not load the file editor'))
      }
    })
    resources.set(path, promise)
    document.head.appendChild(element)
    return promise
  }

  window.PrismCodeEditor = {
    load() {
      if (pending) return pending
      const scripts = resource('script', '/vendor/codemirror/codemirror.js').then(async () => {
        await Promise.all(['javascript', 'python', 'go', 'css', 'xml', 'shell', 'yaml', 'toml']
          .map(mode => resource('script', `/vendor/codemirror/mode/${mode}/${mode}.js`)))
        await Promise.all(['htmlmixed', 'markdown']
          .map(mode => resource('script', `/vendor/codemirror/mode/${mode}/${mode}.js`)))
        if (typeof window.CodeMirror !== 'function') throw new Error('Could not load the file editor')
        return window.CodeMirror
      })
      pending = Promise.all([
        resource('style', '/vendor/codemirror/codemirror.css'),
        resource('style', '/vendor/codemirror/theme/dracula.css'),
        scripts,
      ]).then(([, , editor]) => editor).catch(error => { pending = null; throw error })
      return pending
    },
  }
})()
