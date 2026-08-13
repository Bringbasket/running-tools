package mail

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	appleHashcashMaxBits     = 24
	appleHashcashMaxAttempts = 1 << 24
)

type AppleAuthClient struct {
	httpClient *http.Client
	pendingMu  sync.Mutex
	pending    map[string]appleAuthPending
}

type appleSRPInitResponse struct {
	Iteration int    `json:"iteration"`
	Salt      string `json:"salt"`
	Protocol  string `json:"protocol"`
	B         string `json:"b"`
	C         string `json:"c"`
}

func NewAppleAuthClient() *AppleAuthClient {
	return &AppleAuthClient{httpClient: &http.Client{Timeout: 30 * time.Second}, pending: make(map[string]appleAuthPending)}
}

func (client *AppleAuthClient) Start(ctx context.Context, input AppleLoginStartInput) (AppleLoginStartResult, error) {
	input.AppleID = strings.ToLower(strings.TrimSpace(input.AppleID))
	input.Channel = normalizeAppleChannel(input.Channel)
	input.TwoFactorMethod = normalizeAppleTwoFactorMethod(input.TwoFactorMethod)
	if input.AppleID == "" || input.Password == "" {
		return AppleLoginStartResult{}, appleProtocolError("APPLE_CREDENTIALS_MISSING", "请填写 Apple ID 和密码", false)
	}

	var session *appleAuthSession
	if input.Channel == AppleChannelAccount {
		frameID, err := randomUUID()
		if err != nil {
			return AppleLoginStartResult{}, err
		}
		session = &appleAuthSession{Channel: input.Channel, Endpoints: appleAccountAuthEndpoints(), AppleID: input.AppleID, ClientID: appleManageOAuthClientID, FrameID: strings.ToLower(frameID), UserAgent: appleAccountUserAgent, TwoFactorMethod: input.TwoFactorMethod}
		if err := client.primeAppleAccount(ctx, session); err != nil {
			return AppleLoginStartResult{}, err
		}
	} else {
		frameID, err := randomUUID()
		if err != nil {
			return AppleLoginStartResult{}, err
		}
		session = &appleAuthSession{Channel: input.Channel, Endpoints: appleWebAuthEndpoints(input.Region), AppleID: input.AppleID, ClientID: appleWebOAuthClientID, FrameID: strings.ToLower(frameID), UserAgent: appleAuthUserAgent, TwoFactorMethod: input.TwoFactorMethod}
	}
	return client.startWithSession(ctx, session, input.Password)
}

func (client *AppleAuthClient) startWithSession(ctx context.Context, session *appleAuthSession, password string) (AppleLoginStartResult, error) {
	if err := client.authStart(ctx, session); err != nil {
		return AppleLoginStartResult{}, err
	}
	if session.Channel == AppleChannelAccount {
		if err := client.authDeviceKeyChallenge(ctx, session); err != nil {
			return AppleLoginStartResult{}, err
		}
	}
	if err := client.authFederate(ctx, session); err != nil {
		return AppleLoginStartResult{}, err
	}
	needs2FA, err := client.authSRP(ctx, session, password)
	if err != nil {
		return AppleLoginStartResult{}, err
	}
	if session.Channel == AppleChannelICloudWeb {
		next := appleWebEndpointsForCountry(session.AccountCountry)
		if next.Host != "" && next.Host != session.Endpoints.Host {
			frameID, frameErr := randomUUID()
			if frameErr != nil {
				return AppleLoginStartResult{}, frameErr
			}
			switched := &appleAuthSession{Channel: session.Channel, Endpoints: next, AppleID: session.AppleID, ClientID: session.ClientID, FrameID: strings.ToLower(frameID), UserAgent: session.UserAgent, TwoFactorMethod: session.TwoFactorMethod}
			return client.startWithSession(ctx, switched, password)
		}
	}
	password = ""
	if needs2FA {
		message := "已向受信任设备发送验证码"
		_ = client.refreshAuthState(ctx, session)
		if session.TwoFactorMethod == AppleTwoFactorPhone {
			if err := client.requestPhoneCode(ctx, session); err != nil {
				return AppleLoginStartResult{}, err
			}
			message = "已向受信任手机号发送短信验证码"
		} else if session.Channel == AppleChannelICloudWeb {
			if err := client.requestDeviceCode(ctx, session); err != nil {
				message = "Apple 已要求二次验证，请查看受信任设备"
			}
		}
		pending, err := client.putPending(session)
		if err != nil {
			return AppleLoginStartResult{}, err
		}
		return AppleLoginStartResult{Channel: session.Channel, Needs2FA: true, PendingID: pending.ID, ExpiresAt: unixPointer(pending.ExpiresAt), Message: message, AppleID: session.AppleID}, nil
	}
	result, err := client.finishLogin(ctx, session)
	if err == nil || session.Channel != AppleChannelICloudWeb || session.SessionToken == "" {
		return result, err
	}
	pending, pendingErr := client.putPending(session)
	if pendingErr != nil {
		return AppleLoginStartResult{}, pendingErr
	}
	return AppleLoginStartResult{Channel: session.Channel, Needs2FA: true, PendingID: pending.ID, ExpiresAt: unixPointer(pending.ExpiresAt), Message: "登录已进入二次验证，请输入受信任设备上的验证码", AppleID: session.AppleID}, nil
}

