package mail

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const (
	RegionInternational = "international"
	RegionChina         = "china"
)

var (
	quotedURLPattern = regexp.MustCompile(`(?is)(?:--url(?:=|\s+)|curl\s+(?:--location\s+)?)(?:'([^']+)'|"([^"]+)")`)
	plainURLPattern  = regexp.MustCompile(`(?i)https://[^\s'"\\]+`)
	cookiePattern    = regexp.MustCompile(`(?is)(?:-b|--cookie)(?:=|\s+)(?:'([^']*)'|"([^"]*)")`)
	headerPattern    = regexp.MustCompile(`(?is)(?:-H|--header)(?:=|\s+)(?:'([^']*)'|"([^"]*)")`)
)

var coreSessionCookies = []string{
	"X-APPLE-DS-WEB-SESSION-TOKEN",
	"X-APPLE-WEBAUTH-USER",
	"X-APPLE-WEBAUTH-TOKEN",
}

func ParseImportText(text, region string) (ICloudConfig, error) {
	region, err := normalizeRegion(region)
	if err != nil {
		return ICloudConfig{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ICloudConfig{}, fmt.Errorf("请先粘贴 cURL 命令或 HAR JSON")
	}
	var document map[string]any
	if json.Unmarshal([]byte(text), &document) == nil {
		if _, ok := document["log"].(map[string]any); ok {
			return parseHAR(document, region)
		}
	}
	return ParseHMECurl(text, region)
}

func ParseHMECurl(text, region string) (ICloudConfig, error) {
	region, err := normalizeRegion(region)
	if err != nil {
		return ICloudConfig{}, err
	}
	rawURL := ""
	if match := quotedURLPattern.FindStringSubmatch(text); len(match) == 3 {
		rawURL = strings.TrimSpace(firstMatch(match[1], match[2]))
	} else if match := plainURLPattern.FindString(text); match != "" {
		rawURL = match
	}
	cookie := ""
	if match := cookiePattern.FindStringSubmatch(text); len(match) == 3 {
		cookie = strings.TrimSpace(firstMatch(match[1], match[2]))
	}
	headers := curlHeaders(text)
	if cookie == "" {
		cookie = headers["cookie"]
	}
	return configFromRequest(rawURL, cookie, headers, region)
}

func parseHAR(document map[string]any, region string) (ICloudConfig, error) {
	logObject, _ := document["log"].(map[string]any)
	entries, ok := logObject["entries"].([]any)
	if !ok {
		return ICloudConfig{}, fmt.Errorf("HAR log.entries 必须是数组")
	}
	var fallback map[string]any
	var selected map[string]any
	for _, item := range entries {
		entry, _ := item.(map[string]any)
		request, _ := entry["request"].(map[string]any)
		rawURL, _ := request["url"].(string)
		parsed, err := url.Parse(rawURL)
		if err != nil || !isMailDomainHost(parsed.Hostname()) {
			continue
		}
		if strings.HasPrefix(parsed.Path, "/v2/hme/list") {
			selected = request
			break
		}
		if fallback == nil && strings.Contains(parsed.Path, "/hme/") {
			fallback = request
		}
	}
	if selected == nil {
		selected = fallback
	}
	if selected == nil {
		return ICloudConfig{}, fmt.Errorf("HAR 中没有 maildomainws 的 HME 请求，请打开隐藏邮件地址页面后重新导出 HAR")
	}
	headers := harHeaders(selected)
	cookie := headers["cookie"]
	if cookie == "" {
		cookies, _ := selected["cookies"].([]any)
		pairs := make([]string, 0, len(cookies))
		for _, item := range cookies {
			value, _ := item.(map[string]any)
			name := strings.TrimSpace(fmt.Sprint(value["name"]))
			if name != "" && name != "<nil>" {
				pairs = append(pairs, name+"="+fmt.Sprint(value["value"]))
			}
		}
		cookie = strings.Join(pairs, "; ")
	}
	return configFromRequest(fmt.Sprint(selected["url"]), cookie, headers, region)
}

func configFromRequest(rawURL, cookie string, headers map[string]string, region string) (ICloudConfig, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || !isMailDomainHost(parsed.Hostname()) {
		return ICloudConfig{}, fmt.Errorf("请求 URL 必须是 maildomainws.icloud.com 或 maildomainws.icloud.com.cn")
	}
	if !strings.HasPrefix(parsed.Path, "/v2/hme/list") && !strings.HasPrefix(parsed.Path, "/v1/hme/") {
		return ICloudConfig{}, fmt.Errorf("请求 URL 必须指向 /v2/hme/list 或 /v1/hme/*")
	}
	if err := requireCookies(cookie); err != nil {
		return ICloudConfig{}, err
	}
	query := parsed.Query()
	requiredQuery := func(name string) (string, error) {
		value := strings.TrimSpace(query.Get(name))
		if value == "" {
			return "", fmt.Errorf("缺少查询参数：%s", name)
		}
		return value, nil
	}
	dsid, err := requiredQuery("dsid")
	if err != nil {
		return ICloudConfig{}, err
	}
	clientID, err := requiredQuery("clientId")
	if err != nil {
		return ICloudConfig{}, err
	}
	build, err := requiredQuery("clientBuildNumber")
	if err != nil {
		return ICloudConfig{}, err
	}
	mastering, err := requiredQuery("clientMasteringNumber")
	if err != nil {
		return ICloudConfig{}, err
	}

	host := normalizeServiceHost(parsed.Hostname(), region)
	webBase := "https://www.icloud.com"
	if region == RegionChina {
		webBase += ".cn"
	}
	config := ICloudConfig{
		Host: host, DSID: dsid, ClientID: clientID,
		ClientBuildNumber: build, ClientMasteringNumber: mastering,
		Cookie: cookie, LangCode: "zh-cn", Origin: webBase,
		Referer: webBase + "/", UserAgent: headers["user-agent"],
	}
	config.Origin = normalizeWebURL(headers["origin"], region, config.Origin)
	config.Referer = normalizeWebURL(headers["referer"], region, config.Referer)
	config.applyDefaults()
	return config, config.Validate()
}

func normalizeRegion(region string) (string, error) {
	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		region = RegionInternational
	}
	if region != RegionInternational && region != RegionChina {
		return "", fmt.Errorf("icloud_region 必须是 international 或 china")
	}
	return region, nil
}

