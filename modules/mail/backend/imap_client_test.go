package mail

import (
	"bufio"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

func TestIMAPLogoutRunsAfterMailboxCommands(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := imapclient.New(clientConn, nil)
	defer client.Close()
	defer serverConn.Close()

	commands := make(chan []string, 1)
	go func() {
		reader := bufio.NewReader(serverConn)
		writer := bufio.NewWriter(serverConn)
		writeLine := func(line string) {
			_, _ = fmt.Fprintf(writer, "%s\r\n", line)
			_ = writer.Flush()
		}
		writeLine("* OK test server ready")

		var seen []string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				commands <- seen
				return
			}
			line = strings.TrimSpace(line)
			parts := strings.SplitN(line, " ", 3)
			if len(parts) < 2 {
				commands <- append(seen, line)
				return
			}
			tag, command := parts[0], strings.ToUpper(parts[1])
			seen = append(seen, command)
			switch command {
			case "CAPABILITY":
				writeLine("* CAPABILITY IMAP4rev1")
				writeLine(tag + " OK CAPABILITY completed")
			case "LOGIN":
				writeLine(tag + " OK LOGIN completed")
			case "EXAMINE":
				writeLine("* 0 EXISTS")
				writeLine("* 0 RECENT")
				writeLine("* OK [UIDVALIDITY 1]")
				writeLine("* OK [UIDNEXT 1]")
				writeLine("* FLAGS (\\Seen)")
				writeLine("* OK [PERMANENTFLAGS (\\Seen \\*)]")
				writeLine(tag + " OK [READ-ONLY] EXAMINE completed")
			case "LOGOUT":
				writeLine("* BYE logging out")
				writeLine(tag + " OK LOGOUT completed")
				commands <- seen
				return
			default:
				writeLine(tag + " BAD unsupported")
			}
		}
	}()

	if err := client.WaitGreeting(); err != nil {
		t.Fatal(err)
	}
	if err := loginSelectAndLogoutForTest(client); err != nil {
		t.Fatal(err)
	}
	got := <-commands
	loginIndex := commandIndex(got, "LOGIN")
	examineIndex := commandIndex(got, "EXAMINE")
	logoutIndex := commandIndex(got, "LOGOUT")
	if loginIndex < 0 || examineIndex <= loginIndex || logoutIndex <= examineIndex {
		t.Fatalf("unexpected IMAP command order: %v", got)
	}
}

func TestMailboxServiceReusesAuthenticatedIMAPConnection(t *testing.T) {
	root := t.TempDir()
	session := NewSessionManager(filepath.Join(root, "config.json"), filepath.Join(root, "state"))
	service := NewMailboxService(root, session)
	var connections, logins, noops atomic.Int32
	service.dialIMAP = func(_ string, options *imapclient.Options, _ proxyDialContext) (*imapclient.Client, error) {
		connections.Add(1)
		clientConn, serverConn := net.Pipe()
		go func() {
			defer serverConn.Close()
			reader := bufio.NewReader(serverConn)
			writer := bufio.NewWriter(serverConn)
			writeLine := func(line string) {
				_, _ = fmt.Fprintf(writer, "%s\r\n", line)
				_ = writer.Flush()
			}
			writeLine("* OK reusable test server ready")
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
					writeLine("* CAPABILITY IMAP4rev1 IDLE")
					writeLine(tag + " OK CAPABILITY completed")
				case "LOGIN":
					logins.Add(1)
					writeLine(tag + " OK LOGIN completed")
				case "NOOP":
					noops.Add(1)
					writeLine(tag + " OK NOOP completed")
				default:
					writeLine(tag + " BAD unsupported")
				}
			}
		}()
		return imapclient.New(clientConn, options), nil
	}
	cfg := mailboxConfig{Username: "owner@icloud.com", Password: "app-password", Host: "imap.mail.me.com", Port: 993, Mailbox: "INBOX"}
	service.syncMu.Lock()
	first, err := service.ensureIMAPConnectionLocked(cfg)
	if err == nil {
		second, secondErr := service.ensureIMAPConnectionLocked(cfg)
		if secondErr != nil {
			err = secondErr
		} else if first != second {
			t.Fatal("expected the same IMAP client to be reused")
		}
	}
	service.syncMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if connections.Load() != 1 || logins.Load() != 1 || noops.Load() != 1 {
		t.Fatalf("unexpected reuse counts: connections=%d logins=%d noops=%d", connections.Load(), logins.Load(), noops.Load())
	}

	if err := session.SetProxy("http://127.0.0.1:8080"); err != nil {
		t.Fatal(err)
	}
	service.imapMu.Lock()
	activeAfterProxyChange := service.imapClient
	service.imapMu.Unlock()
	if activeAfterProxyChange != nil {
		t.Fatal("proxy change did not close the active IMAP connection")
	}
	service.syncMu.Lock()
	_, err = service.ensureIMAPConnectionLocked(cfg)
	service.syncMu.Unlock()
	service.closeIMAPConnection()
	if err != nil {
		t.Fatal(err)
	}
	if connections.Load() != 2 || logins.Load() != 2 || noops.Load() != 1 {
		t.Fatalf("unexpected post-proxy-change counts: connections=%d logins=%d noops=%d", connections.Load(), logins.Load(), noops.Load())
	}
}

