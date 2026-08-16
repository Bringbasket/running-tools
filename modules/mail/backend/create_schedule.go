package mail

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/activitylog"
	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

const (
	defaultCreateBatchSize      = 5
	defaultCreateAliasInterval  = 3
	defaultCreateSchedulePeriod = 180
	minimumCreateBatchSize      = 1
	maximumCreateBatchSize      = 20
	minimumCreateAliasInterval  = 1
	maximumCreateAliasInterval  = 3600
	minimumCreateSchedulePeriod = 60
	maximumCreateSchedulePeriod = 7 * 24 * 60 * 60
	createScheduleIdlePoll      = 5 * time.Minute
	createScheduleMinimumWake   = 100 * time.Millisecond
)

// CreateScheduleConfig is the persisted configuration and public status of the
// server-side Hide My Email creation worker. Cookie values are never included.
type CreateScheduleConfig struct {
	Enabled                bool     `json:"enabled"`
	BatchSize              int      `json:"batchSize"`
	AliasIntervalSeconds   int      `json:"aliasIntervalSeconds"`
	IntervalSeconds        int      `json:"intervalSeconds"`
	Label                  string   `json:"label"`
	Note                   string   `json:"note"`
	LastRunAt              *float64 `json:"lastRunAt"`
	LastSuccessAt          *float64 `json:"lastSuccessAt"`
	LastDisabledAt         *float64 `json:"lastDisabledAt"`
	LastError              *string  `json:"lastError"`
	DisabledReason         *string  `json:"disabledReason"`
	LastBatchRequested     int      `json:"lastBatchRequested"`
	LastBatchSuccess       int      `json:"lastBatchSuccess"`
	LastBatchStoppedReason *string  `json:"lastBatchStoppedReason"`
	LastUsedChannel        string   `json:"lastUsedChannel,omitempty"`
	LastFallbackUsed       bool     `json:"lastFallbackUsed,omitempty"`
	LastAttemptedChannels  []string `json:"lastAttemptedChannels,omitempty"`
	WorkerRunning          bool     `json:"workerRunning"`
	Running                bool     `json:"running"`
	CurrentIndex           int      `json:"currentIndex"`
	CurrentTotal           int      `json:"currentTotal"`
	CurrentSuccess         int      `json:"currentSuccess"`
	RemainingSeconds       *int     `json:"remainingSeconds"`
	NextRunAt              *float64 `json:"nextRunAt"`
	ServerNow              float64  `json:"serverNow"`
}

type CreateScheduler struct {
	mu            sync.Mutex
	path          string
	session       *SessionManager
	stop          chan struct{}
	done          chan struct{}
	wake          chan struct{}
	workerRunning bool
	runLock       chan struct{}
	cancel        context.CancelFunc
	createGate    *sync.Mutex
	blocked       func() bool
	logs          *activitylog.Store
}

func NewCreateScheduler(stateDir string, session *SessionManager, gates ...*sync.Mutex) *CreateScheduler {
	gate := &sync.Mutex{}
	if len(gates) > 0 && gates[0] != nil {
		gate = gates[0]
	}
	return &CreateScheduler{
		path:       filepath.Join(stateDir, "create-schedule.json"),
		session:    session,
		runLock:    make(chan struct{}, 1),
		wake:       make(chan struct{}, 1),
		createGate: gate,
	}
}

func (service *CreateScheduler) SetBlocked(blocked func() bool)         { service.blocked = blocked }
func (service *CreateScheduler) SetActivityLog(logs *activitylog.Store) { service.logs = logs }
func (service *CreateScheduler) Running() bool                          { return len(service.runLock) > 0 }

func (service *CreateScheduler) Status() CreateScheduleConfig {
	service.mu.Lock()
	defer service.mu.Unlock()
	config := service.loadLocked()
	now := unixNow()
	config.ServerNow = now
	config.WorkerRunning = service.workerRunning
	config.Running = len(service.runLock) > 0
	if config.Enabled && !config.Running {
		base := now
		if config.LastRunAt != nil {
			base = *config.LastRunAt
		}
		next := base + float64(config.IntervalSeconds)
		remaining := max(0, int(next-now+0.5))
		config.NextRunAt, config.RemainingSeconds = &next, &remaining
	}
	if config.Running {
		config.NextRunAt, config.RemainingSeconds = nil, nil
	}
	return config
}

