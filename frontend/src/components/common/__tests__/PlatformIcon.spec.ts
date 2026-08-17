import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PlatformIcon from '../PlatformIcon.vue'

describe('PlatformIcon', () => {
  it('uses the dedicated Grok asset', () => {
    const wrapper = mount(PlatformIcon, {
      props: { platform: 'grok', size: 'md' }
    })

    const image = wrapper.get('img')
    expect(image.attributes('src')).toBe('/assets/platform/grok.svg')
    expect(image.attributes('aria-hidden')).toBe('true')
  })
})
