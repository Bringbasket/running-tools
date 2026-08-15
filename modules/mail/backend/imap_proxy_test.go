package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
)

func TestNormalizeProxyURLRejectsIgnoredComponentsAndInvalidPorts(t *testing.T) {
	normalized, err := normalizeProxyURL("HTTP://proxy.example.test:8080/")
	if err != nil {
		t.Fatal(err)
	}
	if normalized != "http://proxy.example.test:8080" {
		t.Fatalf("normalized proxy URL = %q", normalized)
	}
	for _, candidate := range []string{
		"http://proxy.example.test:8080/tunnel",
		"http://proxy.example.test:8080?mode=tunnel",
		"http://proxy.example.test:8080#fragment",
		"http://proxy.example.test:0",
		"http://proxy.example.test:65536",
		"http://:8080",
	} {
		if _, err := normalizeProxyURL(candidate); err == nil {
			t.Fatalf("invalid proxy URL %q was accepted", candidate)
		}
	}
}

func TestTCPDialContextForHTTPProxyUsesCONNECTAndAuthentication(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	type observation struct {
		method, host, authorization string
		err                         error
	}
	observed := make(chan observation, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			observed <- observation{err: acceptErr}
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		request, readErr := http.ReadRequest(reader)
		if readErr != nil {
			observed <- observation{err: readErr}
			return
		}
		result := observation{method: request.Method, host: request.Host, authorization: request.Header.Get("Proxy-Authorization")}
		if _, writeErr := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); writeErr != nil {
			result.err = writeErr
			observed <- result
			return
		}
		payload := make([]byte, 4)
		if _, readErr := io.ReadFull(reader, payload); readErr != nil {
			result.err = readErr
		} else if string(payload) != "ping" {
			result.err = fmt.Errorf("unexpected tunnel payload %q", payload)
		} else {
			_, result.err = io.WriteString(conn, "pong")
		}
		observed <- result
	}()

	proxyURL := "http://proxy-user:proxy-password@" + listener.Addr().String()
	dialContext, err := tcpDialContextForProxy(proxyURL, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := dialContext(ctx, "tcp", "imap.unreachable.invalid:993")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "ping"); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if string(response) != "pong" {
		t.Fatalf("unexpected tunnel response %q", response)
	}

	result := <-observed
	if result.err != nil {
		t.Fatal(result.err)
	}
	wantAuthorization := "Basic " + base64.StdEncoding.EncodeToString([]byte("proxy-user:proxy-password"))
	if result.method != http.MethodConnect || result.host != "imap.unreachable.invalid:993" || result.authorization != wantAuthorization {
		t.Fatalf("unexpected CONNECT request: %#v", result)
	}
}

