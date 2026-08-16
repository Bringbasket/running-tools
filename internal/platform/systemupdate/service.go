package systemupdate

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

const staleAfter = 30 * time.Minute

var ErrInProgress = errors.New("system update is already in progress")
var ErrCheckRequired = errors.New("check for updates before requesting an update")
var ErrNoUpdateAvailable = errors.New("no update is available")

type Status struct {
	State            string   `json:"state"`
	Action           string   `json:"action,omitempty"`
	Message          string   `json:"message"`
	CurrentVersion   string   `json:"currentVersion"`
	LatestVersion    *string  `json:"latestVersion"`
	CurrentRevision  string   `json:"currentRevision"`
	LatestRevision   *string  `json:"latestRevision"`
	UpdateAvailable  *bool    `json:"updateAvailable"`
	RequestID        *string  `json:"requestId"`
	RequestedAt      *float64 `json:"requestedAt"`
	StartedAt        *float64 `json:"startedAt"`
	FinishedAt       *float64 `json:"finishedAt"`
	UpdatedAt        *float64 `json:"updatedAt"`
	Error            *string  `json:"error"`
	CanRequestUpdate bool     `json:"canRequestUpdate"`
	RepositoryURL    string   `json:"repositoryUrl"`
}

type requestFile struct {
	RequestID   string  `json:"requestId"`
	RequestedAt float64 `json:"requestedAt"`
	Action      string  `json:"action"`
}

type Service struct {
	mu            sync.Mutex
	stateDir      string
	version       string
	revision      string
	repositoryURL string
	now           func() time.Time
}

func New(stateDir, version, revision, repositoryURL string) *Service {
	version = strings.TrimSpace(version)
	if version == "" {
		version = "0.0.1"
	}
	revision = strings.TrimSpace(revision)
	if revision == "" {
		revision = "dev"
	}
	return &Service{
		stateDir:      stateDir,
		version:       version,
		revision:      revision,
		repositoryURL: repositoryURL,
		now:           time.Now,
	}
}

func (service *Service) Status() Status {
	statusPath := filepath.Join(service.stateDir, "update-status.json")
	status := Status{}
	_ = storage.ReadJSON(statusPath, &status)
	if status.State == "" {
		status.State = "idle"
		status.Message = "尚未检查更新"
	}
	status.CurrentVersion = service.version
	status.CurrentRevision = service.revision
	status.RepositoryURL = service.repositoryURL
	if status.LatestRevision != nil && strings.TrimSpace(*status.LatestRevision) != "" {
		available := strings.TrimSpace(*status.LatestRevision) != service.revision
		status.UpdateAvailable = &available
	} else {
		status.LatestVersion = nil
		status.LatestRevision = nil
		status.UpdateAvailable = nil
	}
	status.CanRequestUpdate = !service.active(status, fileModTime(statusPath))
	return status
}

func (service *Service) Check() (Status, error) {
	return service.enqueue("check")
}

func (service *Service) Request() (Status, error) {
	return service.enqueue("update")
}

func (service *Service) enqueue(action string) (Status, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	status := service.Status()
	if !status.CanRequestUpdate {
		return Status{}, ErrInProgress
	}
	if action == "update" {
		if status.UpdateAvailable == nil {
			return Status{}, ErrCheckRequired
		}
		if !*status.UpdateAvailable {
			return Status{}, ErrNoUpdateAvailable
		}
	}
	requestName := "update-request.json"
	if action == "check" {
		requestName = "check-request.json"
	}
	requestPath := filepath.Join(service.stateDir, requestName)
	if modified := fileModTime(requestPath); !modified.IsZero() && service.now().Sub(modified) < staleAfter {
		return Status{}, ErrInProgress
	}

	now := float64(service.now().UnixNano()) / float64(time.Second)
	requestID, err := secureID()
	if err != nil {
		return Status{}, fmt.Errorf("create update request ID: %w", err)
	}
	state, message := "check_queued", "检查请求已提交"
	if action == "update" {
		state, message = "update_queued", "更新请求已提交"
	}
	queued := Status{
		State:            state,
		Action:           action,
		Message:          message,
		CurrentVersion:   service.version,
		LatestVersion:    status.LatestVersion,
		CurrentRevision:  service.revision,
		LatestRevision:   status.LatestRevision,
		UpdateAvailable:  status.UpdateAvailable,
		RequestID:        &requestID,
		RequestedAt:      &now,
		UpdatedAt:        &now,
		CanRequestUpdate: false,
		RepositoryURL:    service.repositoryURL,
	}
	statusPath := filepath.Join(service.stateDir, "update-status.json")
	if err := storage.WriteJSON(statusPath, queued, 0o600); err != nil {
		return Status{}, err
	}
	if err := storage.WriteJSON(requestPath, requestFile{RequestID: requestID, RequestedAt: now, Action: action}, 0o600); err != nil {
		failureMessage := "无法写入宿主机更新请求文件"
		queued.State = "error"
		queued.Message = "请求提交失败"
		queued.Error = &failureMessage
		queued.FinishedAt = &now
		queued.CanRequestUpdate = true
		_ = storage.WriteJSON(statusPath, queued, 0o600)
		return Status{}, err
	}
	return queued, nil
}

func (service *Service) active(status Status, fallback time.Time) bool {
	switch status.State {
	case "check_queued", "checking", "update_queued", "updating", "queued", "building", "restarting":
	default:
		return false
	}
	updated := fallback
	if status.UpdatedAt != nil && *status.UpdatedAt > 0 {
		updated = time.Unix(0, int64(*status.UpdatedAt*float64(time.Second)))
	} else if status.RequestedAt != nil && *status.RequestedAt > 0 {
		updated = time.Unix(0, int64(*status.RequestedAt*float64(time.Second)))
	}
	return !updated.IsZero() && service.now().Sub(updated) < staleAfter
}

func fileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func secureID() (string, error) {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
