package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/activitylog"
	"github.com/Bringbasket/running-tools/internal/platform/persistence"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

type MailboxStatus struct {
	Configured        bool     `json:"configured"`
	Enabled           bool     `json:"enabled"`
	Host              string   `json:"host,omitempty"`
	Port              int      `json:"port,omitempty"`
	Mailbox           string   `json:"mailbox,omitempty"`
	LastSyncAt        *float64 `json:"lastSyncAt"`
	LastError         string   `json:"lastError,omitempty"`
	Revision          int64    `json:"revision"`
	UIDValidity       uint32   `json:"uidValidity,omitempty"`
	MailboxGeneration string   `json:"mailboxGeneration,omitempty"`
	SyncMode          string   `json:"syncMode,omitempty"`
	WorkerRunning     bool     `json:"workerRunning"`
}

type MailMessage struct {
	UID          uint32   `json:"uid"`
	Aliases      []string `json:"aliases"`
	From         string   `json:"from"`
	Subject      string   `json:"subject"`
	Date         float64  `json:"date"`
	Text         string   `json:"text"`
	SafeHTML     string   `json:"safeHtml,omitempty"`
	Codes        []string `json:"codes"`
	PartnerCodes []string `json:"partnerCodes"`
}

type mailboxCache struct {
	Status         MailboxStatus `json:"status"`
	Messages       []MailMessage `json:"messages"`
	Hidden         []string      `json:"hidden"`
	HighestUID     uint32        `json:"highestUid,omitempty"`
	AllowedAliases []string      `json:"allowedAliases,omitempty"`
}
type mailboxConfig struct {
	Username, Password, Host, Mailbox         string
	Port, PollSeconds, LookbackDays, CacheMax int
	Enabled                                   bool
	Source                                    string
	PasswordStored                            bool
}
type MailboxService struct {
	mu            sync.Mutex
	syncMu        sync.Mutex
	imapMu        sync.Mutex
	path          string
	settingsPath  string
	session       *SessionManager
	stop          chan struct{}
	done          chan struct{}
	wake          chan struct{}
	revisionEvent chan struct{}
	workerRunning bool
	workerMode    string
	repository    mailboxRepository
	persistence   *persistence.Service
	accountID     string
	lastCache     mailboxCache
	cacheLoaded   bool
	lastLoadErr   error
	logs          *activitylog.Store
	lastLogState  string
	imapClient    *imapclient.Client
	imapTarget    string
	imapUpdates   chan struct{}
	dialIMAP      func(string, *imapclient.Options) (*imapclient.Client, error)
}

func (s *MailboxService) SetActivityLog(logs *activitylog.Store) { s.logs = logs }

func (s *MailboxService) Clear(ctx context.Context) error {
	s.RequestSync()
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.repository.Clear(ctx); err != nil {
		return err
	}
	s.lastCache = normalizedMailboxCache(mailboxCache{})
	s.cacheLoaded, s.lastLoadErr = true, nil
	s.notifyRevisionLocked()
	return nil
}

func NewMailboxService(stateDir string, session *SessionManager) *MailboxService {
	service := &MailboxService{
		path:          filepath.Join(stateDir, "mailbox-cache.json"),
		settingsPath:  defaultMailboxSettingsPath(stateDir),
		session:       session,
		wake:          make(chan struct{}, 1),
		revisionEvent: make(chan struct{}),
		accountID:     defaultMailAccountID,
		dialIMAP:      imapclient.DialTLS,
	}
	service.repository = &jsonMailboxRepository{path: service.path}
	return service
}

func NewMailboxServiceWithPersistence(stateDir string, session *SessionManager, persistenceService *persistence.Service) (*MailboxService, error) {
	return NewMailboxServiceWithPersistenceForAccount(stateDir, defaultMailAccountID, session, persistenceService)
}

