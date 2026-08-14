package mail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maximumProxyURLLength = 2048

const (
	defaultProxyTestTarget = "https://www.icloud.com/"
	proxyTestTimeout       = 15 * time.Second
	proxyTestResponseLimit = 4 << 10
	proxyTestUserAgent     = "Mozilla/5.0 (compatible; RunningTools/1.0; +https://github.com/Bringbasket/running-tools)"
)

type ProxyTestResult struct {
	Reachable  bool   `json:"reachable"`
	StatusCode int    `json:"statusCode"`
	LatencyMs  int64  `json:"latencyMs"`
	Target     string `json:"target"`
}

type proxyTestInputError struct {
	message string
}

func (err *proxyTestInputError) Error() string { return err.message }

type proxyTestConnectionError struct {
	message string
}

func (err *proxyTestConnectionError) Error() string { return err.message }

func normalizeProxyURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > maximumProxyURLLength || strings.IndexFunc(raw, func(value rune) bool {
		return value < 0x20 || value == 0x7f
	}) >= 0 {
		return "", errors.New("代理地址格式无效")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User == nil && strings.Contains(parsed.Host, "@") {
		return "", errors.New("代理地址格式无效")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5":
	default:
		return "", errors.New("代理仅支持 http、https 或 socks5")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	return parsed.String(), nil
}

func httpClientForProxy(raw string) (*http.Client, error) {
	normalized, err := normalizeProxyURL(raw)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if normalized != "" {
		parsed, _ := url.Parse(normalized)
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second}, nil
}

func (module *Module) testAccountProxy(ctx context.Context, id, rawProxy, target string) (ProxyTestResult, error) {
	module.mu.RLock()
	_, exists := module.runtimes[strings.TrimSpace(id)]
	module.mu.RUnlock()
	if !exists {
		return ProxyTestResult{}, errMailAccountNotFound
	}

	proxyURL, err := normalizeProxyURL(rawProxy)
	if err != nil {
		return ProxyTestResult{}, &proxyTestInputError{message: err.Error()}
	}
	if proxyURL == "" {
		return ProxyTestResult{}, &proxyTestInputError{message: "请输入代理地址"}
	}
	if strings.TrimSpace(target) == "" {
		target = defaultProxyTestTarget
	}
	return testProxyConnection(ctx, proxyURL, target)
}

func testProxyConnection(ctx context.Context, proxyURL, target string) (ProxyTestResult, error) {
	client, err := httpClientForProxy(proxyURL)
	if err != nil {
		return ProxyTestResult{}, &proxyTestInputError{message: err.Error()}
	}
	defer client.CloseIdleConnections()

	targetURL, err := url.Parse(target)
	if err != nil || targetURL.Hostname() == "" {
		return ProxyTestResult{}, errors.New("代理测试目标无效")
	}
	testCtx, cancel := context.WithTimeout(ctx, proxyTestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(testCtx, http.MethodGet, targetURL.String(), nil)
	if err != nil {
		return ProxyTestResult{}, errors.New("创建代理测试请求失败")
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	request.Header.Set("User-Agent", proxyTestUserAgent)

	startedAt := time.Now()
	response, err := client.Do(request)
	latency := time.Since(startedAt).Milliseconds()
	if err != nil {
		if errors.Is(testCtx.Err(), context.DeadlineExceeded) {
			return ProxyTestResult{}, &proxyTestConnectionError{message: "代理测试超时，请检查代理地址和网络"}
		}
		return ProxyTestResult{}, &proxyTestConnectionError{message: "无法通过该代理访问 Apple，请检查地址、凭据和网络"}
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, proxyTestResponseLimit))

	result := ProxyTestResult{
		Reachable:  response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusBadRequest,
		StatusCode: response.StatusCode,
		LatencyMs:  latency,
		Target:     targetURL.Hostname(),
	}
	if !result.Reachable {
		return result, &proxyTestConnectionError{message: fmt.Sprintf("代理可以连接，但 Apple 返回 HTTP %d", response.StatusCode)}
	}
	return result, nil
}