func (client *AppleAuthClient) Verify(ctx context.Context, pendingID, code string) (AppleLoginStartResult, error) {
	code = strings.TrimSpace(code)
	if len(code) != 6 || strings.IndexFunc(code, func(value rune) bool { return value < '0' || value > '9' }) >= 0 {
		return AppleLoginStartResult{}, appleProtocolError("INVALID_2FA_CODE", "验证码必须是 6 位数字", false)
	}
	pending, ok := client.getPending(pendingID)
	if !ok {
		return AppleLoginStartResult{}, appleProtocolError("APPLE_LOGIN_EXPIRED", "登录验证已过期，请重新开始登录", false)
	}
	session := pending.Session
	var err error
	if session.TwoFactorMethod == AppleTwoFactorPhone {
		err = client.validatePhoneCode(ctx, session, code)
	} else {
		err = client.validateDeviceCode(ctx, session, code)
	}
	if err != nil {
		return AppleLoginStartResult{}, err
	}
	if trustErr := client.trust(ctx, session); trustErr != nil && session.Channel != AppleChannelAccount {
		return AppleLoginStartResult{}, trustErr
	}
	result, err := client.finishLogin(ctx, session)
	if err == nil {
		client.deletePending(pendingID)
	}
	return result, err
}

func (client *AppleAuthClient) finishLogin(ctx context.Context, session *appleAuthSession) (AppleLoginStartResult, error) {
	if session.Channel == AppleChannelAccount {
		state := AppleAccountState{AppleID: session.AppleID, Host: "appleid.apple.com", Origin: "https://account.apple.com", SavedAt: time.Now(), Cookies: append([]AppleSessionCookie(nil), session.Cookies...), Scnt: firstNonEmpty(session.Scnt, session.ManageScnt), SessionID: session.SessionID}
		if err := client.refreshAppleAccountState(ctx, &state); err != nil {
			return AppleLoginStartResult{}, err
		}
		return AppleLoginStartResult{Channel: session.Channel, Message: "Apple Account 登录成功", AppleID: session.AppleID, accountState: &state}, nil
	}
	config, err := client.finishICloudWeb(ctx, session)
	if err != nil {
		return AppleLoginStartResult{}, err
	}
	return AppleLoginStartResult{Channel: session.Channel, Message: "iCloud Web 登录成功", AppleID: session.AppleID, webConfig: &config}, nil
}

func (client *AppleAuthClient) pendingChannel(id string) string {
	pending, ok := client.getPending(id)
	if !ok || pending.Session == nil {
		return ""
	}
	return pending.Session.Channel
}

