// Chat display only. The original message stays intact for storage and tools.
;(function () {
  const groups = [
    ['🙂', ':)', ':-)', ':slightly_smiling_face:'],
    ['😃', ':D', ':-D', ':d', ':-d', ':smiley:'], ['😀', ':grinning:'],
    ['😄', ':smile:', ':sourire:', '(smile)'], ['😊', ':blush:'],
    ['😉', ';)', ';-)', ':wink:', ':clin_oeil:'],
    ['😂', ":')", ':joy:', ':rire:'], ['🤣', ':rofl:'],
    ['😆', 'XD', 'xD', ':laughing:', ':satisfied:', '(laugh)'],
    ['😅', ':sweat_smile:'], ['🙃', ':upside_down_face:'],
    ['😛', ':P', ':-P', ':p', ':-p', ':stuck_out_tongue:', ':tongue:'],
    ['😜', ';P', ';-P', ';p', ';-p', ':stuck_out_tongue_winking_eye:'],
    ['🙁', ':(', ':-(', ':slightly_frowning_face:', ':triste:'],
    ['😢', ":'(", ':cry:', ':pleure:'], ['😭', ':sob:'],
    ['😕', ':/', ':-/', ':S', ':-S', ':s', ':-s', ':confused:'],
    ['😐', ':|', ':-|', ':neutral_face:'], ['😑', ':expressionless:'],
    ['😮', ':O', ':-O', ':o', ':-o', ':open_mouth:', ':surpris:'],
    ['😲', ':astonished:'], ['😱', ':scream:'], ['😨', ':fearful:'],
    ['😳', ':flushed:'], ['😠', '>:(', ':angry:', ':colere:'], ['😡', ':rage:'],
    ['🤔', ':thinking:', ':thinking_face:', ':reflechir:'],
    ['🙄', ':rolling_eyes:'], ['😏', ':smirk:'], ['😬', ':grimacing:'],
    ['😎', '8-)', 'B-)', ':sunglasses:', ':cool:'], ['🤓', ':nerd_face:'],
    ['😍', ':heart_eyes:'], ['😘', ':*', ':-*', ':kissing_heart:', ':bisou:', '(kiss)'],
    ['😇', 'O:)', 'O:-)', ':innocent:'], ['😌', ':relieved:'],
    ['😴', ':sleeping:'], ['🥱', ':yawning_face:'], ['🤗', ':hugging:', ':hug:'],
    ['🥳', ':partying_face:'], ['🤯', ':exploding_head:'], ['🫠', ':melting_face:'],
    ['🤦', ':facepalm:'], ['🤷', ':shrug:'], ['🫡', ':saluting_face:', ':salute:'],
    ['👍', ':thumbsup:', ':thumbs_up:', ':+1:', ':pouce:', ':oui:'],
    ['👎', ':thumbsdown:', ':thumbs_down:', ':-1:', ':non:'],
    ['👏', ':clap:', ':applaudir:', ':bravo:', '(clap)'],
    ['👋', ':wave:', ':bonjour:', '(wave)'], ['🙏', ':pray:', ':merci:'],
    ['🙌', ':raised_hands:'], ['👌', ':ok_hand:'], ['✌️', ':v:', ':victory:'],
    ['🤝', ':handshake:'], ['💪', ':muscle:', ':flexed_biceps:'],
    ['🤞', ':fingers_crossed:'], ['👊', ':punch:'],
    ['❤️', '<3', ':heart:', ':red_heart:', ':coeur:', '(heart)'],
    ['💔', '</3', ':broken_heart:'], ['💙', ':blue_heart:'],
    ['💚', ':green_heart:'], ['💛', ':yellow_heart:'], ['💜', ':purple_heart:'],
    ['🔥', ':fire:', ':feu:'], ['✨', ':sparkles:'],
    ['🎉', ':tada:', ':party_popper:', ':fete:'], ['💯', ':100:'],
    ['✅', ':white_check_mark:', ':check:', ':valide:'], ['❌', ':x:'],
    ['⚠️', ':warning:', ':attention:'], ['👀', ':eyes:'],
    ['🚀', ':rocket:'], ['💡', ':bulb:', ':idee:'],
    ['☕', ':coffee:', ':cafe:'], ['🍺', ':beer:'], ['🏆', ':trophy:'],
  ]
  const faces = Object.fromEntries(groups.flatMap(([emoji, ...names]) => names.map(name => [name, emoji])))
  const starts = Object.create(null)
  for (const alias of Object.keys(faces)) (starts[alias[0]] ||= []).push(alias)
  for (const aliases of Object.values(starts)) aliases.sort((a, b) => b.length - a.length)
  const before = char => !char || /[\s([{'"«“]/u.test(char)
  const after = char => !char || /[\s.,!?;\])}'"»”]/u.test(char)
  function boundaryBefore(source, index) {
    while (index > 0 && /[*_~]/.test(source[index - 1])) index--
    return before(source[index - 1])
  }
  function boundaryAfter(source, index) {
    while (index < source.length && /[*_~]/.test(source[index])) index++
    return after(source[index])
  }

  function balancedEnd(text, start, open, close) {
    let depth = 0
    for (let i = start; i < text.length; i++) {
      if (text[i] === '\\') { i++; continue }
      if (text[i] === open) depth++
      if (text[i] === close && --depth === 0) return i + 1
    }
    return -1
  }

  function text(source) {
    source = String(source ?? '')
    let result = '', i = 0
    while (i < source.length) {
      let end = -1
      const tail = source.slice(i)
      // Keep fenced/indented code intact, including an unfinished streaming fence.
      if (i === 0 || source[i - 1] === '\n') {
        const fence = /^ {0,3}(`{3,}|~{3,})[^\n]*(?:\n|$)/.exec(tail)
        if (fence) {
          const close = new RegExp('^ {0,3}' + fence[1][0] + '{' + fence[1].length + ',}[ \\t]*(?:\\n|$)', 'gm')
          close.lastIndex = i + fence[0].length
          const match = close.exec(source)
          end = match ? close.lastIndex : source.length
        } else if (/^(?: {4}|\t)/.test(tail)) {
          const newline = source.indexOf('\n', i)
          end = newline < 0 ? source.length : newline + 1
        }
      }
      // Inline code and formulas are literal, even while their closing marker
      // has not arrived yet. Backslash escapes remain literal too.
      if (end < 0 && source[i] === '\\') end = Math.min(i + 2, source.length)
      if (end < 0 && (source[i] === '`' || source[i] === '$')) {
        const marker = (source[i] === '`' ? /^`+/ : /^\$+/).exec(tail)[0]
        let found = source.indexOf(marker, i + marker.length)
        while (found >= 0 && (source[found - 1] === marker[0] || source[found + marker.length] === marker[0])) found = source.indexOf(marker, found + marker.length)
        end = found < 0 ? (source[i] === '`' || marker.length > 2 ? source.length : -1) : found + marker.length
      }
      // Skip complete Markdown links/images, including labels and references.
      if (end < 0 && (source[i] === '[' || source.startsWith('![', i))) {
        const start = source[i] === '[' ? i : i + 1
        const label = balancedEnd(source, start, '[', ']')
        end = label < 0 ? source.length : label
        if (label > 0 && (source[label] === '(' || source[label] === '[')) {
          const closing = balancedEnd(source, label, source[label], source[label] === '(' ? ')' : ']')
          end = closing < 0 ? source.length : closing
        }
      }
      // Raw HTML stays literal to this helper. Resume after its paragraph;
      // no parsing or rewriting of tags, attributes or embedded text is needed.
      if (end < 0 && /^<(?:[A-Za-z][\w:-]*\b|\/[A-Za-z]|!--|\?)/.test(tail)) {
        const paragraph = /\n[ \t]*\n/g
        paragraph.lastIndex = i
        const match = paragraph.exec(source)
        end = match ? match.index : source.length
      }
      // URLs, email addresses and Markdown reference destinations are never
      // changed. Boundaries below also exclude emoticons inside paths/words.
      if (end < 0 && before(source[i - 1]) && !/\s/.test(source[i])) {
        const token = /^[^\s]+/.exec(tail)?.[0] || ''
        if (token.includes('://') || token.includes('@') || /^www\./i.test(token)) end = i + token.length
      }
      if (end >= 0) { result += source.slice(i, end); i = end; continue }
      const alias = boundaryBefore(source, i) && starts[source[i]]?.find(value => source.startsWith(value, i) && boundaryAfter(source, i + value.length))
      if (alias) { result += faces[alias]; i += alias.length }
      else result += source[i++]
    }
    return result
  }
  window.PrismSmileys = {text, shortcuts: Object.freeze(faces)}
})()
