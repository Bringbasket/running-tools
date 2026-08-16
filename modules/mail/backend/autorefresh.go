package mail

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/activitylog"
	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

const (
	defaultRefreshInterval = 600
	minimumRefreshInterval = 300
	autoRefreshIdlePoll    = 30 * time.Second
	autoRefreshMinimumWake = 100 * time.Millisecond
	appleKeepAliveInterval = 3 * time.Minute
	appleKeepAliveJitter   = 15
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
	mu                   sync.Mutex
	runMu                sync.Mutex
	path                 string
	session              *SessionManager
	stop                 chan struct{}
	done                 chan struct{}
	wake                 chan struct{}
	running              bool
	defaultInterval      int
	logs                 *activitylog.Store
	lastLoggedState      string
	lastAppleLoggedState string
	appleTargetAt        time.Time
	appleCheckedAt       time.Time
}

func (service *AutoRefresh) SetActivityLog(logs *activitylog.Store) { service.logs = logs }

func NewAutoRefresh(stateDir string, session *SessionManager) *AutoRefresh {
	interval := defaultRefreshInterval
	if configured, err := strconv.Atoi(os.Getenv("MAIL_AUTO_REFRESH_INTERVAL")); err == nil && configured > 0 {
		interval = max(minimumRefreshInterval, configured)
	}
	return &AutoRefresh{
		path:            filepath.Join(stateDir, "auto-refresh.json"),
		session:         session,
		wake:            make(chan struct{}, 1),
		defaultInterval: interval,
	}
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
		if config.LastRunAt != nil {
			base = *config.LastRunAt
		}
		next := base + float64(config.IntervalSeconds)
		// Apple Account management sessions expose a shorter, independent TTL.
		// Keep the configured interval as the floor after a run, but pull the
		// next check forward when the Apple deadline arrives first.
		if due, ok := service.appleKeepAliveDueAtLocked(nowTime); ok {
			dueAt := float64(due.UnixNano()) / float64(time.Second)
			switch {
			case dueAt <= now:
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
		service.mu.Unlock()
		return AutoRefreshConfig{}, err
	}
	service.mu.Unlock()
	service.signalWake()
	return config, nil
}

func (service *AutoRefresh) Run(ctx context.Context) (map[string]any, error) {
	return service.run(ctx, "user")
}

func shouldDisableAutoRefresh(status SessionStatus) bool {
	account := status.AppleLogin.AppleAccount
	return status.NeedsReauth && (!account.Configured || account.RequiresReauth)
}

func (service *AutoRefresh) run(ctx context.Context, source string) (map[string]any, error) {
	// Manual runs and the background ticker share the same Session check. Keep
	// one in flight so a due tick cannot immediately duplicate a user request.
	service.runMu.Lock()
	defer service.runMu.Unlock()
	defer service.signalWake()
	status := service.session.Check(ctx)
	service.mu.Lock()
	defer service.mu.Unlock()
	config := service.loadLocked()
	now := unixNow()
	config.LastRunAt = &now
	appleConfigured := status.AppleLogin.AppleAccount.Configured
	appleReauth := appleConfigured && status.AppleLogin.AppleAccount.RequiresReauth
	appleDegraded := appleConfigured && status.AppleLogin.AppleAccount.State == AppleAccountStateDegraded
	checkHealthy := status.SessionValid && !status.NeedsReauth && !appleReauth && !appleDegraded
	// A transient Apple Account outage must keep this worker alive so the
	// independent keep-alive path can retry it. Disable only when there is no
	// Apple channel to recover or Apple explicitly requires re-authentication.
	if shouldDisableAutoRefresh(status) {
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
		// Web session itself is still valid. A degraded Apple channel is not a
		// session-wide failure; its independent worker will retry it.
		message := ""
		if appleReauth || appleDegraded {
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
		if appleDegraded && status.SessionValid && !status.NeedsReauth && !appleReauth {
			level, outcome, summary = "warning", "failure", "Apple Account 保活临时异常，将自动重试"
			detail = status.AppleLogin.AppleAccount.Message
		} else if !checkHealthy {
			level, outcome, summary = "error", "failure", "Session 自动检测发现登录失效"
			if status.LastError != nil {
				detail = *status.LastError
			}
			if detail == "" && (appleReauth || appleDegraded) {
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
		for {
			timer := time.NewTimer(service.nextWakeDelay())
			select {
			case <-timer.C:
			case <-service.wake:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			case <-stop:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			}
			service.runIfDue()
		}
	}()
}

func (service *AutoRefresh) nextWakeDelay() time.Duration {
	service.mu.Lock()
	defer service.mu.Unlock()
	config := service.loadLocked()
	if !config.Enabled {
		return autoRefreshIdlePoll
	}
	now := time.Now()
	nowUnix := float64(now.UnixNano()) / float64(time.Second)
	if config.LastRunAt == nil {
		return autoRefreshMinimumWake
	}
	next := *config.LastRunAt + float64(config.IntervalSeconds)
	if appleDueAt, ok := service.appleKeepAliveDueAtLocked(now); ok {
		appleDueUnix := float64(appleDueAt.UnixNano()) / float64(time.Second)
		if appleDueUnix < next {
			next = appleDueUnix
		}
	}
	seconds := next - nowUnix
	if seconds <= autoRefreshMinimumWake.Seconds() {
		return autoRefreshMinimumWake
	}
	if seconds >= autoRefreshIdlePoll.Seconds() {
		return autoRefreshIdlePoll
	}
	return time.Duration(seconds * float64(time.Second))
}

func (service *AutoRefresh) signalWake() {
	select {
	case service.wake <- struct{}{}:
	default:
	}
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
	service.mu.Lock()
	config := service.loadLocked()
	now := time.Now()
	webDue := config.LastRunAt == nil || float64(now.UnixNano())/float64(time.Second) >= *config.LastRunAt+float64(config.IntervalSeconds)
	appleDueAt, appleConfigured := service.appleKeepAliveDueAtLocked(now)
	service.mu.Unlock()
	if !config.Enabled {
		return
	}
	if webDue {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		_, _ = service.run(ctx, "background")
		cancel()
		return
	}
	if appleConfigured && !now.Before(appleDueAt) {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		err := service.session.KeepAliveAppleAccount(ctx)
		cancel()
		service.recordAppleKeepAliveResult(err)
	}
}

func (service *AutoRefresh) appleKeepAliveDueAtLocked(now time.Time) (time.Time, bool) {
	checkedAt, deadline, eligible := service.session.appleAccountKeepAliveSchedule(now)
	if !eligible {
		service.appleTargetAt, service.appleCheckedAt = time.Time{}, time.Time{}
		return time.Time{}, false
	}
	if service.appleTargetAt.IsZero() || !service.appleCheckedAt.Equal(checkedAt) {
		spread := int64(appleKeepAliveInterval) * appleKeepAliveJitter / 100
		offset := rand.Int64N(spread*2+1) - spread
		service.appleCheckedAt = checkedAt
		service.appleTargetAt = now
		if !checkedAt.IsZero() {
			service.appleTargetAt = checkedAt.Add(appleKeepAliveInterval + time.Duration(offset))
		}
	}
	if !deadline.IsZero() && deadline.Before(service.appleTargetAt) {
		return deadline, true
	}
	return service.appleTargetAt, true
}

func (service *AutoRefresh) recordAppleKeepAliveResult(err error) {
	defer service.signalWake()
	level, outcome, summary, detail := "info", "success", "Apple Account 保活成功", ""
	if err != nil {
		level, outcome, summary, detail = "warning", "failure", "Apple Account 保活临时失败", safeErrorText(err)
		if appleAccountRequiresReauth(err) {
			level, summary = "error", "Apple Account 登录态失效，需要重新登录"
		}
	}
	stateKey := "apple:" + outcome + ":" + level
	service.mu.Lock()
	config := service.loadLocked()
	now := unixNow()
	if err == nil {
		config.LastSuccessAt = &now
		config.LastError, config.DisabledReason = nil, nil
	} else {
		config.LastError = &detail
	}
	if writeErr := storage.WriteJSON(service.path, persistentAutoRefresh(config), 0o600); writeErr != nil {
		slog.Warn("Apple Account 保活状态保存失败", "error", safeErrorText(writeErr))
	}
	shouldLog := service.logs != nil && service.lastAppleLoggedState != stateKey
	if shouldLog {
		service.lastAppleLoggedState = stateKey
	}
	service.mu.Unlock()
	if !shouldLog {
		return
	}
	service.logs.Record(context.Background(), activitylog.Input{Category: "session", Action: "session.apple_account.keepalive",
		Level: level, Outcome: outcome, Summary: summary, Source: "background", Detail: detail})
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
