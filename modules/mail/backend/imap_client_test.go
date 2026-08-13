package mail

import (
	"bufio"
	"fmt"
	"net"
	"strings"
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