func (service *CreateScheduler) Update(enabled *bool, batchSize, aliasInterval, interval *int, label, note *string) (CreateScheduleConfig, error) {
	service.mu.Lock()
	config := service.loadLocked()
	if enabled != nil {
		config.Enabled = *enabled
		if *enabled {
			if config.LastRunAt == nil {
				now := unixNow()
				config.LastRunAt = &now
			}
			config.LastDisabledAt, config.DisabledReason, config.LastError = nil, nil, nil
		} else {
			now := unixNow()
			config.LastDisabledAt = &now
			reason := "手动停止"
			config.DisabledReason = &reason
		}
	}
	if batchSize != nil {
		config.BatchSize = clamp(*batchSize, minimumCreateBatchSize, maximumCreateBatchSize)
	}
	if aliasInterval != nil {
		config.AliasIntervalSeconds = clamp(*aliasInterval, minimumCreateAliasInterval, maximumCreateAliasInterval)
	}
	if interval != nil {
		config.IntervalSeconds = clamp(*interval, minimumCreateSchedulePeriod, maximumCreateSchedulePeriod)
	}
	if label != nil {
		config.Label = strings.TrimSpace(*label)
		if config.Label == "" {
			config.Label = "shopping"
		}
		if len(config.Label) > 100 {
			config.Label = config.Label[:100]
		}
	}
	if note != nil {
		config.Note = *note
		if len(config.Note) > 500 {
			config.Note = config.Note[:500]
		}
	}
	config = normalizedCreateConfig(config)
	err := storage.WriteJSON(service.path, persistentCreateConfig(config), 0o600)
	cancel := context.CancelFunc(nil)
	if enabled != nil && !*enabled {
		cancel = service.cancel
	}
	service.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if err != nil {
		return CreateScheduleConfig{}, err
	}
	service.signalWake()
	return service.Status(), nil
}

func (service *CreateScheduler) RunNow() error {
	return service.startRun(true)
}

func (service *CreateScheduler) Stop() (CreateScheduleConfig, error) {
	falseValue := false
	config, err := service.Update(&falseValue, nil, nil, nil, nil, nil)
	if err != nil {
		return CreateScheduleConfig{}, err
	}
	return config, nil
}

