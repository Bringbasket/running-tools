package mail

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	AppleChannelICloudWeb = "icloud_web"
	AppleChannelAccount   = "apple_account"
	AppleTwoFactorDevice  = "trusted_device"
	AppleTwoFactorPhone   = "phone"
	appleLoginPendingTTL  = 10 * time.Minute
	// Apple Account's manage token is short-lived. Refresh it before the
	// advertised deadline so a request is not started with a nearly expired
	// token. The effective lead is reduced for very short TTLs below.
	appleAccountRefreshLead  = 2 * time.Minute
	appleAccountMinimumLead  = 30 * time.Second
	appleAuthUserAgent       = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.3.1 Safari/605.1.15"
	appleAccountUserAgent    = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"
	appleWebOAuthClientID    = "d39ba9916b7251055b22c7f910e2ea796ee65e98b2ddecea8f5dde8d9d1a815d"
	appleManageOAuthClientID = "af1139274f266b22b68c2a3e7ad932cb3c0bbe854e13a79af78dcc73136882c3"
)

type AppleProtocolError struct {
	Code           string
	Message        string
	Retryable      bool
	MayHaveCreated bool
	RetryAfter     time.Duration
}

func (err *AppleProtocolError) Error() string { return err.Message }

func appleProtocolError(code, message string, retryable bool) error {
	return &AppleProtocolError{Code: code, Message: message, Retryable: retryable}
}

type AppleSessionCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain,omitempty"`
	Path     string  `json:"path,omitempty"`
	Expires  float64 `json:"expires,omitempty"`
	Secure   bool    `json:"secure,omitempty"`
	HTTPOnly bool    `json:"http_only,omitempty"`
}

type AppleAccountState struct {
	AppleID           string               `json:"appleId,omitempty"`
	Host              string               `json:"host,omitempty"`
	Origin            string               `json:"origin,omitempty"`
	SavedAt           time.Time            `json:"savedAt,omitempty"`
	Cookies           []AppleSessionCookie `json:"cookies,omitempty"`
	Scnt              string               `json:"scnt,omitempty"`
	SessionID         string               `json:"sessionId,omitempty"`
	APIKey            string               `json:"apiKey,omitempty"`
	ManageExpiresAt   time.Time            `json:"manageExpiresAt,omitempty"`
	LastCheckedAt     time.Time            `json:"lastCheckedAt,omitempty"`
	LastCheckOK       bool                 `json:"lastCheckOk,omitempty"`
	LastStatusMessage string               `json:"lastStatusMessage,omitempty"`
}

func (state AppleAccountState) refreshDueAt(now time.Time) time.Time {
	if state.ManageExpiresAt.IsZero() {
		return now
	}
	lead := appleAccountRefreshLead
	if !state.LastCheckedAt.IsZero() {
		ttl := state.ManageExpiresAt.Sub(state.LastCheckedAt)
		if ttl > 0 && ttl/3 < lead {
			lead = ttl / 3
		}
	}
	if lead < appleAccountMinimumLead {
		lead = appleAccountMinimumLead
	}
	return state.ManageExpiresAt.Add(-lead)
}

func (state AppleAccountState) needsRefresh(now time.Time) bool {
	return strings.TrimSpace(state.APIKey) == "" || state.refreshDueAt(now).Compare(now) <= 0
}

type AppleChannelStatus struct {
	Configured          bool     `json:"configured"`
	Healthy             bool     `json:"healthy"`
	AppleID             string   `json:"appleId,omitempty"`
	LastCheckedAt       *float64 `json:"lastCheckedAt,omitempty"`
	ExpiresAt           *float64 `json:"expiresAt,omitempty"`
	Message             string   `json:"message,omitempty"`
	CooldownUntil       *float64 `json:"cooldownUntil,omitempty"`
	CooldownRemaining   int      `json:"cooldownRemainingSeconds,omitempty"`
	LastCreateAt        *float64 `json:"lastCreateAt,omitempty"`
	LastCreateError     string   `json:"lastCreateError,omitempty"`
	LastCreateErrorCode string   `json:"lastCreateErrorCode,omitempty"`
	ConsecutiveFailures int      `json:"consecutiveFailures,omitempty"`
}