func normalizeServiceHost(host, region string) string {
	host = strings.TrimSuffix(strings.ToLower(host), ".cn")
	if region == RegionChina {
		return host + ".cn"
	}
	return host
}

func normalizeWebURL(value, region, fallback string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || (parsed.Hostname() != "www.icloud.com" && parsed.Hostname() != "www.icloud.com.cn") {
		return fallback
	}
	host := "www.icloud.com"
	if region == RegionChina {
		host += ".cn"
	}
	parsed.Host = host
	return parsed.String()
}

func requireCookies(cookie string) error {
	names := map[string]bool{}
	for _, part := range strings.Split(cookie, ";") {
		name, _, _ := strings.Cut(part, "=")
		names[strings.TrimSpace(name)] = true
	}
	missing := make([]string, 0)
	for _, name := range coreSessionCookies {
		if !names[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("Cookie 缺少必要的 iCloud Session 值：%s。请在 /v2/hme/list 请求上使用 Copy as cURL", strings.Join(missing, ", "))
	}
	return nil
}

func curlHeaders(text string) map[string]string {
	result := map[string]string{}
	for _, match := range headerPattern.FindAllStringSubmatch(text, -1) {
		if len(match) != 3 {
			continue
		}
		name, value, ok := strings.Cut(firstMatch(match[1], match[2]), ":")
		if ok {
			result[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(value)
		}
	}
	return result
}

func firstMatch(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func harHeaders(request map[string]any) map[string]string {
	result := map[string]string{}
	headers, _ := request["headers"].([]any)
	for _, item := range headers {
		header, _ := item.(map[string]any)
		name := strings.ToLower(strings.TrimSpace(fmt.Sprint(header["name"])))
		if name != "" && name != "<nil>" {
			result[name] = strings.TrimSpace(fmt.Sprint(header["value"]))
		}
	}
	return result
}
