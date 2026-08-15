package mail

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bringbasket/running-tools/internal/platform/storage"
	"github.com/emersion/go-imap/v2/imapclient"
)

type syncStorageProbe struct {
	cache          mailboxCache
	loadCalls      int
	loadStateCalls int
	saveCalls      int
	saveStateCalls int
}

func (r *syncStorageProbe) Load(context.Context) (mailboxCache, bool, error) {
	r.loadCalls++
	return cloneMailboxCacheForTest(r.cache), true, nil
}

func (r *syncStorageProbe) LoadState(context.Context) (mailboxCache, bool, error) {
	r.loadStateCalls++
	return normalizedMailboxCache(mailboxCache{
		Status:         r.cache.Status,
		HighestUID:     r.cache.HighestUID,
		AllowedAliases: append([]string(nil), r.cache.AllowedAliases...),
	}), true, nil
}

func (r *syncStorageProbe) Save(_ context.Context, cache mailboxCache) error {
	r.saveCalls++
	r.cache = cloneMailboxCacheForTest(cache)
	return nil
}

func (r *syncStorageProbe) SaveState(_ context.Context, state mailboxCache) error {
	r.saveStateCalls++
	r.cache.Status = state.Status
	r.cache.HighestUID = state.HighestUID
	r.cache.AllowedAliases = append([]string(nil), state.AllowedAliases...)
	return nil
}

func (r *syncStorageProbe) ListAliasMessages(_ context.Context, generation, alias string, limit int, detailed bool) ([]MailMessage, error) {
	if generation != r.cache.Status.MailboxGeneration {
		return []MailMessage{}, nil
	}
	return selectAliasMessages(r.cache, alias, limit, detailed), nil
}

func (r *syncStorageProbe) ListRecentMessages(_ context.Context, generation string, cutoff float64, limit int) ([]MailMessage, error) {
	if generation != r.cache.Status.MailboxGeneration {
		return []MailMessage{}, nil
	}
	return selectRecentMessages(r.cache, cutoff, limit), nil
}

func (r *syncStorageProbe) GetMessage(_ context.Context, generation, alias string, uid uint32) (MailMessage, bool, error) {
	if generation != r.cache.Status.MailboxGeneration {
		return MailMessage{}, false, nil
	}
	message, ok := selectMessage(r.cache, alias, uid)
	return message, ok, nil
}

func (r *syncStorageProbe) Clear(context.Context) error {
	r.cache = normalizedMailboxCache(mailboxCache{})
	return nil
}

func cloneMailboxCacheForTest(cache mailboxCache) mailboxCache {
	cache.Messages = cloneMailMessages(cache.Messages)
	cache.Hidden = append([]string(nil), cache.Hidden...)
	cache.AllowedAliases = append([]string(nil), cache.AllowedAliases...)
	return normalizedMailboxCache(cache)
}

func TestRunSyncNoopUsesStateOnlyStorage(t *testing.T) {
	alias := "one@icloud.com"
	service, cfg, repository := newSyncStorageTestService(t)
	repository.cache = mailboxCache{
		Status:         MailboxStatus{UIDValidity: 7, MailboxGeneration: mailboxGeneration(cfg, 7), Revision: 3},
		Messages:       []MailMessage{{UID: 10, Aliases: []string{alias}, Date: 10, Text: strings.Repeat("body", 1000)}},
		HighestUID:     10,
		AllowedAliases: []string{alias},
	}
	service.dialIMAP = emptyMailboxDialer(t, 7)

	status, err := service.RunSync([]string{alias})
	if err != nil {
		t.Fatal(err)
	}
	if status.LastError != "" || repository.loadCalls != 0 || repository.saveCalls != 0 {
		t.Fatalf("no-op sync touched full messages: status=%#v loads=%d saves=%d", status, repository.loadCalls, repository.saveCalls)
	}
	if repository.loadStateCalls != 2 || repository.saveStateCalls != 1 || repository.cache.HighestUID != 10 {
		t.Fatalf("unexpected state-only calls: loads=%d saves=%d cache=%#v", repository.loadStateCalls, repository.saveStateCalls, repository.cache)
	}
}

