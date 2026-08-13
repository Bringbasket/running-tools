package mail

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

const defaultCreateChannelCooldown = 2 * time.Minute
const transientCreateChannelCooldown = 30 * time.Second

type createChannelRuntime struct {
	CooldownUntil       *float64 `json:"cooldownUntil,omitempty"`
	LastCreateAt        *float64 `json:"lastCreateAt,omitempty"`
	LastErrorCode       string   `json:"lastErrorCode,omitempty"`
	LastError           string   `json:"lastError,omitempty"`
	ConsecutiveFailures int      `json:"consecutiveFailures,omitempty"`
}

type createChannelState struct {
	Channels map[string]createChannelRuntime `json:"channels"`
}

func (manager *SessionManager) createChannelRuntime(channel string) createChannelRuntime {
	state := createChannelState{}
	if storage.ReadJSON(manager.createChannelPath, &state) != nil || state.Channels == nil {
		return createChannelRuntime{}
	}
	runtime := state.Channels[channel]
	if runtime.CooldownUntil != nil && *runtime.CooldownUntil <= unixNow() {
		runtime.CooldownUntil = nil
	}
	return runtime
}

func (manager *SessionManager) channelCoolingDown(channel string) (*float64, bool) {
	runtime := manager.createChannelRuntime(channel)
	return runtime.CooldownUntil, runtime.CooldownUntil != nil && *runtime.CooldownUntil > unixNow()
}

func (manager *SessionManager) recordCreateSuccess(channel string) {
	manager.updateCreateChannelRuntime(channel, func(runtime createChannelRuntime) createChannelRuntime {
		now := unixNow()
		runtime.LastCreateAt = &now
		runtime.CooldownUntil = nil
		runtime.LastErrorCode = ""
		runtime.LastError = ""
		runtime.ConsecutiveFailures = 0
		return runtime
	})
}

func (manager *SessionManager) recordCreateFailure(channel string, err error) {
	code, retryAfter, limited := createErrorDetails(err)
	manager.updateCreateChannelRuntime(channel, func(runtime createChannelRuntime) createChannelRuntime {
		runtime.LastErrorCode = code
		runtime.LastError = safeErrorText(err)
		runtime.ConsecutiveFailures++
		cooldown := transientCreateChannelCooldown
		if limited {
			cooldown = defaultCreateChannelCooldown
		}
		if retryAfter > 0 {
			cooldown = retryAfter
		}
		until := unixNow() + cooldown.Seconds()
		runtime.CooldownUntil = &until
		return runtime
	})
}

func (manager *SessionManager) updateCreateChannelRuntime(channel string, update func(createChannelRuntime) createChannelRuntime) {
	manager.channelStateMu.Lock()
	defer manager.channelStateMu.Unlock()
	state := createChannelState{}
	_ = storage.ReadJSON(manager.createChannelPath, &state)
	if state.Channels == nil {
		state.Channels = make(map[string]createChannelRuntime)
	}
	state.Channels[channel] = update(state.Channels[channel])
	_ = storage.WriteJSON(manager.createChannelPath, state, 0o600)
}

func applyCreateRuntime(status *AppleChannelStatus, runtime createChannelRuntime) {
	if status == nil {
		return
	}
	status.CooldownUntil = runtime.CooldownUntil
	if runtime.CooldownUntil != nil {
		status.CooldownRemaining = max(0, int(*runtime.CooldownUntil-unixNow()+0.5))
	}
	status.LastCreateAt = runtime.LastCreateAt
	status.LastCreateError = runtime.LastError
	status.LastCreateErrorCode = runtime.LastErrorCode
	status.ConsecutiveFailures = runtime.ConsecutiveFailures
}

func createErrorDetails(err error) (string, time.Duration, bool) {
	var protocol *AppleProtocolError
	if errors.As(err, &protocol) {
		return protocol.Code, protocol.RetryAfter, protocol.Code == "APPLE_ACCOUNT_LIMIT"
	}
	var apple *AppleError
	if errors.As(err, &apple) {
		code, retryAfter := apple.CodeAndRetryAfter()
		return code, retryAfter, code == "-41015" || strings.Contains(strings.ToLower(apple.Error()), "reached the limit")
	}
	if err == nil {
		return "", 0, false
	}
	return "CREATE_ERROR", 0, strings.Contains(strings.ToLower(err.Error()), "reached the limit") || strings.Contains(err.Error(), "-41015")
}

func allCreateChannelsFailed(accountErr, webErr error) error {
	if accountErr == nil {
		return webErr
	}
	if webErr == nil {
		return accountErr
	}
	return appleProtocolError("CREATE_ALL_CHANNELS_FAILED", fmt.Sprintf("Apple Account 创建失败：%s；iCloud Web 创建失败：%s", safeErrorText(accountErr), safeErrorText(webErr)), true)
}
