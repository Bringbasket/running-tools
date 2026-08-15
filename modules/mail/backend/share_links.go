package mail

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/persistence"
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
	mu        sync.Mutex
	path      string
	db        *sql.DB
	accountID string
}

type shareLinkCreateInput struct {
	Alias          string
	AliasCreatedAt float64
}

func NewShareLinkStore(stateDir string) *ShareLinkStore {
	return &ShareLinkStore{path: filepath.Join(stateDir, "share-links.json"), accountID: defaultMailAccountID}
}

func NewShareLinkStoreWithPersistence(stateDir, accountID string, service *persistence.Service) (*ShareLinkStore, error) {
	store := NewShareLinkStore(stateDir)
	store.accountID = strings.TrimSpace(accountID)
	if store.accountID == "" {
		store.accountID = defaultMailAccountID
	}
	if service != nil && service.Mode() != persistence.StorageJSON {
		store.db = service.DB()
		if err := store.importLegacy(); err != nil {
			return nil, err
		}
	}
	return store, nil
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

func (s *ShareLinkStore) importLegacy() error {
	if s.db == nil {
		return nil
	}
	marker := "mail-share-imported:" + s.accountID + ":" + filepath.Clean(s.path)
	var imported bool
	if err := s.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM running_state WHERE state_key = $1)`, marker).Scan(&imported); err != nil || imported {
		return err
	}
	state := shareState{}
	var data []byte
	stateErr := s.db.QueryRow(`SELECT value::text FROM running_state WHERE state_key = $1`, s.path).Scan(&data)
	if errors.Is(stateErr, sql.ErrNoRows) {
		data, stateErr = os.ReadFile(s.path)
	}
	if stateErr == nil {
		if err := json.Unmarshal(data, &state); err != nil {
			var links []ShareLink
			if legacyErr := json.Unmarshal(data, &links); legacyErr != nil {
				return fmt.Errorf("解析旧分享数据失败: %w", err)
			}
			state.Links = links
		}
	} else if !errors.Is(stateErr, os.ErrNotExist) {
		return stateErr
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	rollback := func(cause error) error { _ = tx.Rollback(); return cause }
	for _, link := range state.Links {
		if _, err := tx.Exec(`INSERT INTO mail_share_links (id, account_id, alias, token_hash, created_at, expires_at, last_used_at, revoked_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, link.ID, s.accountID, link.Alias, link.TokenHash,
			timeFromUnix(link.CreatedAt), nullableTime(link.ExpiresAt), nullableTime(link.LastUsedAt), nullableTime(link.RevokedAt)); err != nil {
			return rollback(err)
		}
	}
	for _, session := range state.Sessions {
		if _, err := tx.Exec(`INSERT INTO mail_share_sessions (token_hash, account_id, link_id, expires_at) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`,
			session.TokenHash, s.accountID, session.LinkID, timeFromUnix(session.ExpiresAt)); err != nil {
			return rollback(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO running_state (state_key, value, updated_at) VALUES ($1, 'true'::jsonb, NOW()) ON CONFLICT (state_key) DO NOTHING`, marker); err != nil {
		return rollback(err)
	}
	if _, err := tx.Exec(`DELETE FROM running_state WHERE state_key = $1`, s.path); err != nil {
		return rollback(err)
	}
	return tx.Commit()
}

func shareAlias(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func (s *ShareLinkStore) Create(alias string, expiresIn *int) (map[string]any, error) {
	items, err := s.CreateBatch([]shareLinkCreateInput{{Alias: alias}}, expiresIn)
	if err != nil {
		return nil, err
	}
	return items[0], nil
}

// CreateBatch creates all links under one lock and one database transaction.
// The raw token is returned only in the response maps; persistent state stores
// only its SHA-256 digest.
func (s *ShareLinkStore) CreateBatch(inputs []shareLinkCreateInput, expiresIn *int) ([]map[string]any, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("at least one alias is required")
	}
	if expiresIn != nil && (*expiresIn < 5*60 || *expiresIn > 365*24*3600) {
		return nil, fmt.Errorf("expiresInSeconds must be between 300 and 31536000")
	}
	now := unixNow()
	links := make([]ShareLink, 0, len(inputs))
	tokens := make([]string, 0, len(inputs))
	for _, input := range inputs {
		alias := shareAlias(input.Alias)
		if alias == "" {
			return nil, fmt.Errorf("alias is required")
		}
		token, err := newShareToken()
		if err != nil {
			return nil, err
		}
		var expires *float64
		if expiresIn != nil {
			value := now + float64(*expiresIn)
			expires = &value
		}
		links = append(links, ShareLink{ID: newID(), Alias: alias, TokenHash: hashShareToken(token), CreatedAt: now, ExpiresAt: expires})
		tokens = append(tokens, token)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		tx, err := s.db.Begin()
		if err != nil {
			return nil, err
		}
		for _, link := range links {
			if _, err := tx.Exec(`INSERT INTO mail_share_links (id, account_id, alias, token_hash, created_at, expires_at) VALUES ($1,$2,$3,$4,$5,$6)`,
				link.ID, s.accountID, link.Alias, link.TokenHash, timeFromUnix(link.CreatedAt), nullableTime(link.ExpiresAt)); err != nil {
				_ = tx.Rollback()
				return nil, err
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	} else {
		state := s.loadLocked()
		state.Links = append(state.Links, links...)
		if err := s.saveLocked(state); err != nil {
			return nil, err
		}
	}
	out := make([]map[string]any, 0, len(links))
	for index, link := range links {
		item := map[string]any{"id": link.ID, "alias": link.Alias, "createdAt": link.CreatedAt, "expiresAt": link.ExpiresAt,
			"shareUrl": "/mail?email=" + url.QueryEscape(link.Alias) + "&token=" + url.QueryEscape(tokens[index])}
		if inputs[index].AliasCreatedAt > 0 {
			item["aliasCreatedAt"] = inputs[index].AliasCreatedAt
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *ShareLinkStore) List(alias string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := unixNow()
	out := []map[string]any{}
	links := []ShareLink{}
	if s.db != nil {
		rows, err := s.db.Query(`SELECT id, alias, token_hash, created_at, expires_at, last_used_at, revoked_at FROM mail_share_links WHERE account_id=$1 AND alias=$2 ORDER BY created_at DESC`, s.accountID, shareAlias(alias))
		if err != nil {
			return out
		}
		defer rows.Close()
		for rows.Next() {
			if link, ok := scanShareLink(rows); ok {
				links = append(links, link)
			}
		}
	} else {
		links = s.loadLocked().Links
	}
	for _, link := range links {
		if link.Alias != shareAlias(alias) {
			continue
		}
		out = append(out, publicShareLink(link, now))
	}
	return out
}

func (s *ShareLinkStore) Revoke(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := unixNow()
	if s.db != nil {
		result, err := s.db.Exec(`UPDATE mail_share_links SET revoked_at=$3 WHERE account_id=$1 AND id=$2 AND revoked_at IS NULL`, s.accountID, id, timeFromUnix(now))
		if err != nil {
			return false
		}
		count, _ := result.RowsAffected()
		return count > 0
	}
	state := s.loadLocked()
	changed := false
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
	now := unixNow()
	if s.db != nil {
		result, err := s.db.Exec(`UPDATE mail_share_links SET revoked_at=$3 WHERE account_id=$1 AND alias=$2 AND revoked_at IS NULL`, s.accountID, alias, timeFromUnix(now))
		if err != nil {
			return 0
		}
		count, _ := result.RowsAffected()
		return int(count)
	}
	state := s.loadLocked()
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
	return s.resolveLocked(token)
}

func (s *ShareLinkStore) resolveLocked(token string) (ShareLink, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return ShareLink{}, false
	}
	now := unixNow()
	digest := hashShareToken(token)
	if s.db != nil {
		row := s.db.QueryRow(`SELECT id, alias, token_hash, created_at, expires_at, last_used_at, revoked_at FROM mail_share_links
			WHERE account_id=$1 AND token_hash=$2 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > NOW())`, s.accountID, digest)
		link, ok := scanShareLink(row)
		if !ok {
			return ShareLink{}, false
		}
		_, _ = s.db.Exec(`UPDATE mail_share_links SET last_used_at=$3 WHERE account_id=$1 AND id=$2`, s.accountID, link.ID, timeFromUnix(now))
		link.LastUsedAt = &now
		return link, true
	}
	state := s.loadLocked()
	for i := range state.Links {
		if state.Links[i].TokenHash == digest && state.Links[i].RevokedAt == nil && (state.Links[i].ExpiresAt == nil || *state.Links[i].ExpiresAt > now) {
			state.Links[i].LastUsedAt = &now
			_ = s.saveLocked(state)
			return state.Links[i], true
		}
	}
	return ShareLink{}, false
}

func (s *ShareLinkStore) CreateSession(token string, maxAge int) (string, ShareLink, bool) {
	if maxAge <= 0 {
		return "", ShareLink{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	link, ok := s.resolveLocked(token)
	if !ok {
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
	if s.db != nil {
		_, _ = s.db.Exec(`DELETE FROM mail_share_sessions WHERE account_id=$1 AND expires_at <= NOW()`, s.accountID)
		if _, err := s.db.Exec(`INSERT INTO mail_share_sessions (token_hash, account_id, link_id, expires_at) VALUES ($1,$2,$3,$4)`,
			hashShareToken(sessionToken), s.accountID, link.ID, timeFromUnix(expires)); err != nil {
			return "", ShareLink{}, false
		}
		return sessionToken, link, true
	}
	state := s.loadLocked()
	active := state.Sessions[:0]
	for _, item := range state.Sessions {
		if item.ExpiresAt > now {
			active = append(active, item)
		}
	}
	state.Sessions = append(active, shareSession{TokenHash: hashShareToken(sessionToken), LinkID: link.ID, ExpiresAt: expires})
	if err := s.saveLocked(state); err != nil {
		return "", ShareLink{}, false
	}
	return sessionToken, link, true
}

func (s *ShareLinkStore) ResolveSession(token string) (ShareLink, bool) {
	if strings.TrimSpace(token) == "" {
		return ShareLink{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := unixNow()
	if s.db != nil {
		row := s.db.QueryRow(`SELECT l.id, l.alias, l.token_hash, l.created_at, l.expires_at, l.last_used_at, l.revoked_at
			FROM mail_share_sessions s JOIN mail_share_links l ON l.id=s.link_id
			WHERE s.account_id=$1 AND s.token_hash=$2 AND s.expires_at > NOW() AND l.revoked_at IS NULL AND (l.expires_at IS NULL OR l.expires_at > NOW())`,
			s.accountID, hashShareToken(token))
		return scanShareLink(row)
	}
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

// ClearInactive permanently deletes expired sessions and revoked/expired links.
func (s *ShareLinkStore) ClearInactive() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := unixNow()
	if s.db != nil {
		tx, err := s.db.Begin()
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM mail_share_sessions WHERE account_id=$1 AND expires_at <= NOW()`, s.accountID); err != nil {
			_ = tx.Rollback()
			return 0, err
		}
		result, err := tx.Exec(`DELETE FROM mail_share_links WHERE account_id=$1 AND (revoked_at IS NOT NULL OR (expires_at IS NOT NULL AND expires_at <= NOW()))`, s.accountID)
		if err != nil {
			_ = tx.Rollback()
			return 0, err
		}
		count, _ := result.RowsAffected()
		return int(count), tx.Commit()
	}
	state := s.loadLocked()
	kept := state.Links[:0]
	removedIDs := map[string]bool{}
	for _, link := range state.Links {
		if link.RevokedAt != nil || link.ExpiresAt != nil && *link.ExpiresAt <= now {
			removedIDs[link.ID] = true
			continue
		}
		kept = append(kept, link)
	}
	state.Links = kept
	activeSessions := state.Sessions[:0]
	for _, session := range state.Sessions {
		if session.ExpiresAt > now && !removedIDs[session.LinkID] {
			activeSessions = append(activeSessions, session)
		}
	}
	state.Sessions = activeSessions
	return len(removedIDs), s.saveLocked(state)
}

