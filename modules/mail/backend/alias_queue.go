package mail

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

const (
	maxAliasQueueCount       = 99
	defaultAliasQueueSpacing = 3
	aliasQueueCooldown       = 30 * time.Minute
	aliasQueueRetry          = time.Minute
)

// AliasQueueStatus is intentionally free of iCloud cookies and session data.
type AliasQueueStatus struct {
	JobID          string   `json:"jobId"`
	RequestID      string   `json:"requestId,omitempty"`
	AccountDSID    string   `json:"accountDsid,omitempty"`
	BaseLabel      string   `json:"baseLabel,omitempty"`
	Note           string   `json:"note,omitempty"`
	Requested      int      `json:"requested"`
	Status         string   `json:"status"`
	Current        int      `json:"current"`
	Success        int      `json:"success"`
	CreatedAt      float64  `json:"createdAt"`
	UpdatedAt      float64  `json:"updatedAt"`
	CompletedAt    *float64 `json:"completedAt"`
	NextAttemptAt  *float64 `json:"nextAttemptAt"`
	LastErrorCode  string   `json:"lastErrorCode,omitempty"`
	LastError      string   `json:"lastError,omitempty"`
	CandidateHME   string   `json:"candidateHme,omitempty"`
	CandidateState string   `json:"candidateState,omitempty"`
	WorkerRunning  bool     `json:"workerRunning"`
	ServerNow      float64  `json:"serverNow"`
}

type aliasQueuePersisted struct {
	Job *AliasQueueStatus `json:"job"`
}

type AliasQueueError struct {
	Message  string
	Code     string
	Conflict bool
}

func (e *AliasQueueError) Error() string { return e.Message }

type AliasQueue struct {
	mu            sync.Mutex
	path          string
	session       *SessionManager
	stop          chan struct{}
	done          chan struct{}
	wake          chan struct{}
	workerRunning bool
	cancel        context.CancelFunc
	createGate    *sync.Mutex
	blocked       func() bool
}

func NewAliasQueue(stateDir string, session *SessionManager, gates ...*sync.Mutex) *AliasQueue {
	gate := &sync.Mutex{}
	if len(gates) > 0 && gates[0] != nil {
		gate = gates[0]
	}
	return &AliasQueue{path: filepath.Join(stateDir, "alias-queue.json"), session: session, wake: make(chan struct{}, 1), createGate: gate}
}

func (q *AliasQueue) SetBlocked(blocked func() bool) { q.blocked = blocked }
func (q *AliasQueue) Active() bool {
	status := q.Status().Status
	return status != "" && status != "idle" && status != "completed" && status != "cancelled"
}
func (q *AliasQueue) AccountDSID() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.loadLocked().AccountDSID
}
func publicQueueStatus(status AliasQueueStatus) AliasQueueStatus {
	status.AccountDSID = ""
	return status
}

func (q *AliasQueue) Start() {
	q.mu.Lock()
	if q.workerRunning {
		q.mu.Unlock()
		return
	}
	q.workerRunning = true
	state := q.loadLocked()
	if state.Status == "running" {
		now := unixNow()
		state.UpdatedAt = now
		if state.CandidateHME != "" && state.CandidateState == "reserving" {
			state.Status = "needs_attention"
			state.LastErrorCode = "RESULT_CONFIRMATION_REQUIRED"
			state.LastError = "服务在保留邮箱时重启，请确认结果后继续"
			state.NextAttemptAt = nil
		} else {
			state.Status = "queued"
			state.NextAttemptAt = &now
		}
		_ = q.saveLocked(state)
	}
	q.stop, q.done = make(chan struct{}), make(chan struct{})
	stop, done := q.stop, q.done
	q.mu.Unlock()
	go func() { defer close(done); q.loop(stop) }()
}

