package mail

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

func TestMailboxSettingsPersistWithoutReturningPassword(t *testing.T) {
	clearMailboxEnvironment(t)
	service := NewMailboxService(t.TempDir(), nil)
	input := MailboxSettingsInput{
		Username: "owner@example.com", Password: "app-password", Host: "imap.example.com",
		Port: 993, Mailbox: "INBOX", Enabled: true, PollSeconds: 60, LookbackDays: 30, CacheMax: 1000,
	}
	settings, err := service.UpdateSettings(input)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.PasswordConfigured || settings.Source != "saved" || settings.Username != input.Username {
		t.Fatalf("unexpected settings: %#v", settings)
	}
	data, err := os.ReadFile(service.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "app-password") {
		t.Fatal("password was not persisted")
	}
	info, err := os.Stat(service.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("settings permissions = %o, want 600", info.Mode().Perm())
	}

	input.Password = ""
	input.PollSeconds = 90
	if _, err := service.UpdateSettings(input); err != nil {
		t.Fatal(err)
	}
	if got := service.config(); got.Password != "app-password" || got.PollSeconds != 90 {
		t.Fatalf("blank password did not preserve the stored secret: %#v", got)
	}
}

func TestMailboxSettingsDoNotCopyEnvironmentPasswordIntoState(t *testing.T) {
	clearMailboxEnvironment(t)
	t.Setenv("HME_IMAP_USERNAME", "owner@example.com")
	t.Setenv("HME_IMAP_PASSWORD", "environment-secret")
	service := NewMailboxService(t.TempDir(), nil)
	input := MailboxSettingsInput{
		Username: "owner@example.com", Host: "imap.example.com", Port: 993, Mailbox: "INBOX",
		Enabled: true, PollSeconds: 120, LookbackDays: 90, CacheMax: 5000,
	}
	settings, err := service.UpdateSettings(input)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.PasswordConfigured {
		t.Fatal("environment password should remain available after saving settings")
	}
	data, err := os.ReadFile(service.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "environment-secret") {
		t.Fatal("environment password was copied into the persisted settings file")
	}
	if _, err := service.UpdateSettings(input); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(service.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "environment-secret") {
		t.Fatal("environment password was copied into state by a repeated save")
	}
}

func TestMailboxSettingsTargetChangeClearsAccountCache(t *testing.T) {
	clearMailboxEnvironment(t)
	service := NewMailboxService(t.TempDir(), nil)
	initial := MailboxSettingsInput{
		Username: "first@example.com", Password: "secret", Host: "imap.example.com",
		Port: 993, Mailbox: "INBOX", Enabled: false, PollSeconds: 120, LookbackDays: 90, CacheMax: 5000,
	}
	if _, err := service.UpdateSettings(initial); err != nil {
		t.Fatal(err)
	}
	cache := mailboxCache{
		Status:   MailboxStatus{Revision: 4, UIDValidity: 7, MailboxGeneration: "old-account"},
		Messages: []MailMessage{{UID: 10, Aliases: []string{"alias@icloud.com"}}},
		Hidden:   []string{"old-account:alias@icloud.com:10"}, HighestUID: 10,
	}
	if err := storage.WriteJSON(service.path, cache, 0o600); err != nil {
		t.Fatal(err)
	}

	initial.Username = "second@example.com"
	if _, err := service.UpdateSettings(initial); err != nil {
		t.Fatal(err)
	}
	var saved mailboxCache
	if err := storage.ReadJSON(service.path, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) != 0 || len(saved.Hidden) != 0 || saved.HighestUID != 0 || saved.Status.Revision != 5 || saved.Status.MailboxGeneration != "" {
		t.Fatalf("old account cache survived target change: %#v", saved)
	}
}

func TestMailboxSettingsRejectInvalidHostAndEnabledWithoutCredentials(t *testing.T) {
	clearMailboxEnvironment(t)
	current := mailboxConfig{}
	base := MailboxSettingsInput{Host: "imap.example.com", Port: 993, Mailbox: "INBOX", PollSeconds: 120, LookbackDays: 90, CacheMax: 5000}
	base.Host = "https://imap.example.com/path"
	if _, _, err := normalizeMailboxSettings(base, current); err == nil {
		t.Fatal("invalid host was accepted")
	}
	base.Host = "imap.example.com"
	base.Enabled = true
	if _, _, err := normalizeMailboxSettings(base, current); err == nil {
		t.Fatal("enabled settings without credentials were accepted")
	}
}

func TestMailboxSettingsUseICloudDefaultAndCorrectGmailForICloudAddress(t *testing.T) {
	clearMailboxEnvironment(t)
	if got := mailboxConfigFromEnv().Host; got != "imap.mail.me.com" {
		t.Fatalf("default IMAP host = %q, want imap.mail.me.com", got)
	}

	input := MailboxSettingsInput{
		Username: "owner@icloud.com", Password: "app-password", Host: "imap.gmail.com",
		Port: 993, Mailbox: "INBOX", PollSeconds: 120, LookbackDays: 90, CacheMax: 5000,
	}
	next, stored, err := normalizeMailboxSettings(input, mailboxConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if next.Host != "imap.mail.me.com" || stored.Host != "imap.mail.me.com" {
		t.Fatalf("iCloud host was not corrected: next=%q stored=%q", next.Host, stored.Host)
	}

	input.Host = "imap.icloud.com"
	next, _, err = normalizeMailboxSettings(input, mailboxConfig{})
	if err != nil || next.Host != "imap.mail.me.com" {
		t.Fatalf("legacy iCloud host was not corrected: host=%q err=%v", next.Host, err)
	}
}

func TestIMAPLoginErrorExplainsICloudAppSpecificPassword(t *testing.T) {
	err := imapLoginError(mailboxConfig{Username: "owner@icloud.com", Host: "imap.mail.me.com", Port: 993}, errors.New("authentication failed"))
	if !strings.Contains(err.Error(), "App 专用密码") || !strings.Contains(err.Error(), "不能使用 Apple ID 登录密码") {
		t.Fatalf("unexpected iCloud login error: %v", err)
	}
}

func clearMailboxEnvironment(t *testing.T) {
	for _, name := range []string{
		"HME_IMAP_USERNAME", "HME_IMAP_PASSWORD", "HME_IMAP_PASSWORD_FILE", "HME_IMAP_HOST",
		"HME_IMAP_PORT", "HME_IMAP_MAILBOX", "HME_MAIL_SYNC_ENABLED", "HME_MAIL_SYNC_POLL_SECONDS",
		"HME_IMAP_LOOKBACK_DAYS", "HME_IMAP_CACHE_MAX_MESSAGES",
	} {
		t.Setenv(name, "")
	}
}