func normalizeAppleChannel(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), AppleChannelAccount) {
		return AppleChannelAccount
	}
	return AppleChannelICloudWeb
}

func normalizeAppleTwoFactorMethod(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), AppleTwoFactorPhone) {
		return AppleTwoFactorPhone
	}
	return AppleTwoFactorDevice
}

func appleWebAuthEndpoints(region string) appleAuthEndpoints {
	if strings.EqualFold(strings.TrimSpace(region), RegionChina) {
		return appleAuthEndpoints{Home: "https://www.icloud.com.cn", Setup: "https://setup.icloud.com.cn/setup/ws/1", Auth: "https://idmsa.apple.com.cn/appleauth/auth", Host: "www.icloud.com.cn"}
	}
	return appleAuthEndpoints{Home: "https://www.icloud.com", Setup: "https://setup.icloud.com/setup/ws/1", Auth: "https://idmsa.apple.com/appleauth/auth", Host: "www.icloud.com"}
}

func appleWebEndpointsForCountry(country string) appleAuthEndpoints {
	country = strings.ToUpper(strings.TrimSpace(country))
	if country == "" {
		return appleAuthEndpoints{}
	}
	if country == "CN" || country == "CHN" {
		return appleWebAuthEndpoints(RegionChina)
	}
	return appleWebAuthEndpoints(RegionInternational)
}

func appleAccountAuthEndpoints() appleAuthEndpoints {
	return appleAuthEndpoints{Home: "https://account.apple.com", Auth: "https://idmsa.apple.com/appleauth/auth", Host: "appleid.apple.com"}
}

func (client *AppleAuthClient) authStart(ctx context.Context, session *appleAuthSession) error {
	frame := "auth-" + session.FrameID
	parsed, _ := url.Parse(session.Endpoints.Auth + "/authorize/signin")
	query := parsed.Query()
	for key, value := range map[string]string{"frame_id": frame, "skVersion": "7", "iframeId": frame, "client_id": session.ClientID, "redirect_uri": session.Endpoints.Home, "response_type": "code", "response_mode": "web_message", "state": frame} {
		query.Set(key, value)
	}
	headers := map[string]string{"Accept": "*/*"}
	if session.Channel == AppleChannelAccount {
		query.Set("authVersion", "8.0.2")
		headers["Referer"] = session.Endpoints.Home + "/"
		headers["Accept"] = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"
		headers["Accept-Language"] = "zh-CN,zh;q=0.9,en;q=0.8"
		headers["Sec-CH-UA"] = `"Google Chrome";v="149", "Chromium";v="149", "Not)A;Brand";v="24"`
		headers["Sec-CH-UA-Mobile"], headers["Sec-CH-UA-Platform"] = "?0", `"macOS"`
		headers["Sec-Fetch-Dest"], headers["Sec-Fetch-Mode"], headers["Sec-Fetch-Site"] = "iframe", "navigate", "same-site"
	} else {
		query.Set("language", "zh_CN")
		query.Set("authVersion", "latest")
	}
	parsed.RawQuery = query.Encode()
	_, _, err := client.do(ctx, session, http.MethodGet, parsed.String(), headers, nil, nil, false)
	if session.Channel == AppleChannelAccount {
		session.CompleteHCBits, session.CompleteHCChallenge = session.HCBits, session.HCChallenge
	}
	return err
}

func (client *AppleAuthClient) authDeviceKeyChallenge(ctx context.Context, session *appleAuthSession) error {
	headers := session.srpHeaders()
	delete(headers, "scnt")
	delete(headers, "X-Apple-ID-Session-Id")
	_, _, err := client.do(ctx, session, http.MethodPost, session.Endpoints.Auth+"/verify/device/key/challenge", headers, map[string]bool{"passkeyAutofill": false}, nil, false)
	return err
}