func TestTCPDialContextForSOCKS5ProxyUsesRequestedTarget(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	observed := make(chan string, 1)
	serverErr := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		defer conn.Close()
		if err := serveSOCKS5Tunnel(conn, observed); err != nil {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	dialContext, err := tcpDialContextForProxy("socks5://"+listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := dialContext(ctx, "tcp", "imap.unreachable.invalid:993")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "ping"); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if string(response) != "pong" {
		t.Fatalf("unexpected tunnel response %q", response)
	}
	if target := <-observed; target != "imap.unreachable.invalid:993" {
		t.Fatalf("SOCKS5 target = %q", target)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

func TestHTTPSConnectProxyPreservesOuterAndInnerTLS(t *testing.T) {
	proxyCertificate, proxyRoots := newTestCertificate(t, "localhost")
	imapCertificate, imapRoots := newTestCertificate(t, "imap.unreachable.invalid")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	type observation struct {
		outerSNI, innerSNI, target string
		err                        error
	}
	observed := make(chan observation, 1)
	go func() {
		result := observation{}
		rawConn, acceptErr := listener.Accept()
		if acceptErr != nil {
			result.err = acceptErr
			observed <- result
			return
		}
		defer rawConn.Close()
		outerTLS := tls.Server(rawConn, &tls.Config{Certificates: []tls.Certificate{proxyCertificate}, MinVersion: tls.VersionTLS12})
		if err := outerTLS.Handshake(); err != nil {
			result.err = err
			observed <- result
			return
		}
		result.outerSNI = outerTLS.ConnectionState().ServerName
		reader := bufio.NewReader(outerTLS)
		request, err := http.ReadRequest(reader)
		if err != nil {
			result.err = err
			observed <- result
			return
		}
		result.target = request.Host
		if _, err := io.WriteString(outerTLS, "HTTP/1.1 204 Connection Established\r\n\r\n"); err != nil {
			result.err = err
			observed <- result
			return
		}
		innerTransport := net.Conn(outerTLS)
		if reader.Buffered() > 0 {
			innerTransport = &bufferedProxyConn{Conn: outerTLS, reader: reader}
		}
		innerTLS := tls.Server(innerTransport, &tls.Config{Certificates: []tls.Certificate{imapCertificate}, MinVersion: tls.VersionTLS12})
		if err := innerTLS.Handshake(); err != nil {
			result.err = err
			observed <- result
			return
		}
		result.innerSNI = innerTLS.ConnectionState().ServerName
		payload := make([]byte, 4)
		if _, err := io.ReadFull(innerTLS, payload); err != nil {
			result.err = err
		} else if string(payload) != "ping" {
			result.err = fmt.Errorf("unexpected inner TLS payload %q", payload)
		} else {
			_, result.err = io.WriteString(innerTLS, "pong")
		}
		observed <- result
	}()

	proxyURL, err := url.Parse("https://localhost:" + fmt.Sprint(listener.Addr().(*net.TCPAddr).Port))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dialHTTPConnectProxyWithTLS(ctx, &net.Dialer{Timeout: 2 * time.Second}, proxyURL, "tcp", "imap.unreachable.invalid:993", 2*time.Second, &tls.Config{RootCAs: proxyRoots})
	if err != nil {
		t.Fatal(err)
	}
	innerTLS := tls.Client(conn, &tls.Config{RootCAs: imapRoots, ServerName: "imap.unreachable.invalid", MinVersion: tls.VersionTLS12})
	if err := innerTLS.HandshakeContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(innerTLS, "ping"); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(innerTLS, response); err != nil {
		t.Fatal(err)
	}
	_ = innerTLS.Close()
	if string(response) != "pong" {
		t.Fatalf("unexpected inner TLS response %q", response)
	}
	result := <-observed
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.outerSNI != "localhost" || result.innerSNI != "imap.unreachable.invalid" || result.target != "imap.unreachable.invalid:993" {
		t.Fatalf("unexpected TLS/CONNECT routing: %#v", result)
	}
}

func TestConfiguredIMAPProxyFailsClosedWithoutCredentialLeak(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyAddress := listener.Addr().String()
	_ = listener.Close()

	dialContext, err := tcpDialContextForProxy("http://proxy-user:proxy-password@"+proxyAddress, 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = dialContext(ctx, "tcp", "127.0.0.1:993")
	if err == nil {
		t.Fatal("configured but unavailable proxy unexpectedly connected")
	}
	if text := err.Error(); strings.Contains(text, "proxy-user") || strings.Contains(text, "proxy-password") {
		t.Fatalf("proxy credentials leaked in error: %s", text)
	}
}

func TestMailboxSettingsUsesAccountProxyDialer(t *testing.T) {
	clearMailboxEnvironment(t)
	root := t.TempDir()
	session := NewSessionManager(filepath.Join(root, "config.json"), filepath.Join(root, "state"))
	if err := session.SetProxy("socks5://127.0.0.1:1080"); err != nil {
		t.Fatal(err)
	}
	var dialCount atomic.Int32
	targets := make(chan string, 1)
	session.mu.Lock()
	session.proxyDial = func(_ context.Context, _, address string) (net.Conn, error) {
		dialCount.Add(1)
		targets <- address
		clientConn, serverConn := net.Pipe()
		go serveTestIMAPConnection(serverConn)
		return clientConn, nil
	}
	session.mu.Unlock()

	service := NewMailboxService(root, session)
	service.dialIMAP = func(address string, options *imapclient.Options, dialContext proxyDialContext) (*imapclient.Client, error) {
		conn, err := dialContext(context.Background(), "tcp", address)
		if err != nil {
			return nil, err
		}
		return imapclient.New(conn, options), nil
	}
	input := MailboxSettingsInput{
		Username: "owner@icloud.com", Password: "app-password", Host: "imap.unreachable.invalid",
		Port: 993, Mailbox: "INBOX", PollSeconds: 120, LookbackDays: 90, CacheMax: 100,
	}
	if err := service.TestSettings(input); err != nil {
		t.Fatal(err)
	}
	if dialCount.Load() != 1 {
		t.Fatalf("account proxy dial count = %d, want 1", dialCount.Load())
	}
	if target := <-targets; target != "imap.unreachable.invalid:993" {
		t.Fatalf("IMAP proxy target = %q", target)
	}
}

func serveSOCKS5Tunnel(conn net.Conn, target chan<- string) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != 5 {
		return fmt.Errorf("unexpected SOCKS version %d", header[0])
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return err
	}
	request := make([]byte, 4)
	if _, err := io.ReadFull(conn, request); err != nil {
		return err
	}
	if request[0] != 5 || request[1] != 1 {
		return fmt.Errorf("unexpected SOCKS request %v", request)
	}
	var host string
	switch request[3] {
	case 1:
		address := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(conn, address); err != nil {
			return err
		}
		host = net.IP(address).String()
	case 3:
		length := []byte{0}
		if _, err := io.ReadFull(conn, length); err != nil {
			return err
		}
		address := make([]byte, int(length[0]))
		if _, err := io.ReadFull(conn, address); err != nil {
			return err
		}
		host = string(address)
	case 4:
		address := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(conn, address); err != nil {
			return err
		}
		host = net.IP(address).String()
	default:
		return fmt.Errorf("unexpected SOCKS address type %d", request[3])
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBytes); err != nil {
		return err
	}
	port := int(portBytes[0])<<8 | int(portBytes[1])
	target <- net.JoinHostPort(host, fmt.Sprint(port))
	if _, err := conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
		return err
	}
	payload := make([]byte, 4)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return err
	}
	if string(payload) != "ping" {
		return fmt.Errorf("unexpected tunnel payload %q", payload)
	}
	_, err := io.WriteString(conn, "pong")
	return err
}

func serveTestIMAPConnection(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writeLine := func(line string) {
		_, _ = fmt.Fprintf(writer, "%s\r\n", line)
		_ = writer.Flush()
	}
	writeLine("* OK proxy IMAP test server ready")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		parts := strings.SplitN(strings.TrimSpace(line), " ", 3)
		if len(parts) < 2 {
			return
		}
		tag, command := parts[0], strings.ToUpper(parts[1])
		switch command {
		case "CAPABILITY":
			writeLine("* CAPABILITY IMAP4rev1")
			writeLine(tag + " OK CAPABILITY completed")
		case "LOGIN":
			writeLine(tag + " OK LOGIN completed")
		case "EXAMINE":
			writeLine("* 0 EXISTS")
			writeLine("* OK [UIDVALIDITY 1]")
			writeLine("* OK [UIDNEXT 1]")
			writeLine("* FLAGS (\\Seen)")
			writeLine("* OK [PERMANENTFLAGS (\\Seen \\*)]")
			writeLine(tag + " OK [READ-ONLY] EXAMINE completed")
		case "LOGOUT":
			writeLine("* BYE logging out")
			writeLine(tag + " OK LOGOUT completed")
			return
		default:
			writeLine(tag + " BAD unsupported")
		}
	}
}

func newTestCertificate(t *testing.T, serverName string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(now.UnixNano()),
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		DNSNames:              []string{serverName},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: privateKey, Leaf: parsed}, roots
}