func NewMailboxServiceWithPersistenceForAccount(stateDir, accountID string, session *SessionManager, persistenceService *persistence.Service) (*MailboxService, error) {
	service := NewMailboxService(stateDir, session)
	repository, err := newMailboxRepositoryForAccount(service.path, accountID, persistenceService)
	if err != nil {
		return nil, err
	}
	service.repository = repository
	service.persistence = persistenceService
	service.accountID = accountID
	return service, nil
}
func (s *MailboxService) Start() {
	s.mu.Lock()
	if s.stop != nil {
		s.mu.Unlock()
		return
	}
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	stop, done := s.stop, s.done
	s.mu.Unlock()
	go func() {
		defer func() {
			s.closeIMAPConnection()
			s.setWorkerState(false, "stopped")
			s.mu.Lock()
			if s.stop == stop {
				s.stop, s.done = nil, nil
			}
			s.mu.Unlock()
			close(done)
		}()
		for {
			if mailboxStopRequested(stop) {
				return
			}
			cfg := s.config()
			if !cfg.Enabled {
				s.closeIMAPConnection()
				s.setWorkerState(false, "disabled")
				select {
				case <-time.After(30 * time.Second):
				case <-s.wake:
				case <-stop:
					return
				}
				continue
			}
			s.setWorkerState(true, "sync")
			_ = s.SyncFromSession()
			if mailboxStopRequested(stop) {
				return
			}
			cfg = s.config()
			s.setWorkerState(true, "idle")
			idleSupported, changed := s.waitForIMAPChange(cfg, time.Duration(cfg.PollSeconds)*time.Second, stop)
			if !idleSupported {
				s.setWorkerState(true, "poll")
				select {
				case <-time.After(time.Duration(cfg.PollSeconds) * time.Second):
				case <-s.wake:
				case <-stop:
					return
				}
				continue
			}
			if changed {
				continue
			}
			select {
			case <-s.wake:
			case <-stop:
				return
			default:
			}
		}
	}()
}
func (s *MailboxService) Stop() {
	s.mu.Lock()
	if s.stop == nil {
		s.mu.Unlock()
		return
	}
	stop, done := s.stop, s.done
	select {
	case <-stop:
	default:
		close(stop)
	}
	s.mu.Unlock()
	// Closing the active socket interrupts SELECT, FETCH and IDLE immediately.
	// The worker owns final cleanup and keeps Start blocked until it has exited.
	s.closeIMAPConnection()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

func mailboxStopRequested(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

func (s *MailboxService) RequestSync() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *MailboxService) setWorkerState(running bool, mode string) {
	s.mu.Lock()
	s.workerRunning = running
	s.workerMode = mode
	s.mu.Unlock()
}
func (s *MailboxService) SyncFromSession() error {
	before := s.Status()
	aliases, err := s.session.ListAliases(context.Background())
	if err != nil {
		s.recordBackgroundSync(before, before, err)
		return err
	}
	addresses := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		if active, ok := alias["isActive"].(bool); ok && !active {
			continue
		}
		if address := strings.TrimSpace(fmt.Sprint(alias["hme"])); address != "" {
			addresses = append(addresses, address)
		}
	}
	status, err := s.RunSync(addresses)
	s.recordBackgroundSync(before, status, err)
	return err
}

func (s *MailboxService) recordBackgroundSync(before, after MailboxStatus, err error) {
	if s.logs == nil {
		return
	}
	if err != nil {
		state := "failure:" + safeErrorText(err)
		s.mu.Lock()
		duplicate := s.lastLogState == state
		if !duplicate {
			s.lastLogState = state
		}
		s.mu.Unlock()
		if duplicate {
			return
		}
		s.logs.Record(context.Background(), activitylog.Input{Category: "mailbox", Action: "mailbox.sync.background",
			Level: "error", Outcome: "failure", Summary: "后台同步收件箱失败", Source: "background", Detail: safeErrorText(err)})
		return
	}
	if after.Revision == before.Revision && before.LastError == "" {
		return
	}
	s.mu.Lock()
	previousState := s.lastLogState
	s.lastLogState = "success"
	s.mu.Unlock()
	summary := "后台同步收件箱完成"
	if before.LastError != "" || strings.HasPrefix(previousState, "failure:") {
		summary = "后台收件箱同步已恢复"
	}
	s.logs.Record(context.Background(), activitylog.Input{Category: "mailbox", Action: "mailbox.sync.background",
		Summary: summary, Source: "background", Metadata: map[string]any{"revision": after.Revision}})
}
func (s *MailboxService) loadLocked() mailboxCache {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cache, _, err := s.repository.Load(ctx)
	if err != nil {
		s.lastLoadErr = err
		if s.cacheLoaded {
			return s.lastCache
		}
		return normalizedMailboxCache(mailboxCache{Status: MailboxStatus{LastError: "邮箱存储读取失败: " + safeErrorText(err)}})
	}
	s.lastCache = cache
	s.cacheLoaded = true
	s.lastLoadErr = nil
	return cache
}

func (s *MailboxService) saveLocked(cache mailboxCache) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.repository.Save(ctx, cache); err != nil {
		return err
	}
	s.lastCache = cache
	s.cacheLoaded = true
	return nil
}
func (s *MailboxService) Status() MailboxStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	cache := s.loadLocked()
	cfg := s.config()
	cache.Status.Configured = cfg.Username != "" && cfg.Password != ""
	cache.Status.Enabled = cfg.Enabled
	cache.Status.Host = cfg.Host
	cache.Status.Port = cfg.Port
	cache.Status.Mailbox = cfg.Mailbox
	cache.Status.WorkerRunning = s.workerRunning
	cache.Status.SyncMode = s.workerMode
	return cache.Status
}
func (s *MailboxService) Messages(alias string, limit int) map[string]any {
	return s.messages(alias, limit, false)
}

// MessagesDetailed is used by read-only share pages that render the message
// body inline. The normal mailbox list keeps the existing compact preview.
func (s *MailboxService) MessagesDetailed(alias string, limit int) map[string]any {
	return s.messages(alias, limit, true)
}

