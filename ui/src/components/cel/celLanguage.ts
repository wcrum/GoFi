import type { Monaco } from '@grafana/ui'

/** Registers a basic CEL language (tokens, brackets, auto-closing) once per
 * Monaco instance. Completions are supplied per editor by CelField. */
export function registerCelLanguage(monaco: Monaco) {
  if (monaco.languages.getLanguages().some(language => language.id === 'cel')) return
  monaco.languages.register({ id: 'cel', aliases: ['CEL'] })
  monaco.languages.setMonarchTokensProvider('cel', {
    keywords: ['true', 'false', 'null', 'in'],
    variables: ['fields', 'meta', 'raw'],
    tokenizer: {
      root: [
        [/\/\/.*$/, 'comment'],
        [/[a-zA-Z_]\w*/, { cases: { '@keywords': 'keyword', '@variables': 'variable', '@default': 'identifier' } }],
        [/[rR]?"([^"\\]|\\.)*"/, 'string'],
        [/[rR]?'([^'\\]|\\.)*'/, 'string'],
        [/0x[0-9a-fA-F]+[uU]?/, 'number'],
        [/\d+(\.\d+)?([eE][-+]?\d+)?[uU]?/, 'number'],
        [/&&|\|\||[=!<>]=?|[-+*/%?:]/, 'operator'],
        [/[{}()[\]]/, '@brackets'],
      ],
    },
  })
  monaco.languages.setLanguageConfiguration('cel', {
    comments: { lineComment: '//' },
    brackets: [['(', ')'], ['[', ']'], ['{', '}']],
    autoClosingPairs: [{ open: '(', close: ')' }, { open: '[', close: ']' }, { open: '{', close: '}' }, { open: '"', close: '"' }, { open: "'", close: "'" }],
  })
}
