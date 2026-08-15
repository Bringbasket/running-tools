package mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

type SessionState struct {
	SessionValid  bool           `json:"sessionValid"`
	LastRefreshAt *float64       `json:"lastRefreshAt"`
	LastValidAt   *float64       `json:"lastValidAt"`
	ExpiresHint   string         `json:"expiresHint"`
	LastError     *string        `json:"lastError"`
	NeedsReauth   bool           `json:"needsReauth"`
	HME           map[string]any `json:"hme,omitempty"`
}

type SessionStatus struct {
	MetadataDetected bool             `json:"metadataDetected"`
	Metadata         *Metadata        `json:"metadata"`
	PersistedSession bool             `json:"persistedSession"`
	ConfigPath       string           `json:"configPath"`
	ConfigUpdatedAt  *float64         `json:"configUpdatedAt"`
	LastSavedAt      *float64         `json:"lastSavedAt"`
	AppleLogin       AppleLoginStatus `json:"appleLogin"`
	SessionState
}

type SessionManager struct {
	mu                  sync.RWMutex
	configPath          string
	metadataPath        string
	statePath           string
	appleAccountPath    string
	createChannelPath   string
	aliasTimestampPath  string
	appleAuth           *AppleAuthClient
	appleOperation      sync.Mutex
	appleLoginOperation sync.Mutex
	checkOperation      sync.Mutex
	webOperation        sync.Mutex
	createOperation     sync.Mutex
	aliasTimestampMu    sync.Mutex
	channelStateMu      sync.Mutex
	proxyURL            string
	proxyDial           proxyDialContext
	proxyRevision       uint64
	proxyObservers      []func()
	httpClient          *http.Client
	newClient           func(ICloudConfig) (*Client, error)
}

func NewSessionManager(configPath, stateDir string) *SessionManager {
	httpClient, _ := httpClientForProxy("")
	proxyDial, _ := tcpDialContextForProxy("", 20*time.Second)
	manager := &SessionManager{
		configPath:         configPath,
		metadataPath:       filepath.Join(stateDir, "hme-session.json"),
		statePath:          filepath.Join(stateDir, "session-state.json"),
		appleAccountPath:   filepath.Join(stateDir, "apple-account-state.json"),
		createChannelPath:  filepath.Join(stateDir, "create-channels.json"),
		aliasTimestampPath: filepath.Join(stateDir, "alias-timestamps.json"),
		appleAuth:          NewAppleAuthClient(),
		httpClient:         httpClient,
		proxyDial:          proxyDial,
		proxyRevision:      1,
	}
	manager.newClient = func(config ICloudConfig) (*Client, error) {
		manager.mu.RLock()
		client := manager.httpClient
		manager.mu.RUnlock()
		return NewClient(config, WithHTTPClient(client))
	}
	manager.appleAuth.SetHTTPClient(httpClient)
	return manager
}

func (manager *SessionManager) Client() (*Client, error) {
	manager.mu.RLock()
	config, err := LoadICloudConfig(manager.configPath)
	newClient := manager.newClient
	manager.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return newClient(config)
}

func (manager *SessionManager) SetProxy(raw string) error {
	proxyURL, err := normalizeProxyURL(raw)
	if err != nil {
		return err
	}
	httpClient, err := httpClientForProxy(proxyURL)
	if err != nil {
		return err
	}
	proxyDial, err := tcpDialContextForProxy(proxyURL, 20*time.Second)
	if err != nil {
		return err
	}
	manager.appleLoginOperation.Lock()
	manager.appleOperation.Lock()
	manager.webOperation.Lock()
	manager.mu.Lock()
	previous := manager.httpClient
	changed := manager.proxyURL != proxyURL
	manager.proxyURL = proxyURL
	manager.httpClient = httpClient
	manager.proxyDial = proxyDial
	if manager.proxyRevision == 0 {
		manager.proxyRevision = 1
	}
	var observers []func()
	if changed {
		manager.proxyRevision++
		observers = make([]func(), 0, len(manager.proxyObservers))
		for _, observer := range manager.proxyObservers {
			observers = append(observers, observer)
		}
	}
	manager.mu.Unlock()
	manager.appleAuth.SetHTTPClient(httpClient)
	manager.webOperation.Unlock()
	manager.appleOperation.Unlock()
	manager.appleLoginOperation.Unlock()
	if previous != nil {
		previous.CloseIdleConnections()
	}
	for _, observer := range observers {
		observer()
	}
	return nil
}

