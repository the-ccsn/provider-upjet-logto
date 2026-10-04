package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUnitTokenCache(t *testing.T) {
	calls := 0
	c, err := NewClient(&Config{Hostname: "example.invalid", ApplicationID: "test", ApplicationSecret: "test", HttpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/oidc/token" {
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"token","expires_in":3600,"token_type":"Bearer"}`))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := c.getAccessToken(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("unexpired token fetched %d times", calls)
	}
	if time.Until(c.accessTokenExpires) < 40*time.Minute {
		t.Fatal("expires_in must be interpreted in seconds")
	}
	c.accessTokenExpires = time.Now().Add(-time.Second)
	if _, err := c.getAccessToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("expired token was reused")
	}
}
func TestUnitErrorsDoNotExposeResponseBody(t *testing.T) {
	_, err := expect(200)(&http.Response{StatusCode: 400, Status: "400 Bad Request", Body: io.NopCloser(strings.NewReader("private-response-value"))}, nil)
	if err == nil || strings.Contains(err.Error(), "private-response-value") {
		t.Fatal("error must omit response body")
	}
}
