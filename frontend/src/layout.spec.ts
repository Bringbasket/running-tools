import { describe, expect, it } from 'vitest'
// The test runner is Node-based; production code remains browser-only.
// @ts-expect-error Node types are intentionally not global in tsconfig.app.json.
import { readFileSync } from 'node:fs'

const globalStyles = readFileSync('src/styles.css', 'utf8')

const pageSources = import.meta.glob(
  ['../../modules/mail/frontend/pages/*.vue', '../../modules/tools/frontend/pages/*.vue'],
  { eager: true, import: 'default', query: '?raw' },
) as Record<string, string>

describe('layout contract', () => {
  it('keeps first-level pages on the shared platform width', () => {
    for (const [path, source] of Object.entries(pageSources)) {
      const root = source.match(/<section\s+class="page\s+([\w-]+)"/)
      expect(root, `${path} must use the shared .page container`).not.toBeNull()

      const pageClass = root![1]
      const localRule = new RegExp(`\\.${pageClass}\\s*\\{([^}]*)\\}`, 's').exec(source)?.[1] || ''
      expect(localRule, `${path} must not override platform page width`).not.toMatch(/(?:^|;)\s*(?:width|max-width|min-width|margin|margin-left|margin-right)\s*:/)
      expect(source, `${path} must not escape the shared horizontal boundary`).not.toMatch(/(?:margin-left|margin-right)\s*:\s*-\d|margin\s*:\s*-\d/)
    }
  })

  it('reserves scrollbar space and owns page width in the shell', () => {
    expect(globalStyles).toMatch(/html\s*\{[^}]*scrollbar-gutter:\s*stable/s)
    expect(globalStyles).toMatch(/\.page\s*\{[^}]*max-width:\s*var\(--page-content-max\)/s)
  })
})