func (client *AppleAuthClient) authFederate(ctx context.Context, session *appleAuthSession) error {
	_, _, err := client.do(ctx, session, http.MethodPost, session.Endpoints.Auth+"/federate?isRememberMeEnabled=true", session.srpHeaders(), map[string]any{"accountName": session.AppleID, "rememberMe": true}, nil, false)
	return err
}

func (client *AppleAuthClient) authSRP(ctx context.Context, session *appleAuthSession, password string) (bool, error) {
	srp, err := newAppleSRPClient()
	if err != nil {
		return false, err
	}
	var challenge appleSRPInitResponse
	initBody := map[string]any{"a": base64.StdEncoding.EncodeToString(srp.ABytes()), "accountName": session.AppleID, "protocols": []string{"s2k", "s2k_fo"}}
	if _, _, err := client.do(ctx, session, http.MethodPost, session.Endpoints.Auth+"/signin/init", session.srpHeaders(), initBody, &challenge, false); err != nil {
		return false, err
	}
	serverB, err := base64.StdEncoding.DecodeString(challenge.B)
	if err != nil {
		return false, err
	}
	salt, err := base64.StdEncoding.DecodeString(challenge.Salt)
	if err != nil {
		return false, err
	}
	derived, err := deriveAppleSRPPassword(password, salt, challenge.Iteration, challenge.Protocol)
	if err != nil {
		return false, err
	}
	if err := srp.processChallenge([]byte(session.AppleID), derived, salt, serverB); err != nil {
		return false, err
	}
	body := map[string]any{"accountName": session.AppleID, "m1": base64.StdEncoding.EncodeToString(srp.M1), "m2": base64.StdEncoding.EncodeToString(srp.M2), "c": challenge.C, "rememberMe": true}
	headers := session.srpHeaders()
	if session.Channel == AppleChannelAccount {
		hashcash, err := generateAppleHashcash(session.CompleteHCBits, session.CompleteHCChallenge, time.Now())
		if err != nil {
			return false, err
		}
		headers["X-Apple-HC"] = hashcash
	} else {
		body["trustTokens"] = []string{}
		if session.TrustToken != "" {
			body["trustTokens"] = []string{session.TrustToken}
		}
	}
	status, _, err := client.do(ctx, session, http.MethodPost, session.Endpoints.Auth+"/signin/complete?isRememberMeEnabled=true", headers, body, nil, true)
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return false, appleProtocolError("APPLE_CREDENTIALS_INVALID", "Apple ID 或密码错误，或账号限制了当前登录", false)
	}
	return status == http.StatusConflict, err
}

func generateAppleHashcash(bits int, challenge string, now time.Time) (string, error) {
	challenge = strings.TrimSpace(challenge)
	if bits <= 0 || challenge == "" {
		return "", appleProtocolError("APPLE_HC_MISSING", "Apple Account 未返回动态验证挑战，请稍后重试", true)
	}
	if bits > appleHashcashMaxBits {
		return "", appleProtocolError("APPLE_HC_TOO_HARD", "Apple Account 动态验证难度过高，请稍后重试", true)
	}
	prefix := fmt.Sprintf("1:%d:%s:%s::", bits, now.UTC().Format("20060102150405"), challenge)
	for counter := int64(0); counter < appleHashcashMaxAttempts; counter++ {
		value := prefix + strconv.FormatInt(counter, 36)
		sum := sha1.Sum([]byte(value))
		if leadingZeroBits(sum[:]) >= bits {
			return value, nil
		}
	}
	return "", appleProtocolError("APPLE_HC_FAILED", "Apple Account 动态验证生成失败", true)
}

func leadingZeroBits(data []byte) int {
	total := 0
	for _, value := range data {
		for bit := 7; bit >= 0; bit-- {
			if value&(1<<bit) != 0 {
				return total
			}
			total++
		}
	}
	return total
}

