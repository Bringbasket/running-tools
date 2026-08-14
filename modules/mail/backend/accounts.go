package mail

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/activitylog"
	"github.com/Bringbasket/running-tools/internal/platform/httpx"
)

const defaultMailAccountID = "default"

type MailAccount struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	AppleID   string    `json:"appleId,omitempty"`
	DSID      string    `json:"dsid,omitempty"`
	ProxyURL  string    `json:"-"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type accountChannelHealth struct {
	Configured    bool     `json:"configured"`
	Healthy       bool     `json:"healthy"`
	LastCheckedAt *float64 `json:"lastCheckedAt,omitempty"`
	ExpiresAt     *float64 `json:"expiresAt,omitempty"`
	Message       string   `json:"message,omitempty"`
}

type accountMailboxHealth struct {
	Configured bool     `json:"configured"`
	Enabled    bool     `json:"enabled"`
	LastSyncAt *float64 `json:"lastSyncAt,omitempty"`
	LastError  string   `json:"lastError,omitempty"`
}

type MailAccountSummary struct {
	ID                 string               `json:"id"`
	Name               string               `json:"name"`
	AppleID            string               `json:"appleId,omitempty"`
	DSID               string               `json:"dsid,omitempty"`
	Enabled            bool                 `json:"enabled"`
	CreatedAt          time.Time            `json:"createdAt"`
	UpdatedAt          time.Time            `json:"updatedAt"`
	HasProxy           bool                 `json:"hasProxy"`
	Status             string               `json:"status"`
	StatusMessage      string               `json:"statusMessage"`
	AliasCount         int                  `json:"aliasCount"`
	ICloudWeb          accountChannelHealth `json:"icloudWeb"`
	AppleAccount       accountChannelHealth `json:"appleAccount"`
	Mailbox            accountMailboxHealth `json:"mailbox"`
	AutoRefreshEnabled bool                 `json:"autoRefreshEnabled"`
	AutoCreateEnabled  bool                 `json:"autoCreateEnabled"`
	AutoCreateRunning  bool                 `json:"autoCreateRunning"`
	AliasQueueStatus   string               `json:"aliasQueueStatus"`
}

type accountRuntime struct {
	account    MailAccount
	session    *SessionManager
	refresh    *AutoRefresh
	creation   *CreateScheduler
	queue      *AliasQueue
	shares     *ShareLinkStore
	mailbox    *MailboxService
	logs       *activitylog.Store
	createGate *sync.Mutex
}

type accountContextKey struct{}

var accountNamePattern = regexp.MustCompile(`^[^\x00-\x1f\x7f]{1,120}$`)

var (
	errDefaultAccountProtected = errors.New("默认账号用于兼容旧数据，不能删除")
	errMailAccountNotFound     = errors.New("未找到邮件账号")
)

func accountID() string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err == nil {
		return "mail-" + hex.EncodeToString(raw)
	}
	return fmt.Sprintf("mail-%x", time.Now().UnixNano())
}

func (module *Module) loadAccounts(ctx context.Context) ([]MailAccount, error) {
	db := module.database()
	if db == nil {
		now := time.Now().UTC()
		return []MailAccount{{ID: defaultMailAccountID, Name: "默认账号", Enabled: true, CreatedAt: now, UpdatedAt: now}}, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT id, name, apple_id, dsid, proxy_url, enabled, created_at, updated_at FROM mail_accounts ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := []MailAccount{}
	for rows.Next() {
		var account MailAccount
		if err := rows.Scan(&account.ID, &account.Name, &account.AppleID, &account.DSID, &account.ProxyURL, &account.Enabled, &account.CreatedAt, &account.UpdatedAt); err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (module *Module) database() *sql.DB {
	if module.persistence == nil {
		return nil
	}
	return module.persistence.DB()
}

func (module *Module) accountList() []MailAccountSummary {
	module.mu.RLock()
	type source struct {
		runtime *accountRuntime
		account MailAccount
	}
	sources := make([]source, 0, len(module.order))
	for _, id := range module.order {
		if runtime := module.runtimes[id]; runtime != nil {
			sources = append(sources, source{runtime: runtime, account: runtime.account})
		}
	}
	module.mu.RUnlock()
	result := make([]MailAccountSummary, 0, len(sources))
	for _, source := range sources {
		result = append(result, summarizeAccount(source.runtime, source.account))
	}
	return result
}

func summarizeAccount(runtime *accountRuntime, account MailAccount) MailAccountSummary {
	session := runtime.session.Status()
	mailbox := runtime.mailbox.Status()
	refresh := runtime.refresh.Status()
	creation := runtime.creation.Status()
	queue := runtime.queue.Status()
	summary := MailAccountSummary{
		ID: account.ID, Name: account.Name, AppleID: account.AppleID, DSID: account.DSID, Enabled: account.Enabled,
		CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt, HasProxy: account.ProxyURL != "", Status: "pending",
		StatusMessage: "等待配置登录", AliasCount: aliasCountFromStatus(session),
		ICloudWeb: channelHealth(session.AppleLogin.ICloudWeb), AppleAccount: channelHealth(session.AppleLogin.AppleAccount),
		Mailbox:            accountMailboxHealth{Configured: mailbox.Configured, Enabled: mailbox.Enabled, LastSyncAt: mailbox.LastSyncAt, LastError: mailbox.LastError},
		AutoRefreshEnabled: refresh.Enabled, AutoCreateEnabled: creation.Enabled, AutoCreateRunning: creation.Running, AliasQueueStatus: queue.Status,
	}
	summary.Status, summary.StatusMessage = accountHealthStatus(account.Enabled, summary, refresh, creation, queue)
	return summary
}

func accountHealthStatus(accountEnabled bool, summary MailAccountSummary, refresh AutoRefreshConfig, creation CreateScheduleConfig, queue AliasQueueStatus) (string, string) {
	configured := summary.ICloudWeb.Configured || summary.AppleAccount.Configured
	healthy := summary.ICloudWeb.Healthy || summary.AppleAccount.Healthy
	checked := summary.ICloudWeb.LastCheckedAt != nil || summary.AppleAccount.LastCheckedAt != nil
	switch {
	case !accountEnabled:
		return "error", "账号已停用"
	case configured && !checked:
		return "pending", "等待首次健康检测"
	case configured && !healthy:
		return "error", "登录状态异常"
	case summary.Mailbox.Enabled && summary.Mailbox.LastError != "":
		return "warning", "IMAP 同步异常"
	case refresh.Enabled && refresh.LastError != nil:
		return "warning", "Session 自动检测异常"
	case creation.Enabled && creation.LastError != nil:
		return "warning", "自动创建任务异常"
	case queue.Status == "needs_attention" || queue.Status == "failed":
		return "warning", "批量队列需要处理"
	case healthy && ((!summary.ICloudWeb.Healthy && summary.ICloudWeb.Configured) || (!summary.AppleAccount.Healthy && summary.AppleAccount.Configured)):
		return "warning", "部分登录通道异常"
	case healthy:
		return "active", "运行正常"
	default:
		return "pending", "等待配置登录"
	}
}

func channelHealth(status AppleChannelStatus) accountChannelHealth {
	return accountChannelHealth{Configured: status.Configured, Healthy: status.Healthy, LastCheckedAt: status.LastCheckedAt, ExpiresAt: status.ExpiresAt, Message: status.Message}
}

func aliasCountFromStatus(status SessionStatus) int {
	if status.HME == nil {
		return 0
	}
	switch value := status.HME["aliasCount"].(type) {
	case int:
		return value
	case float64:
		return int(value)
	case json.Number:
		count, _ := value.Int64()
		return int(count)
	default:
		return 0
	}
}

func (module *Module) runtime(id string) (*accountRuntime, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		id = defaultMailAccountID
	}
	module.mu.RLock()
	runtime, ok := module.runtimes[id]
	module.mu.RUnlock()
	return runtime, ok
}

func (module *Module) createAccount(name string) (MailAccount, error) {
	name = strings.TrimSpace(name)
	if !accountNamePattern.MatchString(name) {
		return MailAccount{}, errors.New("账号名称必须为 1 到 120 个可见字符")
	}
	now := time.Now().UTC()
	account := MailAccount{ID: accountID(), Name: name, Enabled: true, CreatedAt: now, UpdatedAt: now}
	if db := module.database(); db != nil {
		if _, err := db.Exec(`INSERT INTO mail_accounts (id, name, enabled, created_at, updated_at) VALUES ($1, $2, TRUE, $3, $3)`, account.ID, account.Name, now); err != nil {
			return MailAccount{}, err
		}
	}
	runtime, err := module.buildRuntime(account)
	if err != nil {
		if db := module.database(); db != nil {
			_, _ = db.Exec(`DELETE FROM mail_accounts WHERE id = $1`, account.ID)
		}
		return MailAccount{}, err
	}
	module.mu.Lock()
	module.runtimes[account.ID] = runtime
	module.order = append(module.order, account.ID)
	started := module.started
	module.mu.Unlock()
	if started {
		startAccountRuntime(runtime)
	}
	return account, nil
}

func (module *Module) deleteAccount(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == defaultMailAccountID {
		return errDefaultAccountProtected
	}

	module.mu.Lock()
	runtime := module.runtimes[id]
	if runtime == nil {
		module.mu.Unlock()
		return errMailAccountNotFound
	}
	orderIndex := -1
	for index, accountID := range module.order {
		if accountID == id {
			orderIndex = index
			module.order = append(module.order[:index], module.order[index+1:]...)
			break
		}
	}
	delete(module.runtimes, id)
	started := module.started
	module.mu.Unlock()

	stopAccountRuntime(runtime)
	restore := func() {
		module.mu.Lock()
		module.runtimes[id] = runtime
		if orderIndex >= 0 && orderIndex <= len(module.order) {
			module.order = append(module.order, "")
			copy(module.order[orderIndex+1:], module.order[orderIndex:])
			module.order[orderIndex] = id
		} else {
			module.order = append(module.order, id)
		}
		module.mu.Unlock()
		if started {
			startAccountRuntime(runtime)
		}
	}

	root := filepath.Clean(filepath.Join(module.dataDir, "accounts", id))
	if db := module.database(); db != nil {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			restore()
			return fmt.Errorf("开始删除账号事务: %w", err)
		}
		rollback := func(err error) error {
			_ = tx.Rollback()
			restore()
			return err
		}
		statements := []struct {
			query string
			args  []any
		}{
			{`DELETE FROM mail_share_sessions WHERE account_id = $1`, []any{id}},
			{`DELETE FROM mail_share_links WHERE account_id = $1`, []any{id}},
			{`DELETE FROM mailbox_hidden_messages WHERE account_id = $1`, []any{id}},
			{`DELETE FROM mailbox_messages WHERE account_id = $1`, []any{id}},
			{`DELETE FROM mailbox_sync_states WHERE account_id = $1`, []any{id}},
			{`DELETE FROM activity_logs WHERE module = 'mail' AND account_id = $1`, []any{id}},
			{`DELETE FROM running_state WHERE state_key = $1 OR strpos(state_key, $2) = 1 OR strpos(state_key, $3) = 1`, []any{root, root + string(filepath.Separator), "mail-share-imported:" + id + ":"}},
			{`DELETE FROM mail_accounts WHERE id = $1`, []any{id}},
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
				return rollback(fmt.Errorf("清理账号数据: %w", err))
			}
		}
		if err := tx.Commit(); err != nil {
			restore()
			return fmt.Errorf("提交账号删除事务: %w", err)
		}
		if err := os.RemoveAll(root); err != nil {
			slog.Warn("账号已从 PostgreSQL 删除，但旧版本地目录清理失败", "account_id", id, "error", err)
		}
		return nil
	}

	if err := os.RemoveAll(root); err != nil {
		restore()
		return fmt.Errorf("清理账号目录: %w", err)
	}
	return nil
}

func (module *Module) updateAccountIdentity(id string, config *ICloudConfig, appleID string) {
	module.mu.Lock()
	runtime := module.runtimes[id]
	if runtime == nil {
		module.mu.Unlock()
		return
	}
	if config != nil {
		appleID = firstNonEmpty(config.AppleID, appleID)
		runtime.account.DSID = config.DSID
	}
	if strings.TrimSpace(appleID) != "" {
		runtime.account.AppleID = strings.TrimSpace(appleID)
		if runtime.account.Name == "默认账号" || strings.HasPrefix(runtime.account.Name, "账号 ") {
			runtime.account.Name = runtime.account.AppleID
		}
	}
	runtime.account.UpdatedAt = time.Now().UTC()
	account := runtime.account
	module.mu.Unlock()
	if db := module.database(); db != nil {
		_, _ = db.Exec(`UPDATE mail_accounts SET name=$2, apple_id=$3, dsid=$4, updated_at=$5 WHERE id=$1`, account.ID, account.Name, account.AppleID, account.DSID, account.UpdatedAt)
	}
}

func (module *Module) buildRuntime(account MailAccount) (*accountRuntime, error) {
	root := module.dataDir
	if account.ID != defaultMailAccountID {
		root = filepath.Join(root, "accounts", account.ID)
	}
	configPath, stateDir := filepath.Join(root, "hme-config.json"), filepath.Join(root, "state")
	if account.ID == defaultMailAccountID {
		if module.configPath != "" {
			configPath = module.configPath
		}
		if module.stateDir != "" {
			stateDir = module.stateDir
		}
	}
	session := NewSessionManager(configPath, stateDir)
	if err := session.SetProxy(account.ProxyURL); err != nil {
		return nil, fmt.Errorf("加载账号代理: %w", err)
	}
	gate := &sync.Mutex{}
	creation := NewCreateScheduler(stateDir, session, gate)
	queue := NewAliasQueue(stateDir, session, gate)
	creation.SetBlocked(queue.Active)
	queue.SetBlocked(creation.Running)
	logs := activitylog.NewForAccount("mail", account.ID, filepath.Join(root, "logs"), module.persistence)
	creation.SetActivityLog(logs)
	refresh := NewAutoRefresh(stateDir, session)
	refresh.SetActivityLog(logs)
	mailbox, err := NewMailboxServiceWithPersistenceForAccount(stateDir, account.ID, session, module.persistence)
	if err != nil {
		return nil, err
	}
	shares, err := NewShareLinkStoreWithPersistence(stateDir, account.ID, module.persistence)
	if err != nil {
		return nil, fmt.Errorf("初始化分享数据: %w", err)
	}
	mailbox.SetActivityLog(logs)
	return &accountRuntime{account: account, session: session, refresh: refresh, creation: creation, queue: queue,
		shares: shares, mailbox: mailbox, logs: logs, createGate: gate}, nil
}

func startAccountRuntime(runtime *accountRuntime) {
	runtime.refresh.Start()
	runtime.creation.Start()
	runtime.queue.Start()
	runtime.mailbox.Start()
}

func stopAccountRuntime(runtime *accountRuntime) {
	runtime.refresh.Stop()
	runtime.creation.Shutdown()
	runtime.queue.Stop()
	runtime.mailbox.Stop()
}

func (api *routeAPI) accountMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if api.module == nil {
			next.ServeHTTP(w, r)
			return
		}
		id := strings.TrimSpace(r.Header.Get("X-Mail-Account-ID"))
		runtime, ok := api.module.runtime(id)
		if !ok {
			httpx.WriteError(w, r, http.StatusNotFound, "MAIL_ACCOUNT_NOT_FOUND", "未找到邮件账号")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), accountContextKey{}, runtime)))
	})
}

func (api *routeAPI) runtimeFor(r *http.Request) *accountRuntime {
	if runtime, ok := r.Context().Value(accountContextKey{}).(*accountRuntime); ok {
		return runtime
	}
	return &accountRuntime{session: api.session, refresh: api.refresh, creation: api.creation, queue: api.queue,
		shares: api.shares, mailbox: api.mailbox, logs: api.logs, createGate: api.createGate,
		account: MailAccount{ID: defaultMailAccountID, Name: "默认账号", Enabled: true}}
}

func (api *routeAPI) sessionFor(r *http.Request) *SessionManager   { return api.runtimeFor(r).session }
func (api *routeAPI) refreshFor(r *http.Request) *AutoRefresh      { return api.runtimeFor(r).refresh }
func (api *routeAPI) creationFor(r *http.Request) *CreateScheduler { return api.runtimeFor(r).creation }
func (api *routeAPI) queueFor(r *http.Request) *AliasQueue         { return api.runtimeFor(r).queue }
func (api *routeAPI) sharesFor(r *http.Request) *ShareLinkStore    { return api.runtimeFor(r).shares }
func (api *routeAPI) mailboxFor(r *http.Request) *MailboxService   { return api.runtimeFor(r).mailbox }
func (api *routeAPI) logsFor(r *http.Request) *activitylog.Store   { return api.runtimeFor(r).logs }
func (api *routeAPI) createGateFor(r *http.Request) *sync.Mutex    { return api.runtimeFor(r).createGate }

func (api *routeAPI) accounts(w http.ResponseWriter, r *http.Request) {
	httpx.WriteData(w, r, http.StatusOK, api.module.accountList())
}

func (api *routeAPI) testAccountProxy(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Proxy string `json:"proxy"`
	}
	if err := httpx.DecodeJSON(w, r, &payload, 16<<10); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	result, err := api.module.testAccountProxy(r.Context(), r.PathValue("id"), payload.Proxy, api.proxyTestTarget)
	if err != nil {
		if errors.Is(err, errMailAccountNotFound) {
			httpx.WriteError(w, r, http.StatusNotFound, "MAIL_ACCOUNT_NOT_FOUND", err.Error())
			return
		}
		var inputError *proxyTestInputError
		if errors.As(err, &inputError) {
			httpx.WriteError(w, r, http.StatusBadRequest, "MAIL_ACCOUNT_PROXY_TEST_ERROR", inputError.Error())
			return
		}
		httpx.WriteError(w, r, http.StatusBadGateway, "MAIL_ACCOUNT_PROXY_TEST_ERROR", err.Error())
		return
	}
	httpx.WriteData(w, r, http.StatusOK, result)
}

func (module *Module) updateAccountProxy(id, rawProxy string) (MailAccountSummary, error) {
	proxyURL, err := normalizeProxyURL(rawProxy)
	if err != nil {
		return MailAccountSummary{}, err
	}
	module.mu.RLock()
	runtime := module.runtimes[strings.TrimSpace(id)]
	module.mu.RUnlock()
	if runtime == nil {
		return MailAccountSummary{}, errMailAccountNotFound
	}
	if db := module.database(); db != nil {
		if _, err := db.Exec(`UPDATE mail_accounts SET proxy_url=$2, updated_at=NOW() WHERE id=$1`, runtime.account.ID, proxyURL); err != nil {
			return MailAccountSummary{}, err
		}
	}
	if err := runtime.session.SetProxy(proxyURL); err != nil {
		return MailAccountSummary{}, err
	}
	module.mu.Lock()
	runtime.account.ProxyURL = proxyURL
	runtime.account.UpdatedAt = time.Now().UTC()
	module.mu.Unlock()
	module.mu.RLock()
	account := runtime.account
	module.mu.RUnlock()
	return summarizeAccount(runtime, account), nil
}

func (api *routeAPI) updateAccountProxy(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Proxy string `json:"proxy"`
	}
	if err := httpx.DecodeJSON(w, r, &payload, 16<<10); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	account, err := api.module.updateAccountProxy(r.PathValue("id"), payload.Proxy)
	if err != nil {
		if errors.Is(err, errMailAccountNotFound) {
			httpx.WriteError(w, r, http.StatusNotFound, "MAIL_ACCOUNT_NOT_FOUND", err.Error())
			return
		}
		httpx.WriteError(w, r, http.StatusBadRequest, "MAIL_ACCOUNT_PROXY_ERROR", err.Error())
		return
	}
	httpx.WriteData(w, r, http.StatusOK, account)
}

func (api *routeAPI) createAccount(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name string `json:"name"`
	}
	if err := httpx.DecodeJSON(w, r, &payload, 16<<10); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	account, err := api.module.createAccount(payload.Name)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "MAIL_ACCOUNT_ERROR", err.Error())
		return
	}
	runtime, _ := api.module.runtime(account.ID)
	httpx.WriteData(w, r, http.StatusCreated, summarizeAccount(runtime, account))
}

func (api *routeAPI) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if err := api.module.deleteAccount(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, errDefaultAccountProtected):
			httpx.WriteError(w, r, http.StatusConflict, "MAIL_ACCOUNT_PROTECTED", err.Error())
		case errors.Is(err, errMailAccountNotFound):
			httpx.WriteError(w, r, http.StatusNotFound, "MAIL_ACCOUNT_NOT_FOUND", err.Error())
		default:
			httpx.WriteError(w, r, http.StatusInternalServerError, "MAIL_ACCOUNT_DELETE_ERROR", "删除邮件账号失败")
		}
		return
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"deleted": true, "id": id})
}
