import { apiRequest } from '../../../frontend/src/api'
import type { AliasQueueStatus, AutoRefreshStatus, CreateScheduleStatus, MailAlias, MailboxStatus, MailMessage, SessionStatus, ShareLink } from './types'

const base = '/api/mail/v1'

export const mailAPI = {
  aliases: () => apiRequest<MailAlias[]>(`${base}/aliases`),
  createAlias: (label: string, note: string) => apiRequest<MailAlias>(`${base}/aliases`, { method: 'POST', body: JSON.stringify({ label, note }) }),
  aliasAction: (id: string, action: 'enable' | 'disable' | 'delete') => apiRequest(`${base}/aliases/${encodeURIComponent(id)}/${action}`, { method: 'POST', body: '{}' }),
  updateAlias: (id: string, label: string, note: string) => apiRequest<MailAlias>(`${base}/aliases/${encodeURIComponent(id)}/update`, { method: 'POST', body: JSON.stringify({ label, note }) }),
  aliasQueue: () => apiRequest<AliasQueueStatus>(`${base}/alias-queue`),
  enqueueAliases: (baseLabel: string, count: number, note = '', requestId = '') => apiRequest<AliasQueueStatus>(`${base}/alias-queue`, { method: 'POST', body: JSON.stringify({ baseLabel, count, note, requestId }) }),
  aliasQueueControl: (action: 'pause' | 'resume' | 'cancel', jobId: string, confirmUncertain = false) => apiRequest<AliasQueueStatus>(`${base}/alias-queue/${action}`, { method: 'POST', body: JSON.stringify({ jobId, confirmUncertain }) }),
  shareLinks: (id: string) => apiRequest<{ alias: string; links: ShareLink[] }>(`${base}/aliases/${encodeURIComponent(id)}/share-links`),
  createShareLink: (id: string, expiresInSeconds: number | null) => apiRequest<ShareLink>(`${base}/aliases/${encodeURIComponent(id)}/share-links`, { method: 'POST', body: JSON.stringify({ expiresInSeconds }) }),
  revokeShareLink: (id: string) => apiRequest(`${base}/share-links/${encodeURIComponent(id)}/revoke`, { method: 'POST', body: '{}' }),
  mailboxStatus: () => apiRequest<MailboxStatus>(`${base}/mail/sync/status`),
  mailboxRun: () => apiRequest<MailboxStatus>(`${base}/mail/sync/run`, { method: 'POST', body: '{}' }),
  mailboxWait: (revision: number, timeout = 25) => apiRequest<MailboxStatus>(`${base}/mail/sync/wait?revision=${revision}&timeout=${timeout}`),
  mailboxMessages: (alias: string, limit = 20) => apiRequest<{ configured: boolean; alias: string; messages: MailMessage[]; sync: MailboxStatus }>(`${base}/mail/messages?alias=${encodeURIComponent(alias)}&limit=${limit}`),
  mailboxRecent: (limit = 200) => apiRequest<{ days: number; messages: MailMessage[]; sync: MailboxStatus }>(`${base}/mail/recent?limit=${limit}`),
  mailboxMessage: (alias: string, uid: number) => apiRequest<MailMessage>(`${base}/mail/messages/${uid}?alias=${encodeURIComponent(alias)}`),
  hideMailboxMessage: (alias: string, uid: number, sync: MailboxStatus) => apiRequest(`${base}/mail/messages/${uid}/hide`, { method: 'POST', body: JSON.stringify({ alias, uidValidity: sync.uidValidity, mailboxGeneration: sync.mailboxGeneration }) }),
  hideMailboxMessages: (messages: Array<{ alias: string; uid: number }>, sync: MailboxStatus) => apiRequest(`${base}/mail/messages/hide-batch`, { method: 'POST', body: JSON.stringify({ messages, uidValidity: sync.uidValidity, mailboxGeneration: sync.mailboxGeneration }) }),
  session: () => apiRequest<SessionStatus>(`${base}/session/status`),
  refreshSession: () => apiRequest<SessionStatus>(`${base}/session/refresh`, { method: 'POST', body: '{}' }),
  importSession: (curlText: string, region: 'international' | 'china') => apiRequest<{ imported: boolean; icloudRegion: string; host: string }>(`${base}/session/import`, { method: 'POST', body: JSON.stringify({ curl_text: curlText, icloud_region: region }) }),
  autoRefresh: () => apiRequest<AutoRefreshStatus>(`${base}/auto-refresh`),
  updateAutoRefresh: (payload: { enabled?: boolean; intervalSeconds?: number }) => apiRequest<AutoRefreshStatus>(`${base}/auto-refresh`, { method: 'POST', body: JSON.stringify(payload) }),
  runAutoRefresh: () => apiRequest<{ autoRefresh: AutoRefreshStatus; session: SessionStatus }>(`${base}/auto-refresh/run`, { method: 'POST', body: '{}' }),
  createSchedule: () => apiRequest<CreateScheduleStatus>(`${base}/create-schedule`),
  updateCreateSchedule: (payload: Partial<Pick<CreateScheduleStatus, 'enabled' | 'batchSize' | 'aliasIntervalSeconds' | 'intervalSeconds' | 'label' | 'note'>>) => apiRequest<CreateScheduleStatus>(`${base}/create-schedule`, { method: 'POST', body: JSON.stringify(payload) }),
  runCreateSchedule: () => apiRequest<CreateScheduleStatus>(`${base}/create-schedule/run`, { method: 'POST', body: '{}' }),
  stopCreateSchedule: () => apiRequest<CreateScheduleStatus>(`${base}/create-schedule/stop`, { method: 'POST', body: '{}' }),
}