func TestLoadStateInvalidatesFullCacheWhenGenerationChanges(t *testing.T) {
	service, _, repository := newSyncStorageTestService(t)
	service.lastCache = mailboxCache{
		Status:   MailboxStatus{MailboxGeneration: "generation-old"},
		Messages: []MailMessage{{UID: 100, Aliases: []string{"old@icloud.com"}}},
	}
	service.cacheLoaded = true
	repository.cache = mailboxCache{
		Status:     MailboxStatus{MailboxGeneration: "generation-new"},
		HighestUID: 0,
	}

	service.mu.Lock()
	state := service.loadStateLocked()
	cacheLoaded := service.cacheLoaded
	cachedMessages := len(service.lastCache.Messages)
	service.mu.Unlock()

	if state.Status.MailboxGeneration != "generation-new" || cacheLoaded || cachedMessages != 0 {
		t.Fatalf("stale generation cache survived: state=%#v loaded=%v messages=%d", state, cacheLoaded, cachedMessages)
	}
}

func TestRunSyncErrorPersistsOnlyState(t *testing.T) {
	alias := "one@icloud.com"
	service, cfg, repository := newSyncStorageTestService(t)
	repository.cache = mailboxCache{
		Status:         MailboxStatus{UIDValidity: 7, MailboxGeneration: mailboxGeneration(cfg, 7)},
		Messages:       []MailMessage{{UID: 10, Aliases: []string{alias}, Text: "must stay untouched"}},
		HighestUID:     10,
		AllowedAliases: []string{alias},
	}
	service.dialIMAP = func(string, *imapclient.Options, proxyDialContext) (*imapclient.Client, error) {
		return nil, errors.New("dial failed")
	}

	status, err := service.RunSync([]string{alias})
	if err == nil || !strings.Contains(status.LastError, "dial failed") {
		t.Fatalf("expected dial failure, status=%#v err=%v", status, err)
	}
	if repository.loadCalls != 0 || repository.saveCalls != 0 || repository.saveStateCalls != 1 {
		t.Fatalf("error sync touched full messages: loads=%d saves=%d stateSaves=%d", repository.loadCalls, repository.saveCalls, repository.saveStateCalls)
	}
	if len(repository.cache.Messages) != 1 || repository.cache.Messages[0].Text != "must stay untouched" {
		t.Fatalf("error sync changed cached messages: %#v", repository.cache.Messages)
	}
}

func TestRunSyncLoadsLegacyMessagesOnlyToRecoverMissingUIDCursor(t *testing.T) {
	alias := "one@icloud.com"
	service, cfg, repository := newSyncStorageTestService(t)
	repository.cache = mailboxCache{
		Status:         MailboxStatus{UIDValidity: 7, MailboxGeneration: mailboxGeneration(cfg, 7)},
		Messages:       []MailMessage{{UID: 42, Aliases: []string{alias}}},
		HighestUID:     0,
		AllowedAliases: []string{alias},
	}
	service.dialIMAP = func(string, *imapclient.Options, proxyDialContext) (*imapclient.Client, error) {
		return nil, errors.New("dial failed")
	}

	_, _ = service.RunSync([]string{alias})
	if repository.loadCalls != 1 || repository.saveCalls != 0 || repository.saveStateCalls != 1 {
		t.Fatalf("legacy cursor path calls: loads=%d saves=%d stateSaves=%d", repository.loadCalls, repository.saveCalls, repository.saveStateCalls)
	}
}

func TestRunSyncLoadsFullCacheForGenerationAndAliasChanges(t *testing.T) {
	t.Run("uid validity", func(t *testing.T) {
		alias := "one@icloud.com"
		service, cfg, repository := newSyncStorageTestService(t)
		repository.cache = mailboxCache{
			Status:         MailboxStatus{UIDValidity: 7, MailboxGeneration: mailboxGeneration(cfg, 7), Revision: 2},
			Messages:       []MailMessage{{UID: 10, Aliases: []string{alias}}},
			Hidden:         []string{messageKey(mailboxGeneration(cfg, 7), alias, 10)},
			HighestUID:     10,
			AllowedAliases: []string{alias},
		}
		service.dialIMAP = emptyMailboxDialer(t, 8)

		status, err := service.RunSync([]string{alias})
		if err != nil {
			t.Fatal(err)
		}
		if repository.loadCalls != 1 || repository.saveCalls != 1 || len(repository.cache.Messages) != 0 || len(repository.cache.Hidden) != 0 {
			t.Fatalf("generation change did not replace full cache: loads=%d saves=%d cache=%#v", repository.loadCalls, repository.saveCalls, repository.cache)
		}
		if status.Revision != 3 || status.UIDValidity != 8 {
			t.Fatalf("generation status mismatch: %#v", status)
		}
	})

	t.Run("allowed aliases", func(t *testing.T) {
		service, cfg, repository := newSyncStorageTestService(t)
		repository.cache = mailboxCache{
			Status:         MailboxStatus{UIDValidity: 7, MailboxGeneration: mailboxGeneration(cfg, 7), Revision: 2},
			Messages:       []MailMessage{{UID: 10, Aliases: []string{"old@icloud.com"}}},
			HighestUID:     10,
			AllowedAliases: []string{"old@icloud.com"},
		}
		service.dialIMAP = emptyMailboxDialer(t, 7)

		status, err := service.RunSync([]string{"new@icloud.com"})
		if err != nil {
			t.Fatal(err)
		}
		if repository.loadCalls != 1 || repository.saveCalls != 1 || len(repository.cache.Messages) != 0 {
			t.Fatalf("alias change did not prune full cache: loads=%d saves=%d cache=%#v", repository.loadCalls, repository.saveCalls, repository.cache)
		}
		if status.Revision != 3 || len(repository.cache.AllowedAliases) != 1 || repository.cache.AllowedAliases[0] != "new@icloud.com" {
			t.Fatalf("alias status mismatch: status=%#v cache=%#v", status, repository.cache)
		}
	})
}

