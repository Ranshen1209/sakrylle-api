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
    intervals: [
      {
        min_tokens: 0,
        max_tokens: 272000,
        input_price: 2.5e-6,
        output_price: 15e-6,
        cache_write_price: 3.125e-6,
        cache_read_price: 0.25e-6,
        per_request_price: null,
      },
      {
        min_tokens: 272000,
        max_tokens: null,
        input_price: 5e-6,
        output_price: 22.5e-6,
        cache_write_price: 6.25e-6,
        cache_read_price: 0.5e-6,
        per_request_price: null,
      },
    ],
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

function mountCard(longContext: boolean, timePricingMode: 'current' | 'peak' | 'off_peak' = 'current', cardModel = model) {
  return mount(PlazaModelCard, {
    props: {
      model: cardModel,
      showOriginal: false,
      longContext,
      timePricingMode,
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

  it('switches the card between current, peak, and off-peak prices', () => {
    const timeModel: PlazaModel = {
      ...model,
      name: 'deepseek-v4-flash',
      pricing: {
        ...model.pricing!,
        input_price: 1.5e-6,
        output_price: 4.5e-6,
        cache_read_price: 5e-8,
        time_resolution: {
          pricing_at: '2026-08-17T08:00:00+08:00',
          timezone: 'Asia/Shanghai',
          period_label: 'off_peak',
          multiplier: 0.5,
        },
        time_versions: [{
          effective_from: '2026-08-17T00:00:00+08:00',
          effective_until: null,
          timezone: 'Asia/Shanghai',
          default_multiplier: 0.5,
          input_price: 3e-6,
          output_price: 9e-6,
          cache_write_price: null,
          cache_read_price: 1e-7,
          image_input_price: null,
          image_output_price: null,
          windows: [{ label: 'peak', weekdays: 127, start_minute: 540, end_minute: 720, multiplier: 1 }],
        }],
      },
    }

    expect(mountCard(false, 'current', timeModel).text()).toContain('￥1.5')
    expect(mountCard(false, 'peak', timeModel).text()).toContain('￥3')
    expect(mountCard(false, 'peak', timeModel).text()).toContain('modelPlaza.table.timePricingPeakPreview')
    expect(mountCard(false, 'off_peak', timeModel).text()).toContain('￥1.5')
  })
})
