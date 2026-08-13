package mail

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrSessionMissing = errors.New("尚未导入 iCloud session")

type UpstreamError struct {
	Status int
	Body   string
}

func (err *UpstreamError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", err.Status, err.Body)
}

type AppleError struct {
	Payload any
}

func (err *AppleError) Error() string {
	return "iCloud returned success=false: " + safeValue(err.Payload)
}

func (err *AppleError) CodeAndRetryAfter() (string, time.Duration) {
	payload, _ := err.Payload.(map[string]any)
	nested, _ := payload["error"].(map[string]any)
	code := strings.TrimSpace(fmt.Sprint(nested["errorCode"]))
	if code == "<nil>" {
		code = "ICLOUD_ERROR"
	}
	retryAfter, _ := nested["retryAfter"].(float64)
	if retryAfter <= 0 {
		retryAfter, _ = payload["retryAfter"].(float64)
	}
	return code, time.Duration(retryAfter * float64(time.Second))
}

func isSessionExpired(err error) bool {
	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		return upstream.Status == 401 || upstream.Status == 403 || upstream.Status == 421
	}
	message := err.Error()
	return strings.Contains(message, "HTTP 401") || strings.Contains(message, "HTTP 403") || strings.Contains(message, "HTTP 421")
}

func safeValue(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		data = []byte(fmt.Sprint(value))
	}
	text := string(data)
	for _, name := range []string{
		"X-APPLE-DS-WEB-SESSION-TOKEN",
		"X-APPLE-WEBAUTH-TOKEN",
		"X-APPLE-WEBAUTH-LOGIN",
		"X-APPLE-WEBAUTH-VALIDATE",
	} {
		text = strings.ReplaceAll(text, name, name+"<redacted>")
	}
	if len(text) > 500 {
		return text[:500]
	}
	return text
}

func safeErrorText(err error) string {
	text := err.Error()
	for _, name := range []string{
		"X-APPLE-DS-WEB-SESSION-TOKEN",
		"X-APPLE-WEBAUTH-TOKEN",
		"X-APPLE-WEBAUTH-LOGIN",
		"X-APPLE-WEBAUTH-VALIDATE",
	} {
		text = strings.ReplaceAll(text, name, name+"<redacted>")
	}
	if len(text) > 500 {
		return text[:500]
	}
	return text
}
