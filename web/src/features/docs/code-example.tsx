/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useEffect, useState } from 'react'
import type { ThemedToken } from 'shiki'

import { CodeBlockFrame } from '@/components/ai-elements/code-block'
import { CopyButton } from '@/components/copy-button'

const highlighter = import('shiki/core').then(
  async ({ createHighlighterCore }) => {
    const { createJavaScriptRegexEngine } =
      await import('shiki/engine/javascript')
    return createHighlighterCore({
      themes: [
        import('@shikijs/themes/github-light'),
        import('@shikijs/themes/github-dark'),
      ],
      langs: [
        import('@shikijs/langs/json'),
        import('@shikijs/langs/shellscript'),
      ],
      engine: createJavaScriptRegexEngine(),
    })
  }
)

export function CodeExample(props: { code: string; language: string }) {
  const [tokens, setTokens] = useState<ThemedToken[][] | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let active = true
    if (!['json', 'bash'].includes(props.language)) return
    highlighter
      .then((instance) => {
        const result = instance.codeToTokens(props.code, {
          lang: props.language === 'bash' ? 'shellscript' : 'json',
          themes: { light: 'github-light', dark: 'github-dark' },
        })
        if (active) setTokens(result.tokens)
      })
      .catch(() => {
        if (active) setFailed(true)
      })
    return () => {
      active = false
    }
  }, [props.code, props.language])
  return (
    <CodeBlockFrame
      title={props.language}
      showToolbar
      endActions={<CopyButton value={props.code} size='sm' />}
      className='bg-muted/30 my-5 min-w-0 overflow-hidden rounded-xl border'
      bodyClassName='p-0'
    >
      <pre className='overflow-x-auto p-4 font-mono text-xs leading-6 sm:text-sm [&_span]:text-[var(--shiki-light)] dark:[&_span]:text-[var(--shiki-dark)]'>
        <code>
          {tokens && !failed
            ? tokens.flatMap((line) => [
                ...line.map((token) => (
                  <span key={token.offset} style={token.htmlStyle}>
                    {token.content}
                  </span>
                )),
                '\n',
              ])
            : props.code}
        </code>
      </pre>
    </CodeBlockFrame>
  )
}
