package mail

import (
	"context"
	"errors"
	"fmt"
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
	mu                 sync.RWMutex
	configPath         string
	metadataPath       string
	statePath          string
	appleAccountPath   string
	createChannelPath  string
	aliasTimestampPath string
	appleAuth          *AppleAuthClient
	appleOperation     sync.Mutex
	createOperation    sync.Mutex
	aliasTimestampMu   sync.Mutex
	channelStateMu     sync.Mutex
	newClient          func(ICloudConfig) (*Client, error)
}

func NewSessionManager(configPath, stateDir string) *SessionManager {
	return &SessionManager{
		configPath:         configPath,
		metadataPath:       filepath.Join(stateDir, "hme-session.json"),
		statePath:          filepath.Join(stateDir, "session-state.json"),
		appleAccountPath:   filepath.Join(stateDir, "apple-account-state.json"),
		createChannelPath:  filepath.Join(stateDir, "create-channels.json"),
		aliasTimestampPath: filepath.Join(stateDir, "alias-timestamps.json"),
		appleAuth:          NewAppleAuthClient(),
		newClient:          func(config ICloudConfig) (*Client, error) { return NewClient(config) },
	}
}

func (manager *SessionManager) Client() (*Client, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	config, err := LoadICloudConfig(manager.configPath)
	if err != nil {
		return nil, err
	}
	return manager.newClient(config)
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

func (manager *SessionManager) Check(ctx context.Context) SessionStatus {
	manager.mu.Lock()
	status := manager.statusLocked()
	now := unixNow()
	config, err := LoadICloudConfig(manager.configPath)
	if err == nil {
		var client *Client
		client, err = manager.newClient(config)
		if err == nil {
			var hme map[string]any
			hme, err = client.Check(ctx)
			if err == nil {
				status.SessionState = SessionState{SessionValid: true, LastRefreshAt: &now, LastValidAt: &now, ExpiresHint: "apple-controlled", HME: hme}
			}
		}
	}
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
	_ = storage.WriteJSON(manager.statePath, status.SessionState, 0o600)
	manager.mu.Unlock()
	manager.appleOperation.Lock()
	manager.checkAppleAccountLocked(ctx)
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
			account.LastCheckOK = false
			account.LastStatusMessage = "登录账号已切换，请为当前账号重新登录 Apple Account"
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
	if !accountCooling && storage.ReadJSON(manager.appleAccountPath, &account) == nil && sameAppleAccount(webConfig.AppleID, account.AppleID) && len(account.Cookies) > 0 {
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
	client, err := manager.Client()
	if err != nil {
		if accountErr != nil {
			return nil, accountErr
		}
		return nil, err
	}
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
	if storage.ReadJSON(manager.appleAccountPath, &account) == nil && len(account.Cookies) > 0 {
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
	client, err := manager.Client()
	if err != nil {
		if accountErr != nil {
			return nil, AppleChannelAccount, accountErr
		}
		return nil, AppleChannelICloudWeb, err
	}
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
		if client, err := manager.Client(); err == nil {
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
	if storage.ReadJSON(manager.appleAccountPath, &account) == nil && len(account.Cookies) > 0 {
		result, updated, err := manager.appleAuth.UpdateWithAppleAccount(ctx, account, id, label, note)
		_ = storage.WriteJSON(manager.appleAccountPath, updated, 0o600)
		return result, err
	}
	client, err := manager.Client()
	if err != nil {
		return nil, err
	}
	return client.UpdateAlias(ctx, id, label, note)
}

func (manager *SessionManager) SetAliasActive(ctx context.Context, id string, active bool) (map[string]any, error) {
	manager.appleOperation.Lock()
	defer manager.appleOperation.Unlock()
	var account AppleAccountState
	if storage.ReadJSON(manager.appleAccountPath, &account) == nil && len(account.Cookies) > 0 {
		result, updated, err := manager.appleAuth.SetActiveWithAppleAccount(ctx, account, id, active)
		_ = storage.WriteJSON(manager.appleAccountPath, updated, 0o600)
		return result, err
	}
	client, err := manager.Client()
	if err != nil {
		return nil, err
	}
	return client.SetAliasActive(ctx, id, active)
}

func (manager *SessionManager) DeleteAlias(ctx context.Context, id string) (map[string]any, error) {
	manager.appleOperation.Lock()
	defer manager.appleOperation.Unlock()
	var account AppleAccountState
	if storage.ReadJSON(manager.appleAccountPath, &account) == nil && len(account.Cookies) > 0 {
		result, updated, err := manager.appleAuth.DeleteWithAppleAccount(ctx, account, id)
		_ = storage.WriteJSON(manager.appleAccountPath, updated, 0o600)
		return result, err
	}
	client, err := manager.Client()
	if err != nil {
		return nil, err
	}
	return client.DeleteAlias(ctx, id)
}

func appleAccountAllowsWebFallback(err error) bool {
	var protocol *AppleProtocolError
	return errors.As(err, &protocol) && (protocol.Code == "APPLE_ACCOUNT_SESSION_MISSING" || protocol.Code == "APPLE_ACCOUNT_EXPIRED")
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
		result.AppleAccount.Healthy = accountMatches && account.LastCheckOK && (account.ManageExpiresAt.IsZero() || time.Now().Before(account.ManageExpiresAt))
		result.AppleAccount.AppleID = account.AppleID
		result.AppleAccount.LastCheckedAt = unixPointer(account.LastCheckedAt)
		result.AppleAccount.ExpiresAt = unixPointer(account.ManageExpiresAt)
		result.AppleAccount.Message = account.LastStatusMessage
	}
	applyCreateRuntime(&result.AppleAccount, manager.createChannelRuntime(AppleChannelAccount))
	if result.AppleAccount.Configured && sameAppleAccount(result.ICloudWeb.AppleID, result.AppleAccount.AppleID) && result.AppleAccount.CooldownRemaining == 0 {
		result.CreateChannel = AppleChannelAccount
	}
	return result
}

func (manager *SessionManager) checkAppleAccountLocked(ctx context.Context) {
	var account AppleAccountState
	if storage.ReadJSON(manager.appleAccountPath, &account) != nil || len(account.Cookies) == 0 {
		return
	}
	config, _ := LoadICloudConfig(manager.configPath)
	if !sameAppleAccount(config.AppleID, account.AppleID) {
		account.LastCheckedAt = time.Now()
		account.LastCheckOK = false
		account.LastStatusMessage = "当前 iCloud Web 已切换账号，请重新登录 Apple Account"
		_ = storage.WriteJSON(manager.appleAccountPath, account, 0o600)
		return
	}
	if err := manager.appleAuth.refreshAppleAccountState(ctx, &account); err != nil {
		account.LastCheckedAt, account.LastCheckOK, account.LastStatusMessage = time.Now(), false, safeErrorText(err)
	}
	_ = storage.WriteJSON(manager.appleAccountPath, account, 0o600)
}

func unixNow() float64 { return float64(time.Now().UnixNano()) / float64(time.Second) }
