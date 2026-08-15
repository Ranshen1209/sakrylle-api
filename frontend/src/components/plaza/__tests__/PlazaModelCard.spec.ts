import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PlazaModelCard from '../PlazaModelCard.vue'
import type { PlazaModel } from '@/utils/modelPlaza'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string>) =>
        key === 'plaza.longContextBadge'
          ? `Long context > ${params?.threshold}`
          : key,
    }),
  }
})

const model: PlazaModel = {
  id: 'openai::gpt-5.4::14',
  name: 'gpt-5.4',
  platform: 'openai',
  channels: ['GPT'],
  pricing: {
    billing_mode: 'token',
    input_price: 2.5e-6,
    output_price: 15e-6,
    cache_write_price: 3.125e-6,
    cache_read_price: 0.25e-6,
    image_input_price: null,
    image_output_price: null,
    image_input_ratio: null,
    per_request_price: null,
    intervals: [],
  },
  group: {
    id: 14,
    name: 'GPT-Pro',
    platform: 'openai',
    subscriptionType: 'standard',
    isExclusive: false,
    defaultRate: 0.5,
    userRate: null,
    effectiveRate: 0.5,
  },
}

function mountCard(longContext: boolean) {
  return mount(PlazaModelCard, {
    props: {
      model,
      showOriginal: false,
      longContext,
      longContextPricing: {
        input_price: 2.5e-6,
        output_price: 15e-6,
        cache_write_price: 3.125e-6,
        cache_read_price: 0.25e-6,
        long_context_threshold: 272000,
        long_context_input_multiplier: 2,
        long_context_output_multiplier: 1.5,
      },
    },
    global: {
      stubs: {
        Icon: true,
        PlatformIcon: true,
        GroupBadge: true,
      },
    },
  })
}

describe('PlazaModelCard long-context pricing', () => {
  it('applies 2x input/cache and 1.5x output above 272K', () => {
    const standard = mountCard(false).text()
    expect(standard).toContain('￥1.25')
    expect(standard).toContain('￥7.5')
    expect(standard).toContain('￥0.125')
    expect(standard).toContain('￥1.5625')

    const longContext = mountCard(true).text()
    expect(longContext).toContain('￥2.5')
    expect(longContext).toContain('￥11.25')
    expect(longContext).toContain('￥0.25')
    expect(longContext).toContain('￥3.125')
    expect(longContext).toContain('Long context > 272K')
  })
})
