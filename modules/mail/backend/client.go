package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Client struct {
	mu          sync.Mutex
	config      ICloudConfig
	httpClient  *http.Client
	baseURL     string
	cookieDirty bool
}

func (client *Client) ConfigUpdate() (ICloudConfig, bool) {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.config, client.cookieDirty
}

type ClientOption func(*Client)

func WithHTTPClient(client *http.Client) ClientOption {
	return func(target *Client) {
		if client != nil {
			target.httpClient = client
		}
	}
}

func WithBaseURL(baseURL string) ClientOption {
	return func(target *Client) {
		target.baseURL = strings.TrimRight(baseURL, "/")
	}
}

func NewClient(config ICloudConfig, options ...ClientOption) (*Client, error) {
	config.applyDefaults()
	if err := config.Validate(); err != nil {
		return nil, err
	}
	client := &Client{
		config:     config,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    "https://" + config.Host,
	}
	for _, option := range options {
		option(client)
	}
	return client, nil
}

func (client *Client) ListAliases(ctx context.Context) ([]map[string]any, error) {
	settings, err := client.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	raw, ok := settings["hmeEmails"].([]any)
	if !ok && settings["hmeEmails"] == nil {
		return []map[string]any{}, nil
	}
	if !ok {
		return nil, fmt.Errorf("unexpected list response: result.hmeEmails is not a list")
	}
	aliases := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		alias, ok := entry.(map[string]any)
		if ok {
			normalizeAliasTimestamp(alias)
			aliases = append(aliases, alias)
		}
	}
	return aliases, nil
}

func (client *Client) ListSettings(ctx context.Context) (map[string]any, error) {
	response, err := client.request(ctx, http.MethodGet, "/v2/hme/list", nil)
	if err != nil {
		return nil, err
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected list response: result is not an object")
	}
	return result, nil
}

func (client *Client) CreateAlias(ctx context.Context, label, note string) (map[string]any, error) {
	candidate, err := client.GenerateAlias(ctx)
	if err != nil {
		return nil, err
	}
	return client.ReserveAlias(ctx, candidate, label, note)
}

func (client *Client) GenerateAlias(ctx context.Context) (string, error) {
	response, err := client.request(ctx, http.MethodPost, "/v1/hme/generate", map[string]any{"langCode": client.config.LangCode})
	if err != nil {
		return "", err
	}
	result, _ := response["result"].(map[string]any)
	hme, _ := result["hme"].(string)
	if hme == "" {
		return "", fmt.Errorf("unexpected generate response: result.hme is missing")
	}
	return hme, nil
}

func (client *Client) ReserveAlias(ctx context.Context, hme, label, note string) (map[string]any, error) {
	response, err := client.request(ctx, http.MethodPost, "/v1/hme/reserve", map[string]any{"hme": hme, "label": label, "note": note})
	if err != nil {
		return nil, err
	}
	result, _ := response["result"].(map[string]any)
	alias, ok := result["hme"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected reserve response: result.hme is missing")
	}
	return alias, nil
}

// UpdateAlias changes the user-visible label and note of an existing alias.
func (client *Client) UpdateAlias(ctx context.Context, anonymousID, label, note string) (map[string]any, error) {
	response, err := client.request(ctx, http.MethodPost, "/v1/hme/updateMetaData", map[string]any{
		"anonymousId": anonymousID,
		"label":       label,
		"note":        note,
	})
	if err != nil {
		return nil, err
	}
	result, _ := response["result"].(map[string]any)
	if result == nil {
		result = map[string]any{"anonymousId": anonymousID, "label": label, "note": note}
	}
	if alias, ok := result["hme"].(map[string]any); ok {
		return alias, nil
	}
	return result, nil
}

func (client *Client) SetAliasActive(ctx context.Context, anonymousID string, active bool) (map[string]any, error) {
	action := "deactivate"
	if active {
		action = "reactivate"
	}
	response, err := client.request(ctx, http.MethodPost, "/v1/hme/"+action, map[string]any{"anonymousId": anonymousID})
	if err != nil {
		return nil, err
	}
	result, _ := response["result"].(map[string]any)
	if result == nil {
		result = map[string]any{"anonymousId": anonymousID, "isActive": active}
	}
	return result, nil
}

