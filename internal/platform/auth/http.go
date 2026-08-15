package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/httpx"
)

type HTTP struct{ service *Service }

func NewHTTP(service *Service) *HTTP { return &HTTP{service: service} }

func (api *HTTP) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/status", api.status)
	mux.HandleFunc("POST /api/auth/login", api.login)
	mux.HandleFunc("GET /api/auth/me", api.me)
	mux.HandleFunc("POST /api/auth/logout", api.logout)
	mux.HandleFunc("PUT /api/auth/password", api.changePassword)
	mux.HandleFunc("GET /api/auth/tokens", api.tokens)
	mux.HandleFunc("POST /api/auth/tokens", api.createToken)
	mux.HandleFunc("DELETE /api/auth/tokens/{id}", api.revokeToken)
}

func (api *HTTP) status(w http.ResponseWriter, r *http.Request) {
	principal, err := api.service.Authenticate(r.Context(), r)
	if err != nil {
		httpx.WriteData(w, r, http.StatusOK, map[string]any{"authenticated": false, "user": nil})
		return
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"authenticated": true, "user": principal.User})
}

func (api *HTTP) login(w http.ResponseWriter, r *http.Request) {
	if !api.service.sameOrigin(r) {
		httpx.WriteError(w, r, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源校验失败")
		return
	}
	input := struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{}
	if err := httpx.DecodeJSON(w, r, &input, 16<<10); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", "登录参数无效")
		return
	}
	result, err := api.service.Login(r.Context(), input.Username, input.Password, api.service.ClientIP(r), r.UserAgent())
	if err != nil {
		if errors.Is(err, ErrRateLimited) {
			w.Header().Set("Retry-After", "900")
			httpx.WriteError(w, r, http.StatusTooManyRequests, "LOGIN_RATE_LIMITED", err.Error())
			return
		}
		if errors.Is(err, ErrInvalidCredentials) {
			httpx.WriteError(w, r, http.StatusUnauthorized, "INVALID_CREDENTIALS", err.Error())
			return
		}
		httpx.WriteError(w, r, http.StatusInternalServerError, "AUTH_STORAGE_ERROR", "登录服务暂时不可用")
		return
	}
	api.setSessionCookie(w, r, result.Token, result.ExpiresAt)
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"user": result.User, "expiresAt": result.ExpiresAt})
}

func (api *HTTP) me(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", ErrUnauthorized.Error())
		return
	}
	httpx.WriteData(w, r, http.StatusOK, principal.User)
}

func (api *HTTP) logout(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	api.clearSessionCookie(w, r)
	if ok {
		if err := api.service.Logout(r.Context(), principal); err != nil {
			if errors.Is(err, ErrSessionRequired) {
				httpx.WriteError(w, r, http.StatusForbidden, "SESSION_REQUIRED", err.Error())
				return
			}
			httpx.WriteError(w, r, http.StatusInternalServerError, "AUTH_STORAGE_ERROR", "退出登录失败，会话将在到期后失效")
			return
		}
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]bool{"loggedOut": true})
}

func (api *HTTP) changePassword(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", ErrUnauthorized.Error())
		return
	}
	input := struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}{}
	if err := httpx.DecodeJSON(w, r, &input, 16<<10); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", "密码参数无效")
		return
	}
	user, err := api.service.ChangePassword(r.Context(), principal, input.CurrentPassword, input.NewPassword)
	if err != nil {
		status := http.StatusBadRequest
		code := "PASSWORD_INVALID"
		message := err.Error()
		if errors.Is(err, ErrStorage) {
			status, code, message = http.StatusInternalServerError, "AUTH_STORAGE_ERROR", "密码更新服务暂时不可用"
		} else if errors.Is(err, ErrUnauthorized) {
			status, code, message = http.StatusUnauthorized, "UNAUTHORIZED", ErrUnauthorized.Error()
		} else if errors.Is(err, ErrSessionRequired) {
			status, code = http.StatusForbidden, "SESSION_REQUIRED"
		}
		httpx.WriteError(w, r, status, code, message)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, user)
}

func (api *HTTP) tokens(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	items, err := api.service.ListAPITokens(r.Context(), principal)
	if err != nil {
		api.writeTokenError(w, r, err)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"items": items})
}

func (api *HTTP) createToken(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	input := struct {
		Name          string `json:"name"`
		ExpiresInDays int    `json:"expiresInDays"`
	}{}
	if err := httpx.DecodeJSON(w, r, &input, 16<<10); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", "令牌参数无效")
		return
	}
	result, err := api.service.CreateAPIToken(r.Context(), principal, input.Name, input.ExpiresInDays)
	if err != nil {
		api.writeTokenError(w, r, err)
		return
	}
	httpx.WriteData(w, r, http.StatusCreated, result)
}

func (api *HTTP) revokeToken(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	err := api.service.RevokeAPIToken(r.Context(), principal, r.PathValue("id"))
	if errors.Is(err, errNotFound) {
		httpx.WriteError(w, r, http.StatusNotFound, "TOKEN_NOT_FOUND", "访问令牌不存在")
		return
	}
	if err != nil {
		api.writeTokenError(w, r, err)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]bool{"revoked": true})
}

func (api *HTTP) writeTokenError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrSessionRequired) {
		httpx.WriteError(w, r, http.StatusForbidden, "SESSION_REQUIRED", err.Error())
		return
	}
	if strings.Contains(err.Error(), "令牌") {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	httpx.WriteError(w, r, http.StatusInternalServerError, "AUTH_STORAGE_ERROR", "访问令牌服务暂时不可用")
}

func (api *HTTP) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: requestIsHTTPS(r), SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: int(time.Until(expires).Seconds())})
}

func (api *HTTP) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: requestIsHTTPS(r), SameSite: http.SameSiteStrictMode, Expires: time.Unix(1, 0), MaxAge: -1})
}

func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
}

func (s *Service) ProtectAPIs(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		if isPublicAuthRoute(r.Method, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		principal, err := s.Authenticate(r.Context(), r)
		if err != nil {
			httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", ErrUnauthorized.Error())
			return
		}
		if principal.User.MustChangePassword && !passwordChangeAllowed(r.Method, r.URL.Path) {
			httpx.WriteError(w, r, http.StatusForbidden, "PASSWORD_CHANGE_REQUIRED", ErrPasswordRequired.Error())
			return
		}
		if principal.Credential == credentialSession && !isSafeMethod(r.Method) && !s.sameOrigin(r) {
			httpx.WriteError(w, r, http.StatusForbidden, "ORIGIN_REJECTED", "请求来源校验失败")
			return
		}
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), principal)))
	})
}

func isPublicAuthRoute(method, path string) bool {
	return (method == http.MethodGet && path == "/api/auth/status") || (method == http.MethodPost && path == "/api/auth/login")
}

func passwordChangeAllowed(method, path string) bool {
	return (method == http.MethodGet && path == "/api/auth/me") || (method == http.MethodPut && path == "/api/auth/password") || (method == http.MethodPost && path == "/api/auth/logout")
}

func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}
