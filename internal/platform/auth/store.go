package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var errNotFound = errors.New("authentication record not found")

type userRecord struct {
	ID                 int64
	Username           string
	PasswordHash       string
	MustChangePassword bool
	Disabled           bool
	CreatedAt          time.Time
	LastLoginAt        *time.Time
}

type principalRecord struct {
	User      userRecord
	TokenHash string
	Kind      string
	LastSeen  time.Time
	ExpiresAt *time.Time
}

type apiTokenRecord struct {
	ID         string
	Name       string
	Prefix     string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
}

type store interface {
	BootstrapUser(context.Context, string, string) (bool, error)
	FindUserByUsername(context.Context, string) (userRecord, error)
	FindUserByID(context.Context, int64) (userRecord, error)
	CreateSession(context.Context, string, int64, string, string, time.Time) error
	FindSession(context.Context, string, time.Time) (principalRecord, error)
	TouchSession(context.Context, string, time.Time) error
	RevokeSession(context.Context, string, time.Time) error
	UpdatePassword(context.Context, int64, string, string, time.Time) error
	SetLastLogin(context.Context, int64, time.Time) error
	RecordLogin(context.Context, *int64, string, string, string, string, string, time.Time) error
	CreateAPIToken(context.Context, apiTokenInsert) error
	FindAPIToken(context.Context, string, time.Time) (principalRecord, error)
	TouchAPIToken(context.Context, string, time.Time) error
	ListAPITokens(context.Context, int64, time.Time) ([]apiTokenRecord, error)
	RevokeAPIToken(context.Context, int64, string, time.Time) error
	DeleteExpired(context.Context, time.Time) error
}

type apiTokenInsert struct {
	ID        string
	UserID    int64
	Name      string
	Hash      string
	Prefix    string
	CreatedAt time.Time
	ExpiresAt *time.Time
}

type sqlStore struct{ db *sql.DB }

func (s *sqlStore) BootstrapUser(ctx context.Context, username, passwordHash string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", int64(74691920260816)); err != nil {
		return false, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM auth_users").Scan(&count); err != nil {
		return false, err
	}
	if count > 0 {
		// Upgrade installations that initialized the account from the retired API
		// key while it is still waiting for the mandatory first password change.
		if _, err := tx.ExecContext(ctx, `UPDATE auth_users SET password_hash = $2, updated_at = NOW() WHERE username = $1 AND must_change_password = TRUE`, username, passwordHash); err != nil {
			return false, err
		}
		return false, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_users (username, password_hash, must_change_password) VALUES ($1, $2, TRUE)`, username, passwordHash); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

const userColumns = `id, username, password_hash, must_change_password, disabled, created_at, last_login_at`

func scanUser(row interface{ Scan(...any) error }) (userRecord, error) {
	var result userRecord
	err := row.Scan(&result.ID, &result.Username, &result.PasswordHash, &result.MustChangePassword, &result.Disabled, &result.CreatedAt, &result.LastLoginAt)
	if errors.Is(err, sql.ErrNoRows) {
		return userRecord{}, errNotFound
	}
	return result, err
}

func (s *sqlStore) FindUserByUsername(ctx context.Context, username string) (userRecord, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM auth_users WHERE username = $1`, username))
}

func (s *sqlStore) FindUserByID(ctx context.Context, id int64) (userRecord, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM auth_users WHERE id = $1`, id))
}

func (s *sqlStore) CreateSession(ctx context.Context, hash string, userID int64, ip, userAgent string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO auth_sessions (token_hash, user_id, ip_address, user_agent, expires_at) VALUES ($1, $2, $3, $4, $5)`, hash, userID, ip, userAgent, expiresAt)
	return err
}

func (s *sqlStore) FindSession(ctx context.Context, hash string, now time.Time) (principalRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+userColumnsWithPrefix("u")+`, s.last_seen_at, s.expires_at
FROM auth_sessions s JOIN auth_users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND s.expires_at > $2`, hash, now)
	var result principalRecord
	err := row.Scan(&result.User.ID, &result.User.Username, &result.User.PasswordHash, &result.User.MustChangePassword, &result.User.Disabled, &result.User.CreatedAt, &result.User.LastLoginAt, &result.LastSeen, &result.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return principalRecord{}, errNotFound
	}
	result.TokenHash = hash
	result.Kind = credentialSession
	return result, err
}

