package mail

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/activitylog"
	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

const (
	defaultRefreshInterval  = 600
	minimumRefreshInterval  = 300
	autoRefreshPollInterval = 10 * time.Second
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
	logs            *activitylog.Store
	lastLoggedState string
}

func (service *AutoRefresh) SetActivityLog(logs *activitylog.Store) { service.logs = logs }

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
	nowTime := time.Now()
	now := float64(nowTime.UnixNano()) / float64(time.Second)
	config.ServerNow = now
	config.WorkerRunning = service.running
	if config.Enabled {
		base := now
		hasLastRun := config.LastRunAt != nil
		if config.LastRunAt != nil {
			base = *config.LastRunAt
		}
		next := base + float64(config.IntervalSeconds)
		// Apple Account management sessions expose a shorter, independent TTL.
		// Keep the configured interval as the floor after a run, but pull the
		// next check forward when the Apple deadline arrives first.
		if due, ok := service.session.appleAccountRefreshDueAt(nowTime); ok {
			dueAt := float64(due.UnixNano()) / float64(time.Second)
			switch {
			case !hasLastRun && dueAt <= now:
				next = now
			case dueAt > now && dueAt < next:
				next = dueAt
			}
		}
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
	return service.run(ctx, "user")
}

func (service *AutoRefresh) run(ctx context.Context, source string) (map[string]any, error) {
	status := service.session.Check(ctx)
	service.mu.Lock()
	defer service.mu.Unlock()
	config := service.loadLocked()
	now := unixNow()
	config.LastRunAt = &now
	appleConfigured := status.AppleLogin.AppleAccount.Configured
	appleHealthy := !appleConfigured || status.AppleLogin.AppleAccount.Healthy
	checkHealthy := status.SessionValid && appleHealthy && !status.NeedsReauth
	if status.NeedsReauth && !status.AppleLogin.AppleAccount.Healthy {
		reason := "session requires re-import"
		if status.LastError != nil {
			reason = *status.LastError
		}
		config.Enabled = false
		config.LastDisabledAt = &now
		config.DisabledReason, config.LastError = &reason, &reason
	} else if checkHealthy {
		config.LastSuccessAt = &now
		config.LastError, config.DisabledReason = nil, nil
	} else {
		// Keep an Apple Account failure visible even when the long-lived iCloud
		// Web session itself is still valid. The worker must continue retrying
		// this short-lived channel instead of disabling the whole service.
		message := ""
		if !appleHealthy {
			message = status.AppleLogin.AppleAccount.Message
		}
		if message == "" && status.LastError != nil {
			message = *status.LastError
		}
		if message != "" {
			config.LastError = &message
		}
	}
	if err := storage.WriteJSON(service.path, persistentAutoRefresh(config), 0o600); err != nil {
		return nil, err
	}
	if service.logs != nil && source == "background" {
		level, outcome, summary := "info", "success", "Session 自动检测正常"
		detail := ""
		if !checkHealthy {
			level, outcome, summary = "error", "failure", "Session 自动检测发现登录失效"
			if status.LastError != nil {
				detail = *status.LastError
			}
			if detail == "" && !appleHealthy {
				detail = status.AppleLogin.AppleAccount.Message
			}
		}
		if service.lastLoggedState != outcome {
			service.logs.Record(context.Background(), activitylog.Input{Category: "session", Action: "session.auto_refresh.check",
				Level: level, Outcome: outcome, Summary: summary, Source: source, Detail: detail})
			service.lastLoggedState = outcome
		}
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
		ticker := time.NewTicker(autoRefreshPollInterval)
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
		_, _ = service.run(ctx, "background")
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