type shareScanner interface {
	Scan(...any) error
}

func scanShareLink(scanner shareScanner) (ShareLink, bool) {
	var link ShareLink
	var created time.Time
	var expires, lastUsed, revoked sql.NullTime
	if err := scanner.Scan(&link.ID, &link.Alias, &link.TokenHash, &created, &expires, &lastUsed, &revoked); err != nil {
		return ShareLink{}, false
	}
	link.CreatedAt = unixFloat(created)
	link.ExpiresAt = nullableUnixPointer(expires)
	link.LastUsedAt = nullableUnixPointer(lastUsed)
	link.RevokedAt = nullableUnixPointer(revoked)
	return link, true
}

func publicShareLink(link ShareLink, now float64) map[string]any {
	return map[string]any{"id": link.ID, "alias": link.Alias, "createdAt": link.CreatedAt, "expiresAt": link.ExpiresAt,
		"lastUsedAt": link.LastUsedAt, "revokedAt": link.RevokedAt, "active": link.RevokedAt == nil && (link.ExpiresAt == nil || *link.ExpiresAt > now)}
}

func timeFromUnix(value float64) time.Time {
	seconds := int64(value)
	nanos := int64((value - float64(seconds)) * float64(time.Second))
	return time.Unix(seconds, nanos).UTC()
}

func nullableTime(value *float64) any {
	if value == nil {
		return nil
	}
	return timeFromUnix(*value)
}

func unixFloat(value time.Time) float64 {
	return float64(value.UnixNano()) / float64(time.Second)
}

func nullableUnixPointer(value sql.NullTime) *float64 {
	if !value.Valid {
		return nil
	}
	converted := unixFloat(value.Time)
	return &converted
}
