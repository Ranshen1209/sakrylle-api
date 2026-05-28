import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import AuthorizedAppsView from '@/views/user/AuthorizedAppsView.vue'
import type { AuthorizedAppGrant } from '@/types'

const {
  listAuthorizedAppsMock,
  revokeAuthorizedAppMock,
  revokeAuthorizedAppsForClientMock,
  showSuccessMock,
  showErrorMock
} = vi.hoisted(() => ({
  listAuthorizedAppsMock: vi.fn(),
  revokeAuthorizedAppMock: vi.fn(),
  revokeAuthorizedAppsForClientMock: vi.fn(),
  showSuccessMock: vi.fn(),
  showErrorMock: vi.fn()
}))

vi.mock('@/api/oauthGrants', () => ({
  default: {
    listAuthorizedApps: listAuthorizedAppsMock,
    revokeAuthorizedApp: revokeAuthorizedAppMock,
    revokeAuthorizedAppsForClient: revokeAuthorizedAppsForClientMock
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: showSuccessMock,
    showError: showErrorMock
  })
}))

vi.mock('@/utils/format', () => ({
  formatRelativeWithDateTime: (input: unknown) => (input ? `formatted(${String(input)})` : '')
}))

vi.mock('@/utils/scopes', () => ({
  // describeScope no longer takes a `locale` arg — `t` is bound to the active
  // i18n locale, so we just return English labels here (the mocked vue-i18n
  // useI18n() below pins locale to 'en').
  describeScope: (scope: string) => {
    const map: Record<string, { name: string; description: string }> = {
      'profile:read': { name: 'Read profile', description: 'Read profile description' },
      'images:create': { name: 'Create images', description: 'Call image API' },
      image_generation: { name: 'Create images', description: 'Call image API' }
    }
    return map[scope] ?? { name: scope, description: scope }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (params && Object.keys(params).length) {
          const expanded = Object.entries(params)
            .map(([k, v]) => `${k}=${String(v)}`)
            .join(',')
          return `${key}[${expanded}]`
        }
        return key
      },
      locale: { value: 'en' }
    })
  }
})

const grantOne: AuthorizedAppGrant = {
  grant_id: 'grant-1',
  client_id: 'sakrylle-cli',
  client_name: 'Sakrylle CLI',
  client_disabled: false,
  app_type: 'cli',
  icon_url: null,
  device_id: null,
  device_name: 'Ariel MBP',
  group_id: 7,
  group_name: 'Codex',
  scopes: ['profile:read', 'images:create'],
  first_authorized_at: '2026-05-27T12:00:00Z',
  last_used_at: '2026-05-27T12:30:00Z',
  last_used_ip: '203.0.113.1',
  active_access_token_count: 1,
  active_refresh_token_count: 1,
  status: 'active'
}

const grantTwo: AuthorizedAppGrant = {
  ...grantOne,
  grant_id: 'grant-2',
  device_name: 'Ariel iPhone',
  last_used_at: null
}

const disabledGrant: AuthorizedAppGrant = {
  ...grantOne,
  grant_id: 'grant-3',
  client_id: 'old-app',
  client_name: 'Decommissioned App',
  client_disabled: true,
  device_name: 'Server room'
}

