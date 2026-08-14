export interface MailAlias {
  hme: string
  label?: string
  note?: string
  forwardToEmail?: string
  isActive?: boolean
  createTimestamp?: number
  anonymousId: string
  origin?: string
	usedChannel?: 'apple_account' | 'icloud_web'
	attemptedChannels?: Array<'apple_account' | 'icloud_web'>
	fallbackUsed?: boolean
	detailConfirmed?: boolean
	nextRetryAt?: number | null
}

export type ActivityLogLevel = 'info' | 'warning' | 'error'
export type ActivityLogOutcome = 'success' | 'failure'
export type ActivityLogSource = 'user' | 'background' | 'system'

export interface ActivityLogEntry {
  id: string
  module: string
  category: 'alias' | 'session' | 'mailbox' | 'automation' | string
  action: string
  level: ActivityLogLevel
  outcome: ActivityLogOutcome
  summary: string
  source: ActivityLogSource
  method?: string
  path?: string
  httpStatus?: number
  durationMs: number
  requestId?: string
  detail?: string
  metadata?: Record<string, string | number | boolean>
  createdAt: string
}

export interface ActivityLogPage {
  items: ActivityLogEntry[]
  total: number
  page: number
  pageSize: number
  stats: {
    today: number
    failures24h: number
    background24h: number
  }
}

export interface AliasQueueStatus {
  jobId: string
  requestId?: string
  baseLabel?: string
  note?: string
  requested: number
  status: string
  current: number
  success: number
  createdAt: number
  updatedAt: number
  completedAt: number | null
  nextAttemptAt: number | null
  lastErrorCode?: string
  lastError?: string
  candidateHme?: string
  candidateState?: string
  workerRunning: boolean
  serverNow: number
}

export interface ShareLink {
  id: string
  alias: string
  createdAt: number
  expiresAt: number | null
  lastUsedAt: number | null
  revokedAt: number | null
  active: boolean
  shareUrl?: string
}

export interface BatchShareLinkItem extends ShareLink {
  aliasCreatedAt?: number
}

export interface MailboxStatus {
  configured: boolean
  enabled: boolean
  username?: string
  host?: string
  port?: number
  mailbox?: string
  lastSyncAt: number | null
  lastError?: string
  revision: number
  uidValidity?: number
  mailboxGeneration?: string
  syncMode?: string
  workerRunning: boolean
}

export interface MailboxSettings {
  username: string
  host: string
  port: number
  mailbox: string
  enabled: boolean
  pollSeconds: number
  lookbackDays: number
  cacheMax: number
  passwordConfigured: boolean
  source: 'saved' | 'environment'
}

export interface MailboxSettingsInput {
  username: string
  password: string
  host: string
  port: number
  mailbox: string
  enabled: boolean
  pollSeconds: number
  lookbackDays: number
  cacheMax: number
}

export interface MailMessage {
  uid: number
  aliases: string[]
  from: string
  subject: string
  date: number
  text: string
  safeHtml?: string
  codes: string[]
  partnerCodes: string[]
}

export interface SessionStatus {
  metadataDetected: boolean
  metadata: { host: string; dsid: string; clientId: string } | null
  persistedSession: boolean
  configPath: string
  configUpdatedAt: number | null
  lastSavedAt: number | null
  sessionValid: boolean
  lastRefreshAt: number | null
  lastValidAt: number | null
  expiresHint: string
  lastError: string | null
  needsReauth: boolean
  hme?: { aliasCount?: number; selectedForwardTo?: string; forwardToEmails?: string[] }
  appleLogin: AppleLoginStatus
}

export interface AppleChannelStatus {
  configured: boolean
  healthy: boolean
  appleId?: string
  lastCheckedAt?: number
  expiresAt?: number
  message?: string
	cooldownUntil?: number
	cooldownRemainingSeconds?: number
	lastCreateAt?: number
	lastCreateError?: string
	lastCreateErrorCode?: string
	consecutiveFailures?: number
}

export interface AppleLoginStatus {
  icloudWeb: AppleChannelStatus
  appleAccount: AppleChannelStatus
  createChannel: 'icloud_web' | 'apple_account'
}

export interface AppleLoginResult {
  channel: 'icloud_web' | 'apple_account'
  needs2FA: boolean
  pendingId?: string
  expiresAt?: number
  message: string
  appleId?: string
}

export interface AutoRefreshStatus {
  enabled: boolean
  intervalSeconds: number
  lastRunAt: number | null
  lastSuccessAt: number | null
  lastDisabledAt: number | null
  lastError: string | null
  disabledReason: string | null
  workerRunning: boolean
  remainingSeconds: number | null
  nextRunAt: number | null
  serverNow: number
}

export interface CreateScheduleStatus {
  enabled: boolean
  batchSize: number
  aliasIntervalSeconds: number
  intervalSeconds: number
  label: string
  note: string
  lastRunAt: number | null
  lastSuccessAt: number | null
  lastDisabledAt: number | null
  lastError: string | null
  disabledReason: string | null
  lastBatchRequested: number
  lastBatchSuccess: number
  lastBatchStoppedReason: string | null
	lastUsedChannel?: 'apple_account' | 'icloud_web'
	lastFallbackUsed?: boolean
	lastAttemptedChannels?: Array<'apple_account' | 'icloud_web'>
  workerRunning: boolean
  running: boolean
  currentIndex: number
  currentTotal: number
  currentSuccess: number
  remainingSeconds: number | null
  nextRunAt: number | null
  serverNow: number
}