func (s *MailboxService) messages(alias string, limit int, detailed bool) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	cache := s.loadLocked()
	cfg := s.config()
	cache.Status = s.statusWithConfig(cache.Status, cfg)
	alias = strings.ToLower(strings.TrimSpace(alias))
	if limit < 1 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	hidden := hiddenSet(cache.Hidden)
	matches := []MailMessage{}
	for _, m := range cache.Messages {
		if hidden[messageKey(cache.Status.MailboxGeneration, alias, m.UID)] {
			continue
		}
		for _, address := range m.Aliases {
			if address == alias {
				if !detailed {
					m = summarizeMailMessage(m)
				}
				normalizeMailMessageLists(&m)
				matches = append(matches, m)
				break
			}
		}
		if len(matches) >= limit {
			break
		}
	}
	return map[string]any{"configured": cfg.Username != "" && cfg.Password != "", "alias": alias, "messages": matches, "sync": cache.Status}
}

func (s *MailboxService) Recent(limit int) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	cache := s.loadLocked()
	cache.Status = s.statusWithConfig(cache.Status, s.config())
	if limit < 1 {
		limit = 200
	}
	if limit > 500 {
		limit = 500
	}
	hidden := hiddenSet(cache.Hidden)
	items := make([]MailMessage, 0, min(limit, len(cache.Messages)))
	cutoff := float64(time.Now().Add(-72 * time.Hour).Unix())
	for _, message := range cache.Messages {
		if message.Date < cutoff {
			continue
		}
		visible := false
		for _, alias := range message.Aliases {
			if !hidden[messageKey(cache.Status.MailboxGeneration, alias, message.UID)] {
				visible = true
				break
			}
		}
		if visible {
			normalizeMailMessageLists(&message)
			items = append(items, summarizeMailMessage(message))
		}
		if len(items) >= limit {
			break
		}
	}
	return map[string]any{"days": 3, "messages": items, "sync": cache.Status}
}

func summarizeMailMessage(message MailMessage) MailMessage {
	message.Text = truncateText(message.Text, 160)
	message.SafeHTML = ""
	return message
}

func normalizeMailMessageLists(message *MailMessage) {
	if message == nil {
		return
	}
	if message.Aliases == nil {
		message.Aliases = []string{}
	}
	if message.Codes == nil {
		message.Codes = []string{}
	}
	if message.PartnerCodes == nil {
		message.PartnerCodes = []string{}
	}
}

func (s *MailboxService) Message(alias string, uid uint32) (MailMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cache := s.loadLocked()
	alias = strings.ToLower(strings.TrimSpace(alias))
	if hiddenSet(cache.Hidden)[messageKey(cache.Status.MailboxGeneration, alias, uid)] {
		return MailMessage{}, false
	}
	for _, message := range cache.Messages {
		if message.UID != uid {
			continue
		}
		for _, address := range message.Aliases {
			if address == alias {
				normalizeMailMessageLists(&message)
				return message, true
			}
		}
	}
	return MailMessage{}, false
}

func (s *MailboxService) Hide(alias string, uid uint32, uidValidity uint32, generation string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cache := s.loadLocked()
	if s.lastLoadErr != nil {
		return nil, fmt.Errorf("读取邮箱存储失败: %w", s.lastLoadErr)
	}
	if uidValidity != cache.Status.UIDValidity || generation == "" || generation != cache.Status.MailboxGeneration {
		return nil, errors.New("邮箱缓存版本已变化，请刷新后重试")
	}
	alias = strings.ToLower(strings.TrimSpace(alias))
	var target *MailMessage
	for _, message := range cache.Messages {
		if message.UID != uid {
			continue
		}
		for _, address := range message.Aliases {
			if address == alias {
				copy := message
				target = &copy
				break
			}
		}
	}
	if target == nil {
		return nil, errors.New("未找到该邮件")
	}
	hidden := hiddenSet(cache.Hidden)
	newlyHidden := 0
	for _, address := range target.Aliases {
		key := messageKey(generation, address, uid)
		if !hidden[key] {
			cache.Hidden = append(cache.Hidden, key)
			hidden[key] = true
			newlyHidden++
		}
	}
	if newlyHidden > 0 {
		cache.Status.Revision++
		if err := s.saveLocked(cache); err != nil {
			return nil, err
		}
		s.notifyRevisionLocked()
	}
	return map[string]any{"alias": alias, "uid": uid, "hidden": true, "alreadyHidden": newlyHidden == 0, "gmailDeleted": false, "deletedCount": newlyHidden, "revision": cache.Status.Revision}, nil
}

