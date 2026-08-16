package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	xproxy "golang.org/x/net/proxy"
)

const maximumProxyURLLength = 2048

const (
	defaultProxyTestTarget = "https://www.icloud.com/"
	proxyTestTimeout       = 15 * time.Second
	proxyTestResponseLimit = 4 << 10
	proxyTestUserAgent     = "Mozilla/5.0 (compatible; RunningTools/1.0; +https://github.com/Bringbasket/hme-tools)"
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

type proxyDialContext func(context.Context, string, string) (net.Conn, error)

type bufferedProxyConn struct {
	net.Conn
	reader *bufio.Reader
}

func (conn *bufferedProxyConn) Read(buffer []byte) (int, error) {
	return conn.reader.Read(buffer)
}

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
	if err != nil {
		return "", errors.New("代理地址格式无效")
	}
	hasUnexpectedComponents := parsed.Opaque != "" || (parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery
	hasMalformedAuthority := parsed.Host == "" || parsed.Hostname() == "" ||
		(parsed.User == nil && strings.Contains(parsed.Host, "@")) || strings.HasSuffix(parsed.Host, ":")
	if hasUnexpectedComponents || hasMalformedAuthority {
		return "", errors.New("代理地址格式无效")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5":
	default:
		return "", errors.New("代理仅支持 http、https 或 socks5")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", errors.New("代理地址格式无效")
		}
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Path = ""
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

func tcpDialContextForProxy(raw string, timeout time.Duration) (proxyDialContext, error) {
	normalized, err := normalizeProxyURL(raw)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	direct := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	if normalized == "" {
		return direct.DialContext, nil
	}

	parsed, err := url.Parse(normalized)
	if err != nil {
		return nil, errors.New("代理配置无效")
	}
	switch parsed.Scheme {
	case "socks5":
		dialer, err := xproxy.FromURL(parsed, direct)
		if err != nil {
			return nil, errors.New("SOCKS5 代理配置无效")
		}
		contextDialer, ok := dialer.(xproxy.ContextDialer)
		if !ok {
			return nil, errors.New("SOCKS5 代理不支持可取消连接")
		}
		return contextDialer.DialContext, nil
	case "http", "https":
		return func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialHTTPConnectProxy(ctx, direct, parsed, network, address, timeout)
		}, nil
	default:
		return nil, errors.New("不支持的代理协议")
	}
}

func dialHTTPConnectProxy(ctx context.Context, direct *net.Dialer, proxyURL *url.URL, network, address string, timeout time.Duration) (net.Conn, error) {
	return dialHTTPConnectProxyWithTLS(ctx, direct, proxyURL, network, address, timeout, nil)
}

// An HTTPS proxy negotiates TLS before CONNECT. The returned tunnel is then
// wrapped in a second TLS connection by the IMAP client.
func dialHTTPConnectProxyWithTLS(ctx context.Context, direct *net.Dialer, proxyURL *url.URL, network, address string, timeout time.Duration, proxyTLSConfig *tls.Config) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("代理 CONNECT 不支持网络类型 %q", network)
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	port := proxyURL.Port()
	if port == "" {
		if proxyURL.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	proxyAddress := net.JoinHostPort(proxyURL.Hostname(), port)
	conn, err := direct.DialContext(ctx, "tcp", proxyAddress)
	if err != nil {
		return nil, fmt.Errorf("连接代理失败: %w", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = conn.Close()
		}
	}()

	if proxyURL.Scheme == "https" {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
		if proxyTLSConfig != nil {
			tlsConfig = proxyTLSConfig.Clone()
			if tlsConfig.MinVersion == 0 {
				tlsConfig.MinVersion = tls.VersionTLS12
			}
		}
		if tlsConfig.ServerName == "" {
			tlsConfig.ServerName = proxyURL.Hostname()
		}
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return nil, fmt.Errorf("代理 TLS 握手失败: %w", err)
		}
		conn = tlsConn
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	request := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: address},
		Host:   address,
		Header: make(http.Header),
	}
	request.Header.Set("Proxy-Connection", "Keep-Alive")
	if proxyURL.User != nil {
		username := proxyURL.User.Username()
		password, _ := proxyURL.User.Password()
		credentials := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		request.Header.Set("Proxy-Authorization", "Basic "+credentials)
	}
	if err := request.Write(conn); err != nil {
		return nil, fmt.Errorf("发送代理 CONNECT 请求失败: %w", err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return nil, fmt.Errorf("读取代理 CONNECT 响应失败: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_ = response.Body.Close()
		return nil, fmt.Errorf("代理 CONNECT 返回 HTTP %d", response.StatusCode)
	}
	_ = conn.SetDeadline(time.Time{})
	closeOnError = false
	if reader.Buffered() > 0 {
		return &bufferedProxyConn{Conn: conn, reader: reader}, nil
	}
	return conn, nil
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