func (manager *SessionManager) imapProxySnapshot() (proxyDialContext, uint64, error) {
	if manager == nil {
		dial, err := tcpDialContextForProxy("", 20*time.Second)
		return dial, 0, err
	}
	manager.mu.RLock()
	dial, revision, configured := manager.proxyDial, manager.proxyRevision, manager.proxyURL != ""
	manager.mu.RUnlock()
	if dial == nil {
		if configured {
			return nil, revision, errors.New("已配置的代理拨号器不可用")
		}
		var err error
		dial, err = tcpDialContextForProxy("", 20*time.Second)
		if err != nil {
			return nil, revision, err
		}
	}
	return dial, revision, nil
}

func (manager *SessionManager) imapProxyRevision() uint64 {
	if manager == nil {
		return 0
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.proxyRevision
}

func (manager *SessionManager) observeProxyChanges(observer func()) {
	if manager == nil || observer == nil {
		return
	}
	manager.mu.Lock()
	manager.proxyObservers = append(manager.proxyObservers, observer)
	manager.mu.Unlock()
}

func (manager *SessionManager) persistClientConfig(client *Client) {
	config, changed := client.ConfigUpdate()
	if !changed {
		return
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current, err := LoadICloudConfig(manager.configPath)
	if err == nil && current.Cookie == config.Cookie {
		return
	}
	if err := storage.WriteJSON(manager.configPath, config, 0o600); err != nil {
		slog.Warn("iCloud Web Cookie 续存失败", "error", safeErrorText(err))
	}
}

func (manager *SessionManager) Status() SessionStatus {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.statusLocked()
}

// appleAccountRefreshDueAt returns the earliest safe refresh time for the
// short-lived Apple Account management session. A missing or incomplete state
// is treated as due now so the worker can repair it on its next pass.
func (manager *SessionManager) appleAccountRefreshDueAt(now time.Time) (time.Time, bool) {
	var account AppleAccountState
	if storage.ReadJSON(manager.appleAccountPath, &account) != nil || len(account.Cookies) == 0 {
		return time.Time{}, false
	}
	if account.needsRefresh(now) {
		return now, true
	}
	return account.refreshDueAt(now), true
}

func (manager *SessionManager) appleAccountKeepAliveSchedule(now time.Time) (time.Time, time.Time, bool) {
	var account AppleAccountState
	if storage.ReadJSON(manager.appleAccountPath, &account) != nil || len(account.Cookies) == 0 || account.requiresReauth() {
		return time.Time{}, time.Time{}, false
	}
	anchor := account.LastCheckedAt
	if account.LastAttemptAt.After(anchor) {
		anchor = account.LastAttemptAt
	}
	deadline := account.refreshDueAt(now)
	// A transient failure must not turn an expired deadline into a tight
	// ten-second retry loop. Keep the next attempt bounded while preserving
	// the last successful state for normal requests.
	if account.LastAttemptAt.After(account.LastCheckedAt) {
		minimumNext := account.LastAttemptAt.Add(appleKeepAliveInterval)
		if deadline.Before(minimumNext) {
			deadline = minimumNext
		}
	}
	return anchor, deadline, true
}

func (manager *SessionManager) KeepAliveAppleAccount(ctx context.Context) error {
	manager.appleOperation.Lock()
	defer manager.appleOperation.Unlock()
	return manager.checkAppleAccountLocked(ctx)
}

func (manager *SessionManager) Check(ctx context.Context) SessionStatus {
	// The route and the background worker can request a check at the same time.
	// Coalesce their work at the manager boundary so a manual click cannot race
	// a scheduled check and rotate the same iCloud Web/Apple state twice.
	manager.checkOperation.Lock()
	defer manager.checkOperation.Unlock()
	manager.mu.RLock()
	status := manager.statusLocked()
	manager.mu.RUnlock()
	now := unixNow()
	manager.webOperation.Lock()
	client, err := manager.Client()
	if err == nil {
		var hme map[string]any
		hme, err = client.Check(ctx)
		manager.persistClientConfig(client)
		if err == nil {
			status.SessionState = SessionState{SessionValid: true, LastRefreshAt: &now, LastValidAt: &now, ExpiresHint: "apple-controlled", HME: hme}
		}
	}
	manager.webOperation.Unlock()
	if err != nil {
		message := safeErrorText(err)
		status.SessionValid = false
		status.LastRefreshAt = &now
		status.LastError = &message
		status.NeedsReauth = isSessionExpired(err)
		if errors.Is(err, ErrSessionMissing) {
			status.NeedsReauth = false
		}
	}
	manager.mu.Lock()
	_ = storage.WriteJSON(manager.statePath, status.SessionState, 0o600)
	manager.mu.Unlock()
	manager.appleOperation.Lock()
	// A normal Session check should not refresh the short-lived Apple
	// management token on every request. The dedicated worker (or an explicit
	// keep-alive call) forces a refresh when its TTL says it is due.
	_ = manager.checkAppleAccountIfDueLocked(ctx)
	manager.appleOperation.Unlock()
	if !status.SessionValid {
		if aliases, source, listErr := manager.listAliases(ctx); listErr == nil && source == AppleChannelAccount {
			status.HME = appleAliasSummary(aliases)
		}
	}
	manager.mu.RLock()
	status.AppleLogin = manager.appleLoginStatusLocked()
	manager.mu.RUnlock()
	return status
}

func appleAliasSummary(aliases []map[string]any) map[string]any {
	forward := ""
	for _, alias := range aliases {
		forward = firstNonEmpty(forward, stringValue(alias["forwardToEmail"]))
	}
	return map[string]any{"aliasCount": len(aliases), "selectedForwardTo": forward}
}

func (manager *SessionManager) StartAppleLogin(ctx context.Context, input AppleLoginStartInput, expectedDSID string) (AppleLoginStartResult, error) {
	manager.appleLoginOperation.Lock()
	defer manager.appleLoginOperation.Unlock()
	result, err := manager.appleAuth.Start(ctx, input)
	if err != nil {
		return AppleLoginStartResult{}, err
	}
	if !result.Needs2FA {
		if err := ensureAppleLoginAccount(result, expectedDSID); err != nil {
			return AppleLoginStartResult{}, err
		}
		if err := manager.persistAppleLoginResult(result); err != nil {
			return AppleLoginStartResult{}, err
		}
	}
	return result, nil
}

func (manager *SessionManager) VerifyAppleLogin(ctx context.Context, pendingID, code, expectedDSID string) (AppleLoginStartResult, error) {
	manager.appleLoginOperation.Lock()
	defer manager.appleLoginOperation.Unlock()
	result, err := manager.appleAuth.Verify(ctx, pendingID, code)
	if err != nil {
		return AppleLoginStartResult{}, err
	}
	if err := ensureAppleLoginAccount(result, expectedDSID); err != nil {
		return AppleLoginStartResult{}, err
	}
	if err := manager.persistAppleLoginResult(result); err != nil {
		return AppleLoginStartResult{}, err
	}
	return result, nil
}

func (manager *SessionManager) persistAppleLoginResult(result AppleLoginStartResult) error {
	if result.webConfig != nil {
		manager.mu.Lock()
		if err := storage.WriteJSON(manager.configPath, *result.webConfig, 0o600); err != nil {
			manager.mu.Unlock()
			return err
		}
		if err := storage.WriteJSON(manager.metadataPath, result.webConfig.Metadata(), 0o600); err != nil {
			manager.mu.Unlock()
			return err
		}
		state := SessionState{ExpiresHint: "apple-controlled"}
		if err := storage.WriteJSON(manager.statePath, state, 0o600); err != nil {
			manager.mu.Unlock()
			return err
		}
		manager.mu.Unlock()
		manager.appleOperation.Lock()
		var account AppleAccountState
		if storage.ReadJSON(manager.appleAccountPath, &account) == nil && !sameAppleAccount(result.webConfig.AppleID, account.AppleID) {
			now := time.Now()
			account.LastCheckedAt, account.LastAttemptAt = now, now
			account.LastCheckOK = false
			account.LastStatusMessage = "登录账号已切换，请为当前账号重新登录 Apple Account"
			account.HealthState = AppleAccountStateReauthRequired
			_ = storage.WriteJSON(manager.appleAccountPath, account, 0o600)
		}
		manager.appleOperation.Unlock()
	}
	if result.accountState != nil {
		manager.mu.RLock()
		config, configErr := LoadICloudConfig(manager.configPath)
		manager.mu.RUnlock()
		if configErr == nil && !sameAppleAccount(config.AppleID, result.accountState.AppleID) {
			return appleProtocolError("APPLE_ACCOUNT_MISMATCH", "Apple Account 与当前 iCloud Web 不是同一账号，请先切换 iCloud Web 登录", false)
		}
		manager.appleOperation.Lock()
		defer manager.appleOperation.Unlock()
		if err := storage.WriteJSON(manager.appleAccountPath, *result.accountState, 0o600); err != nil {
			return err
		}
		manager.recordCreateSuccess(AppleChannelAccount)
	}
	return nil
}

func sameAppleAccount(left, right string) bool {
	left, right = strings.TrimSpace(left), strings.TrimSpace(right)
	return left == "" || right == "" || strings.EqualFold(left, right)
}

func ensureAppleLoginAccount(result AppleLoginStartResult, expectedDSID string) error {
	expectedDSID = strings.TrimSpace(expectedDSID)
	if expectedDSID != "" && result.webConfig != nil && result.webConfig.DSID != expectedDSID {
		return appleProtocolError("QUEUE_ACCOUNT_MISMATCH", "当前批量队列绑定了另一个 iCloud 账号，请完成或取消队列后再切换登录", false)
	}
	return nil
}

// CreateAlias prefers the Apple Account channel when its short-lived state is
// healthy. It falls back only before the confirmation request could have
// created an address, preventing duplicate aliases on ambiguous responses.
func (manager *SessionManager) CreateAlias(ctx context.Context, label, note string) (map[string]any, error) {
	manager.createOperation.Lock()
	defer manager.createOperation.Unlock()
	manager.mu.RLock()
	webConfig, _ := LoadICloudConfig(manager.configPath)
	manager.mu.RUnlock()
	attempted := make([]string, 0, 2)
	manager.appleOperation.Lock()
	var account AppleAccountState
	var accountErr error
	_, accountCooling := manager.channelCoolingDown(AppleChannelAccount)
	if !accountCooling && storage.ReadJSON(manager.appleAccountPath, &account) == nil && account.availableForUse() && sameAppleAccount(webConfig.AppleID, account.AppleID) {
		attempted = append(attempted, AppleChannelAccount)
		alias, updated, err := manager.appleAuth.CreateWithAppleAccount(ctx, account, label, note)
		_ = storage.WriteJSON(manager.appleAccountPath, updated, 0o600)
		if err == nil {
			manager.recordCreateSuccess(AppleChannelAccount)
			manager.appleOperation.Unlock()
			decorateCreateResult(alias, AppleChannelAccount, attempted)
			manager.rememberAliasTimestamp(alias)
			return alias, nil
		}
		manager.recordCreateFailure(AppleChannelAccount, err)
		accountErr = err
		var protocol *AppleProtocolError
		if errors.As(err, &protocol) && protocol.MayHaveCreated {
			manager.appleOperation.Unlock()
			return nil, err
		}
	}
	manager.appleOperation.Unlock()
	_, webCooling := manager.channelCoolingDown(AppleChannelICloudWeb)
	if webCooling {
		if accountErr != nil {
			return nil, accountErr
		}
		return nil, appleProtocolError("ICLOUD_WEB_COOLDOWN", "iCloud Web 创建通道仍在限额冷却中，请稍后再试", true)
	}
	manager.webOperation.Lock()
	defer manager.webOperation.Unlock()
	client, err := manager.Client()
	if err != nil {
		if accountErr != nil {
			return nil, accountErr
		}
		return nil, err
	}
	defer manager.persistClientConfig(client)
	attempted = append(attempted, AppleChannelICloudWeb)
	alias, webErr := client.CreateAlias(ctx, label, note)
	if webErr != nil {
		manager.recordCreateFailure(AppleChannelICloudWeb, webErr)
		return nil, allCreateChannelsFailed(accountErr, webErr)
	}
	manager.recordCreateSuccess(AppleChannelICloudWeb)
	decorateCreateResult(alias, AppleChannelICloudWeb, attempted)
	manager.rememberAliasTimestamp(alias)
	return alias, nil
}

func (manager *SessionManager) ListAliases(ctx context.Context) ([]map[string]any, error) {
	aliases, _, err := manager.listAliases(ctx)
	return aliases, err
}

func (manager *SessionManager) listAliases(ctx context.Context) ([]map[string]any, string, error) {
	manager.appleOperation.Lock()
	var account AppleAccountState
	var accountErr error
	if storage.ReadJSON(manager.appleAccountPath, &account) == nil && account.availableForUse() {
		aliases, updated, err := manager.appleAuth.ListWithAppleAccount(ctx, account)
		_ = storage.WriteJSON(manager.appleAccountPath, updated, 0o600)
		manager.appleOperation.Unlock()
		if err == nil {
			manager.enrichAliasTimestamps(ctx, aliases)
			return aliases, AppleChannelAccount, nil
		}
		if !appleAccountAllowsWebFallback(err) {
			return nil, AppleChannelAccount, err
		}
		accountErr = err
	} else {
		manager.appleOperation.Unlock()
	}
	manager.webOperation.Lock()
	defer manager.webOperation.Unlock()
	client, err := manager.Client()
	if err != nil {
		if accountErr != nil {
			return nil, AppleChannelAccount, accountErr
		}
		return nil, AppleChannelICloudWeb, err
	}
	defer manager.persistClientConfig(client)
	aliases, err := client.ListAliases(ctx)
	return aliases, AppleChannelICloudWeb, err
}

func (manager *SessionManager) enrichAliasTimestamps(ctx context.Context, aliases []map[string]any) {
	if len(aliases) == 0 {
		return
	}
	manager.aliasTimestampMu.Lock()
	defer manager.aliasTimestampMu.Unlock()
	cache := map[string]float64{}
	_ = storage.ReadJSON(manager.aliasTimestampPath, &cache)
	missing := false
	for _, alias := range aliases {
		normalizeAliasTimestamp(alias)
		if timestamp, ok := aliasTimestamp(alias["createTimestamp"]); ok {
			cacheAliasTimestamp(cache, alias, timestamp)
		} else if cached, ok := cachedAliasTimestamp(cache, alias); ok {
			alias["createTimestamp"] = cached
		} else {
			missing = true
		}
	}
	if missing {
		manager.webOperation.Lock()
		defer manager.webOperation.Unlock()
		if client, err := manager.Client(); err == nil {
			defer manager.persistClientConfig(client)
			if webAliases, err := client.ListAliases(ctx); err == nil {
				for _, alias := range webAliases {
					normalizeAliasTimestamp(alias)
					timestamp, ok := aliasTimestamp(alias["createTimestamp"])
					if !ok {
						continue
					}
					cacheAliasTimestamp(cache, alias, timestamp)
				}
				for _, alias := range aliases {
					if _, ok := aliasTimestamp(alias["createTimestamp"]); ok {
						continue
					}
					if cached, ok := cachedAliasTimestamp(cache, alias); ok {
						alias["createTimestamp"] = cached
					}
				}
			}
		}
	}
	if len(cache) > 0 {
		_ = storage.WriteJSON(manager.aliasTimestampPath, cache, 0o600)
	}
}

func (manager *SessionManager) rememberAliasTimestamp(alias map[string]any) {
	normalizeAliasTimestamp(alias)
	timestamp, ok := aliasTimestamp(alias["createTimestamp"])
	if !ok || len(aliasTimestampKeys(alias)) == 0 {
		return
	}
	manager.aliasTimestampMu.Lock()
	defer manager.aliasTimestampMu.Unlock()
	cache := map[string]float64{}
	_ = storage.ReadJSON(manager.aliasTimestampPath, &cache)
	cacheAliasTimestamp(cache, alias, timestamp)
	_ = storage.WriteJSON(manager.aliasTimestampPath, cache, 0o600)
}

func aliasTimestampKeys(alias map[string]any) []string {
	keys := make([]string, 0, 2)
	if hme := strings.ToLower(aliasTimestampString(alias["hme"])); hme != "" {
		keys = append(keys, "hme:"+hme)
	}
	if id := aliasTimestampString(alias["anonymousId"]); id != "" {
		keys = append(keys, "id:"+id)
	}
	return keys
}

func aliasTimestampString(value any) string {
	if value == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func cacheAliasTimestamp(cache map[string]float64, alias map[string]any, timestamp float64) {
	for _, key := range aliasTimestampKeys(alias) {
		cache[key] = timestamp
	}
}

func cachedAliasTimestamp(cache map[string]float64, alias map[string]any) (float64, bool) {
	for _, key := range aliasTimestampKeys(alias) {
		if timestamp, ok := cache[key]; ok {
			return timestamp, true
		}
	}
	return 0, false
}

func (manager *SessionManager) UpdateAlias(ctx context.Context, id, label, note string) (map[string]any, error) {
	manager.appleOperation.Lock()
	defer manager.appleOperation.Unlock()
	var account AppleAccountState
	if storage.ReadJSON(manager.appleAccountPath, &account) == nil && account.availableForUse() {
		result, updated, err := manager.appleAuth.UpdateWithAppleAccount(ctx, account, id, label, note)
		_ = storage.WriteJSON(manager.appleAccountPath, updated, 0o600)
		return result, err
	}
	manager.webOperation.Lock()
	defer manager.webOperation.Unlock()
	client, err := manager.Client()
	if err != nil {
		return nil, err
	}
	defer manager.persistClientConfig(client)
	return client.UpdateAlias(ctx, id, label, note)
}

func (manager *SessionManager) SetAliasActive(ctx context.Context, id string, active bool) (map[string]any, error) {
	manager.appleOperation.Lock()
	defer manager.appleOperation.Unlock()
	var account AppleAccountState
	if storage.ReadJSON(manager.appleAccountPath, &account) == nil && account.availableForUse() {
		result, updated, err := manager.appleAuth.SetActiveWithAppleAccount(ctx, account, id, active)
		_ = storage.WriteJSON(manager.appleAccountPath, updated, 0o600)
		return result, err
	}
	manager.webOperation.Lock()
	defer manager.webOperation.Unlock()
	client, err := manager.Client()
	if err != nil {
		return nil, err
	}
	defer manager.persistClientConfig(client)
	return client.SetAliasActive(ctx, id, active)
}

func (manager *SessionManager) DeleteAlias(ctx context.Context, id string) (map[string]any, error) {
	manager.appleOperation.Lock()
	defer manager.appleOperation.Unlock()
	var account AppleAccountState
	if storage.ReadJSON(manager.appleAccountPath, &account) == nil && account.availableForUse() {
		result, updated, err := manager.appleAuth.DeleteWithAppleAccount(ctx, account, id)
		_ = storage.WriteJSON(manager.appleAccountPath, updated, 0o600)
		return result, err
	}
	manager.webOperation.Lock()
	defer manager.webOperation.Unlock()
	client, err := manager.Client()
	if err != nil {
		return nil, err
	}
	defer manager.persistClientConfig(client)
	return client.DeleteAlias(ctx, id)
}

func appleAccountAllowsWebFallback(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var protocol *AppleProtocolError
	if errors.As(err, &protocol) {
		return protocol.Code == "APPLE_ACCOUNT_SESSION_MISSING" || protocol.Code == "APPLE_ACCOUNT_EXPIRED" || protocol.Retryable
	}
	return isAppleTransientNetworkError(err)
}

func decorateCreateResult(alias map[string]any, usedChannel string, attempted []string) {
	if alias == nil {
		return
	}
	alias["usedChannel"] = usedChannel
	alias["attemptedChannels"] = append([]string(nil), attempted...)
	alias["fallbackUsed"] = usedChannel == AppleChannelICloudWeb && len(attempted) > 1
	alias["nextRetryAt"] = nil
	if _, ok := alias["detailConfirmed"]; !ok {
		alias["detailConfirmed"] = usedChannel == AppleChannelICloudWeb
	}
}

func (manager *SessionManager) PendingAppleLoginChannel(pendingID string) string {
	return manager.appleAuth.pendingChannel(pendingID)
}

func (manager *SessionManager) Import(text, region string) (map[string]any, error) {
	config, err := ParseImportText(text, region)
	if err != nil {
		return nil, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := storage.WriteJSON(manager.configPath, config, 0o600); err != nil {
		return nil, err
	}
	if err := storage.WriteJSON(manager.metadataPath, config.Metadata(), 0o600); err != nil {
		return nil, err
	}
	state := SessionState{ExpiresHint: "apple-controlled", HME: nil}
	if err := storage.WriteJSON(manager.statePath, state, 0o600); err != nil {
		return nil, err
	}
	actualRegion := RegionInternational
	if filepath.Ext(config.Host) == ".cn" {
		actualRegion = RegionChina
	}
	return map[string]any{"imported": true, "icloudRegion": actualRegion, "host": config.Host}, nil
}

func (manager *SessionManager) statusLocked() SessionStatus {
	status := SessionStatus{ConfigPath: manager.configPath, SessionState: SessionState{ExpiresHint: "apple-controlled"}}
	metadata := Metadata{}
	if storage.ReadJSON(manager.metadataPath, &metadata) != nil {
		if config, err := LoadICloudConfig(manager.configPath); err == nil {
			metadata = config.Metadata()
		}
	}
	if metadata.Host != "" {
		status.MetadataDetected = true
		status.Metadata = &metadata
	}
	if _, err := LoadICloudConfig(manager.configPath); err == nil {
		status.PersistedSession = true
		if info, statErr := os.Stat(manager.configPath); statErr == nil {
			stamp := float64(info.ModTime().UnixNano()) / float64(time.Second)
			status.ConfigUpdatedAt, status.LastSavedAt = &stamp, &stamp
		}
	}
	stored := SessionState{}
	if storage.ReadJSON(manager.statePath, &stored) == nil {
		if stored.ExpiresHint == "" {
			stored.ExpiresHint = "apple-controlled"
		}
		status.SessionState = stored
	}
	status.AppleLogin = manager.appleLoginStatusLocked()
	return status
}

func (manager *SessionManager) appleLoginStatusLocked() AppleLoginStatus {
	result := AppleLoginStatus{CreateChannel: AppleChannelICloudWeb}
	if config, err := LoadICloudConfig(manager.configPath); err == nil {
		result.ICloudWeb.Configured = true
		result.ICloudWeb.AppleID = config.AppleID
		var state SessionState
		if storage.ReadJSON(manager.statePath, &state) == nil {
			result.ICloudWeb.Healthy = state.SessionValid && !state.NeedsReauth
			result.ICloudWeb.LastCheckedAt = state.LastRefreshAt
			if state.LastError != nil {
				result.ICloudWeb.Message = *state.LastError
			} else if result.ICloudWeb.Healthy {
				result.ICloudWeb.Message = "iCloud Web 登录态正常"
			}
		}
	}
	applyCreateRuntime(&result.ICloudWeb, manager.createChannelRuntime(AppleChannelICloudWeb))
	var account AppleAccountState
	if storage.ReadJSON(manager.appleAccountPath, &account) == nil && len(account.Cookies) > 0 {
		var config ICloudConfig
		_ = storage.ReadJSON(manager.configPath, &config)
		accountMatches := sameAppleAccount(config.AppleID, account.AppleID)
		result.AppleAccount.Configured = true
		now := time.Now()
		result.AppleAccount.Healthy = accountMatches && account.healthyForUse(now)
		result.AppleAccount.State = account.HealthState
		if !accountMatches || account.requiresReauth() {
			result.AppleAccount.State = AppleAccountStateReauthRequired
		} else {
			switch result.AppleAccount.State {
			case AppleAccountStateHealthy:
				if !result.AppleAccount.Healthy {
					result.AppleAccount.State = AppleAccountStateDegraded
				}
			case AppleAccountStateDegraded:
				// Preserve an explicitly degraded state until the worker repairs it.
			default:
				// Older state files did not persist HealthState. Derive a
				// compatible value instead of exposing healthy=true with a
				// contradictory degraded state.
				if result.AppleAccount.Healthy {
					result.AppleAccount.State = AppleAccountStateHealthy
				} else {
					result.AppleAccount.State = AppleAccountStateDegraded
				}
			}
		}
		result.AppleAccount.RequiresReauth = !accountMatches || account.requiresReauth()
		result.AppleAccount.AppleID = account.AppleID
		result.AppleAccount.LastCheckedAt = unixPointer(account.LastCheckedAt)
		result.AppleAccount.LastAttemptAt = unixPointer(account.LastAttemptAt)
		result.AppleAccount.ExpiresAt = unixPointer(account.ManageExpiresAt)
		result.AppleAccount.Message = account.LastStatusMessage
	}
	applyCreateRuntime(&result.AppleAccount, manager.createChannelRuntime(AppleChannelAccount))
	if result.AppleAccount.Healthy && sameAppleAccount(result.ICloudWeb.AppleID, result.AppleAccount.AppleID) && result.AppleAccount.CooldownRemaining == 0 {
		result.CreateChannel = AppleChannelAccount
	}
	return result
}

func (manager *SessionManager) checkAppleAccountLocked(ctx context.Context) error {
	return manager.checkAppleAccountLockedWithForce(ctx, true)
}

func (manager *SessionManager) checkAppleAccountIfDueLocked(ctx context.Context) error {
	return manager.checkAppleAccountLockedWithForce(ctx, false)
}

func (manager *SessionManager) checkAppleAccountLockedWithForce(ctx context.Context, force bool) error {
	var account AppleAccountState
	if storage.ReadJSON(manager.appleAccountPath, &account) != nil || len(account.Cookies) == 0 {
		return nil
	}
	if account.requiresReauth() {
		return appleProtocolError("APPLE_ACCOUNT_EXPIRED", account.LastStatusMessage, false)
	}
	if !force && account.healthyForUse(time.Now()) && !account.needsRefresh(time.Now()) {
		return nil
	}
	config, _ := LoadICloudConfig(manager.configPath)
	if !sameAppleAccount(config.AppleID, account.AppleID) {
		now := time.Now()
		account.LastCheckedAt = now
		account.LastAttemptAt = now
		account.LastCheckOK = false
		account.LastStatusMessage = "当前 iCloud Web 已切换账号，请重新登录 Apple Account"
		account.HealthState = AppleAccountStateReauthRequired
		if err := storage.WriteJSON(manager.appleAccountPath, account, 0o600); err != nil {
			return fmt.Errorf("保存 Apple Account 账号状态: %w", err)
		}
		return appleProtocolError("APPLE_ACCOUNT_MISMATCH", account.LastStatusMessage, false)
	}
	previous := account
	if err := manager.appleAuth.refreshAppleAccountState(ctx, &account); err != nil {
		now := time.Now()
		if appleAccountRequiresReauth(err) {
			previous.LastCheckedAt, previous.LastAttemptAt = now, now
			previous.LastCheckOK = false
			previous.HealthState = AppleAccountStateReauthRequired
			previous.LastStatusMessage = safeErrorText(err)
		} else {
			previous.LastAttemptAt = now
			previous.HealthState = AppleAccountStateDegraded
			previous.LastStatusMessage = "Apple Account 保活临时失败，将自动重试：" + safeErrorText(err)
		}
		if writeErr := storage.WriteJSON(manager.appleAccountPath, previous, 0o600); writeErr != nil {
			return fmt.Errorf("保存 Apple Account 保活状态: %w", writeErr)
		}
		return err
	}
	account.HealthState = AppleAccountStateHealthy
	account.LastAttemptAt = account.LastCheckedAt
	if err := storage.WriteJSON(manager.appleAccountPath, account, 0o600); err != nil {
		return fmt.Errorf("保存 Apple Account 保活结果: %w", err)
	}
	return nil
}

func unixNow() float64 { return float64(time.Now().UnixNano()) / float64(time.Second) }
