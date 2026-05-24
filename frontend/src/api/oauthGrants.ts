/**
 * OAuth provider — user-facing "Authorized Apps" management.
 *
 * Mirrors backend handlers at:
 *   GET    /api/v1/oauth/grants
 *   DELETE /api/v1/oauth/grants/:client_id
 *
 * Both endpoints sit behind the JWT auth middleware; the apiClient injects
 * the bearer token automatically.
 */

import { apiClient } from './client'
import type { AuthorizedApp, AuthorizedAppsResponse } from '@/types'

/** List the current user's active OAuth authorizations, one per third-party app. */
export async function list(options?: { signal?: AbortSignal }): Promise<AuthorizedApp[]> {
  const { data } = await apiClient.get<AuthorizedAppsResponse>('/oauth/grants', {
    signal: options?.signal
  })
  return data.items ?? []
}

/**
 * Revoke every active token the current user holds for the given client.
 * Idempotent — revoking an already-revoked grant returns `{ revoked: 0 }`.
 */
export async function revoke(clientId: string): Promise<{ revoked: number }> {
  const { data } = await apiClient.delete<{ revoked: number }>(
    `/oauth/grants/${encodeURIComponent(clientId)}`
  )
  return data
}

export const oauthGrantsAPI = {
  list,
  revoke
}

export default oauthGrantsAPI