func (q *AliasQueue) Stop() {
	q.mu.Lock()
	if !q.workerRunning {
		q.mu.Unlock()
		return
	}
	close(q.stop)
	cancel := q.cancel
	done := q.done
	q.workerRunning = false
	q.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

func (q *AliasQueue) loop(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			q.runIfDue()
		case <-q.wake:
			q.runIfDue()
		case <-stop:
			return
		}
	}
}

func (q *AliasQueue) Status() AliasQueueStatus {
	q.mu.Lock()
	defer q.mu.Unlock()
	state := q.loadLocked()
	state.WorkerRunning = q.workerRunning
	state.ServerNow = unixNow()
	if state.Status == "" {
		state.Status = "idle"
	}
	return publicQueueStatus(state)
}

func (q *AliasQueue) Enqueue(ctx context.Context, base string, count int, note, requestID string) (AliasQueueStatus, error) {
	base = strings.TrimSpace(base)
	note = strings.TrimSpace(note)
	requestID = strings.TrimSpace(requestID)
	if base == "" {
		return AliasQueueStatus{}, &AliasQueueError{"必须填写基础标签", "BAD_LABEL", false}
	}
	if count < 1 || count > maxAliasQueueCount {
		return AliasQueueStatus{}, &AliasQueueError{"数量必须是 1 到 99", "BAD_COUNT", false}
	}
	if len(base)+2 > 160 {
		return AliasQueueStatus{}, &AliasQueueError{"基础标签过长", "BAD_LABEL", false}
	}
	if q.blocked != nil && q.blocked() {
		return AliasQueueStatus{}, &AliasQueueError{"周期创建计划正在执行，请等待本轮结束后再加入队列", "CREATE_IN_PROGRESS", true}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	current := q.loadLocked()
	if requestID != "" && current.RequestID == requestID {
		if current.BaseLabel == base && current.Requested == count && current.Note == note {
			return publicQueueStatus(current), nil
		}
		return AliasQueueStatus{}, &AliasQueueError{"requestId 已用于另一批参数", "IDEMPOTENCY_CONFLICT", true}
	}
	if current.JobID != "" && current.Status != "completed" && current.Status != "cancelled" && current.Status != "idle" {
		return AliasQueueStatus{}, &AliasQueueError{"已有批量创建队列，请先等待、暂停或取消当前队列", "QUEUE_ACTIVE", true}
	}
	client, err := q.session.Client()
	if err != nil {
		return AliasQueueStatus{}, err
	}
	now := unixNow()
	current = AliasQueueStatus{JobID: newID(), RequestID: requestID, AccountDSID: client.config.DSID, BaseLabel: base, Note: note, Requested: count, Status: "queued", CreatedAt: now, UpdatedAt: now, NextAttemptAt: &now}
	if err := q.saveLocked(current); err != nil {
		return AliasQueueStatus{}, err
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return publicQueueStatus(current), nil
}

func (q *AliasQueue) Pause(jobID string) (AliasQueueStatus, error) { return q.control(jobID, "paused") }
func (q *AliasQueue) Resume(jobID string, confirmUncertain bool) (AliasQueueStatus, error) {
	q.mu.Lock()
	s := q.loadLocked()
	if s.JobID == "" || (jobID != "" && s.JobID != jobID) || (s.Status != "paused" && s.Status != "needs_attention") {
		q.mu.Unlock()
		return AliasQueueStatus{}, &AliasQueueError{"当前没有可继续的批量队列", "QUEUE_NOT_PAUSED", true}
	}
	q.mu.Unlock()
	if s.Status == "needs_attention" && s.CandidateHME != "" {
		client, err := q.session.Client()
		if err != nil {
			return AliasQueueStatus{}, err
		}
		aliases, err := client.ListAliases(context.Background())
		if err != nil {
			return AliasQueueStatus{}, err
		}
		matched := false
		for _, alias := range aliases {
			if strings.EqualFold(strings.TrimSpace(fmt.Sprint(alias["hme"])), s.CandidateHME) {
				matched = true
				break
			}
		}
		if !matched && !confirmUncertain {
			return AliasQueueStatus{}, &AliasQueueError{"未在邮箱列表中确认到上次候选地址，请确认后再继续", "RESULT_CONFIRMATION_REQUIRED", true}
		}
		q.mu.Lock()
		s = q.loadLocked()
		if matched {
			s.Current++
			s.Success++
		}
		s.CandidateHME, s.CandidateState = "", ""
		_ = q.saveLocked(s)
		q.mu.Unlock()
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	s = q.loadLocked()
	if s.Current >= s.Requested {
		now := unixNow()
		s.Status, s.CompletedAt, s.NextAttemptAt = "completed", &now, nil
		s.UpdatedAt = now
		if err := q.saveLocked(s); err != nil {
			return AliasQueueStatus{}, err
		}
		return publicQueueStatus(s), nil
	}
	now := unixNow()
	s.Status = "queued"
	s.NextAttemptAt = &now
	s.UpdatedAt = now
	if err := q.saveLocked(s); err != nil {
		return AliasQueueStatus{}, err
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return publicQueueStatus(s), nil
}
func (q *AliasQueue) Cancel(jobID string) (AliasQueueStatus, error) {
	return q.control(jobID, "cancelled")
}
func (q *AliasQueue) control(jobID, target string) (AliasQueueStatus, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := q.loadLocked()
	if s.JobID == "" || (jobID != "" && s.JobID != jobID) || s.Status == "completed" || s.Status == "cancelled" {
		return AliasQueueStatus{}, &AliasQueueError{"当前没有可控制的批量队列", "QUEUE_NOT_ACTIVE", true}
	}
	s.Status = target
	s.UpdatedAt = unixNow()
	s.NextAttemptAt = nil
	if target == "cancelled" {
		now := unixNow()
		s.CompletedAt = &now
	}
	cancel := q.cancel
	q.cancel = nil
	if err := q.saveLocked(s); err != nil {
		return AliasQueueStatus{}, err
	}
	if cancel != nil {
		cancel()
	}
	return publicQueueStatus(s), nil
}

func (q *AliasQueue) runIfDue() {
	q.mu.Lock()
	s := q.loadLocked()
	if s.JobID == "" || s.NextAttemptAt == nil || *s.NextAttemptAt > unixNow() {
		q.mu.Unlock()
		return
	}
	if s.Status == "waiting_rate_limit" || s.Status == "waiting_retry" {
		s.Status = "queued"
	}
	if s.Status != "queued" {
		q.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	q.cancel = cancel
	s.Status = "running"
	s.UpdatedAt = unixNow()
	_ = q.saveLocked(s)
	q.mu.Unlock()
	go q.run(ctx, s)
}

func (q *AliasQueue) run(ctx context.Context, initial AliasQueueStatus) {
	defer func() { q.mu.Lock(); q.cancel = nil; q.mu.Unlock() }()
	client, err := q.session.Client()
	if err != nil {
		q.fail(initial.JobID, err, "SESSION_ERROR", "needs_attention")
		return
	}
	if initial.AccountDSID != "" && client.config.DSID != initial.AccountDSID {
		q.fail(initial.JobID, errors.New("当前 Session 与队列绑定的 iCloud 账号不一致"), "QUEUE_ACCOUNT_MISMATCH", "needs_attention")
		return
	}
	for initial.Current < initial.Requested {
		select {
		case <-ctx.Done():
			q.mu.Lock()
			current := q.loadLocked()
			q.mu.Unlock()
			if current.Status != "cancelled" {
				q.fail(initial.JobID, ctx.Err(), "CANCELLED", "paused")
			}
			return
		default:
		}
		index := initial.Current + 1
		label := fmt.Sprintf("%s%02d", initial.BaseLabel, index)
		candidate := initial.CandidateHME
		var s AliasQueueStatus
		if candidate == "" {
			q.createGate.Lock()
			candidate, err = client.GenerateAlias(ctx)
			q.createGate.Unlock()
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				q.handleCreateError(initial.JobID, err, false)
				return
			}
			q.mu.Lock()
			s = q.loadLocked()
			s.CandidateHME, s.CandidateState = candidate, "generated"
			s.UpdatedAt = unixNow()
			_ = q.saveLocked(s)
			initial = s
			q.mu.Unlock()
		}
		q.mu.Lock()
		s = q.loadLocked()
		if s.Status == "cancelled" || s.Status == "paused" {
			q.mu.Unlock()
			return
		}
		s.CandidateState = "reserving"
		s.UpdatedAt = unixNow()
		_ = q.saveLocked(s)
		q.mu.Unlock()
		q.createGate.Lock()
		_, err = client.ReserveAlias(ctx, candidate, label, initial.Note)
		q.createGate.Unlock()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			q.handleCreateError(initial.JobID, err, true)
			return
		}
		q.mu.Lock()
		s = q.loadLocked()
		s.Current = index
		s.Success = index
		s.CandidateHME, s.CandidateState = "", ""
		s.UpdatedAt = unixNow()
		if index >= s.Requested {
			now := unixNow()
			s.CompletedAt = &now
			s.NextAttemptAt = nil
			s.Status = "completed"
		}
		_ = q.saveLocked(s)
		initial = s
		q.mu.Unlock()
		if index < initial.Requested {
			timer := time.NewTimer(defaultAliasQueueSpacing * time.Second)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return
			}
		}
	}
}

func (q *AliasQueue) handleCreateError(jobID string, err error, reserveStarted bool) {
	if strings.Contains(err.Error(), "-41015") {
		q.failWithDelay(jobID, err, "RATE_LIMITED", "waiting_rate_limit", aliasQueueCooldown)
		return
	}
	if strings.Contains(err.Error(), "-41003") {
		q.mu.Lock()
		s := q.loadLocked()
		s.CandidateHME, s.CandidateState = "", ""
		s.Status = "waiting_retry"
		s.LastErrorCode = "INVALID_CANDIDATE_RETRY"
		s.LastError = safeErrorText(err)
		next := unixNow() + 2
		s.NextAttemptAt = &next
		s.UpdatedAt = unixNow()
		_ = q.saveLocked(s)
		q.mu.Unlock()
		return
	}
	if isSessionExpired(err) {
		q.fail(jobID, err, "SESSION_EXPIRED", "needs_attention")
		return
	}
	if strings.Contains(strings.ToLower(err.Error()), "network") || strings.Contains(strings.ToLower(err.Error()), "timeout") {
		if reserveStarted {
			q.fail(jobID, err, "RESULT_CONFIRMATION_REQUIRED", "needs_attention")
		} else {
			q.failWithDelay(jobID, err, "NETWORK_RETRY", "waiting_retry", aliasQueueRetry)
		}
		return
	}
	q.fail(jobID, err, "CREATE_ERROR", "needs_attention")
}

func (q *AliasQueue) fail(jobID string, err error, code, state string) {
	q.failWithDelay(jobID, err, code, state, 0)
}
func (q *AliasQueue) failWithDelay(jobID string, err error, code, state string, delay time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := q.loadLocked()
	if s.JobID != jobID {
		return
	}
	s.Status = state
	s.LastErrorCode = code
	s.LastError = safeErrorText(err)
	s.UpdatedAt = unixNow()
	if delay > 0 {
		next := unixNow() + delay.Seconds()
		s.NextAttemptAt = &next
	} else {
		s.NextAttemptAt = nil
	}
	_ = q.saveLocked(s)
}

func (q *AliasQueue) loadLocked() AliasQueueStatus {
	var p aliasQueuePersisted
	if storage.ReadJSON(q.path, &p) != nil || p.Job == nil {
		return AliasQueueStatus{Status: "idle"}
	}
	return *p.Job
}
func (q *AliasQueue) saveLocked(s AliasQueueStatus) error {
	return storage.WriteJSON(q.path, aliasQueuePersisted{Job: &s}, 0o600)
}
func newID() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }
