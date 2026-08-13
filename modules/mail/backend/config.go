package mail

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36"

type ICloudConfig struct {
	AppleID               string `json:"appleId,omitempty"`
	Host                  string `json:"host"`
	DSID                  string `json:"dsid"`
	ClientID              string `json:"clientId"`
	ClientBuildNumber     string `json:"clientBuildNumber"`
	ClientMasteringNumber string `json:"clientMasteringNumber"`
	Cookie                string `json:"cookie"`
	LangCode              string `json:"langCode,omitempty"`
	Origin                string `json:"origin,omitempty"`
	Referer               string `json:"referer,omitempty"`
	UserAgent             string `json:"userAgent,omitempty"`
}

type Metadata struct {
	Host                  string `json:"host"`
	DSID                  string `json:"dsid"`
	ClientID              string `json:"clientId"`
	ClientBuildNumber     string `json:"clientBuildNumber"`
	ClientMasteringNumber string `json:"clientMasteringNumber"`
}

func LoadICloudConfig(path string) (ICloudConfig, error) {
	config := ICloudConfig{}
	if err := storage.ReadJSON(path, &config); err != nil {
		if os.IsNotExist(err) {
			return ICloudConfig{}, ErrSessionMissing
		}
		return ICloudConfig{}, err
	}
	config.applyDefaults()
	if err := config.Validate(); err != nil {
		return ICloudConfig{}, err
	}
	return config, nil
}

func (config *ICloudConfig) applyDefaults() {
	config.Host = cleanHost(config.Host)
	china := strings.HasSuffix(strings.ToLower(config.Host), ".icloud.com.cn")
	base := "https://www.icloud.com"
	if china {
		base = "https://www.icloud.com.cn"
	}
	if strings.TrimSpace(config.LangCode) == "" {
		config.LangCode = "zh-cn"
	}
	if strings.TrimSpace(config.Origin) == "" {
		config.Origin = base
	}
	if strings.TrimSpace(config.Referer) == "" {
		config.Referer = base + "/"
	}
	if strings.TrimSpace(config.UserAgent) == "" {
		config.UserAgent = defaultUserAgent
	}
}

func (config ICloudConfig) Validate() error {
	required := map[string]string{
		"host":                  config.Host,
		"dsid":                  config.DSID,
		"clientId":              config.ClientID,
		"clientBuildNumber":     config.ClientBuildNumber,
		"clientMasteringNumber": config.ClientMasteringNumber,
		"cookie":                config.Cookie,
	}
	missing := make([]string, 0)
	for key, value := range required {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config value(s): %s", strings.Join(missing, ", "))
	}
	if !isMailDomainHost(config.Host) {
		return fmt.Errorf("invalid iCloud maildomainws host")
	}
	for label, raw := range map[string]string{"origin": config.Origin, "referer": config.Referer} {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || (parsed.Hostname() != "www.icloud.com" && parsed.Hostname() != "www.icloud.com.cn") {
			return fmt.Errorf("invalid %s URL", label)
		}
	}
	return nil
}

func (config ICloudConfig) Metadata() Metadata {
	return Metadata{
		Host:                  config.Host,
		DSID:                  config.DSID,
		ClientID:              config.ClientID,
		ClientBuildNumber:     config.ClientBuildNumber,
		ClientMasteringNumber: config.ClientMasteringNumber,
	}
}

func cleanHost(value string) string {
	value = strings.TrimSpace(value)
	if parsed, err := url.Parse(value); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return strings.Trim(value, "/")
}

func isMailDomainHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return strings.HasSuffix(host, "-maildomainws.icloud.com") || strings.HasSuffix(host, "-maildomainws.icloud.com.cn")
}
