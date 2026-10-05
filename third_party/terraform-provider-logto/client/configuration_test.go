package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestConfigurationRejectsInvalidContractsBeforeRequests(t *testing.T) {
	c, err := NewClient(&Config{Hostname: "example.invalid", ApplicationID: "test", ApplicationSecret: "test", HttpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid configuration reached the network")
		return nil, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ kind, id, payload string }{
		{"../../users", "default", `{}`}, {"account_center", "not-default", `{}`}, {"connector", "../other", `{}`}, {"account_center", "default", `{"tenantId":"other"}`}, {"connector", "valid", `{"secret":"must-not-be-logged"}`}, {"oidc_session_config", "default", `null`},
	} {
		if _, err := c.ConfigurationWrite(context.Background(), row.kind, row.id, "", row.payload, false); err == nil {
			t.Fatalf("invalid %s contract accepted", row.kind)
		}
	}
}

func TestConfigurationReadNullIsErrorAndDeleteSingletonDoesNotWrite(t *testing.T) {
	c, err := NewClient(&Config{Hostname: "example.invalid", ApplicationID: "test", ApplicationSecret: "test", HttpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `null`
		if r.URL.Path == "/oidc/token" {
			body = `{"access_token":"test","token_type":"Bearer","expires_in":3600}`
		} else if r.Method != http.MethodGet {
			t.Fatal("singleton delete performed a remote write")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfigurationGet(context.Background(), "account_center", "default"); err == nil {
		t.Fatal("null response was treated as a configuration object")
	}
	if err := c.ConfigurationDelete(context.Background(), "account_center", "default"); err != nil {
		t.Fatal(err)
	}
}
