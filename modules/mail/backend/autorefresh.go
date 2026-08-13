package mail

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

const (
	defaultRefreshInterval = 600
	minimumRefreshInterval = 300
)

type AutoRefreshConfig struct {
	Enabled          bool     `json:"enabled"`
	IntervalSeconds  int      `json:"intervalSeconds"`
	LastRunAt        *float64 `json:"lastRunAt"`
	LastSuccessAt    *float64 `json:"lastSuccessAt"`
	LastDisabledAt   *float64 `json:"lastDisabledAt"`
	LastError        *string  `json:"lastError"`
	DisabledReason   *string  `json:"disabledReason"`
	WorkerRunning    bool     `json:"workerRunning"`
	RemainingSeconds *int     `json:"remainingSeconds"`
	NextRunAt        *float64 `json:"nextRunAt"`
	ServerNow        float64  `json:"serverNow"`
}

type AutoRefresh struct {
	mu              sync.Mutex
	path            string
	session         *SessionManager
	stop            chan struct{}
	done            chan struct{}
	running         bool
	defaultInterval int
}

func NewAutoRefresh(stateDir string, session *SessionManager) *AutoRefresh {
	interval := defaultRefreshInterval
	if configured, err := strconv.Atoi(os.Getenv("MAIL_AUTO_REFRESH_INTERVAL")); err == nil && configured > 0 {
		interval = max(minimumRefreshInterval, configured)
	}
	return &AutoRefresh{path: filepath.Join(stateDir, "auto-refresh.json"), session: session, defaultInterval: interval}
}

func (service *AutoRefresh) Status() AutoRefreshConfig {
	service.mu.Lock()
	defer service.mu.Unlock()
	config := service.loadLocked()
	now := unixNow()
	config.ServerNow = now
	config.WorkerRunning = service.running
	if config.Enabled {
		base := now
		if config.LastRunAt != nil {
			base = *config.LastRunAt
		}
		next := base + float64(config.IntervalSeconds)
		remaining := max(0, int(next-now+0.5))
		config.NextRunAt, config.RemainingSeconds = &next, &remaining
	}
	return config
}

func (service *AutoRefresh) Update(enabled *bool, interval *int) (AutoRefreshConfig, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	config := service.loadLocked()
	if enabled != nil {
		config.Enabled = *enabled
		if *enabled {
			config.LastDisabledAt, config.DisabledReason, config.LastError = nil, nil, nil
		}
	}
	if interval != nil {
		config.IntervalSeconds = max(minimumRefreshInterval, *interval)
	}
	if err := storage.WriteJSON(service.path, persistentAutoRefresh(config), 0o600); err != nil {
		return AutoRefreshConfig{}, err
	}
	return config, nil
}

func (service *AutoRefresh) Run(ctx context.Context) (map[string]any, error) {
	status := service.session.Check(ctx)
	service.mu.Lock()
	defer service.mu.Unlock()
	config := service.loadLocked()
	now := unixNow()
	config.LastRunAt = &now
	if status.NeedsReauth && !status.AppleLogin.AppleAccount.Healthy {
		reason := "session requires re-import"
		if status.LastError != nil {
			reason = *status.LastError
		}
		config.Enabled = false
		config.LastDisabledAt = &now
		config.DisabledReason, config.LastError = &reason, &reason
	} else if status.SessionValid {
		config.LastSuccessAt = &now
		config.LastError, config.DisabledReason = nil, nil
	} else if status.LastError != nil {
		config.LastError = status.LastError
	}
	if err := storage.WriteJSON(service.path, persistentAutoRefresh(config), 0o600); err != nil {
		return nil, err
	}
	return map[string]any{"autoRefresh": config, "session": status}, nil
}

func (service *AutoRefresh) Start() {
	service.mu.Lock()
	if service.running {
		service.mu.Unlock()
		return
	}
	service.running, service.stop, service.done = true, make(chan struct{}), make(chan struct{})
	stop, done := service.stop, service.done
	service.mu.Unlock()
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				service.runIfDue()
			case <-stop:
				return
			}
		}
	}()
}

func (service *AutoRefresh) Stop() {
	service.mu.Lock()
	if !service.running {
		service.mu.Unlock()
		return
	}
	close(service.stop)
	done := service.done
	service.running = false
	service.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

func (service *AutoRefresh) runIfDue() {
	config := service.Status()
	if config.Enabled && config.RemainingSeconds != nil && *config.RemainingSeconds <= 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		_, _ = service.Run(ctx)
	}
}

func (service *AutoRefresh) loadLocked() AutoRefreshConfig {
	config := AutoRefreshConfig{Enabled: true, IntervalSeconds: service.defaultInterval}
	_ = storage.ReadJSON(service.path, &config)
	if config.IntervalSeconds < minimumRefreshInterval {
		config.IntervalSeconds = minimumRefreshInterval
	}
	return config
}

func persistentAutoRefresh(config AutoRefreshConfig) AutoRefreshConfig {
	config.WorkerRunning, config.RemainingSeconds, config.NextRunAt, config.ServerNow = false, nil, nil, 0
	return config
}
