package mail

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
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
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
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
	rows, err := db.QueryContext(ctx, `SELECT id, name, apple_id, dsid, enabled, created_at, updated_at FROM mail_accounts ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := []MailAccount{}
	for rows.Next() {
		var account MailAccount
		if err := rows.Scan(&account.ID, &account.Name, &account.AppleID, &account.DSID, &account.Enabled, &account.CreatedAt, &account.UpdatedAt); err != nil {
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

func (module *Module) accountList() []MailAccount {
	module.mu.RLock()
	defer module.mu.RUnlock()
	result := make([]MailAccount, 0, len(module.order))
	for _, id := range module.order {
		if runtime := module.runtimes[id]; runtime != nil {
			result = append(result, runtime.account)
		}
	}
	return result
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
	httpx.WriteData(w, r, http.StatusCreated, account)
}