const stubs = {
  AppLayout: { template: '<div><slot /></div>' },
  TablePageLayout: {
    template:
      '<div><slot name="actions" /><slot name="filters" /><slot name="table" /></div>'
  },
  DataTable: {
    props: ['data', 'columns', 'loading'],
    template: `
      <div data-testid="data-table">
        <div v-if="loading" data-testid="loading">loading</div>
        <div v-else-if="!data || data.length === 0" data-testid="empty"><slot name="empty" /></div>
        <div v-else>
          <div v-for="row in data" :key="row.grant_id" :data-testid="'row-' + row.grant_id">
            <slot name="cell-app" :row="row" />
            <slot name="cell-device" :row="row" />
            <slot name="cell-group" :row="row" />
            <slot name="cell-scopes" :row="row" />
            <slot name="cell-first_authorized_at" :row="row" />
            <slot name="cell-last_used_at" :row="row" />
            <slot name="cell-tokens" :row="row" />
            <slot name="cell-actions" :row="row" />
          </div>
        </div>
      </div>
    `
  },
  ConfirmDialog: {
    props: ['show', 'title', 'message'],
    emits: ['confirm', 'cancel'],
    template: `
      <div v-if="show" data-testid="confirm-dialog">
        <div data-testid="confirm-title">{{ title }}</div>
        <div data-testid="confirm-message">{{ message }}</div>
        <button data-testid="confirm-ok" @click="$emit('confirm')">ok</button>
        <button data-testid="confirm-cancel" @click="$emit('cancel')">cancel</button>
      </div>
    `
  },
  EmptyState: {
    props: ['title', 'description'],
    template: '<div data-testid="empty-state">{{ title }}</div>'
  },
  Icon: { template: '<span />' }
}