func (s *MailboxService) HideBatch(items []struct {
	Alias string `json:"alias"`
	UID   uint32 `json:"uid"`
}, uidValidity uint32, generation string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cache := s.loadLocked()
	if s.lastLoadErr != nil {
		return nil, fmt.Errorf("读取邮箱存储失败: %w", s.lastLoadErr)
	}
	if uidValidity != cache.Status.UIDValidity || generation == "" || generation != cache.Status.MailboxGeneration {
		return nil, errors.New("邮箱缓存版本已变化，请刷新后重试")
	}
	if len(items) == 0 || len(items) > 200 {
		return nil, errors.New("每次需选择 1 到 200 封邮件")
	}

	byUID := make(map[uint32]MailMessage, len(cache.Messages))
	for _, message := range cache.Messages {
		byUID[message.UID] = message
	}
	requestedUIDs := make(map[uint32]bool, len(items))
	for _, item := range items {
		alias := strings.ToLower(strings.TrimSpace(item.Alias))
		message, ok := byUID[item.UID]
		if !ok || alias == "" {
			return nil, errors.New("批量选择中包含未找到的邮件")
		}
		belongsToAlias := false
		for _, address := range message.Aliases {
			if address == alias {
				belongsToAlias = true
				break
			}
		}
		if !belongsToAlias {
			return nil, errors.New("批量选择中包含未找到的邮件")
		}
		requestedUIDs[item.UID] = true
	}

	hidden := hiddenSet(cache.Hidden)
	newUIDs, alreadyUIDs := 0, 0
	for uid := range requestedUIDs {
		message := byUID[uid]
		uidWasVisible := false
		for _, address := range message.Aliases {
			key := messageKey(generation, address, uid)
			if !hidden[key] {
				cache.Hidden = append(cache.Hidden, key)
				hidden[key] = true
				uidWasVisible = true
			}
		}
		if uidWasVisible {
			newUIDs++
		} else {
			alreadyUIDs++
		}
	}
	if newUIDs > 0 {
		cache.Status.Revision++
		if err := s.saveLocked(cache); err != nil {
			return nil, err
		}
		s.notifyRevisionLocked()
	}
	return map[string]any{"hidden": true, "requestedCount": len(items), "uniqueUIDCount": len(requestedUIDs), "newlyHiddenCount": newUIDs, "alreadyHiddenCount": alreadyUIDs, "gmailDeleted": false, "revision": cache.Status.Revision}, nil
}

func (s *MailboxService) WaitForRevision(revision int64, timeout time.Duration) MailboxStatus {
	s.mu.Lock()
	cache := s.loadLocked()
	if cache.Status.Revision != revision {
		s.mu.Unlock()
		return s.Status()
	}
	event := s.revisionEvent
	s.mu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-event:
	case <-timer.C:
	}
	return s.Status()
}

func (s *MailboxService) notifyRevisionLocked() {
	close(s.revisionEvent)
	s.revisionEvent = make(chan struct{})
}

func (s *MailboxService) RunSync(aliases []string) (MailboxStatus, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	if s.persistence != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		release, acquired, lockErr := s.persistence.AcquireLock(ctx, "mail:imap-sync:"+s.accountID)
		cancel()
		if lockErr == nil && !acquired {
			return s.Status(), errors.New("另一实例正在同步收件箱")
		}
		if lockErr == nil {
			defer release()
		} else {
			slog.Warn("Redis 同步锁不可用，已回退到本进程锁", "error", safeErrorText(lockErr))
		}
	}

	s.mu.Lock()
	cfg := s.config()
	cache := s.loadLocked()
	if s.lastLoadErr != nil {
		err := s.lastLoadErr
		s.mu.Unlock()
		return MailboxStatus{LastError: "邮箱存储读取失败: " + safeErrorText(err)}, err
	}
	if cfg.Username == "" || cfg.Password == "" {
		cache.Status = s.statusWithConfig(cache.Status, cfg)
		cache.Status.LastError = "请在收件箱设置中配置 IMAP 账号和应用专用密码"
		s.mu.Unlock()
		return cache.Status, errors.New(cache.Status.LastError)
	}
	previousUIDValidity := cache.Status.UIDValidity
	afterUID := cache.HighestUID
	normalizedAliases := normalizeAliases(aliases)
	if !reflect.DeepEqual(cache.AllowedAliases, normalizedAliases) {
		afterUID = 0
	}
	if afterUID == 0 {
		for _, message := range cache.Messages {
			if message.UID > afterUID {
				afterUID = message.UID
			}
		}
	}
	s.mu.Unlock()

	client, err := s.ensureIMAPConnectionLocked(cfg)
	var messages []MailMessage
	var uidValidity, highestUID uint32
	if err == nil {
		messages, uidValidity, highestUID, err = fetchIMAPWithClient(client, cfg, aliases, previousUIDValidity, afterUID)
	}
	if err != nil {
		s.closeIMAPConnectionLocked()
	}
	now := unixNow()
	s.mu.Lock()
	defer s.mu.Unlock()
	cache = s.loadLocked()
	cache.Status = s.statusWithConfig(cache.Status, cfg)
	cache.Status.LastSyncAt = &now
	if err != nil {
		cache.Status.LastError = safeErrorText(err)
		_ = s.saveLocked(cache)
		return cache.Status, err
	}
	generation := mailboxGeneration(cfg, uidValidity)
	generationChanged := cache.Status.MailboxGeneration != "" && cache.Status.MailboxGeneration != generation
	if generationChanged {
		cache.Messages = nil
		cache.Hidden = nil
		cache.HighestUID = 0
	}
	before := cloneMailMessages(cache.Messages)
	cache.Messages = mergeMailMessages(cache.Messages, messages, aliases, cfg.CacheMax)
	cache.HighestUID = highestUID
	cache.AllowedAliases = normalizedAliases
	cache.Status.UIDValidity = uidValidity
	cache.Status.MailboxGeneration = generation
	cache.Status.LastError = ""
	changed := generationChanged || !reflect.DeepEqual(before, cache.Messages)
	if changed {
		cache.Status.Revision++
	}
	if err := s.saveLocked(cache); err != nil {
		return cache.Status, err
	}
	if changed {
		s.notifyRevisionLocked()
	}
	return cache.Status, nil
}
func (s *MailboxService) statusWithConfig(status MailboxStatus, cfg mailboxConfig) MailboxStatus {
	status.Configured = cfg.Username != "" && cfg.Password != ""
	status.Enabled = cfg.Enabled
	status.Host = cfg.Host
	status.Port = cfg.Port
	status.Mailbox = cfg.Mailbox
	status.WorkerRunning = s.workerRunning
	status.SyncMode = s.workerMode
	return status
}

