export default {
  authorizedApps: {
    title: '已授权应用',
    description: '管理第三方应用对你账户的访问授权',
    emptyTitle: '还没有授权任何应用',
    empty: '当你授权第三方应用时，它们会显示在这里。你可以随时撤销访问。',
    columns: {
      app: '应用',
      device: '设备',
      group: '分组',
      scopes: '权限',
      firstAuthorized: '首次授权',
      lastUsed: '最近使用',
      tokens: '令牌'
    },
    revoke: '撤销授权',
    revokeDevice: '撤销此设备',
    revokeAllForClient: '撤销该应用全部授权',
    revokeConfirmTitle: '撤销授权？',
    revokeConfirmMessage: '撤销后，{name} 将无法再访问你的账户。已签发的访问令牌会立即失效。',
    revokeDeviceConfirmTitle: '撤销此设备的授权？',
    revokeDeviceConfirmMessage: '只会撤销 {device} 上的 {name}。该应用在其他设备上仍可继续访问。已签发的访问令牌会立即失效。',
    revokeAllConfirmTitle: '撤销 {name} 的全部授权？',
    revokeAllConfirmMessage: '将同时撤销该应用在所有设备上的访问权限（共 {count} 个会话）。已签发的访问令牌会立即失效。',
    revoked: '已撤销 {count} 个会话',
    revokedOne: '已撤销该设备的授权',
    revokeFailed: '撤销失败',
    stepUpRequired: '为了安全，请重新登录后再撤销授权',
    loadFailed: '加载失败',
    disabledBadge: '已下线',
    disabledTooltip: '该应用已被管理员下线，但你仍可以撤销之前的授权',
    deviceUnknown: '未命名设备',
    lastUsedNever: '从未使用',
    tokenCount: '{access} 个访问令牌 / {refresh} 个刷新令牌',
    appType: {
      web: 'Web',
      cli: 'CLI',
      mobile: '移动端',
      desktop: '桌面端',
      chat: '聊天客户端',
      image: '图片生成'
    }
  },

  oauthScopes: {
    'profile:read': {
      name: '查看您的档案（用户名、头像）',
      description: '允许应用读取你的用户名、显示名称和头像，不包括邮箱或余额。'
    },
    'email:read': {
      name: '查看您的邮箱地址',
      description: '允许应用读取你的邮箱地址。'
    },
    'account:read': {
      name: '查看账户信息（当前分组、可用分组、配额）',
      description: '允许应用读取你的当前分组、可用分组和配额，不包括账户密码或 API 密钥。'
    },
    'account:balance:read': {
      name: '查看账户余额',
      description: '允许应用查看你的账户余额和币种显示设置。'
    },
    'models:read': {
      name: '列出可用模型',
      description: '允许应用列出当前分组可用的模型。'
    },
    'chat.completions:create': {
      name: '调用聊天补全 API（/v1/chat/completions）',
      description: '允许应用代表你调用 /v1/chat/completions 发起对话请求，将按用量计费。'
    },
    'responses:create': {
      name: '调用 Responses API（/v1/responses）',
      description: '允许应用代表你调用 /v1/responses 发起请求，将按用量计费。'
    },
    'messages:create': {
      name: '调用 Messages API（/v1/messages）',
      description: '允许应用代表你调用 /v1/messages 发起请求，将按用量计费。'
    },
    'images:create': {
      name: '生成与编辑图片',
      description: '允许应用代表你调用 /v1/images/* 端点生成或编辑图片，将按用量计费。'
    },
    'usage:read': {
      name: '查看用量统计',
      description: '允许应用读取你的用量记录和统计数据。'
    },
    offline_access: {
      name: '保持离线访问（颁发 refresh token）',
      description: '允许应用即使在你不在线时也可以续期访问令牌。可随时撤销。'
    }
  },

  plaza: {
    title: '模型广场',
    description: '浏览你可用的全部模型与价格。',
    modelCount: '共 {count} 个模型',
    searchPlaceholder: '搜索模型、平台或渠道...',
    showOriginal: '显示原价',
    standard: '标准',
    longContext: '长上下文 (>272K)',
    longContextBadge: '长上下文 > {threshold}',
    empty: '暂无可用模型，请联系管理员开通分组。',
    noMatch: '没有匹配的模型，试试调整筛选条件。',
    noPricing: '该模型尚未配置价格',
    copy: '复制 {name}',
    moreGroupsTooltip: '还有更多分组可访问该模型',
    filters: {
      title: '筛选',
      reset: '重置',
      all: '全部',
      platform: '平台',
      group: '可用分组',
      billing: '计费类型'
    },
    billing: {
      token: '按量计费',
      perRequest: '按次计费',
      image: '按图片计费'
    },
    pricing: {
      input: '输入',
      output: '输出',
      imageInput: '图片输入',
      cacheRead: '缓存读取',
      cacheWrite: '缓存写入',
      perRequest: '每次',
      image: '图片',
      unitPerMillion: '/ 1M token',
      unitPerRequest: '/ 次'
    }
  }
}