func (client *Client) DeleteAlias(ctx context.Context, anonymousID string) (map[string]any, error) {
	response, err := client.request(ctx, http.MethodPost, "/v1/hme/delete", map[string]any{"anonymousId": anonymousID})
	if err != nil {
		return nil, err
	}
	result, _ := response["result"].(map[string]any)
	if result == nil {
		result = map[string]any{"anonymousId": anonymousID, "deleted": true}
	}
	return result, nil
}

func (client *Client) Check(ctx context.Context) (map[string]any, error) {
	settings, err := client.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	aliases, _ := settings["hmeEmails"].([]any)
	return map[string]any{
		"selectedForwardTo": settings["selectedForwardTo"],
		"forwardToEmails":   settings["forwardToEmails"],
		"aliasCount":        len(aliases),
	}, nil
}

func (client *Client) request(ctx context.Context, method, endpoint string, payload any) (map[string]any, error) {
	client.mu.Lock()
	config := client.config
	client.mu.Unlock()
	requestURL, err := url.Parse(client.baseURL + endpoint)
	if err != nil {
		return nil, err
	}
	query := requestURL.Query()
	query.Set("clientBuildNumber", config.ClientBuildNumber)
	query.Set("clientMasteringNumber", config.ClientMasteringNumber)
	query.Set("clientId", config.ClientID)
	query.Set("dsid", config.DSID)
	requestURL.RawQuery = query.Encode()

	var body io.Reader
	if payload != nil {
		encoded, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return nil, marshalErr
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "*/*")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7")
	request.Header.Set("Content-Type", "text/plain")
	request.Header.Set("Origin", config.Origin)
	request.Header.Set("Referer", config.Referer)
	request.Header.Set("User-Agent", config.UserAgent)
	request.Header.Set("Cookie", config.Cookie)

	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer response.Body.Close()
	client.mergeResponseCookies(response.Cookies())
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		if response.StatusCode >= 400 {
			body := strings.TrimSpace(string(data))
			if body == "" {
				body = http.StatusText(response.StatusCode) + "（上游未返回正文）"
			}
			return nil, &UpstreamError{Status: response.StatusCode, Body: body}
		}
		return nil, fmt.Errorf("unexpected response: body is not a JSON object")
	}
	if response.StatusCode >= 400 {
		body := safeValue(decoded)
		if len(decoded) == 0 {
			body = http.StatusText(response.StatusCode) + "（上游未返回正文）"
		}
		return nil, &UpstreamError{Status: response.StatusCode, Body: body}
	}
	if success, _ := decoded["success"].(bool); !success {
		return nil, &AppleError{Payload: decoded}
	}
	return decoded, nil
}

func (client *Client) mergeResponseCookies(updates []*http.Cookie) {
	if len(updates) == 0 {
		return
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	request := &http.Request{Header: http.Header{"Cookie": []string{client.config.Cookie}}}
	existing := request.Cookies()
	values := make(map[string]string, len(existing)+len(updates))
	order := make([]string, 0, len(existing)+len(updates))
	for _, cookie := range existing {
		if _, seen := values[cookie.Name]; !seen {
			order = append(order, cookie.Name)
		}
		values[cookie.Name] = cookie.Value
	}
	for _, cookie := range updates {
		if cookie.Name == "" {
			continue
		}
		if cookie.MaxAge < 0 || (!cookie.Expires.IsZero() && cookie.Expires.Before(time.Now())) {
			delete(values, cookie.Name)
			continue
		}
		if _, seen := values[cookie.Name]; !seen {
			order = append(order, cookie.Name)
		}
		values[cookie.Name] = cookie.Value
	}
	parts := make([]string, 0, len(values))
	for _, name := range order {
		value, ok := values[name]
		if !ok {
			continue
		}
		parts = append(parts, (&http.Cookie{Name: name, Value: value}).String())
	}
	merged := strings.Join(parts, "; ")
	if merged != client.config.Cookie {
		client.config.Cookie = merged
		client.cookieDirty = true
	}
}