func fetchIMAPWithClient(client *imapclient.Client, cfg mailboxConfig, aliases []string, previousUIDValidity, afterUID uint32) ([]MailMessage, uint32, uint32, error) {
	selected, err := client.Select(cfg.Mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nil, 0, afterUID, fmt.Errorf("IMAP 邮箱选择失败: %w", err)
	}
	criteria := &imap.SearchCriteria{}
	if previousUIDValidity == selected.UIDValidity && afterUID > 0 && afterUID < ^uint32(0) {
		var uidSet imap.UIDSet
		uidSet.AddRange(imap.UID(afterUID+1), 0)
		criteria.UID = []imap.UIDSet{uidSet}
	} else {
		afterUID = 0
		criteria.Since = time.Now().AddDate(0, 0, -cfg.LookbackDays)
	}
	search, err := client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, 0, afterUID, fmt.Errorf("IMAP 搜索失败: %w", err)
	}
	uids := search.AllUIDs()
	highestUID := afterUID
	for _, uid := range uids {
		if uint32(uid) > highestUID {
			highestUID = uint32(uid)
		}
	}
	if len(uids) > 200 {
		if afterUID == 0 {
			uids = uids[len(uids)-200:]
		} else {
			uids = uids[:200]
			highestUID = uint32(uids[len(uids)-1])
		}
	}
	if len(uids) == 0 {
		return []MailMessage{}, selected.UIDValidity, highestUID, nil
	}
	set := imap.UIDSetNum(uids...)
	section := &imap.FetchItemBodySection{Peek: true, Partial: &imap.SectionPartial{Size: 2 << 20}}
	buffers, err := client.Fetch(set, &imap.FetchOptions{UID: true, Envelope: true, InternalDate: true, BodySection: []*imap.FetchItemBodySection{section}}).Collect()
	if err != nil {
		return nil, 0, afterUID, fmt.Errorf("IMAP 邮件读取失败: %w", err)
	}
	allowed := map[string]bool{}
	for _, a := range aliases {
		allowed[strings.ToLower(strings.TrimSpace(a))] = true
	}
	out := []MailMessage{}
	for _, buffer := range buffers {
		raw := buffer.FindBodySection(section)
		message, parseErr := parseMailMessage(uint32(buffer.UID), raw, allowed)
		if parseErr == nil && len(message.Aliases) > 0 {
			out = append(out, message)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date > out[j].Date })
	return out, selected.UIDValidity, highestUID, nil
}

func mergeMailMessages(existing, incoming []MailMessage, aliases []string, maximum int) []MailMessage {
	normalizedAliases := normalizeAliases(aliases)
	allowed := make(map[string]bool, len(normalizedAliases))
	for _, alias := range normalizedAliases {
		allowed[alias] = true
	}
	byUID := make(map[uint32]MailMessage, len(existing)+len(incoming))
	for _, message := range append(existing, incoming...) {
		filtered := make([]string, 0, len(message.Aliases))
		for _, alias := range message.Aliases {
			alias = strings.ToLower(strings.TrimSpace(alias))
			if allowed[alias] {
				filtered = append(filtered, alias)
			}
		}
		if len(filtered) == 0 {
			delete(byUID, message.UID)
			continue
		}
		message.Aliases = append([]string(nil), filtered...)
		byUID[message.UID] = message
	}
	out := make([]MailMessage, 0, len(byUID))
	for _, message := range byUID {
		out = append(out, message)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date == out[j].Date {
			return out[i].UID > out[j].UID
		}
		return out[i].Date > out[j].Date
	})
	if maximum > 0 && len(out) > maximum {
		out = out[:maximum]
	}
	return out
}

