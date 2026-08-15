package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	cookieName             = "running_session"
	defaultInitialPassword = "admin123"
	credentialSession      = "session"
	credentialAPIToken     = "api_token"
)

var (
	ErrInvalidCredentials = errors.New("账号或密码错误")
	ErrRateLimited        = errors.New("登录尝试过于频繁，请 15 分钟后再试")
	ErrUnauthorized       = errors.New("登录状态无效或已过期")
	ErrPasswordRequired   = errors.New("请先修改初始密码")
	ErrSessionRequired    = errors.New("此操作需要浏览器登录会话")
	ErrStorage            = errors.New("authentication storage error")
	usernamePattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9._@-]{2,63}$`)
)

type Config struct {
	AdminUsername string
	SessionTTL    time.Duration
	TrustProxy    bool
}

type User struct {
	ID                 int64      `json:"id"`
	Username           string     `json:"username"`
	MustChangePassword bool       `json:"mustChangePassword"`
	CreatedAt          time.Time  `json:"createdAt"`
	LastLoginAt        *time.Time `json:"lastLoginAt"`
}

type Principal struct {
	User       User
	TokenHash  string
	Credential string
}

type SessionResult struct {
	User      User      `json:"user"`
	ExpiresAt time.Time `json:"expiresAt"`
	Token     string    `json:"-"`
}

type APIToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`
}

type CreatedAPIToken struct {
	APIToken
	Token string `json:"token"`
}

type Service struct {
	store     store
	limiter   *loginLimiter
	config    Config
	dummyHash string
	now       func() time.Time
}

// NewService initializes authentication against the shared PostgreSQL and Redis services.
func NewService(db *sql.DB, redisClient *redis.Client, redisPrefix string, cfg Config) (*Service, error) {
	if db == nil {
		return nil, errors.New("authentication requires PostgreSQL")
	}
	if cfg.SessionTTL < time.Hour || cfg.SessionTTL > 30*24*time.Hour {
		return nil, errors.New("invalid authentication session TTL")
	}
	cfg.AdminUsername = normalizeUsername(cfg.AdminUsername)
	if !usernamePattern.MatchString(cfg.AdminUsername) {
		return nil, errors.New("invalid bootstrap administrator username")
	}
	dummyHash, err := hashPassword("running-tools-dummy-password")
	if err != nil {
		return nil, err
	}
	service := &Service{
		store:     &sqlStore{db: db},
		limiter:   newLoginLimiter(redisClient, redisPrefix),
		config:    cfg,
		dummyHash: dummyHash,
		now:       time.Now,
	}
	bootstrapContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := service.bootstrap(bootstrapContext); err != nil {
		return nil, err
	}
	return service, nil
}

func newServiceWithStore(store store, limiter *loginLimiter, cfg Config) (*Service, error) {
	dummyHash, err := hashPassword("running-tools-dummy-password")
	if err != nil {
		return nil, err
	}
	return &Service{store: store, limiter: limiter, config: cfg, dummyHash: dummyHash, now: time.Now}, nil
}

func (s *Service) bootstrap(ctx context.Context) error {
	hash, err := hashPassword(defaultInitialPassword)
	if err != nil {
		return err
	}
	if _, err := s.store.BootstrapUser(ctx, s.config.AdminUsername, hash); err != nil {
		return fmt.Errorf("bootstrap administrator: %w", err)
	}
	return nil
}

