package auth

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type memoryStore struct {
	user     userRecord
	sessions map[string]principalRecord
	tokens   map[string]principalRecord
}

func newMemoryStore(t *testing.T, mustChange bool) *memoryStore {
	t.Helper()
	hash, err := hashPassword("initial-password-123")
	if err != nil {
		t.Fatal(err)
	}
	return &memoryStore{
		user:     userRecord{ID: 1, Username: "admin", PasswordHash: hash, MustChangePassword: mustChange, CreatedAt: time.Now().UTC()},
		sessions: make(map[string]principalRecord),
		tokens:   make(map[string]principalRecord),
	}
}

func (m *memoryStore) BootstrapUser(context.Context, string, string) (bool, error) { return false, nil }
func (m *memoryStore) FindUserByUsername(_ context.Context, username string) (userRecord, error) {
	if username != m.user.Username {
		return userRecord{}, errNotFound
	}
	return m.user, nil
}
func (m *memoryStore) FindUserByID(_ context.Context, id int64) (userRecord, error) {
	if id != m.user.ID {
		return userRecord{}, errNotFound
	}
	return m.user, nil
}
func (m *memoryStore) CreateSession(_ context.Context, hash string, _ int64, _, _ string, expires time.Time) error {
	m.sessions[hash] = principalRecord{User: m.user, TokenHash: hash, Kind: credentialSession, LastSeen: time.Now().UTC(), ExpiresAt: &expires}
	return nil
}
func (m *memoryStore) FindSession(_ context.Context, hash string, now time.Time) (principalRecord, error) {
	record, ok := m.sessions[hash]
	if !ok || record.ExpiresAt == nil || !record.ExpiresAt.After(now) {
		return principalRecord{}, errNotFound
	}
	record.User = m.user
	return record, nil
}
func (m *memoryStore) TouchSession(context.Context, string, time.Time) error { return nil }
func (m *memoryStore) RevokeSession(_ context.Context, hash string, _ time.Time) error {
	delete(m.sessions, hash)
	return nil
}
func (m *memoryStore) UpdatePassword(_ context.Context, _ int64, hash, _ string, _ time.Time) error {
	m.user.PasswordHash = hash
	m.user.MustChangePassword = false
	return nil
}
func (m *memoryStore) SetLastLogin(_ context.Context, _ int64, now time.Time) error {
	m.user.LastLoginAt = &now
	return nil
}
func (m *memoryStore) RecordLogin(context.Context, *int64, string, string, string, string, string, time.Time) error {
	return nil
}
func (m *memoryStore) CreateAPIToken(_ context.Context, input apiTokenInsert) error {
	m.tokens[input.Hash] = principalRecord{User: m.user, TokenHash: input.Hash, Kind: credentialAPIToken, LastSeen: input.CreatedAt, ExpiresAt: input.ExpiresAt}
	return nil
}
func (m *memoryStore) FindAPIToken(_ context.Context, hash string, now time.Time) (principalRecord, error) {
	record, ok := m.tokens[hash]
	if !ok || (record.ExpiresAt != nil && !record.ExpiresAt.After(now)) {
		return principalRecord{}, errNotFound
	}
	return record, nil
}
func (m *memoryStore) TouchAPIToken(context.Context, string, time.Time) error { return nil }
func (m *memoryStore) ListAPITokens(context.Context, int64, time.Time) ([]apiTokenRecord, error) {
	return nil, nil
}
func (m *memoryStore) RevokeAPIToken(context.Context, int64, string, time.Time) error {
	return errNotFound
}
func (m *memoryStore) DeleteExpired(context.Context, time.Time) error { return nil }