func normalizeAliases(aliases []string) []string {
	seen := make(map[string]bool, len(aliases))
	normalized := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		alias = strings.ToLower(strings.TrimSpace(alias))
		if alias != "" && !seen[alias] {
			seen[alias] = true
			normalized = append(normalized, alias)
		}
	}
	sort.Strings(normalized)
	return normalized
}

func cloneMailMessages(messages []MailMessage) []MailMessage {
	cloned := make([]MailMessage, len(messages))
	for index, message := range messages {
		cloned[index] = message
		cloned[index].Aliases = append([]string(nil), message.Aliases...)
		cloned[index].Codes = append([]string(nil), message.Codes...)
		cloned[index].PartnerCodes = append([]string(nil), message.PartnerCodes...)
	}
	return cloned
}

func (s *MailboxService) waitForIMAPChange(cfg mailboxConfig, timeout time.Duration, stop <-chan struct{}) (bool, bool) {
	if cfg.Username == "" || cfg.Password == "" {
		return false, false
	}
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	client, err := s.ensureIMAPConnectionLocked(cfg)
	if err != nil {
		return false, false
	}
	if _, err := client.Select(cfg.Mailbox, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		s.closeIMAPConnectionLocked()
		return false, false
	}
	s.imapMu.Lock()
	updates := s.imapUpdates
	s.imapMu.Unlock()
	for {
		select {
		case <-updates:
		default:
			goto drained
		}
	}

drained:
	idle, err := client.Idle()
	if err != nil {
		return false, false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	changed := false
	select {
	case <-updates:
		changed = true
	case <-s.wake:
		changed = true
	case <-stop:
	case <-timer.C:
	}
	if err := idle.Close(); err != nil {
		s.closeIMAPConnectionLocked()
		return false, changed
	}
	if err := idle.Wait(); err != nil {
		s.closeIMAPConnectionLocked()
		return false, changed
	}
	return true, changed
}

func (s *MailboxService) ensureIMAPConnectionLocked(cfg mailboxConfig) (*imapclient.Client, error) {
	target := imapConnectionTarget(cfg)
	s.imapMu.Lock()
	current, currentTarget := s.imapClient, s.imapTarget
	s.imapMu.Unlock()
	if current != nil && currentTarget == target {
		if err := current.Noop().Wait(); err == nil {
			return current, nil
		}
		s.closeIMAPConnectionLocked()
	} else if current != nil {
		s.closeIMAPConnectionLocked()
	}
	updates := make(chan struct{}, 1)
	options := &imapclient.Options{
		Dialer: &net.Dialer{Timeout: 20 * time.Second},
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				if data.NumMessages != nil {
					select {
					case updates <- struct{}{}:
					default:
					}
				}
			},
		},
	}
	client, err := s.dialIMAP(net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)), options)
	if err != nil {
		return nil, fmt.Errorf("IMAP TLS 连接失败: %w", err)
	}
	if err := client.Login(cfg.Username, cfg.Password).Wait(); err != nil {
		_ = client.Close()
		return nil, imapLoginError(cfg, err)
	}
	s.imapMu.Lock()
	s.imapClient, s.imapTarget, s.imapUpdates = client, target, updates
	s.imapMu.Unlock()
	return client, nil
}

func (s *MailboxService) closeIMAPConnection() {
	s.closeIMAPConnectionLocked()
}

func (s *MailboxService) closeIMAPConnectionLocked() {
	s.imapMu.Lock()
	client := s.imapClient
	s.imapClient, s.imapTarget, s.imapUpdates = nil, "", nil
	s.imapMu.Unlock()
	if client != nil {
		_ = client.Close()
	}
}

func imapConnectionTarget(cfg mailboxConfig) string {
	sum := sha256.Sum256([]byte(strings.ToLower(cfg.Host) + "\x00" + strconv.Itoa(cfg.Port) + "\x00" + cfg.Username + "\x00" + cfg.Password + "\x00" + cfg.Mailbox))
	return fmt.Sprintf("%x", sum[:])
}

func mailboxGeneration(cfg mailboxConfig, uidValidity uint32) string {
	sum := sha256.Sum256([]byte(strings.ToLower(cfg.Host) + "\x00" + cfg.Username + "\x00" + cfg.Mailbox + "\x00" + strconv.FormatUint(uint64(uidValidity), 10)))
	return fmt.Sprintf("%x", sum[:8])
}
func messageKey(generation, alias string, uid uint32) string {
	return generation + ":" + strings.ToLower(strings.TrimSpace(alias)) + ":" + strconv.FormatUint(uint64(uid), 10)
}
func hiddenSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