func normalizeUsername(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func publicUser(record userRecord) User {
	return User{ID: record.ID, Username: record.Username, MustChangePassword: record.MustChangePassword, CreatedAt: record.CreatedAt, LastLoginAt: record.LastLoginAt}
}

func (s *Service) Login(ctx context.Context, username, password, ip, userAgent string) (SessionResult, error) {
	now := s.now().UTC()
	username = normalizeUsername(username)
	ip = truncate(ip, 128)
	userAgent = truncate(userAgent, 500)
	if !s.limiter.allow(ctx, "ip:"+ip, now) {
		_ = s.store.RecordLogin(ctx, nil, truncate(username, 64), "blocked", "rate_limited", ip, userAgent, now)
		return SessionResult{}, ErrRateLimited
	}
	if !usernamePattern.MatchString(username) || len(password) == 0 || len(password) > 256 {
		_ = s.store.RecordLogin(ctx, nil, truncate(username, 64), "failure", "invalid_credentials", ip, userAgent, now)
		return SessionResult{}, ErrInvalidCredentials
	}
	if !s.limiter.allow(ctx, "user:"+username, now) {
		_ = s.store.RecordLogin(ctx, nil, username, "blocked", "rate_limited", ip, userAgent, now)
		return SessionResult{}, ErrRateLimited
	}
	record, err := s.store.FindUserByUsername(ctx, username)
	if err != nil && !errors.Is(err, errNotFound) {
		return SessionResult{}, err
	}
	passwordHash := s.dummyHash
	if err == nil {
		passwordHash = record.PasswordHash
	}
	valid := verifyPassword(passwordHash, password) && err == nil && !record.Disabled
	if !valid {
		var userID *int64
		if err == nil {
			userID = &record.ID
		}
		_ = s.store.RecordLogin(ctx, userID, username, "failure", "invalid_credentials", ip, userAgent, now)
		return SessionResult{}, ErrInvalidCredentials
	}
	s.limiter.clear(ctx, "user:"+username, "ip:"+ip)
	token, hash, err := randomToken(32, "")
	if err != nil {
		return SessionResult{}, err
	}
	expiresAt := now.Add(s.config.SessionTTL)
	if err := s.store.CreateSession(ctx, hash, record.ID, ip, userAgent, expiresAt); err != nil {
		return SessionResult{}, err
	}
	_ = s.store.SetLastLogin(ctx, record.ID, now)
	_ = s.store.RecordLogin(ctx, &record.ID, username, "success", "", ip, userAgent, now)
	record.LastLoginAt = &now
	return SessionResult{User: publicUser(record), ExpiresAt: expiresAt, Token: token}, nil
}

func (s *Service) Authenticate(ctx context.Context, request *http.Request) (Principal, error) {
	now := s.now().UTC()
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
		token := strings.TrimSpace(authorization[7:])
		if !strings.HasPrefix(token, "rtk_") {
			return Principal{}, ErrUnauthorized
		}
		record, err := s.store.FindAPIToken(ctx, tokenHash(token), now)
		if err != nil || record.User.Disabled || record.User.MustChangePassword {
			return Principal{}, ErrUnauthorized
		}
		if now.Sub(record.LastSeen) >= 5*time.Minute {
			_ = s.store.TouchAPIToken(ctx, record.TokenHash, now)
		}
		return Principal{User: publicUser(record.User), TokenHash: record.TokenHash, Credential: credentialAPIToken}, nil
	}
	cookie, err := request.Cookie(cookieName)
	if err != nil || cookie.Value == "" {
		return Principal{}, ErrUnauthorized
	}
	record, err := s.store.FindSession(ctx, tokenHash(cookie.Value), now)
	if err != nil || record.User.Disabled {
		return Principal{}, ErrUnauthorized
	}
	if now.Sub(record.LastSeen) >= 5*time.Minute {
		_ = s.store.TouchSession(ctx, record.TokenHash, now)
	}
	return Principal{User: publicUser(record.User), TokenHash: record.TokenHash, Credential: credentialSession}, nil
}

func (s *Service) ChangePassword(ctx context.Context, principal Principal, currentPassword, newPassword string) (User, error) {
	if principal.Credential != credentialSession {
		return User{}, ErrSessionRequired
	}
	record, err := s.store.FindUserByID(ctx, principal.User.ID)
	if err != nil {
		return User{}, ErrUnauthorized
	}
	if !verifyPassword(record.PasswordHash, currentPassword) {
		return User{}, errors.New("当前密码不正确")
	}
	if err := validateNewPassword(newPassword); err != nil {
		return User{}, err
	}
	if verifyPassword(record.PasswordHash, newPassword) {
		return User{}, errors.New("新密码不能与当前密码相同")
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return User{}, fmt.Errorf("%w: hash password", ErrStorage)
	}
	if err := s.store.UpdatePassword(ctx, record.ID, hash, principal.TokenHash, s.now().UTC()); err != nil {
		return User{}, fmt.Errorf("%w: update password", ErrStorage)
	}
	record.MustChangePassword = false
	return publicUser(record), nil
}

