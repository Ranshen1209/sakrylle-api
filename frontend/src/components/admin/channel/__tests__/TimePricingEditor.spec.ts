import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import TimePricingEditor from '../TimePricingEditor.vue'

vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

async function addVersion(platform?: string) {
  const wrapper = mount(TimePricingEditor, {
    props: { versions: [], platform },
    global: { stubs: { Icon: true } },
  })
  await wrapper.get('[data-testid="add-time-version"]').trigger('click')
  return wrapper.emitted('update')?.[0]?.[0]
}

describe('TimePricingEditor defaults', () => {
  it('uses the official DeepSeek weekday windows', async () => {
    const versions = await addVersion(' DeepSeek ')

    expect(versions).toEqual([expect.objectContaining({
      timezone: 'Asia/Shanghai',
      default_multiplier: 0.5,
      windows: [
        expect.objectContaining({ weekdays: 31, start_minute: 540, end_minute: 720, multiplier: 1 }),
        expect.objectContaining({ weekdays: 31, start_minute: 840, end_minute: 1080, multiplier: 1 }),
      ],
    })])
  })

  it('keeps all-week defaults for other platforms', async () => {
    const versions = await addVersion('openai')

    expect(versions?.[0].windows.map(window => window.weekdays)).toEqual([127, 127])
  })
})
