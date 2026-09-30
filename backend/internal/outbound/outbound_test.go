package outbound

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestOutboundTransportUsesEnvironmentProxyAndHonorsNoProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://172.24.176.1:10808")
	t.Setenv("HTTPS_PROXY", "http://172.24.176.1:10808")
	t.Setenv("NO_PROXY", ".se7endot.top,100.64.0.0/10")
	transport := newOutboundTransport(resolveOutboundHost)

	proxiedRequest, _ := http.NewRequest(http.MethodGet, "https://api.mikoto.vip/v1/models", nil)
	proxyURL, err := transport.Proxy(proxiedRequest)
	if err != nil {
		t.Fatal(err)
	}
	wantProxy, _ := url.Parse("http://172.24.176.1:10808")
	if proxyURL == nil || proxyURL.String() != wantProxy.String() {
		t.Fatalf("proxy = %v, want %v", proxyURL, wantProxy)
	}

	directRequest, _ := http.NewRequest(http.MethodGet, "https://api.se7endot.top/v1/models", nil)
	proxyURL, err = transport.Proxy(directRequest)
	if err != nil {
		t.Fatal(err)
	}
	if proxyURL != nil {
		t.Fatalf("NO_PROXY request proxy = %v, want nil", proxyURL)
	}
}

func TestConfiguredProxyHostIsTrustedAsDeploymentEgress(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://172.24.176.1:10808")
	if !configuredProxyHost("172.24.176.1") {
		t.Fatal("configuredProxyHost() rejected the configured private proxy")
	}
	if configuredProxyHost("172.24.176.2") {
		t.Fatal("configuredProxyHost() accepted an unrelated private host")
	}
}

func TestNormalizeOutboundHeadersAllowsCustomUserAgent(t *testing.T) {
	headers, err := NormalizeOutboundHeaders([]OutboundHeader{{Name: "user-agent", Value: "Custom Gateway/2.0"}, {Name: "X-Gateway-Tenant", Value: "tenant-a"}})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	ApplyOutboundHeaders(request, headers)
	ApplyDefaultOutboundHeaders(request)
	if got := request.Header.Get("User-Agent"); got != "Custom Gateway/2.0" {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := request.Header.Get("X-Gateway-Tenant"); got != "tenant-a" {
		t.Fatalf("X-Gateway-Tenant = %q", got)
	}
}

func TestNormalizeOutboundHeadersRejectsUnsafeAndDuplicateValues(t *testing.T) {
	tests := [][]OutboundHeader{
		{{Name: "Authorization", Value: "Bearer attacker"}},
		{{Name: "X-Test", Value: "safe\r\ninjected: true"}},
		{{Name: "X-Test", Value: "one"}, {Name: "x-test", Value: "two"}},
	}
	for _, headers := range tests {
		if _, err := NormalizeOutboundHeaders(headers); err == nil {
			t.Fatalf("NormalizeOutboundHeaders(%#v) should fail", headers)
		}
	}
}

func TestDecodeRelayOutboundHeaders(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString([]byte(`[{"name":"User-Agent","value":"Relay Agent"}]`))
	headers, err := DecodeRelayOutboundHeaders(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(headers) != 1 || headers[0].Name != "User-Agent" || headers[0].Value != "Relay Agent" {
		t.Fatalf("DecodeRelayOutboundHeaders() = %#v", headers)
	}
}

func TestApplyDefaultOutboundHeaders(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	ApplyDefaultOutboundHeaders(request)
	if got := request.Header.Get("User-Agent"); got != DefaultOutboundUserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, DefaultOutboundUserAgent)
	}

	request.Header.Set("User-Agent", "custom-agent")
	ApplyDefaultOutboundHeaders(request)
	if got := request.Header.Get("User-Agent"); got != "custom-agent" {
		t.Fatalf("custom User-Agent = %q", got)
	}
}

