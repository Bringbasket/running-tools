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
	service := NewMailboxService(t.TempDir(), NewSessionManager(filepath.Join(t.TempDir(), "config.json"), t.TempDir()))
	var connections, logins, noops atomic.Int32
	service.dialIMAP = func(_ string, options *imapclient.Options) (*imapclient.Client, error) {
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
	service.closeIMAPConnection()
	if err != nil {
		t.Fatal(err)
	}
	if connections.Load() != 1 || logins.Load() != 1 || noops.Load() != 1 {
		t.Fatalf("unexpected reuse counts: connections=%d logins=%d noops=%d", connections.Load(), logins.Load(), noops.Load())
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
