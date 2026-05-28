/**
 * Canonical OAuth v2 scope helpers.
 *
 * The display labels themselves live in the i18n locale files (single source
 * of truth on the frontend) under the `oauthScopes` namespace. Those locale
 * strings are kept byte-for-byte aligned with `ScopeDisplay` in
 * `backend/internal/service/oauth_scopes.go`. This module exposes:
 *
 *   - canonical-scope identification
 *   - legacy-alias rewriting (mirrors backend `legacyScopeAliases`)
 *   - `describeScope(scope, t?)` for synchronous label resolution
 *
 * Unknown scopes fall back to the raw scope identifier so a backend that
 * adds a scope before the frontend redeploys still renders something
 * meaningful instead of breaking.
 *
 * See OAUTH_V2_DESIGN.md §7.1, §7.2, §14.1.
 */

import type { ComposerTranslation } from 'vue-i18n'

import { i18n } from '@/i18n'
import type { OAuthScope } from '@/types'
import { OAUTH_SCOPES } from '@/types'

export interface ScopeLabel {
  /** Short label rendered as chip text. */
  name: string
  /** Longer description used as tooltip / secondary text. */
  description: string
}

type LocaleCode = 'zh' | 'en'

const CANONICAL_SCOPE_SET: ReadonlySet<string> = new Set<string>(OAUTH_SCOPES)

/**
 * Legacy v1 alias → canonical v2 scope. Mirrors backend
 * `legacyScopeAliases` in `oauth_scopes.go`. Used so old grant rows still
 * resolve to a canonical label even before the backend rewrites them.
 */
const LEGACY_SCOPE_ALIASES: Readonly<Record<string, OAuthScope>> = {
  image_generation: 'images:create',
  'balance:read': 'account:balance:read'
}

/**
 * `'zh-CN'` / `'zh-TW'` / `'zh'` → `'zh'`. Anything else → `'en'`. Mirrors
 * backend `normalizeDisplayLocale` in `oauth_scopes.go`.
 */
export function normalizeScopeLocale(locale: string): LocaleCode {
  const lower = locale.trim().toLowerCase()
  return lower.startsWith('zh') ? 'zh' : 'en'
}

/**
 * Resolve a possibly-legacy scope identifier to its canonical form.
 * Returns `null` for unknown scopes.
 */
export function canonicalizeScope(scope: string): OAuthScope | null {
  const trimmed = scope.trim()
  if (CANONICAL_SCOPE_SET.has(trimmed)) {
    return trimmed as OAuthScope
  }
  if (trimmed in LEGACY_SCOPE_ALIASES) {
    return LEGACY_SCOPE_ALIASES[trimmed]
  }
  return null
}

type Translator = ComposerTranslation | ((key: string, ...rest: unknown[]) => string)

function asString(value: unknown, fallback: string): string {
  return typeof value === 'string' && value.length > 0 ? value : fallback
}

/**
 * Resolve a scope to its `{ name, description }` label using the active i18n
 * locale. Falls back to the raw scope identifier (as both `name` and
 * `description`) for unknown scopes.
 *
 * Pass an explicit `t` from `useI18n()` when calling inside a Vue component
 * — that way the label updates reactively when the user switches locale.
 * The default uses `i18n.global.t`, which is fine for one-shot rendering
 * (and the Authorized Apps page reloads on locale change anyway).
 *
 * Locale is honoured implicitly via the `t` function (which is bound to the
 * active i18n locale). The previous `locale` parameter was load-bearing in
 * name only — `t` already routes to the right locale dictionary — so the
 * parameter has been removed to stop callers from passing a value that has
 * no effect.
 */
export function describeScope(
  scope: string,
  t: Translator = i18n.global.t
): ScopeLabel {
  const canonical = canonicalizeScope(scope)
  if (canonical) {
    const nameKey = `oauthScopes.${canonical}.name`
    const descKey = `oauthScopes.${canonical}.description`
    return {
      name: asString(t(nameKey), canonical),
      description: asString(t(descKey), canonical)
    }
  }
  return { name: scope, description: scope }
}

export const __testing = {
  CANONICAL_SCOPE_SET,
  LEGACY_SCOPE_ALIASES
}
