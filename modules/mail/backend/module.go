package mail

import (
	"context"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/Bringbasket/running-tools/internal/platform/httpx"
	"github.com/Bringbasket/running-tools/internal/platform/persistence"
	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

type Module struct {
	mu          sync.RWMutex
	dataDir     string
	configPath  string
	stateDir    string
	persistence *persistence.Service
	runtimes    map[string]*accountRuntime
	order       []string
	started     bool
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
	if persistenceService != nil && persistenceService.Mode() == persistence.StoragePostgres {
		storage.ConfigurePostgres(persistenceService.DB(), dataDir, filepath.Dir(configPath), stateDir)
	}
	module := &Module{dataDir: dataDir, configPath: configPath, stateDir: stateDir, persistence: persistenceService, runtimes: map[string]*accountRuntime{}}
	accounts, err := module.loadAccounts(context.Background())
	if err != nil {
		return nil, err
	}
	for _, account := range accounts {
		runtime, buildErr := module.buildRuntime(account)
		if buildErr != nil {
			return nil, buildErr
		}
		module.runtimes[account.ID] = runtime
		module.order = append(module.order, account.ID)
	}
	return module, nil
}

func (module *Module) ID() string { return "mail" }

func (module *Module) RegisterRoutes(mux *http.ServeMux, auth httpx.Middleware) {
	api := &routeAPI{module: module}
	api.register(mux, auth, "/api/mail/v1", false)
	api.register(mux, auth, "/v1", true)
}

func (module *Module) Start() {
	module.mu.Lock()
	module.started = true
	runtimes := make([]*accountRuntime, 0, len(module.runtimes))
	for _, runtime := range module.runtimes {
		runtimes = append(runtimes, runtime)
	}
	module.mu.Unlock()
	for _, runtime := range runtimes {
		startAccountRuntime(runtime)
	}
}
func (module *Module) Stop() {
	module.mu.Lock()
	module.started = false
	runtimes := make([]*accountRuntime, 0, len(module.runtimes))
	for _, runtime := range module.runtimes {
		runtimes = append(runtimes, runtime)
	}
	module.mu.Unlock()
	for _, runtime := range runtimes {
		stopAccountRuntime(runtime)
	}
}