var emailPattern = regexp.MustCompile(`(?i)[a-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-z0-9.-]+\.[a-z]{2,}`)
var numericCodePattern = regexp.MustCompile(`(?i)(?:验证码|verification\s+code|security\s+code|one[- ]time(?:\s+password)?|otp|passcode|code)[^0-9]{0,160}([0-9]{4,10})(?:\D|$)`)
var alphaCodePattern = regexp.MustCompile(`(?i)(?:验证码|verification\s+code|security\s+code|one[- ]time(?:\s+password)?|otp|passcode|code)(?:\s+(?:is|:))?[^A-Za-z0-9]{0,16}([A-Z0-9]{4,10})(?:\b|$)`)

func parseMailMessage(uid uint32, raw []byte, allowed map[string]bool) (MailMessage, error) {
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return MailMessage{}, err
	}
	addresses := map[string]bool{}
	for key, values := range message.Header {
		lower := strings.ToLower(key)
		if lower == "to" || lower == "cc" || lower == "delivered-to" || lower == "x-original-to" || lower == "x-forwarded-to" {
			for _, value := range values {
				for _, candidate := range emailPattern.FindAllString(value, -1) {
					candidate = strings.ToLower(candidate)
					if allowed[candidate] {
						addresses[candidate] = true
					}
				}
			}
		}
	}
	body, safeHTML, err := readMailBody(message.Header, message.Body, 0)
	if err != nil {
		return MailMessage{}, err
	}
	aliases := make([]string, 0, len(addresses))
	for address := range addresses {
		aliases = append(aliases, address)
	}
	sort.Strings(aliases)
	date := time.Now()
	if parsed, err := message.Header.Date(); err == nil {
		date = parsed
	}
	from := decodeHeader(message.Header.Get("From"))
	subject := decodeHeader(message.Header.Get("Subject"))
	return MailMessage{UID: uid, Aliases: aliases, From: from, Subject: subject, Date: float64(date.UnixNano()) / float64(time.Second), Text: truncateText(body, 200000), SafeHTML: truncateText(safeHTML, 200000), Codes: extractCodes(subject + "\n" + body), PartnerCodes: extractPartnerCodes(body)}, nil
}
func readMailBody(header mail.Header, body io.Reader, depth int) (string, string, error) {
	if depth > 8 {
		return "", "", nil
	}
	mediaType, params, _ := mime.ParseMediaType(header.Get("Content-Type"))
	if disposition, _, _ := mime.ParseMediaType(header.Get("Content-Disposition")); disposition == "attachment" {
		return "", "", nil
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		reader := multipart.NewReader(body, params["boundary"])
		textParts, htmlParts := []string{}, []string{}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return strings.Join(textParts, "\n"), strings.Join(htmlParts, "\n"), nil
			}
			text, safeHTML, _ := readMailBody(mail.Header(part.Header), part, depth+1)
			if text != "" {
				textParts = append(textParts, text)
			}
			if safeHTML != "" {
				htmlParts = append(htmlParts, safeHTML)
			}
		}
		return strings.Join(textParts, "\n"), strings.Join(htmlParts, "\n"), nil
	}
	if mediaType != "" && mediaType != "text/plain" && mediaType != "text/html" {
		return "", "", nil
	}
	decodedBody := body
	switch strings.ToLower(strings.TrimSpace(header.Get("Content-Transfer-Encoding"))) {
	case "base64":
		decodedBody = base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		decodedBody = quotedprintable.NewReader(body)
	}
	data, err := io.ReadAll(io.LimitReader(decodedBody, 2<<20))
	if err != nil {
		return "", "", err
	}
	text := string(data)
	if mediaType == "text/html" {
		safeHTML := sanitizeEmailHTML(text)
		return strings.TrimSpace(stripHTML(safeHTML)), safeHTML, nil
	}
	return strings.TrimSpace(text), "", nil
}
func stripHTML(value string) string {
	value = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</li>`).ReplaceAllString(value, "\n")
	value = regexp.MustCompile(`(?s)<[^>]+>`).ReplaceAllString(value, "")
	return stdhtml.UnescapeString(value)
}

var safeEmailElements = map[string]bool{
	"a": true, "blockquote": true, "br": true, "code": true, "div": true,
	"em": true, "h1": true, "h2": true, "h3": true, "h4": true,
	"hr": true, "li": true, "ol": true, "p": true, "pre": true,
	"span": true, "strong": true, "table": true, "tbody": true, "td": true,
	"th": true, "thead": true, "tr": true, "u": true, "ul": true,
}

var blockedEmailElements = map[string]bool{
	"form": true, "iframe": true, "math": true, "object": true,
	"script": true, "style": true, "svg": true, "template": true,
}

var emailHrefPattern = regexp.MustCompile(`(?is)\bhref\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>` + "`" + `]+))`)

func sanitizeEmailHTML(value string) string {
	var output strings.Builder
	blockedDepth := 0
	for cursor := 0; cursor < len(value); {
		tagStart := strings.IndexByte(value[cursor:], '<')
		if tagStart < 0 {
			if blockedDepth == 0 {
				writeSafeEmailText(&output, value[cursor:])
			}
			break
		}
		tagStart += cursor
		if blockedDepth == 0 {
			writeSafeEmailText(&output, value[cursor:tagStart])
		}
		if strings.HasPrefix(value[tagStart:], "<!--") {
			commentEnd := strings.Index(value[tagStart+4:], "-->")
			if commentEnd < 0 {
				break
			}
			cursor = tagStart + 4 + commentEnd + 3
			continue
		}
		tagEnd := emailTagEnd(value, tagStart+1)
		if tagEnd < 0 {
			if blockedDepth == 0 {
				writeSafeEmailText(&output, value[tagStart:])
			}
			break
		}
		rawTag := strings.TrimSpace(value[tagStart+1 : tagEnd])
		closing := strings.HasPrefix(rawTag, "/")
		if closing {
			rawTag = strings.TrimSpace(strings.TrimPrefix(rawTag, "/"))
		}
		name := emailTagName(rawTag)
		if blockedEmailElements[name] {
			if closing {
				if blockedDepth > 0 {
					blockedDepth--
				}
			} else {
				blockedDepth++
			}
			cursor = tagEnd + 1
			continue
		}
		if blockedDepth == 0 && safeEmailElements[name] {
			if closing {
				if name != "br" && name != "hr" {
					output.WriteString("</")
					output.WriteString(name)
					output.WriteByte('>')
				}
			} else {
				output.WriteByte('<')
				output.WriteString(name)
				if name == "a" {
					if href := emailSafeHref(rawTag); href != "" {
						output.WriteString(` href="`)
						output.WriteString(stdhtml.EscapeString(href))
						output.WriteString(`" target="_blank" rel="noopener noreferrer"`)
					}
				}
				output.WriteByte('>')
			}
		}
		cursor = tagEnd + 1
	}
	return truncateText(output.String(), 200000)
}

func writeSafeEmailText(output *strings.Builder, value string) {
	output.WriteString(stdhtml.EscapeString(stdhtml.UnescapeString(value)))
}

func emailTagEnd(value string, start int) int {
	var quote byte
	for index := start; index < len(value); index++ {
		character := value[index]
		if quote != 0 {
			if character == quote {
				quote = 0
			}
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			continue
		}
		if character == '>' {
			return index
		}
	}
	return -1
}

func emailTagName(rawTag string) string {
	end := 0
	for end < len(rawTag) {
		character := rawTag[end]
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			end++
			continue
		}
		break
	}
	return strings.ToLower(rawTag[:end])
}