type AppleLoginStatus struct {
	ICloudWeb     AppleChannelStatus `json:"icloudWeb"`
	AppleAccount  AppleChannelStatus `json:"appleAccount"`
	CreateChannel string             `json:"createChannel"`
}

type AppleLoginStartInput struct {
	AppleID         string `json:"appleId"`
	Password        string `json:"password"`
	Channel         string `json:"channel"`
	Region          string `json:"region"`
	TwoFactorMethod string `json:"twoFactorMethod"`
}

type AppleLoginStartResult struct {
	Channel      string   `json:"channel"`
	Needs2FA     bool     `json:"needs2FA"`
	PendingID    string   `json:"pendingId,omitempty"`
	ExpiresAt    *float64 `json:"expiresAt,omitempty"`
	Message      string   `json:"message"`
	AppleID      string   `json:"appleId,omitempty"`
	webConfig    *ICloudConfig
	accountState *AppleAccountState
}

type appleAuthEndpoints struct {
	Home  string
	Setup string
	Auth  string
	Host  string
}

type appleAuthSession struct {
	Channel             string
	Endpoints           appleAuthEndpoints
	AppleID             string
	ClientID            string
	FrameID             string
	UserAgent           string
	SessionToken        string
	Scnt                string
	ManageScnt          string
	SessionID           string
	AccountCountry      string
	TrustToken          string
	AuthAttributes      string
	HCBits              int
	HCChallenge         string
	CompleteHCBits      int
	CompleteHCChallenge string
	TwoFactorPhone      []byte
	TwoFactorMethod     string
	Cookies             []AppleSessionCookie
}

type appleAuthPending struct {
	ID        string
	Session   *appleAuthSession
	ExpiresAt time.Time
}

func randomToken(bytesLen int) (string, error) {
	data := make([]byte, bytesLen)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func randomUUID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", data[0:4], data[4:6], data[6:8], data[8:10], data[10:16]), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func appleCookieHeader(cookies []AppleSessionCookie, rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host, path := strings.ToLower(parsed.Hostname()), parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	now := float64(time.Now().Unix())
	type pair struct {
		name, value string
		index       int
	}
	pairs := make([]pair, 0, len(cookies))
	for index, cookie := range cookies {
		domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cookie.Domain)), ".")
		if cookie.Name == "" || cookie.Value == "" || (cookie.Expires > 0 && cookie.Expires < now) {
			continue
		}
		if domain != "" && host != domain && !strings.HasSuffix(host, "."+domain) {
			continue
		}
		if cookie.Path != "" && !strings.HasPrefix(path, cookie.Path) {
			continue
		}
		pairs = append(pairs, pair{cookie.Name, cookie.Value, index})
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].index < pairs[j].index })
	values := make([]string, 0, len(pairs))
	for _, item := range pairs {
		values = append(values, item.name+"="+item.value)
	}
	return strings.Join(values, "; ")
}

func mergeAppleCookies(target *[]AppleSessionCookie, requestURL *url.URL, cookies []*http.Cookie) {
	if target == nil {
		return
	}
	for _, item := range cookies {
		domain := strings.TrimSpace(item.Domain)
		if domain == "" && requestURL != nil {
			domain = requestURL.Hostname()
		}
		path := item.Path
		if path == "" {
			path = "/"
		}
		next := AppleSessionCookie{Name: item.Name, Value: item.Value, Domain: domain, Path: path, Secure: item.Secure, HTTPOnly: item.HttpOnly}
		if !item.Expires.IsZero() {
			next.Expires = float64(item.Expires.Unix())
		}
		replaced := false
		for index, old := range *target {
			if old.Name == next.Name && strings.EqualFold(strings.TrimPrefix(old.Domain, "."), strings.TrimPrefix(next.Domain, ".")) && old.Path == next.Path {
				replaced = true
				if next.Value == "" {
					*target = append((*target)[:index], (*target)[index+1:]...)
				} else {
					(*target)[index] = next
				}
				break
			}
		}
		if !replaced && next.Value != "" {
			*target = append(*target, next)
		}
	}
}

func unixPointer(value time.Time) *float64 {
	if value.IsZero() {
		return nil
	}
	stamp := float64(value.UnixNano()) / float64(time.Second)
	return &stamp
}
