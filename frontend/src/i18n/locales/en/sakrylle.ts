export default {
  authorizedApps: {
    title: 'Authorized Apps',
    description: 'Manage third-party apps that can access your account',
    emptyTitle: 'No authorized apps yet',
    empty: 'When you authorize a third-party app, it will appear here. You can revoke access at any time.',
    columns: {
      app: 'App',
      device: 'Device',
      group: 'Group',
      scopes: 'Permissions',
      firstAuthorized: 'First authorized',
      lastUsed: 'Last used',
      tokens: 'Tokens'
    },
    revoke: 'Revoke',
    revokeDevice: 'Revoke this device',
    revokeAllForClient: 'Revoke all devices for this app',
    revokeConfirmTitle: 'Revoke access?',
    revokeConfirmMessage: '{name} will no longer be able to access your account. Any access tokens already issued will be invalidated immediately.',
    revokeDeviceConfirmTitle: 'Revoke access for this device?',
    revokeDeviceConfirmMessage: 'Only the {name} session on {device} will be revoked. The app will keep working on your other devices. Any access tokens already issued for this device will be invalidated immediately.',
    revokeAllConfirmTitle: 'Revoke every session for {name}?',
    revokeAllConfirmMessage: 'This revokes the app on every device ({count} session(s) total). Any access tokens already issued will be invalidated immediately.',
    revoked: 'Revoked {count} session(s)',
    revokedOne: 'Device revoked',
    revokeFailed: 'Revocation failed',
    stepUpRequired: 'For security, please log in again to revoke authorization',
    loadFailed: 'Failed to load',
    disabledBadge: 'Decommissioned',
    disabledTooltip: 'This app has been decommissioned by the administrator, but you can still revoke prior authorizations',
    deviceUnknown: 'Unnamed device',
    lastUsedNever: 'Never used',
    tokenCount: '{access} access / {refresh} refresh',
    appType: {
      web: 'Web',
      cli: 'CLI',
      mobile: 'Mobile',
      desktop: 'Desktop',
      chat: 'Chat',
      image: 'Image'
    }
  },

  oauthScopes: {
    'profile:read': {
      name: 'Read your profile (username, avatar)',
      description: 'Read your username, display name, and avatar. Does not include email or balance.'
    },
    'email:read': {
      name: 'Read your email address',
      description: 'Read your email address.'
    },
    'account:read': {
      name: 'Read account info (current group, allowed groups, quota)',
      description: 'Read your current group, allowed groups, and quota. Does not include passwords or API keys.'
    },
    'account:balance:read': {
      name: 'Read account balance',
      description: 'Read your account balance and currency display settings.'
    },
    'models:read': {
      name: 'List available models',
      description: 'List models available to your current group.'
    },
    'chat.completions:create': {
      name: 'Call chat completions API',
      description: 'Call /v1/chat/completions on your behalf. Usage is billed to your account.'
    },
    'responses:create': {
      name: 'Call Responses API',
      description: 'Call /v1/responses on your behalf. Usage is billed to your account.'
    },
    'messages:create': {
      name: 'Call Messages API',
      description: 'Call /v1/messages on your behalf. Usage is billed to your account.'
    },
    'images:create': {
      name: 'Create and edit images',
      description: 'Call /v1/images/* on your behalf to generate or edit images. Usage is billed to your account.'
    },
    'usage:read': {
      name: 'Read usage statistics',
      description: 'Read your usage records and statistics.'
    },
    offline_access: {
      name: 'Offline access (issue a refresh token)',
      description: 'Keep your authorization active across sessions by issuing a refresh token. You can revoke at any time.'
    }
  },

  plaza: {
    title: 'Model Plaza',
    description: 'Browse every model and price available to you.',
    modelCount: '{count} models',
    searchPlaceholder: 'Search models, platform, or channel...',
    showOriginal: 'Show original',
    standard: 'Standard',
    longContext: 'Long context (>272K)',
    longContextBadge: 'Long context > {threshold}',
    empty: 'No models available yet - ask an admin to grant you a group.',
    noMatch: 'No models match the current filters.',
    noPricing: 'No pricing configured for this model',
    copy: 'Copy {name}',
    moreGroupsTooltip: 'More groups can reach this model',
    filters: {
      title: 'Filters',
      reset: 'Reset',
      all: 'All',
      platform: 'Platform',
      group: 'Available Groups',
      billing: 'Billing Type'
    },
    billing: {
      token: 'Per Token',
      perRequest: 'Per Request',
      image: 'Per Image'
    },
    pricing: {
      input: 'Input',
      output: 'Output',
      imageInput: 'Image input',
      cacheRead: 'Cache Read',
      cacheWrite: 'Cache Write',
      perRequest: 'Per Request',
      image: 'Image',
      unitPerMillion: '/ 1M tokens',
      unitPerRequest: '/ request'
    }
  }
}
