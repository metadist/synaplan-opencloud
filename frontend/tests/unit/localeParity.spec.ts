import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const src = join(import.meta.dirname, '../../src')
const catalog = JSON.parse(
  readFileSync(join(import.meta.dirname, '../../l10n/translations.json'), 'utf8')
) as Record<string, Record<string, string | string[]>>

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    return statSync(path).isDirectory()
      ? files(path)
      : path.endsWith('.vue') || path.endsWith('.ts')
        ? [path]
        : []
  })
}

const literal = `(?:'((?:\\\\'|[^'])*)'|"((?:\\\\"|[^"])*)")`
const unescape = (raw: string) => raw.replace(/\\'/g, "'").replace(/\\"/g, '"')

/** Every `$gettext('…')` and `$gettext('…', {…})` source string. */
function sourceStrings(): string[] {
  const pattern = new RegExp(`\\$gettext\\(\\s*${literal}\\s*[,)]`, 'gs')
  const found = new Set<string>()
  for (const path of files(src)) {
    const text = readFileSync(path, 'utf8')
    for (const match of text.matchAll(pattern)) {
      found.add(unescape(match[1] ?? match[2]))
    }
  }
  return [...found]
}

/** Every `$ngettext('one', 'many', …)` singular, which is the catalog key. */
function pluralStrings(): string[] {
  const pattern = new RegExp(`\\$ngettext\\(\\s*${literal}\\s*,\\s*${literal}\\s*,`, 'gs')
  const found = new Set<string>()
  for (const path of files(src)) {
    const text = readFileSync(path, 'utf8')
    for (const match of text.matchAll(pattern)) {
      found.add(unescape(match[1] ?? match[2]))
    }
  }
  return [...found]
}

describe('locale catalog', () => {
  const locales = ['de', 'es', 'fr', 'tr'] as const

  it('translates every source string in de, es, fr and tr', () => {
    const missing: string[] = []
    for (const source of sourceStrings()) {
      for (const locale of locales) {
        if (!catalog[locale]?.[source]) missing.push(`${locale}: ${source}`)
      }
    }
    expect(missing).toEqual([])
  })

  it('translates every plural string with both forms in de, es, fr and tr', () => {
    const missing: string[] = []
    for (const source of pluralStrings()) {
      for (const locale of locales) {
        const entry = catalog[locale]?.[source] as unknown
        if (!Array.isArray(entry) || entry.length !== 2 || entry.some((form) => !form)) {
          missing.push(`${locale}: ${source}`)
        }
      }
    }
    expect(missing).toEqual([])
  })

  it('uses the same keys in every locale', () => {
    const keys = locales.map((locale) =>
      Object.keys(catalog[locale] ?? {})
        .sort()
        .join('\n')
    )
    expect(new Set(keys).size).toBe(1)
  })
})
