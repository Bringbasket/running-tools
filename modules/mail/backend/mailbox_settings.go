package mail

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/storage"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

var ErrInvalidMailboxSettings = errors.New("invalid mailbox settings")

type MailboxSettings struct {
	Username           string `json:"username"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	Mailbox            string `json:"mailbox"`
	Enabled            bool   `json:"enabled"`
	PollSeconds        int    `json:"pollSeconds"`
	LookbackDays       int    `json:"lookbackDays"`
	CacheMax           int    `json:"cacheMax"`
	PasswordConfigured bool   `json:"passwordConfigured"`
	Source             string `json:"source"`
}

type MailboxSettingsInput struct {
	Username     string `json:"username"`
	Password     string `json:"password"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Mailbox      string `json:"mailbox"`
	Enabled      bool   `json:"enabled"`
	PollSeconds  int    `json:"pollSeconds"`
	LookbackDays int    `json:"lookbackDays"`
	CacheMax     int    `json:"cacheMax"`
}

type mailboxStoredConfig struct {
	Username     string `json:"username"`
	Password     string `json:"password,omitempty"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Mailbox      string `json:"mailbox"`
	Enabled      bool   `json:"enabled"`
	PollSeconds  int    `json:"pollSeconds"`
	LookbackDays int    `json:"lookbackDays"`
	CacheMax     int    `json:"cacheMax"`
}

var imapHostnamePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

func (s *MailboxService) Settings() MailboxSettings {
	return mailboxSettingsView(s.config())
}

func (s *MailboxService) UpdateSettings(input MailboxSettingsInput) (MailboxSettings, error) {
	s.RequestSync()
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	s.mu.Lock()
	current := s.config()
	next, stored, err := normalizeMailboxSettings(input, current)
	if err != nil {
		s.mu.Unlock()
		return MailboxSettings{}, err
	}
	if err := storage.WriteJSON(s.settingsPath, stored, 0o600); err != nil {
		s.mu.Unlock()
		return MailboxSettings{}, err
	}

	cache := s.loadLocked()
	if s.lastLoadErr != nil {
		err := s.lastLoadErr
		s.mu.Unlock()
		return MailboxSettings{}, fmt.Errorf("读取邮箱存储失败: %w", err)
	}
	if mailboxTargetChanged(current, next) {
		s.closeIMAPConnectionLocked()
		cache.Messages = []MailMessage{}
		cache.Hidden = []string{}
		cache.HighestUID = 0
		cache.AllowedAliases = nil
		cache.Status.UIDValidity = 0
		cache.Status.MailboxGeneration = ""
		cache.Status.LastSyncAt = nil
		cache.Status.LastError = ""
		cache.Status.Revision++
		if err := s.saveLocked(cache); err != nil {
			s.mu.Unlock()
			return MailboxSettings{}, err
		}
		s.notifyRevisionLocked()
	}
	s.mu.Unlock()
	s.RequestSync()
	return mailboxSettingsView(next), nil
}

func (s *MailboxService) TestSettings(input MailboxSettingsInput) error {
	current := s.config()
	next, _, err := normalizeMailboxSettings(input, current)
	if err != nil {
		return err
	}
	if next.Username == "" || next.Password == "" {
		return fmt.Errorf("%w: 请填写 IMAP 账号和应用专用密码", ErrInvalidMailboxSettings)
	}
	return testIMAPConnection(next)
}

func (s *MailboxService) config() mailboxConfig {
	cfg := mailboxConfigFromEnv()
	var stored mailboxStoredConfig
	if err := storage.ReadJSON(s.settingsPath, &stored); err == nil {
		cfg.Username = strings.TrimSpace(stored.Username)
		if stored.Password != "" {
			cfg.Password = stored.Password
			cfg.PasswordStored = true
		}
		cfg.Host = strings.TrimSpace(stored.Host)
		cfg.Port = stored.Port
		cfg.Mailbox = strings.TrimSpace(stored.Mailbox)
		cfg.Enabled = stored.Enabled
		cfg.PollSeconds = stored.PollSeconds
		cfg.LookbackDays = stored.LookbackDays
		cfg.CacheMax = stored.CacheMax
		cfg.Source = "saved"
	}
	return withMailboxDefaults(cfg)
}

func mailboxConfigFromEnv() mailboxConfig {
	password := strings.TrimSpace(os.Getenv("HME_IMAP_PASSWORD"))
	if file := strings.TrimSpace(os.Getenv("HME_IMAP_PASSWORD_FILE")); password == "" && file != "" {
		if data, err := os.ReadFile(file); err == nil {
			password = strings.TrimSpace(string(data))
		}
	}
	return withMailboxDefaults(mailboxConfig{
		Username:     strings.TrimSpace(os.Getenv("HME_IMAP_USERNAME")),
		Password:     password,
		Host:         strings.TrimSpace(os.Getenv("HME_IMAP_HOST")),
		Port:         envInt("HME_IMAP_PORT"),
		Mailbox:      strings.TrimSpace(os.Getenv("HME_IMAP_MAILBOX")),
		Enabled:      envBool("HME_MAIL_SYNC_ENABLED"),
		PollSeconds:  envInt("HME_MAIL_SYNC_POLL_SECONDS"),
		LookbackDays: envInt("HME_IMAP_LOOKBACK_DAYS"),
		CacheMax:     envInt("HME_IMAP_CACHE_MAX_MESSAGES"),
		Source:       "environment",
	})
}

func withMailboxDefaults(cfg mailboxConfig) mailboxConfig {
	if cfg.Host == "" {
		cfg.Host = "imap.mail.me.com"
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		cfg.Port = 993
	}
	if cfg.Mailbox == "" {
		cfg.Mailbox = "INBOX"
	}
	if cfg.PollSeconds < 30 || cfg.PollSeconds > 86400 {
		cfg.PollSeconds = 120
	}
	if cfg.LookbackDays < 1 || cfg.LookbackDays > 3650 {
		cfg.LookbackDays = 90
	}
	if cfg.CacheMax < 100 || cfg.CacheMax > 50000 {
		cfg.CacheMax = 5000
	}
	if cfg.Source == "" {
		cfg.Source = "environment"
	}
	return cfg
}

func normalizeMailboxSettings(input MailboxSettingsInput, current mailboxConfig) (mailboxConfig, mailboxStoredConfig, error) {
	host := strings.TrimSpace(strings.Trim(input.Host, "[]"))
	username := strings.TrimSpace(input.Username)
	mailbox := strings.TrimSpace(input.Mailbox)
	password := input.Password
	if password == "" {
		password = current.Password
	}
	if username == "" && input.Enabled {
		return mailboxConfig{}, mailboxStoredConfig{}, fmt.Errorf("%w: 启用同步前请填写 IMAP 账号", ErrInvalidMailboxSettings)
	}
	if password == "" && input.Enabled {
		return mailboxConfig{}, mailboxStoredConfig{}, fmt.Errorf("%w: 启用同步前请填写应用专用密码", ErrInvalidMailboxSettings)
	}
	if err := validateIMAPHost(host); err != nil {
		return mailboxConfig{}, mailboxStoredConfig{}, err
	}
	if isICloudMailbox(username) && isLegacyICloudIMAPHost(host) {
		host = "imap.mail.me.com"
	}
	if input.Port < 1 || input.Port > 65535 {
		return mailboxConfig{}, mailboxStoredConfig{}, fmt.Errorf("%w: IMAP 端口必须在 1 到 65535 之间", ErrInvalidMailboxSettings)
	}
	if mailbox == "" || len(mailbox) > 255 || strings.ContainsAny(mailbox, "\r\n\x00") {
		return mailboxConfig{}, mailboxStoredConfig{}, fmt.Errorf("%w: 邮箱目录无效", ErrInvalidMailboxSettings)
	}
	if len(username) > 320 || len(password) > 4096 {
		return mailboxConfig{}, mailboxStoredConfig{}, fmt.Errorf("%w: IMAP 账号或密码过长", ErrInvalidMailboxSettings)
	}
	if input.PollSeconds < 30 || input.PollSeconds > 86400 {
		return mailboxConfig{}, mailboxStoredConfig{}, fmt.Errorf("%w: 轮询间隔必须在 30 到 86400 秒之间", ErrInvalidMailboxSettings)
	}
	if input.LookbackDays < 1 || input.LookbackDays > 3650 {
		return mailboxConfig{}, mailboxStoredConfig{}, fmt.Errorf("%w: 首次回看天数必须在 1 到 3650 天之间", ErrInvalidMailboxSettings)
	}
	if input.CacheMax < 100 || input.CacheMax > 50000 {
		return mailboxConfig{}, mailboxStoredConfig{}, fmt.Errorf("%w: 缓存上限必须在 100 到 50000 封之间", ErrInvalidMailboxSettings)
	}

	next := mailboxConfig{
		Username: username, Password: password, Host: host, Port: input.Port, Mailbox: mailbox,
		Enabled: input.Enabled, PollSeconds: input.PollSeconds, LookbackDays: input.LookbackDays,
		CacheMax: input.CacheMax, Source: "saved",
	}
	stored := mailboxStoredConfig{
		Username: username, Password: password, Host: host, Port: input.Port, Mailbox: mailbox,
		Enabled: input.Enabled, PollSeconds: input.PollSeconds, LookbackDays: input.LookbackDays,
		CacheMax: input.CacheMax,
	}
	if input.Password == "" && !current.PasswordStored {
		stored.Password = ""
	}
	return next, stored, nil
}

func mailboxSettingsView(cfg mailboxConfig) MailboxSettings {
	return MailboxSettings{
		Username: cfg.Username, Host: cfg.Host, Port: cfg.Port, Mailbox: cfg.Mailbox,
		Enabled: cfg.Enabled, PollSeconds: cfg.PollSeconds, LookbackDays: cfg.LookbackDays,
		CacheMax: cfg.CacheMax, PasswordConfigured: cfg.Password != "", Source: cfg.Source,
	}
}

func mailboxTargetChanged(current, next mailboxConfig) bool {
	return current.Username != next.Username ||
		!strings.EqualFold(current.Host, next.Host) || current.Port != next.Port || current.Mailbox != next.Mailbox
}

func validateIMAPHost(host string) error {
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "/\\@ \t\r\n\x00") {
		return fmt.Errorf("%w: IMAP 服务器地址无效，请只填写主机名或 IP", ErrInvalidMailboxSettings)
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	if strings.Contains(host, ":") || !imapHostnamePattern.MatchString(host) {
		return fmt.Errorf("%w: IMAP 服务器地址无效，请只填写主机名或 IP", ErrInvalidMailboxSettings)
	}
	return nil
}

func testIMAPConnection(cfg mailboxConfig) error {
	client, err := imapclient.DialTLS(net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)), &imapclient.Options{Dialer: &net.Dialer{Timeout: 20 * time.Second}})
	if err != nil {
		return fmt.Errorf("IMAP TLS 连接失败: %w", err)
	}
	defer client.Close()
	if err := client.Login(cfg.Username, cfg.Password).Wait(); err != nil {
		return imapLoginError(cfg, err)
	}
	defer logoutIMAP(client)
	if _, err := client.Select(cfg.Mailbox, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return fmt.Errorf("IMAP 邮箱选择失败: %w", err)
	}
	return nil
}

func isICloudMailbox(username string) bool {
	address := strings.ToLower(strings.TrimSpace(username))
	return strings.HasSuffix(address, "@icloud.com") ||
		strings.HasSuffix(address, "@me.com") ||
		strings.HasSuffix(address, "@mac.com")
}

func isLegacyICloudIMAPHost(host string) bool {
	return strings.EqualFold(host, "imap.gmail.com") || strings.EqualFold(host, "imap.icloud.com")
}

func imapLoginError(cfg mailboxConfig, err error) error {
	if isICloudMailbox(cfg.Username) || strings.EqualFold(cfg.Host, "imap.mail.me.com") {
		return fmt.Errorf("IMAP 身份验证失败: 请确认服务器为 imap.mail.me.com:993，并使用 Apple Account 生成的 App 专用密码（不能使用 Apple ID 登录密码）: %w", err)
	}
	return fmt.Errorf("IMAP 身份验证失败: %w", err)
}

func logoutIMAP(client *imapclient.Client) {
	_ = client.Logout().Wait()
}

func envInt(name string) int {
	value, _ := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	return value
}

func envBool(name string) bool {
	value := strings.TrimSpace(os.Getenv(name))
	return value == "1" || strings.EqualFold(value, "true")
}

func defaultMailboxSettingsPath(stateDir string) string {
	return filepath.Join(stateDir, "mailbox-config.json")
}
