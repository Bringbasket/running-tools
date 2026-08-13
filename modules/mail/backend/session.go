package mail

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

type SessionState struct {
	SessionValid  bool           `json:"sessionValid"`
	LastRefreshAt *float64       `json:"lastRefreshAt"`
	LastValidAt   *float64       `json:"lastValidAt"`
	ExpiresHint   string         `json:"expiresHint"`
	LastError     *string        `json:"lastError"`
	NeedsReauth   bool           `json:"needsReauth"`
	HME           map[string]any `json:"hme,omitempty"`
}

type SessionStatus struct {
	MetadataDetected bool      `json:"metadataDetected"`
	Metadata         *Metadata `json:"metadata"`
	PersistedSession bool      `json:"persistedSession"`
	ConfigPath       string    `json:"configPath"`
	ConfigUpdatedAt  *float64  `json:"configUpdatedAt"`
	LastSavedAt      *float64  `json:"lastSavedAt"`
	SessionState
}

type SessionManager struct {
	mu           sync.RWMutex
	configPath   string
	metadataPath string
	statePath    string
	newClient    func(ICloudConfig) (*Client, error)
}

func NewSessionManager(configPath, stateDir string) *SessionManager {
	return &SessionManager{
		configPath:   configPath,
		metadataPath: filepath.Join(stateDir, "hme-session.json"),
		statePath:    filepath.Join(stateDir, "session-state.json"),
		newClient:    func(config ICloudConfig) (*Client, error) { return NewClient(config) },
	}
}

func (manager *SessionManager) Client() (*Client, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	config, err := LoadICloudConfig(manager.configPath)
	if err != nil {
		return nil, err
	}
	return manager.newClient(config)
}

func (manager *SessionManager) Status() SessionStatus {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.statusLocked()
}

func (manager *SessionManager) Check(ctx context.Context) SessionStatus {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	status := manager.statusLocked()
	now := unixNow()
	config, err := LoadICloudConfig(manager.configPath)
	if err == nil {
		var client *Client
		client, err = manager.newClient(config)
		if err == nil {
			var hme map[string]any
			hme, err = client.Check(ctx)
			if err == nil {
				status.SessionState = SessionState{SessionValid: true, LastRefreshAt: &now, LastValidAt: &now, ExpiresHint: "apple-controlled", HME: hme}
			}
		}
	}
	if err != nil {
		message := safeErrorText(err)
		status.SessionValid = false
		status.LastRefreshAt = &now
		status.LastError = &message
		status.NeedsReauth = isSessionExpired(err)
		if errors.Is(err, ErrSessionMissing) {
			status.NeedsReauth = false
		}
	}
	_ = storage.WriteJSON(manager.statePath, status.SessionState, 0o600)
	return status
}

func (manager *SessionManager) Import(text, region string) (map[string]any, error) {
	config, err := ParseImportText(text, region)
	if err != nil {
		return nil, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := storage.WriteJSON(manager.configPath, config, 0o600); err != nil {
		return nil, err
	}
	if err := storage.WriteJSON(manager.metadataPath, config.Metadata(), 0o600); err != nil {
		return nil, err
	}
	state := SessionState{ExpiresHint: "apple-controlled", HME: nil}
	if err := storage.WriteJSON(manager.statePath, state, 0o600); err != nil {
		return nil, err
	}
	actualRegion := RegionInternational
	if filepath.Ext(config.Host) == ".cn" {
		actualRegion = RegionChina
	}
	return map[string]any{"imported": true, "icloudRegion": actualRegion, "host": config.Host}, nil
}

func (manager *SessionManager) statusLocked() SessionStatus {
	status := SessionStatus{ConfigPath: manager.configPath, SessionState: SessionState{ExpiresHint: "apple-controlled"}}
	metadata := Metadata{}
	if storage.ReadJSON(manager.metadataPath, &metadata) != nil {
		if config, err := LoadICloudConfig(manager.configPath); err == nil {
			metadata = config.Metadata()
		}
	}
	if metadata.Host != "" {
		status.MetadataDetected = true
		status.Metadata = &metadata
	}
	if _, err := LoadICloudConfig(manager.configPath); err == nil {
		status.PersistedSession = true
		if info, statErr := os.Stat(manager.configPath); statErr == nil {
			stamp := float64(info.ModTime().UnixNano()) / float64(time.Second)
			status.ConfigUpdatedAt, status.LastSavedAt = &stamp, &stamp
		}
	}
	stored := SessionState{}
	if storage.ReadJSON(manager.statePath, &stored) == nil {
		if stored.ExpiresHint == "" {
			stored.ExpiresHint = "apple-controlled"
		}
		status.SessionState = stored
	}
	return status
}

func unixNow() float64 { return float64(time.Now().UnixNano()) / float64(time.Second) }