func (client *AppleAuthClient) requestDeviceCode(ctx context.Context, session *appleAuthSession) error {
	_, _, err := client.do(ctx, session, http.MethodPut, session.Endpoints.Auth+"/verify/trusteddevice/securitycode", session.twoFactorHeaders(), nil, nil, false)
	return err
}

func (client *AppleAuthClient) validateDeviceCode(ctx context.Context, session *appleAuthSession, code string) error {
	_, _, err := client.do(ctx, session, http.MethodPost, session.Endpoints.Auth+"/verify/trusteddevice/securitycode", session.twoFactorHeaders(), map[string]any{"securityCode": map[string]string{"code": code}}, nil, false)
	if err != nil {
		return appleProtocolError("APPLE_2FA_FAILED", "验证码不正确或已过期", true)
	}
	return nil
}

func (client *AppleAuthClient) requestPhoneCode(ctx context.Context, session *appleAuthSession) error {
	phone, err := applePhonePayload(session.TwoFactorPhone, false)
	if err != nil {
		return err
	}
	_, _, err = client.do(ctx, session, http.MethodPut, session.Endpoints.Auth+"/verify/phone", session.twoFactorHeaders(), map[string]any{"phoneNumber": phone, "mode": "sms"}, nil, false)
	return err
}

func (client *AppleAuthClient) validatePhoneCode(ctx context.Context, session *appleAuthSession, code string) error {
	phone, err := applePhonePayload(session.TwoFactorPhone, true)
	if err != nil {
		return err
	}
	_, _, err = client.do(ctx, session, http.MethodPost, session.Endpoints.Auth+"/verify/phone/securitycode", session.twoFactorHeaders(), map[string]any{"phoneNumber": phone, "securityCode": map[string]string{"code": code}, "mode": "sms"}, nil, false)
	if err != nil {
		return appleProtocolError("APPLE_2FA_FAILED", "短信验证码不正确或已过期", true)
	}
	return nil
}

func applePhonePayload(raw []byte, includeNonFTEU bool) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte(`{"id":1,"nonFTEU":true}`)
	}
	var phone map[string]any
	if json.Unmarshal(raw, &phone) != nil || phone["id"] == nil {
		return nil, appleProtocolError("APPLE_PHONE_MISSING", "未找到可用的受信任手机号", false)
	}
	if !includeNonFTEU {
		delete(phone, "nonFTEU")
	}
	return phone, nil
}

func (client *AppleAuthClient) refreshAuthState(ctx context.Context, session *appleAuthSession) error {
	headers := session.authHeaders()
	headers["Accept"], headers["Referer"] = "text/html", strings.TrimSuffix(session.Endpoints.Auth, "/appleauth/auth")+"/"
	delete(headers, "Origin")
	_, data, err := client.do(ctx, session, http.MethodGet, session.Endpoints.Auth, headers, nil, nil, false)
	if err == nil {
		session.rememberPhone(data)
	}
	return err
}

func (client *AppleAuthClient) trust(ctx context.Context, session *appleAuthSession) error {
	_, _, err := client.do(ctx, session, http.MethodGet, session.Endpoints.Auth+"/2sv/trust", session.authHeaders(), nil, nil, false)
	return err
}

func (client *AppleAuthClient) putPending(session *appleAuthSession) (appleAuthPending, error) {
	id, err := randomToken(18)
	if err != nil {
		return appleAuthPending{}, err
	}
	pending := appleAuthPending{ID: id, Session: session, ExpiresAt: time.Now().Add(appleLoginPendingTTL)}
	client.pendingMu.Lock()
	defer client.pendingMu.Unlock()
	client.cleanPendingLocked()
	client.pending[id] = pending
	return pending, nil
}

func (client *AppleAuthClient) getPending(id string) (appleAuthPending, bool) {
	client.pendingMu.Lock()
	defer client.pendingMu.Unlock()
	client.cleanPendingLocked()
	pending, ok := client.pending[strings.TrimSpace(id)]
	return pending, ok
}

