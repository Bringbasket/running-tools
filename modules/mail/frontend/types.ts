export interface MailAlias {
  hme: string
  label?: string
  note?: string
  forwardToEmail?: string
  isActive?: boolean
  createTimestamp?: number
  anonymousId: string
  origin?: string
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
  workerRunning: boolean
  running: boolean
  currentIndex: number
  currentTotal: number
  currentSuccess: number
  remainingSeconds: number | null
  nextRunAt: number | null
  serverNow: number
}
