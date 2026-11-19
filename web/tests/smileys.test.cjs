const {test} = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs'), path = require('node:path'), vm = require('node:vm')
const root = path.resolve(__dirname, '..')
function fixture() {
  const context = {window: {}}
  vm.createContext(context)
  vm.runInContext(fs.readFileSync(path.join(root, 'smileys.js'), 'utf8'), context)
  return context.window.PrismSmileys
}

test('classic faces, named English/French shortcuts and gestures render naturally', () => {
  const {text} = fixture()
  assert.equal(text('Salut :) :D ;) :( :P :o :/ <3'), 'Salut 🙂 😃 😉 🙁 😛 😮 😕 ❤️')
  assert.equal(text('Ça marche :thumbsup: :+1: :clap: :heart: :thinking: :joy:'), 'Ça marche 👍 👍 👏 ❤️ 🤔 😂')
  assert.equal(text(':merci: :bravo: :coeur: :rire: (smile) (wave) XD'), '🙏 👏 ❤️ 😂 😄 👋 😆')
  assert.equal(text('«:)» (:smile:) :D! :-).'), '«🙂» (😄) 😃! 🙂.')
  assert.equal(text('Prix : $5 :smile:'), 'Prix : $5 😄')
  assert.equal(text('**:smile:** *:D* ~~:heart:~~'), '**😄** *😃* ~~❤️~~')
})

test('code, formulas, links and raw HTML remain exact', () => {
  const {text} = fixture()
  for (const literal of [
    '`:) :smile:`', '``a ` :smile:``',
    '```js\nconst x = ":) :thumbsup:";\n```',
    '~~~~text\n:) :heart:\n~~~~', '```text\n:) unfinished',
    '    print(":smile:")', '\t:)',
    '$x :D <3$', '$$x :thumbsup:$$',
    '[docs :smile:](https://example.com/ok/:D)',
    '![image :)](/file?name=:smile:)', '[docs :heart:][reference]',
    '[:smile:]', '[":)", ":D"]',
    '<span title=":smile:">:)</span>', '<!-- :D -->',
    'https://example.com/:) ftp://host/:smile: www.example.com/:D',
    'user@example.com https://user:smile:@example.com',
    '/workspace/:smile:/file C:\\data\\:D C:/dev/:smile:',
    '12:30 name:smile:other :unknown: :(invalid) :Dword x_:smile:_x',
    '\\:) \\:smile: \\<3',
    // Single-letter variables in parentheses are not legacy emoji aliases.
    'f(x) + (y) + (n)',
  ]) assert.equal(text(literal), literal, literal)
  assert.equal(text('`:)` puis :smile:'), '`:)` puis 😄')
  assert.equal(text('```text\n:D\n```\n:)'), '```text\n:D\n```\n🙂')
  assert.equal(text('<span>:)</span>\n\n:thumbsup:'), '<span>:)</span>\n\n👍')
})

test('replacement is repeatable, preserves the source and has bounded token matching', () => {
  const {text, shortcuts} = fixture()
  const source = 'Prêt :smile: 👍 :)'
  const rendered = text(source)
  assert.equal(source, 'Prêt :smile: 👍 :)')
  assert.equal(text(rendered), rendered)
  assert.equal(shortcuts[':thumbsup:'], '👍')
  assert.ok(Object.isFrozen(shortcuts))
  const technical = 'https://example.com/a/:D ' + '[a'.repeat(5000) + ' :smile:'
  assert.equal(text(technical), technical)
})