func (client *AppleAuthClient) deletePending(id string) {
	client.pendingMu.Lock()
	defer client.pendingMu.Unlock()
	delete(client.pending, strings.TrimSpace(id))
}

func (client *AppleAuthClient) cleanPendingLocked() {
	now := time.Now()
	for id, pending := range client.pending {
		if now.After(pending.ExpiresAt) {
			delete(client.pending, id)
		}
	}
}

func (client *AppleAuthClient) do(ctx context.Context, session *appleAuthSession, method, rawURL string, headers map[string]string, body, result any, allowConflict bool) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("User-Agent", firstNonEmpty(session.UserAgent, appleAuthUserAgent))
	for key, value := range headers {
		if strings.TrimSpace(value) != "" {
			request.Header.Set(key, value)
		}
	}
	if body != nil && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie := appleCookieHeader(session.Cookies, rawURL); cookie != "" {
		request.Header.Set("Cookie", cookie)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("Apple 网络请求失败: %w", err)
	}
	defer response.Body.Close()
	session.extract(response)
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return response.StatusCode, nil, err
	}
	if allowConflict && response.StatusCode == http.StatusConflict {
		return response.StatusCode, data, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := "Apple 登录请求被拒绝"
		if response.StatusCode >= 500 {
			message = "Apple 登录服务暂时不可用"
		}
		return response.StatusCode, data, appleProtocolError("APPLE_PROTOCOL_ERROR", fmt.Sprintf("%s（HTTP %d）", message, response.StatusCode), response.StatusCode >= 500)
	}
	if result != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, result); err != nil {
			return response.StatusCode, data, appleProtocolError("APPLE_BAD_RESPONSE", "Apple 登录接口返回无法解析", true)
		}
	}
	return response.StatusCode, data, nil
}

func (session *appleAuthSession) extract(response *http.Response) {
	mergeAppleCookies(&session.Cookies, response.Request.URL, response.Cookies())
	for header, target := range map[string]*string{"X-Apple-ID-Account-Country": &session.AccountCountry, "X-Apple-ID-Session-Id": &session.SessionID, "X-Apple-Session-Token": &session.SessionToken, "X-Apple-TwoSV-Trust-Token": &session.TrustToken, "scnt": &session.Scnt, "X-Apple-Auth-Attributes": &session.AuthAttributes} {
		if value := response.Header.Get(header); value != "" {
			*target = value
		}
	}
	if bits, err := strconv.Atoi(response.Header.Get("X-Apple-HC-Bits")); err == nil && bits > 0 {
		session.HCBits = bits
	}
	if value := response.Header.Get("X-Apple-HC-Challenge"); value != "" {
		session.HCChallenge = value
	}
}