func TestFetchIMAPAliasHeadersReadsOnlyRecipientHeaders(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := imapclient.New(clientConn, nil)
	defer client.Close()
	defer serverConn.Close()

	serverErr := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(serverConn)
		writer := bufio.NewWriter(serverConn)
		writeLine := func(line string) error {
			if _, err := fmt.Fprintf(writer, "%s\r\n", line); err != nil {
				return err
			}
			return writer.Flush()
		}
		if err := writeLine("* OK header test server ready"); err != nil {
			serverErr <- err
			return
		}
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				serverErr <- err
				return
			}
			parts := strings.SplitN(strings.TrimSpace(line), " ", 3)
			if len(parts) < 2 {
				serverErr <- fmt.Errorf("invalid IMAP command %q", line)
				return
			}
			tag, command := parts[0], strings.ToUpper(parts[1])
			switch command {
			case "CAPABILITY":
				_ = writeLine("* CAPABILITY IMAP4rev1")
				_ = writeLine(tag + " OK CAPABILITY completed")
			case "LOGIN":
				_ = writeLine(tag + " OK LOGIN completed")
			case "EXAMINE":
				_ = writeLine("* 1 EXISTS")
				_ = writeLine("* OK [UIDVALIDITY 1]")
				_ = writeLine("* OK [UIDNEXT 8]")
				_ = writeLine("* FLAGS (\\Seen)")
				_ = writeLine("* OK [PERMANENTFLAGS (\\Seen \\*)]")
				_ = writeLine(tag + " OK [READ-ONLY] EXAMINE completed")
			case "UID":
				if len(parts) < 3 || !strings.HasPrefix(strings.ToUpper(parts[2]), "FETCH ") {
					serverErr <- fmt.Errorf("unexpected UID command %q", line)
					return
				}
				header := "To: Display <one@icloud.com>\r\nDelivered-To: two@icloud.com\r\n\r\n"
				section := "BODY[HEADER.FIELDS (TO CC DELIVERED-TO X-ORIGINAL-TO X-FORWARDED-TO)]"
				if _, err := fmt.Fprintf(writer, "* 1 FETCH (UID 7 %s {%d}\r\n%s)\r\n", section, len(header), header); err != nil {
					serverErr <- err
					return
				}
				if err := writeLine(tag + " OK UID FETCH completed"); err != nil {
					serverErr <- err
					return
				}
				serverErr <- nil
				return
			default:
				serverErr <- fmt.Errorf("unsupported IMAP command %q", command)
				return
			}
		}
	}()

	if err := client.WaitGreeting(); err != nil {
		t.Fatal(err)
	}
	if err := client.Login("owner@icloud.com", "app-password").Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		t.Fatal(err)
	}
	headers, err := fetchIMAPAliasHeaders(client, []imap.UID{7}, map[string]bool{
		"one@icloud.com": true,
		"two@icloud.com": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(headers) != 1 || headers[0].UID != 7 || strings.Join(headers[0].Aliases, ",") != "one@icloud.com,two@icloud.com" {
		t.Fatalf("unexpected recipient headers: %#v", headers)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

func TestMailboxWorkerCanRestartAfterStop(t *testing.T) {
	service := NewMailboxService(t.TempDir(), NewSessionManager(filepath.Join(t.TempDir(), "config.json"), t.TempDir()))
	for cycle := 0; cycle < 2; cycle++ {
		service.Start()
		service.Stop()
		service.mu.Lock()
		stop, done, running := service.stop, service.done, service.workerRunning
		service.mu.Unlock()
		if stop != nil || done != nil || running {
			t.Fatalf("worker cycle %d did not stop cleanly", cycle+1)
		}
	}
}

func commandIndex(commands []string, target string) int {
	for index, command := range commands {
		if command == target {
			return index
		}
	}
	return -1
}

func loginSelectAndLogoutForTest(client *imapclient.Client) error {
	if err := client.Login("owner@icloud.com", "app-password").Wait(); err != nil {
		return err
	}
	defer logoutIMAP(client)
	_, err := client.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait()
	return err
}
