package mail

import (
	"net/http"
	"path/filepath"
	"sync"

	"github.com/Bringbasket/running-tools/internal/platform/httpx"
	"github.com/Bringbasket/running-tools/internal/platform/persistence"
)

type Module struct {
	session    *SessionManager
	refresh    *AutoRefresh
	creation   *CreateScheduler
	queue      *AliasQueue
	shares     *ShareLinkStore
	mailbox    *MailboxService
	createGate *sync.Mutex
}

func NewModule(dataDir, configPath, stateDir string) *Module {
	module, err := NewModuleWithPersistence(dataDir, configPath, stateDir, nil)
	if err != nil {
		panic(err)
	}
	return module
}

func NewModuleWithPersistence(dataDir, configPath, stateDir string, persistenceService *persistence.Service) (*Module, error) {
	if configPath == "" {
		configPath = filepath.Join(dataDir, "hme-config.json")
	}
	if stateDir == "" {
		stateDir = filepath.Join(dataDir, "state")
	}
	session := NewSessionManager(configPath, stateDir)
	gate := &sync.Mutex{}
	creation := NewCreateScheduler(stateDir, session, gate)
	queue := NewAliasQueue(stateDir, session, gate)
	creation.SetBlocked(queue.Active)
	queue.SetBlocked(creation.Running)
	mailbox, err := NewMailboxServiceWithPersistence(stateDir, session, persistenceService)
	if err != nil {
		return nil, err
	}
	return &Module{session: session, refresh: NewAutoRefresh(stateDir, session), creation: creation, queue: queue, shares: NewShareLinkStore(stateDir), mailbox: mailbox, createGate: gate}, nil
}

func (module *Module) ID() string { return "mail" }

func (module *Module) RegisterRoutes(mux *http.ServeMux, auth httpx.Middleware) {
	api := &routeAPI{session: module.session, refresh: module.refresh, queue: module.queue, shares: module.shares, mailbox: module.mailbox, createGate: module.createGate}
	api.creation = module.creation
	api.register(mux, auth, "/api/mail/v1", false)
	api.register(mux, auth, "/v1", true)
}

func (module *Module) Start() {
	module.refresh.Start()
	module.creation.Start()
	module.queue.Start()
	module.mailbox.Start()
}
func (module *Module) Stop() {
	module.refresh.Stop()
	module.creation.Shutdown()
	module.queue.Stop()
	module.mailbox.Stop()
}