func emailSafeHref(rawTag string) string {
	match := emailHrefPattern.FindStringSubmatch(rawTag)
	if len(match) == 0 {
		return ""
	}
	for _, candidate := range match[1:] {
		if candidate != "" {
			candidate = strings.TrimSpace(stdhtml.UnescapeString(candidate))
			if safeEmailURL(candidate) {
				return candidate
			}
			return ""
		}
	}
	return ""
}

func safeEmailURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}
func decodeHeader(value string) string {
	decoded, err := new(mime.WordDecoder).DecodeHeader(value)
	if err == nil {
		return decoded
	}
	return value
}
func extractCodes(value string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(raw string) {
		code := strings.ToUpper(strings.TrimSpace(raw))
		if len(code) < 4 || len(code) > 10 || seen[code] {
			return
		}
		hasDigit := false
		for _, char := range code {
			if char >= '0' && char <= '9' {
				hasDigit = true
				break
			}
		}
		if !hasDigit {
			return
		}
		seen[code] = true
		out = append(out, code)
	}
	for _, match := range numericCodePattern.FindAllStringSubmatch(value, -1) {
		add(match[1])
	}
	for _, match := range alphaCodePattern.FindAllStringSubmatch(value, -1) {
		add(match[1])
	}
	return out
}

var partnerCodePattern = regexp.MustCompile(`^[A-Za-z0-9]{16}$`)

func extractPartnerCodes(value string) []string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	out := []string{}
	seen := map[string]bool{}
	for index, line := range lines {
		normalized := strings.TrimSuffix(strings.TrimSpace(strings.ToLower(line)), ":")
		if normalized != "your partner code" {
			continue
		}
		for _, following := range lines[index+1:] {
			candidate := strings.TrimSpace(following)
			if candidate == "" {
				continue
			}
			hasLetter, hasDigit := false, false
			for _, char := range candidate {
				if char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' {
					hasLetter = true
				}
				if char >= '0' && char <= '9' {
					hasDigit = true
				}
			}
			if partnerCodePattern.MatchString(candidate) && hasLetter && hasDigit && !seen[candidate] {
				seen[candidate] = true
				out = append(out, candidate)
			}
			break
		}
		if len(out) >= 3 {
			break
		}
	}
	return out
}
func truncateText(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return value[:max]
	}
	return value
}