func (service *CreateScheduler) Start() {
	service.mu.Lock()
	if service.workerRunning {
		service.mu.Unlock()
		return
	}
	service.workerRunning = true
	config := service.loadLocked()
	if config.Enabled && config.LastRunAt == nil {
		now := unixNow()
		config.LastRunAt = &now
	}
	config.CurrentIndex, config.CurrentTotal, config.CurrentSuccess = 0, 0, 0
	_ = storage.WriteJSON(service.path, persistentCreateConfig(config), 0o600)
	service.stop, service.done = make(chan struct{}), make(chan struct{})
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

func (service *CreateScheduler) nextWakeDelay() time.Duration {
	status := service.Status()
	if !status.Enabled || status.Running || status.NextRunAt == nil {
		return createScheduleIdlePoll
	}
	seconds := *status.NextRunAt - unixNow()
	if seconds <= createScheduleMinimumWake.Seconds() {
		return createScheduleMinimumWake
	}
	if seconds >= createScheduleIdlePoll.Seconds() {
		return createScheduleIdlePoll
	}
	return time.Duration(seconds * float64(time.Second))
}

func (service *CreateScheduler) signalWake() {
	select {
	case service.wake <- struct{}{}:
	default:
	}
}

func (service *CreateScheduler) Shutdown() {
	service.mu.Lock()
	if !service.workerRunning {
		service.mu.Unlock()
		return
	}
	close(service.stop)
	done := service.done
	service.workerRunning = false
	cancel := service.cancel
	service.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

func (service *CreateScheduler) runIfDue() {
	config := service.Status()
	if config.Enabled && !config.Running && config.RemainingSeconds != nil && *config.RemainingSeconds <= 0 {
		_ = service.startRun(false)
	}
}

func (service *CreateScheduler) startRun(manual bool) error {
	if service.blocked != nil && service.blocked() {
		return ErrCreateInProgress
	}
	select {
	case service.runLock <- struct{}{}:
	default:
		return ErrCreateInProgress
	}
	ctx, cancel := context.WithCancel(context.Background())
	service.mu.Lock()
	service.cancel = cancel
	service.mu.Unlock()
	go func() {
		defer func() {
			<-service.runLock
			service.mu.Lock()
			service.cancel = nil
			service.mu.Unlock()
			service.signalWake()
		}()
		service.runBatch(ctx, manual)
	}()
	return nil
}

func (service *CreateScheduler) runBatch(ctx context.Context, manual bool) {
	service.mu.Lock()
	config := normalizedCreateConfig(service.loadLocked())
	if !manual && !config.Enabled {
		service.mu.Unlock()
		return
	}
	now := unixNow()
	config.LastRunAt = &now
	config.LastError = nil
	config.LastBatchStoppedReason = nil
	config.LastBatchRequested = config.BatchSize
	config.LastBatchSuccess = 0
	config.CurrentIndex = 0
	config.CurrentTotal = config.BatchSize
	config.CurrentSuccess = 0
	_ = storage.WriteJSON(service.path, persistentCreateConfig(config), 0o600)
	service.mu.Unlock()

	for index := 1; index <= config.BatchSize; index++ {
		if err := ctx.Err(); err != nil {
			service.finishBatch(err, "任务已停止", manual)
			return
		}
		service.setProgress(index, config.BatchSize, config.LastBatchSuccess)
		service.createGate.Lock()
		created, err := service.session.CreateAlias(ctx, config.Label, config.Note)
		service.createGate.Unlock()
		if err != nil {
			reason := "创建失败"
			if strings.Contains(err.Error(), "-41015") {
				reason = "Apple 当前创建限额已达到"
			} else if isSessionExpired(err) {
				reason = "Session 已失效，请重新导入"
			}
			service.finishBatch(err, reason, manual)
			return
		}
		service.setLastCreateRoute(created)
		config.LastBatchSuccess++
		service.setProgress(index, config.BatchSize, config.LastBatchSuccess)
		if index < config.BatchSize {
			timer := time.NewTimer(time.Duration(config.AliasIntervalSeconds) * time.Second)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				service.finishBatch(ctx.Err(), "任务已停止", manual)
				return
			}
		}
	}
	service.finishBatch(nil, "本轮创建完成", manual)
}

func (service *CreateScheduler) setLastCreateRoute(created map[string]any) {
	service.mu.Lock()
	defer service.mu.Unlock()
	config := service.loadLocked()
	config.LastUsedChannel = strings.TrimSpace(fmt.Sprint(created["usedChannel"]))
	config.LastFallbackUsed, _ = created["fallbackUsed"].(bool)
	config.LastAttemptedChannels = nil
	if attempted, ok := created["attemptedChannels"].([]string); ok {
		config.LastAttemptedChannels = append([]string(nil), attempted...)
	}
	_ = storage.WriteJSON(service.path, persistentCreateConfig(config), 0o600)
}

func (service *CreateScheduler) setProgress(index, total, success int) {
	service.mu.Lock()
	defer service.mu.Unlock()
	config := service.loadLocked()
	config.CurrentIndex, config.CurrentTotal, config.CurrentSuccess = index, total, success
	config.LastBatchSuccess = success
	_ = storage.WriteJSON(service.path, persistentCreateConfig(config), 0o600)
}

func (service *CreateScheduler) finishBatch(err error, reason string, manual bool) {
	service.mu.Lock()
	config := service.loadLocked()
	config.CurrentIndex, config.CurrentTotal, config.CurrentSuccess = 0, 0, 0
	config.LastBatchStoppedReason = &reason
	if err != nil && !errors.Is(err, context.Canceled) {
		message := err.Error()
		config.LastError = &message
	}
	if strings.Contains(reason, "Session 已失效") || strings.Contains(reason, "Session 未导入") {
		now := unixNow()
		config.Enabled, config.LastDisabledAt = false, &now
		config.DisabledReason = &reason
	}
	if err == nil {
		now := unixNow()
		config.LastSuccessAt = &now
		config.LastError = nil
	}
	_ = storage.WriteJSON(service.path, persistentCreateConfig(config), 0o600)
	service.mu.Unlock()
	if service.logs != nil {
		level, outcome := "info", "success"
		detail := ""
		if err != nil {
			level, outcome = "error", "failure"
			detail = safeErrorText(err)
			if errors.Is(err, context.Canceled) || strings.Contains(reason, "限额") {
				level = "warning"
			}
		}
		source := "background"
		if manual {
			source = "user"
		}
		service.logs.Record(context.Background(), activitylog.Input{Category: "automation", Action: "alias.schedule.batch",
			Level: level, Outcome: outcome, Summary: reason, Source: source, Detail: detail,
			Metadata: map[string]any{"requested": config.LastBatchRequested, "success": config.LastBatchSuccess}})
	}
}

func (service *CreateScheduler) loadLocked() CreateScheduleConfig {
	config := CreateScheduleConfig{BatchSize: defaultCreateBatchSize, AliasIntervalSeconds: defaultCreateAliasInterval, IntervalSeconds: defaultCreateSchedulePeriod, Label: "shopping"}
	_ = storage.ReadJSON(service.path, &config)
	return normalizedCreateConfig(config)
}

func normalizedCreateConfig(config CreateScheduleConfig) CreateScheduleConfig {
	config.BatchSize = clamp(config.BatchSize, minimumCreateBatchSize, maximumCreateBatchSize)
	config.AliasIntervalSeconds = clamp(config.AliasIntervalSeconds, minimumCreateAliasInterval, maximumCreateAliasInterval)
	config.IntervalSeconds = clamp(config.IntervalSeconds, minimumCreateSchedulePeriod, maximumCreateSchedulePeriod)
	if strings.TrimSpace(config.Label) == "" {
		config.Label = "shopping"
	}
	return config
}

func persistentCreateConfig(config CreateScheduleConfig) CreateScheduleConfig {
	config.WorkerRunning, config.Running = false, false
	config.RemainingSeconds, config.NextRunAt, config.ServerNow = nil, nil, 0
	return config
}

func clamp(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

var ErrCreateInProgress = errors.New("邮箱创建任务正在运行")
