package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	appleRequestMaxAttempts       = 3
	appleRequestRetryBaseDelay    = 150 * time.Millisecond
	appleAccountFallbackManageTTL = 4 * time.Minute
)

// cloneAppleAccountState returns an isolated request working copy. Cookie
// slices are mutable, so a shallow struct copy would still let a failed
// response alter the persisted session.
func cloneAppleAccountState(state *AppleAccountState) AppleAccountState {
	if state == nil {
		return AppleAccountState{}
	}
	copyState := *state
	copyState.Cookies = append([]AppleSessionCookie(nil), state.Cookies...)
	return copyState
}

func (client *AppleAuthClient) httpClientSnapshot() *http.Client {
	client.httpMu.RLock()
	httpClient := client.httpClient
	client.httpMu.RUnlock()
	return httpClient
}

// doAppleHTTP executes one Apple request with bounded retries. Only transport
// failures and HTTP 5xx responses are retryable; authentication, rate-limit,
// client, and mutation-confirmation responses are never retried here.
func (client *AppleAuthClient) doAppleHTTP(ctx context.Context, method, rawURL string, headers map[string]string, body []byte, retryable bool) (*http.Response, []byte, error) {
	httpClient := client.httpClientSnapshot()
	if httpClient == nil {
		return nil, nil, errors.New("Apple HTTP 客户端未初始化")
	}
	for attempt := 1; attempt <= appleRequestMaxAttempts; attempt++ {
		request, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
		if err != nil {
			return nil, nil, err
		}
		for key, value := range headers {
			if strings.TrimSpace(value) != "" {
				request.Header.Set(key, value)
			}
		}
		response, err := httpClient.Do(request)
		if err != nil {
			if retryable && attempt < appleRequestMaxAttempts && isAppleTransientNetworkError(err) && ctx.Err() == nil {
				if err := waitAppleRetry(ctx, attempt); err != nil {
					return nil, nil, err
				}
				continue
			}
			return nil, nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		_ = response.Body.Close()
		if readErr != nil {
			if retryable && attempt < appleRequestMaxAttempts && isAppleTransientNetworkError(readErr) && ctx.Err() == nil {
				if err := waitAppleRetry(ctx, attempt); err != nil {
					return nil, nil, err
				}
				continue
			}
			return nil, nil, readErr
		}
		if retryable && attempt < appleRequestMaxAttempts && response.StatusCode >= 500 && response.StatusCode <= 599 && ctx.Err() == nil {
			if err := waitAppleRetry(ctx, attempt); err != nil {
				return nil, nil, err
			}
			continue
		}
		return response, data, nil
	}
	return nil, nil, errors.New("Apple 请求重试次数已用尽")
}

func waitAppleRetry(ctx context.Context, attempt int) error {
	delay := appleRequestRetryBaseDelay * time.Duration(1<<(attempt-1))
	if delay > time.Second {
		delay = time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isAppleTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{"timeout", "timed out", "connection reset", "connection refused", "eof", "temporary"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func appleAccountRequestRetryable(method, path string) bool {
	// Retry only idempotent reads and the pre-confirmation candidate request.
	// Once a mutating operation can change an existing alias (or confirmation
	// starts), a transport/5xx response is ambiguous and replaying it may apply
	// the action twice.
	if method == http.MethodGet || method == http.MethodHead {
		return true
	}
	return method == http.MethodPost && path == "/account/manage/email/private/add"
}

func (client *AppleAuthClient) finishICloudWeb(ctx context.Context, session *appleAuthSession) (ICloudConfig, error) {
	if session.SessionToken == "" {
		return ICloudConfig{}, appleProtocolError("APPLE_SESSION_TOKEN_MISSING", "Apple 登录没有返回 Session Token，请重新发起登录", true)
	}
	var account struct {
		DSInfo struct {
			DSID         string `json:"dsid"`
			AppleID      string `json:"appleId"`
			PrimaryEmail string `json:"primaryEmail"`
		} `json:"dsInfo"`
	}
	body := map[string]any{"accountCountryCode": session.AccountCountry, "dsWebAuthToken": session.SessionToken, "extended_login": true, "trustToken": session.TrustToken}
	headers := map[string]string{"Accept": "application/json", "Content-Type": "application/json", "Origin": session.Endpoints.Home, "Referer": session.Endpoints.Home + "/"}
	if _, _, err := client.do(ctx, session, http.MethodPost, session.Endpoints.Setup+"/accountLogin", headers, body, &account, false); err != nil {
		return ICloudConfig{}, err
	}
	config, err := client.validateICloudWeb(ctx, session)
	if err != nil {
		return ICloudConfig{}, err
	}
	config.AppleID = firstNonEmpty(account.DSInfo.AppleID, account.DSInfo.PrimaryEmail, session.AppleID)
	return config, nil
}

func (client *AppleAuthClient) validateICloudWeb(ctx context.Context, session *appleAuthSession) (ICloudConfig, error) {
	clientID, err := randomUUID()
	if err != nil {
		return ICloudConfig{}, err
	}
	requestID, err := randomUUID()
	if err != nil {
		return ICloudConfig{}, err
	}
	build := "2626Build21"
	setupHost := "setup.icloud.com"
	if strings.HasSuffix(session.Endpoints.Host, ".com.cn") {
		setupHost = "setup.icloud.com.cn"
	}
	parsed := &url.URL{Scheme: "https", Host: setupHost, Path: "/setup/ws/1/validate"}
	query := parsed.Query()
	query.Set("clientBuildNumber", build)
	query.Set("clientMasteringNumber", build)
	query.Set("clientId", clientID)
	query.Set("requestId", requestID)
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), nil)
	if err != nil {
		return ICloudConfig{}, err
	}
	request.Header.Set("Accept", "*/*")
	request.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	request.Header.Set("Origin", session.Endpoints.Home)
	request.Header.Set("Referer", session.Endpoints.Home+"/")
	request.Header.Set("User-Agent", appleAuthUserAgent)
	if cookie := appleCookieHeader(session.Cookies, parsed.String()); cookie != "" {
		request.Header.Set("Cookie", cookie)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return ICloudConfig{}, fmt.Errorf("iCloud 登录态校验失败: %w", err)
	}
	defer response.Body.Close()
	mergeAppleCookies(&session.Cookies, response.Request.URL, response.Cookies())
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return ICloudConfig{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ICloudConfig{}, appleProtocolError("ICLOUD_VALIDATE_FAILED", fmt.Sprintf("iCloud 登录态校验失败，HTTP %d", response.StatusCode), true)
	}
	var result struct {
		DSInfo struct {
			DSID         string `json:"dsid"`
			AppleID      string `json:"appleId"`
			PrimaryEmail string `json:"primaryEmail"`
		} `json:"dsInfo"`
		Webservices map[string]struct {
			URL string `json:"url"`
		} `json:"webservices"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return ICloudConfig{}, appleProtocolError("ICLOUD_VALIDATE_BAD_RESPONSE", "iCloud 登录态校验返回无法解析", true)
	}
	premiumURL := result.Webservices["premiummailsettings"].URL
	premium, err := url.Parse(premiumURL)
	if err != nil || premium.Hostname() == "" {
		return ICloudConfig{}, appleProtocolError("ICLOUD_HME_UNAVAILABLE", "当前账号没有返回隐藏邮件地址服务", false)
	}
	cookie := appleCookieHeader(session.Cookies, premiumURL)
	if cookie == "" {
		return ICloudConfig{}, appleProtocolError("ICLOUD_COOKIE_MISSING", "Apple 登录成功，但未生成可用的 iCloud Cookie", true)
	}
	config := ICloudConfig{Host: premium.Hostname(), DSID: result.DSInfo.DSID, ClientID: clientID, ClientBuildNumber: build, ClientMasteringNumber: build, Cookie: cookie, AppleID: firstNonEmpty(result.DSInfo.AppleID, result.DSInfo.PrimaryEmail, session.AppleID), UserAgent: appleAuthUserAgent}
	config.applyDefaults()
	return config, config.Validate()
}

func (client *AppleAuthClient) primeAppleAccount(ctx context.Context, session *appleAuthSession) error {
	state := &AppleAccountState{
		Host:    "appleid.apple.com",
		Origin:  "https://account.apple.com",
		Cookies: append([]AppleSessionCookie(nil), session.Cookies...),
	}
	if err := client.warmAppleAccount(ctx, state); err != nil {
		return err
	}
	session.Cookies = state.Cookies
	// The portal pages may return a page-scoped scnt. Apple expects the
	// pre-login manage-token request to omit it; only its response scnt is
	// carried into the SRP flow.
	tokenState := *state
	tokenState.Scnt = ""
	var token struct {
		TimeOutInterval int `json:"timeOutInterval"`
	}
	scnt, err := client.appleAccountRequest(ctx, &tokenState, http.MethodGet, "/account/manage/gs/ws/token", "", nil, &token)
	session.Cookies = tokenState.Cookies
	session.SessionID = firstNonEmpty(tokenState.SessionID, session.SessionID)
	if scnt != "" {
		session.ManageScnt = scnt
	}
	if err != nil && scnt != "" {
		return nil
	}
	return err
}

// refreshAppleAccountState refreshes the short-lived management state using an
// isolated working copy. A failed request never writes rotated cookies or
// headers into the caller's state.
func (client *AppleAuthClient) refreshAppleAccountState(ctx context.Context, state *AppleAccountState) error {
	if state == nil || len(state.Cookies) == 0 {
		return appleProtocolError("APPLE_ACCOUNT_SESSION_MISSING", "Apple Account session is missing", false)
	}
	working := cloneAppleAccountState(state)
	var token struct {
		TimeOutInterval int `json:"timeOutInterval"`
	}
	_, tokenErr := client.appleAccountRequest(ctx, &working, http.MethodGet, "/account/manage/gs/ws/token", "", nil, &token)
	if tokenErr != nil {
		if appleAccountRequiresReauth(tokenErr) {
			return tokenErr
		}
		if warmErr := client.warmAppleAccount(ctx, &working); warmErr != nil {
			if appleAccountRequiresReauth(warmErr) {
				return warmErr
			}
			return tokenErr
		}
		var retryToken struct {
			TimeOutInterval int `json:"timeOutInterval"`
		}
		if retryErr := client.refreshAppleAccountTokenWithoutScnt(ctx, &working, &retryToken); retryErr != nil {
			return retryErr
		} else {
			token = retryToken
		}
	} else if token.TimeOutInterval <= 0 {
		// A 2xx token response without a TTL is common during portal transitions.
		// Warm the portal and retry the token endpoint without page-scoped scnt.
		if warmErr := client.warmAppleAccount(ctx, &working); warmErr == nil {
			var retryToken struct {
				TimeOutInterval int `json:"timeOutInterval"`
			}
			if retryErr := client.refreshAppleAccountTokenWithoutScnt(ctx, &working, &retryToken); retryErr == nil {
				token = retryToken
			} else if appleAccountRequiresReauth(retryErr) {
				return retryErr
			}
		} else if appleAccountRequiresReauth(warmErr) {
			return warmErr
		}
	}
	updateAppleAccountExpiry(&working, token.TimeOutInterval)

	manageErr := client.loadAppleAccountAPIKey(ctx, &working)
	if manageErr != nil || strings.TrimSpace(working.APIKey) == "" {
		if manageErr != nil && appleAccountRequiresReauth(manageErr) {
			return manageErr
		}
		if warmErr := client.warmAppleAccount(ctx, &working); warmErr != nil {
			if appleAccountRequiresReauth(warmErr) {
				return warmErr
			}
			if manageErr != nil {
				return manageErr
			}
			return warmErr
		}
		var retryToken struct {
			TimeOutInterval int `json:"timeOutInterval"`
		}
		if retryErr := client.refreshAppleAccountTokenWithoutScnt(ctx, &working, &retryToken); retryErr != nil {
			return retryErr
		}
		updateAppleAccountExpiry(&working, retryToken.TimeOutInterval)
		manageErr = client.loadAppleAccountAPIKey(ctx, &working)
		if manageErr != nil {
			return manageErr
		}
	}
	if strings.TrimSpace(working.APIKey) == "" {
		return appleProtocolError("APPLE_ACCOUNT_API_KEY_MISSING", "Apple Account API key is missing", true)
	}
	if _, verifyErr := client.appleAccountRequest(ctx, &working, http.MethodGet, "/account/manage/forwardemail", working.APIKey, nil, nil); verifyErr != nil {
		return verifyErr
	}

	// jslogs is only a browser-session hint. It is deliberately best effort;
	// the real management endpoint above is the health check of record.
	jsCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	_, _ = client.appleAccountRequest(jsCtx, &working, http.MethodPost, "/v2/jslogs", working.APIKey, appleAccountJSLogBody(), nil)
	cancel()

	now := time.Now()
	if working.ManageExpiresAt.IsZero() || !working.ManageExpiresAt.After(now) {
		working.ManageExpiresAt = now.Add(appleAccountFallbackManageTTL)
	}
	markAppleAccountHealthy(&working, now)
	*state = working
	return nil
}

func (client *AppleAuthClient) refreshAppleAccountTokenWithoutScnt(ctx context.Context, state *AppleAccountState, result any) error {
	withoutScnt := *state
	withoutScnt.Scnt = ""
	scnt, err := client.appleAccountRequest(ctx, &withoutScnt, http.MethodGet, "/account/manage/gs/ws/token", "", nil, result)
	if err != nil {
		return err
	}
	*state = withoutScnt
	if scnt != "" {
		state.Scnt = scnt
	}
	return nil
}

func (client *AppleAuthClient) loadAppleAccountAPIKey(ctx context.Context, state *AppleAccountState) error {
	var manage struct {
		APIKey string `json:"apiKey"`
	}
	if _, err := client.appleAccountRequest(ctx, state, http.MethodGet, "/account/manage", "", nil, &manage); err != nil {
		return err
	}
	// Do not retain an old key when the manage response no longer contains one.
	state.APIKey = strings.TrimSpace(manage.APIKey)
	return nil
}

func (client *AppleAuthClient) ListWithAppleAccount(ctx context.Context, state AppleAccountState) ([]map[string]any, AppleAccountState, error) {
	if err := client.ensureAppleAccountReady(ctx, &state); err != nil {
		markAppleAccountFailure(&state, err, time.Now())
		return nil, state, err
	}
	var result struct {
		ForwardToEmailAddress string           `json:"forwardToEmailAddress"`
		PrivateEmailList      []map[string]any `json:"privateEmailList"`
		InactivePrivateEmails []map[string]any `json:"inactivePrivateEmailList"`
	}
	if _, err := client.appleAccountRequest(ctx, &state, http.MethodGet, "/account/manage/email/private", state.APIKey, nil, &result); err != nil {
		markAppleAccountFailure(&state, err, time.Now())
		return nil, state, err
	}
	aliases := make([]map[string]any, 0, len(result.PrivateEmailList)+len(result.InactivePrivateEmails))
	for _, item := range append(result.PrivateEmailList, result.InactivePrivateEmails...) {
		aliases = append(aliases, normalizeAppleAccountAlias(item, result.ForwardToEmailAddress))
	}
	markAppleAccountHealthy(&state, time.Now())
	return aliases, state, nil
}

func (client *AppleAuthClient) UpdateWithAppleAccount(ctx context.Context, state AppleAccountState, id, label, note string) (map[string]any, AppleAccountState, error) {
	if err := client.ensureAppleAccountReady(ctx, &state); err != nil {
		markAppleAccountFailure(&state, err, time.Now())
		return nil, state, err
	}
	path := "/account/manage/email/private/" + url.PathEscape(id) + "/note"
	payload := map[string]string{"id": id, "label": label, "note": note}
	if _, err := client.appleAccountRequest(ctx, &state, http.MethodPost, path, state.APIKey, payload, nil); err != nil {
		markAppleAccountFailure(&state, err, time.Now())
		return nil, state, err
	}
	markAppleAccountHealthy(&state, time.Now())
	return map[string]any{"anonymousId": id, "label": label, "note": note}, state, nil
}

func (client *AppleAuthClient) SetActiveWithAppleAccount(ctx context.Context, state AppleAccountState, id string, active bool) (map[string]any, AppleAccountState, error) {
	if err := client.ensureAppleAccountReady(ctx, &state); err != nil {
		markAppleAccountFailure(&state, err, time.Now())
		return nil, state, err
	}
	method, suffix := http.MethodDelete, "/stop"
	if active {
		method, suffix = http.MethodPost, "/reactivate"
	}
	path := "/account/manage/email/private/" + url.PathEscape(id) + suffix
	if _, err := client.appleAccountRequest(ctx, &state, method, path, state.APIKey, nil, nil); err != nil {
		markAppleAccountFailure(&state, err, time.Now())
		return nil, state, err
	}
	markAppleAccountHealthy(&state, time.Now())
	return map[string]any{"anonymousId": id, "isActive": active}, state, nil
}

func (client *AppleAuthClient) DeleteWithAppleAccount(ctx context.Context, state AppleAccountState, id string) (map[string]any, AppleAccountState, error) {
	if err := client.ensureAppleAccountReady(ctx, &state); err != nil {
		markAppleAccountFailure(&state, err, time.Now())
		return nil, state, err
	}
	path := "/account/manage/email/private/" + url.PathEscape(id) + "/remove"
	if _, err := client.appleAccountRequest(ctx, &state, http.MethodDelete, path, state.APIKey, nil, nil); err != nil {
		markAppleAccountFailure(&state, err, time.Now())
		return nil, state, err
	}
	markAppleAccountHealthy(&state, time.Now())
	return map[string]any{"anonymousId": id, "deleted": true}, state, nil
}

func (client *AppleAuthClient) ensureAppleAccountReady(ctx context.Context, state *AppleAccountState) error {
	if state == nil || len(state.Cookies) == 0 {
		return appleProtocolError("APPLE_ACCOUNT_SESSION_MISSING", "尚未登录 Apple Account", false)
	}
	if state.requiresReauth() {
		return appleProtocolError("APPLE_ACCOUNT_EXPIRED", "Apple Account 登录态已失效，请重新登录", false)
	}
	if state.HealthState == AppleAccountStateDegraded {
		return appleProtocolError("APPLE_ACCOUNT_DEGRADED", "Apple Account 暂时不可用，后台正在重试", true)
	}
	if !state.needsRefresh(time.Now()) {
		return nil
	}
	return client.refreshAppleAccountState(ctx, state)
}

func updateAppleAccountExpiry(state *AppleAccountState, timeoutMinutes int) {
	if state == nil || timeoutMinutes <= 0 {
		return
	}
	state.ManageExpiresAt = time.Now().Add(time.Duration(timeoutMinutes) * time.Minute)
}

func markAppleAccountHealthy(state *AppleAccountState, now time.Time) {
	if state == nil {
		return
	}
	state.SavedAt = now
	state.LastCheckedAt = now
	state.LastAttemptAt = now
	state.LastCheckOK = true
	state.HealthState = AppleAccountStateHealthy
	state.LastStatusMessage = "Apple Account 登录态正常"
}

// markAppleAccountFailure converts an operation error into persistent channel
// health without replacing cookies, scnt, or the last known-good API key.
// Transient failures retain LastCheckOK/LastCheckedAt so the worker can recover
// the same session; explicit authentication failures require a fresh login.
func markAppleAccountFailure(state *AppleAccountState, err error, now time.Time) {
	if state == nil || err == nil {
		return
	}
	state.LastAttemptAt = now
	if appleAccountRequiresReauth(err) {
		state.LastCheckedAt = now
		state.LastCheckOK = false
		state.HealthState = AppleAccountStateReauthRequired
		state.LastStatusMessage = "Apple Account 登录态已失效：" + safeErrorText(err)
		return
	}
	var protocol *AppleProtocolError
	if errors.As(err, &protocol) && protocol.Code == "APPLE_ACCOUNT_LIMIT" {
		// A creation quota response is a business limit, not evidence that the
		// management session is broken. Keep the healthy channel visible while
		// the separate create-channel cooldown controls the next attempt.
		state.LastStatusMessage = safeErrorText(err)
		return
	}
	state.HealthState = AppleAccountStateDegraded
	state.LastStatusMessage = "Apple Account 请求临时失败，将自动重试：" + safeErrorText(err)
}

func normalizeAppleAccountAlias(item map[string]any, defaultForward string) map[string]any {
	alias := map[string]any{
		"anonymousId":    strings.TrimSpace(fmt.Sprint(item["id"])),
		"hme":            strings.TrimSpace(fmt.Sprint(item["emailAddress"])),
		"label":          stringValue(item["label"]),
		"note":           stringValue(item["note"]),
		"forwardToEmail": firstNonEmpty(stringValue(item["forwardToEmail"]), defaultForward),
		"origin":         "APPLE_ACCOUNT",
		"isActive":       true,
	}
	if timestamp, ok := aliasTimestampFromMap(item); ok {
		alias["createTimestamp"] = timestamp
	}
	if active, ok := item["active"].(bool); ok {
		alias["isActive"] = active
	}
	return alias
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func (client *AppleAuthClient) warmAppleAccount(ctx context.Context, state *AppleAccountState) error {
	if _, err := client.appleAccountPortalRequest(ctx, state, "/account/manage/section/privacy", false); err != nil {
		return err
	}
	data, err := client.appleAccountPortalRequest(ctx, state, "/bootstrap/portal", true)
	if err != nil {
		return err
	}
	var portal struct {
		TimeOutInterval int `json:"timeOutInterval"`
	}
	if json.Unmarshal(data, &portal) == nil {
		updateAppleAccountExpiry(state, portal.TimeOutInterval)
	}
	return nil
}

func (client *AppleAuthClient) appleAccountPortalRequest(ctx context.Context, state *AppleAccountState, path string, jsonContent bool) ([]byte, error) {
	if state == nil {
		return nil, errors.New("Apple Account session is missing")
	}
	working := cloneAppleAccountState(state)
	rawURL := strings.TrimRight(firstNonEmpty(working.Origin, "https://account.apple.com"), "/") + path
	headers := map[string]string{
		"Accept":             "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
		"Referer":            strings.TrimRight(working.Origin, "/") + "/",
		"User-Agent":         appleAccountUserAgent,
		"Accept-Language":    "zh-CN,zh;q=0.9,en;q=0.8",
		"Sec-CH-UA":          `"Google Chrome";v="149", "Chromium";v="149", "Not)A;Brand";v="24"`,
		"Sec-CH-UA-Mobile":   "?0",
		"Sec-CH-UA-Platform": `"macOS"`,
		"Sec-Fetch-Site":     "same-origin",
	}
	if jsonContent {
		headers["Content-Type"] = "application/json"
		headers["X-Apple-I-Request-Context"] = "ca"
		headers["X-Apple-I-TimeZone"] = "Asia/Shanghai"
		headers["X-Apple-I-FD-Client-Info"] = appleFDClientInfo(appleAccountUserAgent)
		headers["Sec-Fetch-Dest"] = "empty"
		headers["Sec-Fetch-Mode"] = "cors"
	} else {
		headers["Sec-Fetch-Dest"] = "document"
		headers["Sec-Fetch-Mode"] = "navigate"
	}
	if cookie := appleCookieHeader(working.Cookies, rawURL); cookie != "" {
		headers["Cookie"] = cookie
	}
	response, data, err := client.doAppleHTTP(ctx, http.MethodGet, rawURL, headers, nil, true)
	if err != nil {
		return nil, err
	}
	requestURL := response.Request.URL
	if requestURL == nil {
		requestURL, _ = url.Parse(rawURL)
	}
	mergeAppleCookies(&working.Cookies, requestURL, response.Cookies())
	updateAppleAccountHeaders(&working, response.Header)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return data, appleAccountHTTPErrorAt(response.StatusCode, data, false, path)
	}
	*state = working
	return data, nil
}

func (client *AppleAuthClient) appleAccountRequest(ctx context.Context, state *AppleAccountState, method, path, apiKey string, body, result any) (string, error) {
	if state == nil {
		return "", errors.New("Apple Account session is missing")
	}
	working := cloneAppleAccountState(state)
	base := "https://" + firstNonEmpty(working.Host, "appleid.apple.com")
	rawURL := strings.TrimRight(base, "/") + path
	var bodyData []byte
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		bodyData = data
	}
	headers := map[string]string{
		"Accept":                    "application/json, text/plain, */*",
		"Content-Type":              "application/json",
		"Origin":                    firstNonEmpty(working.Origin, "https://account.apple.com"),
		"Referer":                   strings.TrimRight(firstNonEmpty(working.Origin, "https://account.apple.com"), "/") + "/",
		"User-Agent":                appleAccountUserAgent,
		"Accept-Language":           "zh-CN,zh;q=0.9,en;q=0.8",
		"X-Apple-I-Request-Context": "ca",
		"X-Apple-I-TimeZone":        "Asia/Shanghai",
		"X-Apple-I-FD-Client-Info":  appleFDClientInfo(appleAccountUserAgent),
		"Sec-CH-UA":                 `"Google Chrome";v="149", "Chromium";v="149", "Not)A;Brand";v="24"`,
		"Sec-CH-UA-Mobile":          "?0",
		"Sec-CH-UA-Platform":        `"macOS"`,
		"Sec-Fetch-Site":            "same-site",
		"Sec-Fetch-Mode":            "cors",
		"Sec-Fetch-Dest":            "empty",
	}
	if working.Scnt != "" {
		headers["scnt"] = working.Scnt
	}
	if working.SessionID != "" {
		headers["X-Apple-ID-Session-Id"] = working.SessionID
	}
	if apiKey != "" {
		headers["X-Apple-Api-Key"] = apiKey
	}
	if cookie := appleCookieHeader(working.Cookies, rawURL); cookie != "" {
		headers["Cookie"] = cookie
	}
	response, data, err := client.doAppleHTTP(ctx, method, rawURL, headers, bodyData, appleAccountRequestRetryable(method, path))
	if err != nil {
		return "", err
	}
	scnt := response.Header.Get("scnt")
	requestURL := response.Request.URL
	if requestURL == nil {
		requestURL, _ = url.Parse(rawURL)
	}
	mergeAppleCookies(&working.Cookies, requestURL, response.Cookies())
	updateAppleAccountHeaders(&working, response.Header)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return scnt, appleAccountHTTPErrorAt(response.StatusCode, data, method == http.MethodPut && path == "/account/manage/email/private/add/complete", path)
	}
	if appleAccountResponseFailed(data) {
		return scnt, appleAccountHTTPErrorAt(response.StatusCode, data, method == http.MethodPut && path == "/account/manage/email/private/add/complete", path)
	}
	if result != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, result); err != nil {
			return scnt, appleProtocolError("APPLE_ACCOUNT_BAD_RESPONSE", "Apple Account response could not be parsed", true)
		}
	}
	*state = working
	return scnt, nil
}

func appleAccountResponseFailed(data []byte) bool {
	var envelope struct {
		Success *bool `json:"success"`
		Error   any   `json:"error"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return false
	}
	return (envelope.Success != nil && !*envelope.Success) || envelope.Error != nil
}

func updateAppleAccountHeaders(state *AppleAccountState, headers http.Header) {
	if value := headers.Get("scnt"); value != "" {
		state.Scnt = value
	}
	if value := headers.Get("X-Apple-ID-Session-Id"); value != "" {
		state.SessionID = value
	}
	state.SavedAt = time.Now()
}

func appleAccountHTTPError(status int, data []byte, mayHaveCreated bool) error {
	return appleAccountHTTPErrorAt(status, data, mayHaveCreated, "")
}

func appleAccountHTTPErrorAt(status int, data []byte, mayHaveCreated bool, path string) error {
	rawText := strings.TrimSpace(string(data))
	text := appleAccountErrorBody(data)
	retryAfter := appleRetryAfter(data)
	lower := strings.ToLower(rawText)
	code, message := "APPLE_ACCOUNT_ERROR", fmt.Sprintf("Apple Account 接口 HTTP %d", status)
	if stage := appleAccountRequestStage(path); stage != "" {
		message += "（" + stage + "）"
	}
	if text != "" {
		message += "：" + text
	}
	if strings.Contains(lower, "limit") || strings.Contains(lower, "too many") || strings.Contains(lower, "rate") {
		code, message = "APPLE_ACCOUNT_LIMIT", "Apple Account 已达到当前创建限额，请稍后再试"
	} else if status == 419 || appleAccountBodyLooksAuthExpired(lower) {
		code, message = "APPLE_ACCOUNT_EXPIRED", "Apple Account 登录态已失效，请重新登录"
	}
	retryable := code != "APPLE_ACCOUNT_EXPIRED"
	return &AppleProtocolError{Code: code, Message: message, Retryable: retryable, MayHaveCreated: mayHaveCreated, RetryAfter: retryAfter}
}

// appleAccountBodyLooksAuthExpired keeps generic 401/403 responses (for
// example, a temporary anti-bot or upstream policy response) in the retryable
// degraded state. Only explicit session/authentication markers are strong
// enough to require a new Apple Account login.
func appleAccountBodyLooksAuthExpired(lower string) bool {
	lower = strings.ToLower(strings.TrimSpace(lower))
	if lower == "" {
		return false
	}
	for _, marker := range []string{
		"authentication_failed",
		"authentication failed",
		"auth_failed",
		"auth failed",
		"gsa_invalid_session",
		"invalid global session",
		"invalid_global_session",
		"invalid session",
		"scnt_expired",
		"session expired",
		"session has expired",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return strings.Contains(lower, "authentication") && (strings.Contains(lower, "failed") || strings.Contains(lower, "expired"))
}

// appleAccountErrorBody extracts only short, non-secret fields from Apple's
// JSON error envelope. Raw response bodies may contain cookies or tokens.
func appleAccountErrorBody(data []byte) string {
	var envelope struct {
		Reason  string          `json:"reason"`
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &envelope) == nil {
		var detail struct {
			Code    string `json:"errorCode"`
			Message string `json:"errorMessage"`
		}
		_ = json.Unmarshal(envelope.Error, &detail)
		value := firstNonEmpty(detail.Message, envelope.Reason, envelope.Message)
		if value == "" && detail.Code != "" {
			value = "Apple error " + detail.Code
		}
		if value == "" && len(envelope.Error) > 0 && string(envelope.Error) != "null" {
			var scalar string
			if json.Unmarshal(envelope.Error, &scalar) == nil {
				value = scalar
			}
		}
		return truncateAppleError(value)
	}
	text := strings.TrimSpace(string(data))
	if strings.HasPrefix(strings.ToLower(text), "<html") {
		return "Apple 返回了 HTML 错误页"
	}
	if text != "" {
		return "Apple 返回了非 JSON 错误响应"
	}
	return ""
}

func truncateAppleError(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len(value) > 160 {
		return value[:160] + "..."
	}
	return value
}

func appleAccountJSLogBody() []map[string]any {
	eventID, err := randomUUID()
	if err != nil {
		eventID = fmt.Sprintf("running-tools-%d", time.Now().UnixNano())
	}
	return []map[string]any{{
		"eventId":       eventID,
		"timestamp":     time.Now().UnixMilli(),
		"eventType":     "custom",
		"componentName": "performance",
		"action":        "memory",
		"metadata": map[string]any{
			"domNodeCount":    0,
			"usedJSHeapSize":  0,
			"totalJSHeapSize": 0,
			"heapUtilization": 0,
			"createdAt":       0,
			"elapsedTime":     0,
			"marks":           map[string]any{"startTime": 0},
			"eventVersion":    1,
		},
	}}
}

func appleAccountRequestStage(path string) string {
	switch {
	case path == "/account/manage/gs/ws/token":
		return "刷新管理 token"
	case path == "/account/manage":
		return "读取管理入口"
	case path == "/account/manage/section/privacy":
		return "打开隐私页面"
	case path == "/bootstrap/portal":
		return "预热门户"
	case path == "/v2/jslogs":
		return "Apple browser session hint"
	case path == "/account/manage/forwardemail":
		return "读取转发邮箱"
	case path == "/account/manage/email/private":
		return "读取隐藏邮箱列表"
	case path == "/account/manage/email/private/add":
		return "生成候选隐藏邮箱"
	case path == "/account/manage/email/private/add/complete":
		return "确认创建隐藏邮箱"
	default:
		return ""
	}
}

func (client *AppleAuthClient) CreateWithAppleAccount(ctx context.Context, state AppleAccountState, label, note string) (map[string]any, AppleAccountState, error) {
	if state.requiresReauth() {
		err := appleProtocolError("APPLE_ACCOUNT_EXPIRED", "Apple Account 登录态已失效，请重新登录", false)
		markAppleAccountFailure(&state, err, time.Now())
		return nil, state, err
	}
	if state.HealthState == AppleAccountStateDegraded {
		err := appleProtocolError("APPLE_ACCOUNT_DEGRADED", "Apple Account 暂时不可用，后台正在重试", true)
		markAppleAccountFailure(&state, err, time.Now())
		return nil, state, err
	}
	refreshedBeforeCreate := false
	if state.needsRefresh(time.Now()) {
		if err := client.refreshAppleAccountState(ctx, &state); err != nil {
			markAppleAccountFailure(&state, err, time.Now())
			return nil, state, err
		}
		refreshedBeforeCreate = true
	}
	alias, updated, err := client.createWithAppleAccountState(ctx, state, label, note)
	if err == nil || refreshedBeforeCreate || !appleAccountAuthExpiredBeforeConfirmation(err) {
		markAppleAccountFailure(&updated, err, time.Now())
		return alias, updated, err
	}
	if refreshErr := client.refreshAppleAccountState(ctx, &updated); refreshErr != nil {
		markAppleAccountFailure(&updated, refreshErr, time.Now())
		return nil, updated, refreshErr
	}
	alias, updated, err = client.createWithAppleAccountState(ctx, updated, label, note)
	markAppleAccountFailure(&updated, err, time.Now())
	return alias, updated, err
}

func (client *AppleAuthClient) createWithAppleAccountState(ctx context.Context, state AppleAccountState, label, note string) (map[string]any, AppleAccountState, error) {
	var generated struct {
		EmailAddress string `json:"emailAddress"`
	}
	if _, err := client.appleAccountRequest(ctx, &state, http.MethodPost, "/account/manage/email/private/add", state.APIKey, map[string]any{}, &generated); err != nil {
		return nil, state, err
	}
	if strings.TrimSpace(generated.EmailAddress) == "" {
		return nil, state, appleProtocolError("APPLE_ACCOUNT_GENERATE_EMPTY", "Apple Account 未返回候选隐藏邮箱", true)
	}
	var completed struct {
		EmailAddress string `json:"emailAddress"`
		Label        string `json:"label"`
		Note         string `json:"note"`
		ID           string `json:"id"`
		Active       bool   `json:"active"`
	}
	if _, err := client.appleAccountRequest(ctx, &state, http.MethodPut, "/account/manage/email/private/add/complete", state.APIKey, map[string]string{"emailAddress": generated.EmailAddress, "label": label, "note": note}, &completed); err != nil {
		var protocol *AppleProtocolError
		if !errors.As(err, &protocol) {
			protocol = &AppleProtocolError{Code: "APPLE_ACCOUNT_CREATE_UNCERTAIN", Message: err.Error(), Retryable: true}
		}
		protocol.MayHaveCreated = true
		return nil, state, protocol
	}
	alias := map[string]any{
		"anonymousId":     completed.ID,
		"hme":             firstNonEmpty(completed.EmailAddress, generated.EmailAddress),
		"label":           firstNonEmpty(completed.Label, label),
		"note":            firstNonEmpty(completed.Note, note),
		"isActive":        completed.Active,
		"origin":          "APPLE_ACCOUNT",
		"detailConfirmed": false,
		"createTimestamp": float64(time.Now().UnixMilli()),
	}
	detailAuthFailure := false
	if strings.TrimSpace(completed.ID) != "" {
		var confirmed struct {
			EmailAddress   string `json:"emailAddress"`
			Label          string `json:"label"`
			Note           string `json:"note"`
			ID             string `json:"id"`
			ForwardToEmail string `json:"forwardToEmail"`
			Active         bool   `json:"active"`
		}
		path := "/account/manage/email/private/" + url.PathEscape(completed.ID) + ".em"
		if _, detailErr := client.appleAccountRequest(ctx, &state, http.MethodGet, path, state.APIKey, nil, &confirmed); detailErr == nil {
			alias["anonymousId"] = firstNonEmpty(confirmed.ID, completed.ID)
			alias["hme"] = firstNonEmpty(confirmed.EmailAddress, fmt.Sprint(alias["hme"]))
			alias["label"] = firstNonEmpty(confirmed.Label, fmt.Sprint(alias["label"]))
			alias["note"] = firstNonEmpty(confirmed.Note, fmt.Sprint(alias["note"]))
			alias["forwardToEmail"] = confirmed.ForwardToEmail
			alias["isActive"] = confirmed.Active
			alias["detailConfirmed"] = true
		} else if appleAccountRequiresReauth(detailErr) {
			// The alias has already been confirmed. Return the successful alias,
			// but retain the explicit auth failure so the next operation uses Web
			// and the UI asks for a fresh Apple Account login.
			markAppleAccountFailure(&state, detailErr, time.Now())
			detailAuthFailure = true
		}
	}
	if !detailAuthFailure {
		markAppleAccountHealthy(&state, time.Now())
	}
	return alias, state, nil
}

func appleAccountAuthExpiredBeforeConfirmation(err error) bool {
	var protocol *AppleProtocolError
	return errors.As(err, &protocol) && protocol.Code == "APPLE_ACCOUNT_EXPIRED" && !protocol.MayHaveCreated
}

func appleRetryAfter(data []byte) time.Duration {
	var payload struct {
		RetryAfter float64 `json:"retryAfter"`
		Error      struct {
			RetryAfter float64 `json:"retryAfter"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return 0
	}
	seconds := payload.RetryAfter
	if seconds <= 0 {
		seconds = payload.Error.RetryAfter
	}
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}
