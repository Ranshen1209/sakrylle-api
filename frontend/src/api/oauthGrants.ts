/**
 * OAuth provider — user-facing "Authorized Apps" management.
 *
 * v2 endpoints (preferred, see OAUTH_V2_DESIGN.md §12.10):
 *   GET    /api/v1/oauth/authorized-apps
 *   DELETE /api/v1/oauth/authorized-apps/:grant_id
 *   DELETE /api/v1/oauth/authorized-apps/client/:client_id
 *
 * v1 endpoints (compatibility, retained until v1 is removed):
 *   GET    /api/v1/oauth/grants
 *   DELETE /api/v1/oauth/grants/:client_id
 *
 * All endpoints sit behind the JWT auth middleware; the apiClient injects
 * the bearer token automatically.
 */

import { apiClient } from './client'
import type {
  AuthorizedApp,
  AuthorizedAppGrant,
  AuthorizedAppsResponse,
  AuthorizedAppsV2Response
} from '@/types'

// ── v2: per-grant (device-level) ────────────────────────────────────────────

/**
 * List the current user's active OAuth grants, one row per device.
 * A user who logged the same client in from two devices receives two rows.
 */
export async function listAuthorizedApps(options?: {
  signal?: AbortSignal
}): Promise<AuthorizedAppGrant[]> {
  const { data } = await apiClient.get<AuthorizedAppsV2Response>('/oauth/authorized-apps', {
    signal: options?.signal
  })
  return data.items ?? []
}

/**
 * Revoke a single grant (one device for one client). Idempotent: revoking a
 * missing or other-user grant returns success — the backend never reveals
 * whether the grant existed.
 */
export async function revokeAuthorizedApp(grantId: string): Promise<void> {
  await apiClient.delete(`/oauth/authorized-apps/${encodeURIComponent(grantId)}`)
}

/**
 * Revoke every grant the current user holds for the given client. Idempotent.
 * Returns the number of refresh-token rows revoked.
 *
 * Defensively coerces missing fields (e.g. 204 / empty body) to `{ revoked: 0 }`
 * so callers can rely on the shape without optional chaining.
 */
export async function revokeAuthorizedAppsForClient(
  clientId: string
): Promise<{ revoked: number }> {
  const { data } = await apiClient.delete<{ revoked?: number } | undefined>(
    `/oauth/authorized-apps/client/${encodeURIComponent(clientId)}`
  )
  return { revoked: data?.revoked ?? 0 }
}

// ── v1: per-client (legacy) ─────────────────────────────────────────────────

/** Legacy v1 list — one entry per client. Use `listAuthorizedApps` instead. */
export async function list(options?: { signal?: AbortSignal }): Promise<AuthorizedApp[]> {
  const { data } = await apiClient.get<AuthorizedAppsResponse>('/oauth/grants', {
    signal: options?.signal
  })
  return data.items ?? []
}

/**
 * Legacy v1 revoke. Use `revokeAuthorizedAppsForClient` instead.
 * Idempotent — revoking an already-revoked grant returns `{ revoked: 0 }`.
 *
 * Defensively coerces missing fields (e.g. 204 / empty body) to `{ revoked: 0 }`.
 */
export async function revoke(clientId: string): Promise<{ revoked: number }> {
  const { data } = await apiClient.delete<{ revoked?: number } | undefined>(
    `/oauth/grants/${encodeURIComponent(clientId)}`
  )
  return { revoked: data?.revoked ?? 0 }
}

export const oauthGrantsAPI = {
  // v2
  listAuthorizedApps,
  revokeAuthorizedApp,
  revokeAuthorizedAppsForClient,
  // v1 (legacy)
  list,
  revoke
}

export default oauthGrantsAPI