func (s *Service) Logout(ctx context.Context, principal Principal) error {
	if principal.Credential != credentialSession {
		return ErrSessionRequired
	}
	if err := s.store.RevokeSession(ctx, principal.TokenHash, s.now().UTC()); err != nil {
		return fmt.Errorf("%w: revoke session", ErrStorage)
	}
	return nil
}

func (s *Service) ListAPITokens(ctx context.Context, principal Principal) ([]APIToken, error) {
	if principal.Credential != credentialSession {
		return nil, ErrSessionRequired
	}
	records, err := s.store.ListAPITokens(ctx, principal.User.ID, s.now().UTC())
	if err != nil {
		return nil, fmt.Errorf("%w: list API tokens", ErrStorage)
	}
	result := make([]APIToken, 0, len(records))
	for _, record := range records {
		result = append(result, APIToken{ID: record.ID, Name: record.Name, Prefix: record.Prefix, CreatedAt: record.CreatedAt, LastUsedAt: record.LastUsedAt, ExpiresAt: record.ExpiresAt})
	}
	return result, nil
}

func (s *Service) CreateAPIToken(ctx context.Context, principal Principal, name string, expiresInDays int) (CreatedAPIToken, error) {
	if principal.Credential != credentialSession {
		return CreatedAPIToken{}, ErrSessionRequired
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return CreatedAPIToken{}, errors.New("令牌名称长度必须为 1 至 100 个字符")
	}
	if expiresInDays < 1 || expiresInDays > 365 {
		return CreatedAPIToken{}, errors.New("令牌有效期必须为 1 至 365 天")
	}
	id, _, err := randomToken(16, "tok_")
	if err != nil {
		return CreatedAPIToken{}, err
	}
	token, hash, err := randomToken(32, "rtk_")
	if err != nil {
		return CreatedAPIToken{}, err
	}
	now := s.now().UTC()
	expiresAt := now.Add(time.Duration(expiresInDays) * 24 * time.Hour)
	prefix := token
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	input := apiTokenInsert{ID: id, UserID: principal.User.ID, Name: name, Hash: hash, Prefix: prefix, CreatedAt: now, ExpiresAt: &expiresAt}
	if err := s.store.CreateAPIToken(ctx, input); err != nil {
		return CreatedAPIToken{}, fmt.Errorf("%w: create API token", ErrStorage)
	}
	return CreatedAPIToken{APIToken: APIToken{ID: id, Name: name, Prefix: prefix, CreatedAt: now, ExpiresAt: &expiresAt}, Token: token}, nil
}

func (s *Service) RevokeAPIToken(ctx context.Context, principal Principal, id string) error {
	if principal.Credential != credentialSession {
		return ErrSessionRequired
	}
	err := s.store.RevokeAPIToken(ctx, principal.User.ID, strings.TrimSpace(id), s.now().UTC())
	if errors.Is(err, errNotFound) {
		return err
	}
	if err != nil {
		return fmt.Errorf("%w: revoke API token", ErrStorage)
	}
	return nil
}

func (s *Service) Cleanup(ctx context.Context) error {
	return s.store.DeleteExpired(ctx, s.now().UTC())
}

func randomToken(size int, prefix string) (string, string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", "", err
	}
	token := prefix + base64.RawURLEncoding.EncodeToString(buffer)
	return token, tokenHash(token), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func truncate(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return value[:max]
	}
	return value
}

func (s *Service) ClientIP(request *http.Request) string {
	if s.config.TrustProxy {
		if forwarded := strings.TrimSpace(strings.Split(request.Header.Get("X-Forwarded-For"), ",")[0]); net.ParseIP(forwarded) != nil {
			return forwarded
		}
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil {
		return host
	}
	return request.RemoteAddr
}

func (s *Service) sameOrigin(request *http.Request) bool {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	host := request.Host
	if s.config.TrustProxy {
		if forwarded := strings.TrimSpace(strings.Split(request.Header.Get("X-Forwarded-Host"), ",")[0]); forwarded != "" {
			host = forwarded
		}
	}
	return strings.EqualFold(parsed.Host, host)
}

type principalContextKey struct{}

func withPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}