func TestValidateOutboundURLRejectsPrivateHosts(t *testing.T) {
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "false")
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "")
	for _, rawURL := range []string{"http://127.0.0.1:8080", "http://localhost:8080", "http://169.254.169.254/latest/meta-data"} {
		if _, err := ValidateOutboundURL(rawURL); err == nil {
			t.Fatalf("ValidateOutboundURL(%q) should fail", rawURL)
		}
	}
}

func TestValidateOutboundURLAllowsExplicitPrivateUpstreamOverride(t *testing.T) {
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "")
	if _, err := ValidateOutboundURL("http://127.0.0.1:8080"); err != nil {
		t.Fatalf("ValidateOutboundURL() error = %v", err)
	}
}

func TestValidateOutboundURLAllowsOnlyNamedPrivateUpstream(t *testing.T) {
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "false")
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	if _, err := ValidateOutboundURL("http://127.0.0.1:8080"); err != nil {
		t.Fatalf("ValidateOutboundURL() error = %v", err)
	}
	if _, err := ValidateOutboundURL("http://127.0.0.2:8080"); err == nil {
		t.Fatal("ValidateOutboundURL() should reject an unlisted private host")
	}
}

func TestAllowedPrivateUpstreamHostUsesExactCaseInsensitiveMatch(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", " API.EXAMPLE.COM.,trusted.internal ")
	if !AllowedPrivateUpstreamHost("api.example.com") {
		t.Fatal("AllowedPrivateUpstreamHost() should allow exact normalized hostname")
	}
	if AllowedPrivateUpstreamHost("api.example.com.evil.test") {
		t.Fatal("AllowedPrivateUpstreamHost() should reject hostname suffix confusion")
	}
}

func TestValidateCustomRelayURLAllowsHTTPOnlyForExactPrivateAllowlist(t *testing.T) {
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "")
	if _, err := ValidateCustomRelayURL("http://127.0.0.1:8080/v1/models"); err == nil {
		t.Fatal("ValidateCustomRelayURL() should reject HTTP without an exact host allowlist")
	}
	if _, err := ValidateCustomRelayURL("https://127.0.0.1:8080/v1/models"); err == nil {
		t.Fatal("ValidateCustomRelayURL() should ignore the global private upstream override")
	}
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	for _, rawURL := range []string{
		"http://127.0.0.1:8080/v1/models",
		"https://127.0.0.1:8080/v1/models",
	} {
		if _, err := ValidateCustomRelayURL(rawURL); err != nil {
			t.Fatalf("ValidateCustomRelayURL(%q) error = %v", rawURL, err)
		}
	}
}

func TestValidateCustomRelayURLRejectsCredentialsAndFragment(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	for _, rawURL := range []string{
		"https://user:pass@127.0.0.1/v1/models",
		"https://127.0.0.1/v1/models#secret",
	} {
		if _, err := ValidateCustomRelayURL(rawURL); err == nil {
			t.Fatalf("ValidateCustomRelayURL(%q) should fail", rawURL)
		}
	}
}

func TestBlockedCustomRelayIPRejectsCarrierGradeNATAndReservedRanges(t *testing.T) {
	for _, value := range []string{"100.100.100.200", "192.0.2.10", "198.18.0.1", "2001:db8::1"} {
		if !blockedCustomRelayIP(net.ParseIP(value)) {
			t.Fatalf("blockedCustomRelayIP(%q) = false", value)
		}
	}
	if blockedCustomRelayIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("blockedCustomRelayIP() rejected a public address")
	}
}

func TestCustomRelayHTTPClientDoesNotFollowRedirects(t *testing.T) {
	redirected := false
	destination := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected = true
	}))
	defer destination.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer source.Close()

	client := CustomRelayHTTPClient(time.Second)
	client.Transport = source.Client().Transport
	if _, err := client.Get(source.URL); err == nil {
		t.Fatal("CustomRelayHTTPClient() should reject redirects")
	}
	if redirected {
		t.Fatal("redirect destination should not receive the request")
	}
}

