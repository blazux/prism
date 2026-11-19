# Browser dependencies

The main dashboard loads these pinned assets from the Prism server. No package
manager or build step is required to serve them.

| Library | Version | License | Source |
| --- | --- | --- | --- |
| marked | 9.1.6 | MIT | [npm tarball](https://registry.npmjs.org/marked/-/marked-9.1.6.tgz) |
| KaTeX | 0.16.47 | MIT | [npm tarball](https://registry.npmjs.org/katex/-/katex-0.16.47.tgz) |
| CodeMirror | 5.65.16 | MIT | [npm tarball](https://registry.npmjs.org/codemirror/-/codemirror-5.65.16.tgz) |

The files are unmodified upstream distributions. Each library directory retains
its upstream license. KaTeX includes every font referenced by its CSS. CodeMirror
includes its core, Dracula theme, and the ten modes loaded on demand by
`editor-loader.js` when a file is opened.

`ui-dependencies.json` records the exact registry metadata URL, tarball integrity,
upstream asset path, size, and SHA-256 for each copied file. Tarball SHA-512 values
were verified against the registry metadata before extraction. To refresh an
asset, select an explicit compatible version, verify its tarball integrity, copy
the corresponding distribution files and license, and update the manifest.

The existing `purify.min.js`, `xterm.js`, `xterm.css`, and `xterm-addon-fit.js`
remain separate dependencies; the manifest covers the libraries listed above.
