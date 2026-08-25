import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PlatformIcon from '../PlatformIcon.vue'

describe('PlatformIcon', () => {
  it('uses the dedicated Grok asset', () => {
    const wrapper = mount(PlatformIcon, {
      props: { platform: 'grok', size: 'md' },
    })

    const image = wrapper.get('img')
    expect(image.attributes('src')).toBe('/assets/platform/grok.svg')
    expect(image.attributes('aria-hidden')).toBe('true')
  })

  it('uses the Gemini 2025 asset for Gemini models', () => {
    const wrapper = mount(PlatformIcon, {
      props: { platform: 'gemini', size: 'md' },
    })

    const icon = wrapper.get('img')
    expect(icon.attributes('src')).toBe('/assets/platform/gemini.svg')
    expect(icon.attributes('aria-hidden')).toBe('true')
    expect(icon.classes()).toContain('w-4')
    expect(icon.classes()).toContain('h-4')
  })
})
