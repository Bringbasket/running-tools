package mail

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

type ShareLink struct {
	ID         string   `json:"id"`
	Alias      string   `json:"alias"`
	TokenHash  string   `json:"tokenHash"`
	CreatedAt  float64  `json:"createdAt"`
	ExpiresAt  *float64 `json:"expiresAt"`
	LastUsedAt *float64 `json:"lastUsedAt"`
	RevokedAt  *float64 `json:"revokedAt"`
}

type shareSession struct {
	TokenHash string  `json:"tokenHash"`
	LinkID    string  `json:"linkId"`
	ExpiresAt float64 `json:"expiresAt"`
}

type shareState struct {
	Links    []ShareLink    `json:"links"`
	Sessions []shareSession `json:"sessions"`
}

type ShareLinkStore struct {
	mu   sync.Mutex
	path string
}

func NewShareLinkStore(stateDir string) *ShareLinkStore {
	return &ShareLinkStore{path: filepath.Join(stateDir, "share-links.json")}
}
func hashShareToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum[:])
}
func newShareToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func (s *ShareLinkStore) loadLocked() shareState {
	var state shareState
	if storage.ReadJSON(s.path, &state) == nil {
		return state
	}
	var links []ShareLink
	if storage.ReadJSON(s.path, &links) == nil {
		return shareState{Links: links}
	}
	return shareState{}
}
func (s *ShareLinkStore) saveLocked(state shareState) error {
	return storage.WriteJSON(s.path, state, 0o600)
}
func shareAlias(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
func (s *ShareLinkStore) Create(alias string, expiresIn *int) (map[string]any, error) {
	alias = shareAlias(alias)
	if alias == "" {
		return nil, fmt.Errorf("alias is required")
	}
	token, err := newShareToken()
	if err != nil {
		return nil, err
	}
	now := unixNow()
	var expires *float64
	if expiresIn != nil {
		if *expiresIn < 5*60 || *expiresIn > 365*24*3600 {
			return nil, fmt.Errorf("expiresInSeconds must be between 300 and 31536000")
		}
		value := now + float64(*expiresIn)
		expires = &value
	}
	link := ShareLink{ID: newID(), Alias: alias, TokenHash: hashShareToken(token), CreatedAt: now, ExpiresAt: expires}
	s.mu.Lock()
	state := s.loadLocked()
	state.Links = append(state.Links, link)
	err = s.saveLocked(state)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": link.ID, "alias": alias, "createdAt": now, "expiresAt": expires, "shareUrl": "/share/#" + token}, nil
}
func (s *ShareLinkStore) List(alias string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := unixNow()
	out := []map[string]any{}
	for _, l := range s.loadLocked().Links {
		if l.Alias != shareAlias(alias) {
			continue
		}
		out = append(out, map[string]any{"id": l.ID, "alias": l.Alias, "createdAt": l.CreatedAt, "expiresAt": l.ExpiresAt, "lastUsedAt": l.LastUsedAt, "revokedAt": l.RevokedAt, "active": l.RevokedAt == nil && (l.ExpiresAt == nil || *l.ExpiresAt > now)})
	}
	return out
}
func (s *ShareLinkStore) Revoke(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.loadLocked()
	changed := false
	now := unixNow()
	for i := range state.Links {
		if state.Links[i].ID == id && state.Links[i].RevokedAt == nil {
			state.Links[i].RevokedAt = &now
			changed = true
		}
	}
	if changed {
		_ = s.saveLocked(state)
	}
	return changed
}

func (s *ShareLinkStore) RevokeForAlias(alias string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	alias = shareAlias(alias)
	state := s.loadLocked()
	now := unixNow()
	count := 0
	for i := range state.Links {
		if state.Links[i].Alias == alias && state.Links[i].RevokedAt == nil {
			state.Links[i].RevokedAt = &now
			count++
		}
	}
	if count > 0 {
		_ = s.saveLocked(state)
	}
	return count
}
func (s *ShareLinkStore) Resolve(token string) (ShareLink, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token = strings.TrimSpace(token)
	if token == "" {
		return ShareLink{}, false
	}
	now := unixNow()
	state := s.loadLocked()
	for i := range state.Links {
		if state.Links[i].TokenHash == hashShareToken(token) && state.Links[i].RevokedAt == nil && (state.Links[i].ExpiresAt == nil || *state.Links[i].ExpiresAt > now) {
			state.Links[i].LastUsedAt = &now
			_ = s.saveLocked(state)
			return state.Links[i], true
		}
	}
	return ShareLink{}, false
}

func (s *ShareLinkStore) CreateSession(token string, maxAge int) (string, ShareLink, bool) {
	link, ok := s.Resolve(token)
	if !ok || maxAge <= 0 {
		return "", ShareLink{}, false
	}
	sessionToken, err := newShareToken()
	if err != nil {
		return "", ShareLink{}, false
	}
	now := unixNow()
	expires := now + float64(maxAge)
	if link.ExpiresAt != nil && *link.ExpiresAt < expires {
		expires = *link.ExpiresAt
	}
	if expires <= now {
		return "", ShareLink{}, false
	}
	s.mu.Lock()
	state := s.loadLocked()
	active := state.Sessions[:0]
	for _, item := range state.Sessions {
		if item.ExpiresAt > now {
			active = append(active, item)
		}
	}
	state.Sessions = append(active, shareSession{TokenHash: hashShareToken(sessionToken), LinkID: link.ID, ExpiresAt: expires})
	if err := s.saveLocked(state); err != nil {
		s.mu.Unlock()
		return "", ShareLink{}, false
	}
	s.mu.Unlock()
	return sessionToken, link, true
}

func (s *ShareLinkStore) ResolveSession(token string) (ShareLink, bool) {
	if strings.TrimSpace(token) == "" {
		return ShareLink{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := unixNow()
	state := s.loadLocked()
	for _, session := range state.Sessions {
		if session.TokenHash != hashShareToken(token) || session.ExpiresAt <= now {
			continue
		}
		for _, link := range state.Links {
			if link.ID == session.LinkID && link.RevokedAt == nil && (link.ExpiresAt == nil || *link.ExpiresAt > now) {
				return link, true
			}
		}
	}
	return ShareLink{}, false
}
