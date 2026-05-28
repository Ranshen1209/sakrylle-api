import { beforeEach, describe, expect, it, vi } from 'vitest'

const get = vi.fn()
const del = vi.fn()

vi.mock('@/api/client', () => ({
  apiClient: {
    get,
    delete: del
  }
}))

describe('oauthGrants v2 authorized apps api', () => {
  beforeEach(() => {
    get.mockReset()
    del.mockReset()
  })

  it('lists per-grant authorized apps from the v2 endpoint', async () => {
    get.mockResolvedValue({
      data: {
        items: [
          {
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
            scopes: ['profile:read', 'responses:create'],
            first_authorized_at: '2026-05-27T12:00:00Z',
            last_used_at: '2026-05-27T12:30:00Z',
            last_used_ip: null,
            active_access_token_count: 1,
            active_refresh_token_count: 1,
            status: 'active'
          }
        ]
      }
    })

    const { listAuthorizedApps } = await import('@/api/oauthGrants')
    const grants = await listAuthorizedApps()

    expect(get).toHaveBeenCalledWith('/oauth/authorized-apps', { signal: undefined })
    expect(grants).toHaveLength(1)
    expect(grants[0].grant_id).toBe('grant-1')
  })

  it('returns an empty array when the v2 list endpoint omits items', async () => {
    get.mockResolvedValue({ data: {} })

    const { listAuthorizedApps } = await import('@/api/oauthGrants')
    const grants = await listAuthorizedApps()

    expect(grants).toEqual([])
  })

  it('revokes a single device grant via DELETE /authorized-apps/:grant_id', async () => {
    del.mockResolvedValue({ data: {} })

    const { revokeAuthorizedApp } = await import('@/api/oauthGrants')
    await revokeAuthorizedApp('grant-id-with/slash')

    expect(del).toHaveBeenCalledWith(
      '/oauth/authorized-apps/grant-id-with%2Fslash'
    )
  })

  it('revokes every grant for a client via DELETE /authorized-apps/client/:client_id', async () => {
    del.mockResolvedValue({ data: { revoked: 3 } })

    const { revokeAuthorizedAppsForClient } = await import('@/api/oauthGrants')
    const result = await revokeAuthorizedAppsForClient('sakrylle-cli')

    expect(del).toHaveBeenCalledWith('/oauth/authorized-apps/client/sakrylle-cli')
    expect(result).toEqual({ revoked: 3 })
  })

  it('coerces a 204-style empty response to { revoked: 0 } for revokeAuthorizedAppsForClient', async () => {
    del.mockResolvedValue({ data: undefined })

    const { revokeAuthorizedAppsForClient } = await import('@/api/oauthGrants')
    const result = await revokeAuthorizedAppsForClient('sakrylle-cli')

    expect(result).toEqual({ revoked: 0 })
  })

  it('coerces a 204-style empty response to { revoked: 0 } for the legacy revoke helper', async () => {
    del.mockResolvedValue({ data: undefined })

    const oauthGrantsAPI = (await import('@/api/oauthGrants')).default
    const result = await oauthGrantsAPI.revoke('legacy-client')

    expect(result).toEqual({ revoked: 0 })
  })

  it('exposes the v1 list/revoke helpers for backward compatibility', async () => {
    get.mockResolvedValue({ data: { items: [] } })
    del.mockResolvedValue({ data: { revoked: 0 } })

    const oauthGrantsAPI = (await import('@/api/oauthGrants')).default
    await oauthGrantsAPI.list()
    await oauthGrantsAPI.revoke('legacy-client')

    expect(get).toHaveBeenCalledWith('/oauth/grants', { signal: undefined })
    expect(del).toHaveBeenCalledWith('/oauth/grants/legacy-client')
  })
})