// TestOutboundTransportTriesEveryResolvedAddress 覆盖"解析结果里的第一个地址连不上"。
//
// 这正是双栈域名的常见形态：IPv6 排在解析结果前面，而运行环境没有 IPv6 出口。只拨
// addresses[0] 的实现会在这里失败，而失败的那次生成在上游侧可能已经计费——代价不只是
// 一次重试，是一笔说不清的账。
func TestOutboundTransportTriesEveryResolvedAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("解析测试服务地址失败: %v", err)
	}
	_, port, err := net.SplitHostPort(serverURL.Host)
	if err != nil {
		t.Fatalf("解析测试服务地址失败: %v", err)
	}

	// 127.0.0.2 与 127.0.0.1 同端口：前者没人监听（立刻连接被拒），后者是真实服务。
	// 第一个地址必须被跳过，才可能连上。
	transport := newOutboundTransport(func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.2"), net.ParseIP("127.0.0.1")}, nil
	})
	client := &http.Client{Transport: transport}
	response, err := client.Get("http://outbound-test.invalid:" + port + "/")
	if err != nil {
		t.Fatalf("第一个解析地址不可达时应继续尝试下一个，实际报错: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
}

// TestOutboundTransportBoundsStalledResponseHeaders 覆盖"连接活着、响应头永远不来"。
// 没有这条上限时只能等内核放弃：生产机上实测卡满 179 秒，而上游这段时间早就受理、
// 出图、把成品丢了。所以这里把上限压到毫秒级，确认它确实作用在传输层上。
func TestOutboundTransportBoundsStalledResponseHeaders(t *testing.T) {
	original := outboundResponseHeaderTimeout
	outboundResponseHeaderTimeout = 200 * time.Millisecond
	defer func() { outboundResponseHeaderTimeout = original }()

	stalled := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		// 故意不回响应头：模拟上游把连接吊住。
		<-request.Context().Done()
	}))
	defer stalled.Close()
	stalledURL, err := url.Parse(stalled.URL)
	if err != nil {
		t.Fatalf("解析测试服务地址失败: %v", err)
	}
	_, port, err := net.SplitHostPort(stalledURL.Host)
	if err != nil {
		t.Fatalf("解析测试服务地址失败: %v", err)
	}

	transport := newOutboundTransport(func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	})
	client := &http.Client{Transport: transport}
	startedAt := time.Now()
	response, err := client.Get("http://outbound-test.invalid:" + port + "/")
	if err == nil {
		_ = response.Body.Close()
		t.Fatal("响应头超时未生效：吊住的连接没有报错")
	}
	elapsed := time.Since(startedAt)
	if elapsed > 5*time.Second {
		t.Fatalf("响应头超时未生效：等了 %s 才失败", elapsed)
	}
	var timeoutError net.Error
	if !errors.As(err, &timeoutError) || !timeoutError.Timeout() {
		t.Fatalf("错误应该来自响应头超时，实际: %v", err)
	}
}

// TestPreferIPv4Order 覆盖拨号顺序：IPv4 在前，同族内保持解析顺序。
func TestPreferIPv4Order(t *testing.T) {
	ordered := preferIPv4Order([]net.IP{
		net.ParseIP("2606:4700::6812:23c"),
		net.ParseIP("2606:4700::6812:33c"),
		net.ParseIP("104.18.3.60"),
		net.ParseIP("104.18.2.60"),
	})
	want := []string{"104.18.3.60", "104.18.2.60", "2606:4700::6812:23c", "2606:4700::6812:33c"}
	if len(ordered) != len(want) {
		t.Fatalf("地址数量 = %d, want %d", len(ordered), len(want))
	}
	for index, ip := range ordered {
		if ip.String() != want[index] {
			t.Fatalf("第 %d 个地址 = %s, want %s", index, ip, want[index])
		}
	}
}
