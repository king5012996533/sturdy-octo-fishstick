package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthTransportUsesSessionCookie(t *testing.T) {
	var cookie, token string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie = r.Header.Get("Cookie")
		token = r.Header.Get("X-Desktop-Token")
	}))
	defer server.Close()

	client := &http.Client{Transport: authTransport{base: http.DefaultTransport, cookie: "kinotv_session=abc"}}
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Desktop-Token", "")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if cookie != "kinotv_session=abc" {
		t.Fatalf("cookie = %q", cookie)
	}
	if token != "" {
		t.Fatalf("desktop token should be removed, got %q", token)
	}
}

func TestAuthTransportUsesBearerToken(t *testing.T) {
	var authorization, cookie, token string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		cookie = r.Header.Get("Cookie")
		token = r.Header.Get("X-Desktop-Token")
	}))
	defer server.Close()

	client := &http.Client{Transport: authTransport{base: http.DefaultTransport, bearer: "ktv_mcp_abc"}}
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if authorization != "Bearer ktv_mcp_abc" || cookie != "" || token != "" {
		t.Fatalf("authorization=%q cookie=%q token=%q", authorization, cookie, token)
	}
}

func TestAuthTransportUsesDesktopToken(t *testing.T) {
	var cookie, token string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie = r.Header.Get("Cookie")
		token = r.Header.Get("X-Desktop-Token")
	}))
	defer server.Close()

	client := &http.Client{Transport: authTransport{base: http.DefaultTransport, token: "desktop-token"}}
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if token != "desktop-token" || cookie != "" {
		t.Fatalf("token=%q cookie=%q", token, cookie)
	}
}

func TestResolveMCPLocalRuntime(t *testing.T) {
	for _, test := range []struct {
		profile, token, bearer, cookie string
		want                           bool
	}{
		{"", "", "", "canana_session=abc", false},
		{"", "desktop-token", "", "", true},
		{"", "", "ktv_mcp_token", "", false},
		{"", "", "", "", false},
		{"local", "", "", "canana_session=abc", true},
		{"hosted", "desktop-token", "", "", false},
		{"desktop", "", "", "", true},
		{"server", "", "", "", false},
	} {
		if got := resolveMCPLocalRuntime(test.profile, test.token, test.bearer, test.cookie); got != test.want {
			t.Fatalf("resolveMCPLocalRuntime(%q,%q,%q,%q) = %v, want %v", test.profile, test.token, test.bearer, test.cookie, got, test.want)
		}
	}
}

func TestNormalizeMCPBaseURL(t *testing.T) {
	for _, test := range []struct{ raw, want string }{
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080/api"},
		{"http://127.0.0.1:8080/", "http://127.0.0.1:8080/api"},
		{"http://127.0.0.1:8080/api", "http://127.0.0.1:8080/api"},
		{"http://127.0.0.1:8080/api/", "http://127.0.0.1:8080/api"},
		{"https://kinotv.example.com", "https://kinotv.example.com/api"},
	} {
		if got := normalizeMCPBaseURL(test.raw); got != test.want {
			t.Fatalf("normalizeMCPBaseURL(%q) = %q, want %q", test.raw, got, test.want)
		}
	}
}