func newSyncStorageTestService(t *testing.T) (*MailboxService, mailboxConfig, *syncStorageProbe) {
	t.Helper()
	root := t.TempDir()
	session := NewSessionManager(filepath.Join(root, "config.json"), filepath.Join(root, "state"))
	service := NewMailboxService(root, session)
	repository := &syncStorageProbe{}
	service.repository = repository
	cfg := withMailboxDefaults(mailboxConfig{
		Username: "owner@icloud.com", Password: "app-password", Host: "imap.mail.me.com", Port: 993,
		Mailbox: "INBOX", PollSeconds: 120, LookbackDays: 90, CacheMax: 5000, Source: "saved",
	})
	stored := mailboxStoredConfig{
		Username: cfg.Username, Password: cfg.Password, Host: cfg.Host, Port: cfg.Port, Mailbox: cfg.Mailbox,
		PollSeconds: cfg.PollSeconds, LookbackDays: cfg.LookbackDays, CacheMax: cfg.CacheMax,
	}
	if err := storage.WriteJSON(service.settingsPath, stored, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.closeIMAPConnection)
	return service, cfg, repository
}

func emptyMailboxDialer(t *testing.T, uidValidity uint32) func(string, *imapclient.Options, proxyDialContext) (*imapclient.Client, error) {
	t.Helper()
	return func(_ string, options *imapclient.Options, _ proxyDialContext) (*imapclient.Client, error) {
		clientConn, serverConn := net.Pipe()
		go serveEmptyTestMailbox(serverConn, uidValidity)
		return imapclient.New(clientConn, options), nil
	}
}

func serveEmptyTestMailbox(conn net.Conn, uidValidity uint32) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writeLine := func(line string) bool {
		if _, err := fmt.Fprintf(writer, "%s\r\n", line); err != nil {
			return false
		}
		return writer.Flush() == nil
	}
	if !writeLine("* OK storage test server ready") {
		return
	}
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
			if !writeLine("* CAPABILITY IMAP4rev1") || !writeLine(tag+" OK CAPABILITY completed") {
				return
			}
		case "LOGIN":
			if !writeLine(tag + " OK LOGIN completed") {
				return
			}
		case "EXAMINE":
			responses := []string{
				"* FLAGS (\\Seen)", "* 0 EXISTS", "* 0 RECENT",
				fmt.Sprintf("* OK [UIDVALIDITY %d] UIDs valid", uidValidity),
				"* OK [UIDNEXT 1] Predicted next UID", "* OK [PERMANENTFLAGS (\\Seen \\*)]",
				tag + " OK [READ-ONLY] EXAMINE completed",
			}
			for _, response := range responses {
				if !writeLine(response) {
					return
				}
			}
		case "UID":
			if len(parts) < 3 || !strings.HasPrefix(strings.ToUpper(parts[2]), "SEARCH ") {
				_ = writeLine(tag + " BAD unsupported UID command")
				continue
			}
			if !writeLine("* SEARCH") || !writeLine(tag+" OK SEARCH completed") {
				return
			}
		case "NOOP":
			if !writeLine(tag + " OK NOOP completed") {
				return
			}
		case "LOGOUT":
			_ = writeLine("* BYE logging out")
			_ = writeLine(tag + " OK LOGOUT completed")
			return
		default:
			if !writeLine(tag + " BAD unsupported") {
				return
			}
		}
	}
}

var _ mailboxRepository = (*syncStorageProbe)(nil)