func testService(t *testing.T, mustChange bool) (*Service, *memoryStore) {
	t.Helper()
	store := newMemoryStore(t, mustChange)
	service, err := newServiceWithStore(store, newLoginLimiter(nil, "test:"), Config{AdminUsername: "admin", SessionTTL: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return service, store
}

func TestPasswordHashUsesArgon2id(t *testing.T) {
	hash, err := hashPassword("correct-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(hash, "correct-password-123") {
		t.Fatal("correct password was rejected")
	}
	if verifyPassword(hash, "wrong-password") {
		t.Fatal("wrong password was accepted")
	}
}

func TestLoginCreatesOpaqueSession(t *testing.T) {
	service, store := testService(t, true)
	result, err := service.Login(context.Background(), "ADMIN", "initial-password-123", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Token == "" || len(store.sessions) != 1 {
		t.Fatal("session was not created")
	}
	if !result.User.MustChangePassword {
		t.Fatal("bootstrap password-change requirement was lost")
	}
	if _, err := service.Login(context.Background(), "admin", "incorrect", "127.0.0.2", "test"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unexpected login error: %v", err)
	}
}

func TestProtectAPIsFailsClosedAndAllowsAuthEntryPoints(t *testing.T) {
	service, _ := testService(t, false)
	handler := service.ProtectAPIs(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	publicRequest := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	publicResponse := httptest.NewRecorder()
	handler.ServeHTTP(publicResponse, publicRequest)
	if publicResponse.Code != http.StatusNoContent {
		t.Fatalf("public auth status returned %d", publicResponse.Code)
	}

	privateRequest := httptest.NewRequest(http.MethodGet, "/api/future-route", nil)
	privateResponse := httptest.NewRecorder()
	handler.ServeHTTP(privateResponse, privateRequest)
	if privateResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unregistered protected API returned %d", privateResponse.Code)
	}
}

func TestInitialPasswordBlocksBusinessAPIs(t *testing.T) {
	service, _ := testService(t, true)
	login, err := service.Login(context.Background(), "admin", "initial-password-123", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	handler := service.ProtectAPIs(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	business := httptest.NewRequest(http.MethodGet, "/api/system/version", nil)
	business.AddCookie(&http.Cookie{Name: cookieName, Value: login.Token})
	businessResponse := httptest.NewRecorder()
	handler.ServeHTTP(businessResponse, business)
	if businessResponse.Code != http.StatusForbidden {
		t.Fatalf("business API returned %d", businessResponse.Code)
	}

	password := httptest.NewRequest(http.MethodPut, "/api/auth/password", nil)
	password.AddCookie(&http.Cookie{Name: cookieName, Value: login.Token})
	passwordResponse := httptest.NewRecorder()
	handler.ServeHTTP(passwordResponse, password)
	if passwordResponse.Code != http.StatusNoContent {
		t.Fatalf("password endpoint returned %d", passwordResponse.Code)
	}
}

func TestLoginHTTPUsesHardenedCookie(t *testing.T) {
	service, _ := testService(t, false)
	mux := http.NewServeMux()
	NewHTTP(service).RegisterRoutes(mux)
	request := httptest.NewRequest(http.MethodPost, "http://example.com/api/auth/login", bytes.NewBufferString(`{"username":"admin","password":"initial-password-123"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://example.com")
	request.RemoteAddr = "127.0.0.1:12345"
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login returned %d: %s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != cookieName {
		t.Fatal("session cookie was not set")
	}
	if !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" {
		t.Fatalf("unsafe session cookie attributes: %#v", cookies[0])
	}
}

func TestAPITokenAuthenticatesWithoutExposingSession(t *testing.T) {
	service, _ := testService(t, false)
	login, err := service.Login(context.Background(), "admin", "initial-password-123", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	request.AddCookie(&http.Cookie{Name: cookieName, Value: login.Token})
	principal, err := service.Authenticate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateAPIToken(context.Background(), principal, "test token", 30)
	if err != nil {
		t.Fatal(err)
	}
	if created.Token == "" || created.Prefix == created.Token {
		t.Fatal("API token response or prefix is invalid")
	}
	bearerRequest := httptest.NewRequest(http.MethodGet, "/api/system/version", nil)
	bearerRequest.Header.Set("Authorization", "Bearer "+created.Token)
	bearerPrincipal, err := service.Authenticate(context.Background(), bearerRequest)
	if err != nil {
		t.Fatal(err)
	}
	if bearerPrincipal.Credential != credentialAPIToken || bearerPrincipal.User.Username != "admin" {
		t.Fatalf("unexpected bearer principal: %#v", bearerPrincipal)
	}
}
