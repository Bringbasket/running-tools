package mail

import (
	"encoding/json"
	"strings"
	"testing"
)

const validCurl = `curl --url 'https://p120-maildomainws.icloud.com/v2/hme/list?clientBuildNumber=2626Build21&clientMasteringNumber=2626Build21&clientId=client-1&dsid=123456' \
  -H 'Origin: https://www.icloud.com' \
  -H 'Referer: https://www.icloud.com/icloudplus/' \
  -H 'User-Agent: Test Browser' \
  -b 'X-APPLE-WEBAUTH-USER="user"; X-APPLE-WEBAUTH-TOKEN="token"; X-APPLE-DS-WEB-SESSION-TOKEN="session"'`

func TestParseHMECurl(t *testing.T) {
	config, err := ParseHMECurl(validCurl, "")
	if err != nil {
		t.Fatal(err)
	}
	if config.Host != "p120-maildomainws.icloud.com" || config.DSID != "123456" || config.ClientID != "client-1" {
		t.Fatalf("unexpected config: %#v", config.Metadata())
	}
	if config.UserAgent != "Test Browser" || config.Referer != "https://www.icloud.com/icloudplus/" {
		t.Fatalf("optional headers were not preserved: %#v", config)
	}
}

func TestParseHMECurlDetectsChinaRegion(t *testing.T) {
	chinaCurl := strings.ReplaceAll(validCurl, "icloud.com", "icloud.com.cn")
	config, err := ParseHMECurl(chinaCurl, "")
	if err != nil {
		t.Fatal(err)
	}
	if config.Host != "p120-maildomainws.icloud.com.cn" || config.Origin != "https://www.icloud.com.cn" || config.Referer != "https://www.icloud.com.cn/icloudplus/" {
		t.Fatalf("China region was not detected from request URL: %#v", config)
	}
}

func TestParseHMECurlNormalizesChinaRegion(t *testing.T) {
	config, err := ParseHMECurl(validCurl, RegionChina)
	if err != nil {
		t.Fatal(err)
	}
	if config.Host != "p120-maildomainws.icloud.com.cn" || config.Origin != "https://www.icloud.com.cn" {
		t.Fatalf("China region was not applied: %#v", config)
	}
}

func TestParseHARCookieArray(t *testing.T) {
	document := map[string]any{"log": map[string]any{"entries": []any{map[string]any{"request": map[string]any{
		"url":     "https://p120-maildomainws.icloud.com/v2/hme/list?clientBuildNumber=b&clientMasteringNumber=m&clientId=c&dsid=d",
		"headers": []any{}, "cookies": []any{
			map[string]any{"name": "X-APPLE-WEBAUTH-USER", "value": "u"},
			map[string]any{"name": "X-APPLE-WEBAUTH-TOKEN", "value": "t"},
			map[string]any{"name": "X-APPLE-DS-WEB-SESSION-TOKEN", "value": "s"},
		},
	}}}}}
	encoded, _ := json.Marshal(document)
	config, err := ParseImportText(string(encoded), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(config.Cookie, "X-APPLE-DS-WEB-SESSION-TOKEN=s") {
		t.Fatalf("cookie array not imported: %s", config.Cookie)
	}
}

func TestParseCurlRequiresCoreCookies(t *testing.T) {
	_, err := ParseHMECurl(strings.Replace(validCurl, `X-APPLE-WEBAUTH-TOKEN="token"; `, "", 1), RegionInternational)
	if err == nil || !strings.Contains(err.Error(), "X-APPLE-WEBAUTH-TOKEN") {
		t.Fatalf("expected missing-cookie error, got %v", err)
	}
}