describe('AuthorizedAppsView (v2 grant rows)', () => {
  beforeEach(() => {
    listAuthorizedAppsMock.mockReset()
    revokeAuthorizedAppMock.mockReset()
    revokeAuthorizedAppsForClientMock.mockReset()
    showSuccessMock.mockReset()
    showErrorMock.mockReset()
  })

  it('renders one row per device for the same client', async () => {
    listAuthorizedAppsMock.mockResolvedValue([grantOne, grantTwo])

    const wrapper = mount(AuthorizedAppsView, { global: { stubs } })
    await flushPromises()

    expect(wrapper.find('[data-testid="row-grant-1"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="row-grant-2"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="row-grant-1"]').text()).toContain('Ariel MBP')
    expect(wrapper.find('[data-testid="row-grant-2"]').text()).toContain('Ariel iPhone')
  })

  it('renders empty state when no grants exist', async () => {
    listAuthorizedAppsMock.mockResolvedValue([])

    const wrapper = mount(AuthorizedAppsView, { global: { stubs } })
    await flushPromises()

    expect(wrapper.find('[data-testid="empty"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="empty-state"]').exists()).toBe(true)
  })

  it('renders a disabled-client badge for decommissioned clients', async () => {
    listAuthorizedAppsMock.mockResolvedValue([disabledGrant])

    const wrapper = mount(AuthorizedAppsView, { global: { stubs } })
    await flushPromises()

    expect(wrapper.find('[data-testid="row-grant-3"]').text()).toContain(
      'authorizedApps.disabledBadge'
    )
  })

  it('renders human-readable scope labels', async () => {
    listAuthorizedAppsMock.mockResolvedValue([grantOne])

    const wrapper = mount(AuthorizedAppsView, { global: { stubs } })
    await flushPromises()

    const row = wrapper.find('[data-testid="row-grant-1"]').text()
    expect(row).toContain('Read profile')
    expect(row).toContain('Create images')
  })

  it('shows lastUsedNever placeholder when last_used_at is null', async () => {
    listAuthorizedAppsMock.mockResolvedValue([grantTwo])

    const wrapper = mount(AuthorizedAppsView, { global: { stubs } })
    await flushPromises()

    expect(wrapper.find('[data-testid="row-grant-2"]').text()).toContain(
      'authorizedApps.lastUsedNever'
    )
  })

  it('revokes a single device after confirmation', async () => {
    listAuthorizedAppsMock.mockResolvedValue([grantOne, grantTwo])
    revokeAuthorizedAppMock.mockResolvedValue(undefined)

    const wrapper = mount(AuthorizedAppsView, { global: { stubs } })
    await flushPromises()

    await wrapper
      .find('[data-testid="row-grant-1"] button')
      .trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="confirm-dialog"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="confirm-title"]').text()).toBe(
      'authorizedApps.revokeDeviceConfirmTitle'
    )

    await wrapper.find('[data-testid="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(revokeAuthorizedAppMock).toHaveBeenCalledWith('grant-1')
    expect(wrapper.find('[data-testid="row-grant-1"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="row-grant-2"]').exists()).toBe(true)
    expect(showSuccessMock).toHaveBeenCalledWith('authorizedApps.revokedOne')
  })

  it('revokes every grant for a client when revoke-all is confirmed', async () => {
    listAuthorizedAppsMock.mockResolvedValue([grantOne, grantTwo, disabledGrant])
    revokeAuthorizedAppsForClientMock.mockResolvedValue({ revoked: 2 })

    const wrapper = mount(AuthorizedAppsView, { global: { stubs } })
    await flushPromises()

    const buttons = wrapper.findAll('[data-testid="row-grant-1"] button')
    await buttons[buttons.length - 1].trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="confirm-title"]').text()).toContain(
      'authorizedApps.revokeAllConfirmTitle'
    )
    expect(wrapper.find('[data-testid="confirm-message"]').text()).toContain('count=2')

    await wrapper.find('[data-testid="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(revokeAuthorizedAppsForClientMock).toHaveBeenCalledWith('sakrylle-cli')
    expect(wrapper.find('[data-testid="row-grant-1"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="row-grant-2"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="row-grant-3"]').exists()).toBe(true)
  })

  it('shows an error toast when listing fails', async () => {
    listAuthorizedAppsMock.mockRejectedValue(new Error('network down'))

    mount(AuthorizedAppsView, { global: { stubs } })
    await flushPromises()

    expect(showErrorMock).toHaveBeenCalledWith('network down')
  })

  it('shows an error toast when device revoke fails and closes the dialog', async () => {
    listAuthorizedAppsMock.mockResolvedValue([grantOne])
    revokeAuthorizedAppMock.mockRejectedValue(new Error('boom'))

    const wrapper = mount(AuthorizedAppsView, { global: { stubs } })
    await flushPromises()

    await wrapper.find('[data-testid="row-grant-1"] button').trigger('click')
    await flushPromises()
    await wrapper.find('[data-testid="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(showErrorMock).toHaveBeenCalledWith('boom')
    expect(wrapper.find('[data-testid="row-grant-1"]').exists()).toBe(true)
    // Dialog must close on failure too — otherwise the user is stuck staring at a
    // confirmation modal after seeing the error toast.
    expect(wrapper.find('[data-testid="confirm-dialog"]').exists()).toBe(false)
  })

  it('closes the dialog when revoke-all fails', async () => {
    listAuthorizedAppsMock.mockResolvedValue([grantOne, grantTwo])
    revokeAuthorizedAppsForClientMock.mockRejectedValue(new Error('upstream down'))

    const wrapper = mount(AuthorizedAppsView, { global: { stubs } })
    await flushPromises()

    const buttons = wrapper.findAll('[data-testid="row-grant-1"] button')
    await buttons[buttons.length - 1].trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="confirm-dialog"]').exists()).toBe(true)

    await wrapper.find('[data-testid="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(showErrorMock).toHaveBeenCalledWith('upstream down')
    // Both rows still present — neither client was actually revoked.
    expect(wrapper.find('[data-testid="row-grant-1"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="row-grant-2"]').exists()).toBe(true)
    // Dialog closed despite the failure.
    expect(wrapper.find('[data-testid="confirm-dialog"]').exists()).toBe(false)
  })

  it('cancels the confirmation dialog without calling the API', async () => {
    listAuthorizedAppsMock.mockResolvedValue([grantOne])

    const wrapper = mount(AuthorizedAppsView, { global: { stubs } })
    await flushPromises()

    await wrapper.find('[data-testid="row-grant-1"] button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="confirm-dialog"]').exists()).toBe(true)

    await wrapper.find('[data-testid="confirm-cancel"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="confirm-dialog"]').exists()).toBe(false)
    expect(revokeAuthorizedAppMock).not.toHaveBeenCalled()
    expect(revokeAuthorizedAppsForClientMock).not.toHaveBeenCalled()
  })
})