func (session *appleAuthSession) srpHeaders() map[string]string {
	frame := "auth-" + session.FrameID
	fdClientInfo := `{"U":"` + session.UserAgent + `","L":"zh-CN","Z":"GMT+08:00","V":"1.1","F":""}`
	if session.Channel == AppleChannelAccount {
		fdClientInfo = appleFDClientInfo(session.UserAgent)
	}
	headers := map[string]string{"Accept": "application/json", "Content-Type": "application/json", "Origin": strings.TrimSuffix(session.Endpoints.Auth, "/appleauth/auth"), "Referer": strings.TrimSuffix(session.Endpoints.Auth, "/appleauth/auth") + "/", "X-Apple-Widget-Key": session.ClientID, "X-Apple-OAuth-Client-Id": session.ClientID, "X-Apple-OAuth-Client-Type": "firstPartyAuth", "X-Apple-OAuth-Redirect-URI": session.Endpoints.Home, "X-Apple-OAuth-Require-Grant-Code": "true", "X-Apple-OAuth-Response-Mode": "web_message", "X-Apple-OAuth-Response-Type": "code", "X-Apple-OAuth-State": frame, "X-Apple-Frame-Id": frame, "X-Requested-With": "XMLHttpRequest", "X-Apple-Mandate-Security-Upgrade": "0", "X-Apple-I-Require-UE": "true", "X-Apple-I-FD-Client-Info": fdClientInfo}
	if session.Scnt != "" {
		headers["scnt"] = session.Scnt
	}
	if session.SessionID != "" {
		headers["X-Apple-ID-Session-Id"] = session.SessionID
	}
	if session.SessionToken != "" {
		headers["X-Apple-Session-Token"] = session.SessionToken
	}
	if session.AuthAttributes != "" {
		headers["X-Apple-Auth-Attributes"] = session.AuthAttributes
	}
	if session.Channel == AppleChannelAccount {
		headers["Accept"] = "application/json, text/javascript, */*; q=0.01"
		headers["Accept-Language"] = "zh-CN,zh;q=0.9,en;q=0.8"
		headers["Sec-CH-UA"] = `"Google Chrome";v="149", "Chromium";v="149", "Not)A;Brand";v="24"`
		headers["Sec-CH-UA-Mobile"], headers["Sec-CH-UA-Platform"] = "?0", `"macOS"`
		headers["Sec-Fetch-Dest"], headers["Sec-Fetch-Mode"], headers["Sec-Fetch-Site"] = "empty", "cors", "same-origin"
		headers["X-Apple-Domain-Id"], headers["X-Apple-Privacy-Consent"], headers["X-Apple-Privacy-Consent-Accepted"] = "11", "true", "true"
		delete(headers, "X-Apple-OAuth-Require-Grant-Code")
		delete(headers, "X-Apple-Mandate-Security-Upgrade")
		delete(headers, "X-Apple-I-Require-UE")
	}
	return headers
}

func (session *appleAuthSession) authHeaders() map[string]string { return session.srpHeaders() }
func (session *appleAuthSession) twoFactorHeaders() map[string]string {
	headers := session.srpHeaders()
	if session.Channel == AppleChannelAccount {
		headers["Accept"] = "application/json, text/plain, */*"
		headers["X-Apple-App-Id"] = session.ClientID
		delete(headers, "X-Requested-With")
	}
	return headers
}

func (session *appleAuthSession) rememberPhone(data []byte) {
	var root any
	if json.Unmarshal(data, &root) != nil {
		text := string(data)
		for _, marker := range []string{`id="app_config"`, `id='app_config'`} {
			index := strings.Index(text, marker)
			if index < 0 {
				continue
			}
			open := strings.Index(text[index:], ">")
			if open < 0 {
				continue
			}
			close := strings.Index(text[index+open+1:], "</script>")
			if close >= 0 {
				_ = json.Unmarshal([]byte(text[index+open+1:index+open+1+close]), &root)
				break
			}
		}
	}
	if phone, ok := findApplePhone(root, 0); ok {
		session.TwoFactorPhone, _ = json.Marshal(phone)
	}
}

func findApplePhone(value any, depth int) (map[string]any, bool) {
	if depth > 16 {
		return nil, false
	}
	switch current := value.(type) {
	case map[string]any:
		if values, ok := current["trustedPhoneNumbers"].([]any); ok {
			for _, value := range values {
				if phone, ok := normalizeApplePhone(value); ok {
					return phone, true
				}
			}
		}
		if phone, ok := normalizeApplePhone(current["phoneNumber"]); ok {
			return phone, true
		}
		for _, child := range current {
			if phone, ok := findApplePhone(child, depth+1); ok {
				return phone, true
			}
		}
	case []any:
		for _, child := range current {
			if phone, ok := findApplePhone(child, depth+1); ok {
				return phone, true
			}
		}
	}
	return nil, false
}

func normalizeApplePhone(value any) (map[string]any, bool) {
	phone, ok := value.(map[string]any)
	if !ok || phone["id"] == nil {
		return nil, false
	}
	result := map[string]any{"id": phone["id"]}
	if nonFTEU, exists := phone["nonFTEU"]; exists {
		result["nonFTEU"] = nonFTEU
	}
	return result, true
}