func userColumnsWithPrefix(prefix string) string {
	return prefix + `.id, ` + prefix + `.username, ` + prefix + `.password_hash, ` + prefix + `.must_change_password, ` + prefix + `.disabled, ` + prefix + `.created_at, ` + prefix + `.last_login_at`
}

func (s *sqlStore) TouchSession(ctx context.Context, hash string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE auth_sessions SET last_seen_at = $2 WHERE token_hash = $1 AND revoked_at IS NULL`, hash, now)
	return err
}

func (s *sqlStore) RevokeSession(ctx context.Context, hash string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at = COALESCE(revoked_at, $2) WHERE token_hash = $1`, hash, now)
	return err
}

func (s *sqlStore) UpdatePassword(ctx context.Context, userID int64, passwordHash, currentSessionHash string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE auth_users SET password_hash = $2, must_change_password = FALSE, updated_at = $3 WHERE id = $1`, userID, passwordHash, now)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at = $3 WHERE user_id = $1 AND token_hash <> $2 AND revoked_at IS NULL`, userID, currentSessionHash, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_api_tokens SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`, userID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqlStore) SetLastLogin(ctx context.Context, userID int64, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE auth_users SET last_login_at = $2 WHERE id = $1`, userID, now)
	return err
}

func (s *sqlStore) RecordLogin(ctx context.Context, userID *int64, username, outcome, reason, ip, userAgent string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO auth_login_events (user_id, username, outcome, reason, ip_address, user_agent, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`, userID, username, outcome, reason, ip, userAgent, now)
	return err
}

func (s *sqlStore) CreateAPIToken(ctx context.Context, input apiTokenInsert) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO auth_api_tokens (id, user_id, name, token_hash, token_prefix, created_at, expires_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`, input.ID, input.UserID, input.Name, input.Hash, input.Prefix, input.CreatedAt, input.ExpiresAt)
	return err
}

func (s *sqlStore) FindAPIToken(ctx context.Context, hash string, now time.Time) (principalRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+userColumnsWithPrefix("u")+`, COALESCE(t.last_used_at, t.created_at), t.expires_at
FROM auth_api_tokens t JOIN auth_users u ON u.id = t.user_id
WHERE t.token_hash = $1 AND t.revoked_at IS NULL AND (t.expires_at IS NULL OR t.expires_at > $2)`, hash, now)
	var result principalRecord
	err := row.Scan(&result.User.ID, &result.User.Username, &result.User.PasswordHash, &result.User.MustChangePassword, &result.User.Disabled, &result.User.CreatedAt, &result.User.LastLoginAt, &result.LastSeen, &result.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return principalRecord{}, errNotFound
	}
	result.TokenHash = hash
	result.Kind = credentialAPIToken
	return result, err
}

func (s *sqlStore) TouchAPIToken(ctx context.Context, hash string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE auth_api_tokens SET last_used_at = $2 WHERE token_hash = $1 AND revoked_at IS NULL`, hash, now)
	return err
}

func (s *sqlStore) ListAPITokens(ctx context.Context, userID int64, now time.Time) ([]apiTokenRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, token_prefix, created_at, last_used_at, expires_at
FROM auth_api_tokens WHERE user_id = $1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > $2)
ORDER BY created_at DESC`, userID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]apiTokenRecord, 0)
	for rows.Next() {
		var item apiTokenRecord
		if err := rows.Scan(&item.ID, &item.Name, &item.Prefix, &item.CreatedAt, &item.LastUsedAt, &item.ExpiresAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *sqlStore) RevokeAPIToken(ctx context.Context, userID int64, id string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE auth_api_tokens SET revoked_at = $3 WHERE user_id = $1 AND id = $2 AND revoked_at IS NULL`, userID, id, now)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errNotFound
	}
	return nil
}

func (s *sqlStore) DeleteExpired(ctx context.Context, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE expires_at <= $1::timestamptz OR revoked_at < ($1::timestamptz - INTERVAL '7 days')`, now); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM auth_api_tokens WHERE expires_at <= $1::timestamptz OR revoked_at < ($1::timestamptz - INTERVAL '30 days')`, now); err != nil {
		return fmt.Errorf("delete expired API tokens: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM auth_login_events WHERE created_at < ($1::timestamptz - INTERVAL '90 days')`, now); err != nil {
		return fmt.Errorf("delete old login events: %w", err)
	}
	return nil
}
