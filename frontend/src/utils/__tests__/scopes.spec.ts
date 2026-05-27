import { describe, expect, it } from 'vitest'

import { canonicalizeScope, describeScope, normalizeScopeLocale } from '@/utils/scopes'

const fakeT = (locale: 'zh' | 'en') => (key: string): string => {
  // Minimal mirror of the locale dictionaries used by `oauthScopes.<scope>.{name,description}`.
  // Mirrors backend ScopeDisplay() exactly for the fields tested below.
  const en: Record<string, string> = {
    'oauthScopes.profile:read.name': 'Read your profile (username, avatar)',
    'oauthScopes.profile:read.description':
      'Read your username, display name, and avatar. Does not include email or balance.',
    'oauthScopes.images:create.name': 'Create and edit images',
    'oauthScopes.images:create.description':
      'Call /v1/images/* on your behalf to generate or edit images. Usage is billed to your account.',
    'oauthScopes.account:balance:read.name': 'Read account balance',
    'oauthScopes.account:balance:read.description':
      'Read your account balance and currency display settings.',
    'oauthScopes.offline_access.name': 'Offline access (issue a refresh token)',
    'oauthScopes.offline_access.description':
      'Keep your authorization active across sessions by issuing a refresh token. You can revoke at any time.'
  }
  const zh: Record<string, string> = {
    'oauthScopes.profile:read.name': '查看您的档案（用户名、头像）',
    'oauthScopes.profile:read.description':
      '允许应用读取你的用户名、显示名称和头像，不包括邮箱或余额。',
    'oauthScopes.images:create.name': '生成与编辑图片',
    'oauthScopes.images:create.description':
      '允许应用代表你调用 /v1/images/* 端点生成或编辑图片，将按用量计费。',
    'oauthScopes.account:balance:read.name': '查看账户余额',
    'oauthScopes.account:balance:read.description':
      '允许应用查看你的账户余额和币种显示设置。'
  }
  return (locale === 'zh' ? zh : en)[key] ?? key
}

describe('utils/scopes — normalizeScopeLocale', () => {
  it('treats any zh-prefixed locale as zh', () => {
    expect(normalizeScopeLocale('zh')).toBe('zh')
    expect(normalizeScopeLocale('zh-CN')).toBe('zh')
    expect(normalizeScopeLocale('ZH-TW')).toBe('zh')
  })

  it('falls back to en for non-zh locales', () => {
    expect(normalizeScopeLocale('en')).toBe('en')
    expect(normalizeScopeLocale('en-US')).toBe('en')
    expect(normalizeScopeLocale('fr')).toBe('en')
  })
})

describe('utils/scopes — canonicalizeScope', () => {
  it('returns canonical scopes unchanged', () => {
    expect(canonicalizeScope('profile:read')).toBe('profile:read')
    expect(canonicalizeScope('chat.completions:create')).toBe('chat.completions:create')
  })

  it('rewrites legacy aliases', () => {
    expect(canonicalizeScope('image_generation')).toBe('images:create')
    expect(canonicalizeScope('balance:read')).toBe('account:balance:read')
  })

  it('returns null for unknown scopes', () => {
    expect(canonicalizeScope('mystery:scope')).toBeNull()
  })

  it('trims surrounding whitespace', () => {
    expect(canonicalizeScope('  profile:read  ')).toBe('profile:read')
  })
})

describe('utils/scopes — describeScope', () => {
  it('renders English labels for canonical scopes', () => {
    const label = describeScope('profile:read', 'en', fakeT('en'))
    expect(label.name).toBe('Read your profile (username, avatar)')
    expect(label.description).toContain('Does not include email or balance')
  })

  it('renders Chinese labels for canonical scopes', () => {
    const label = describeScope('profile:read', 'zh-CN', fakeT('zh'))
    expect(label.name).toBe('查看您的档案（用户名、头像）')
  })

  it('rewrites legacy aliases when describing', () => {
    const label = describeScope('image_generation', 'en', fakeT('en'))
    expect(label.name).toBe('Create and edit images')
  })

  it('falls back to the raw identifier for unknown scopes', () => {
    const label = describeScope('future:unknown', 'en', fakeT('en'))
    expect(label.name).toBe('future:unknown')
    expect(label.description).toBe('future:unknown')
  })
})
