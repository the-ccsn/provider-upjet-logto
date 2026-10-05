package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestApplicationMetadataUpdatePreservesUnmanagedFieldsAndExplicitFalseZero(t *testing.T) {
	patched := false
	c, err := NewClient(&Config{Hostname: "example.invalid", ApplicationID: "test", ApplicationSecret: "test", HttpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"id":"app","name":"test","type":"Traditional","oidcClientMetadata":{"redirectUris":[],"postLogoutRedirectUris":[],"futureServerField":"preserve","backchannelLogoutSessionRequired":true},"customClientMetadata":{"idTokenTtl":3600,"rotateRefreshToken":true,"futureServerField":"preserve"}}`
		if r.URL.Path == "/oidc/token" {
			body = `{"access_token":"test","token_type":"Bearer","expires_in":3600}`
		} else if r.Method == http.MethodPatch {
			patched = true
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			oidc := payload["oidcClientMetadata"].(map[string]any)
			custom := payload["customClientMetadata"].(map[string]any)
			if oidc["backchannelLogoutSessionRequired"] != false || custom["rotateRefreshToken"] != false || custom["idTokenTtl"] != float64(0) || oidc["futureServerField"] != "preserve" || custom["futureServerField"] != "preserve" {
				t.Fatal("explicit values or unowned metadata lost")
			}
			if _, exists := payload["isAdmin"]; exists {
				t.Fatal("read-only management grant was patched")
			}
			payload["id"] = "app"
			encoded, _ := json.Marshal(payload)
			body = string(encoded)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.ApplicationUpdate(context.Background(), &ApplicationModel{ID: "app", Name: "test", OidcClientMetadataExtra: map[string]any{"backchannelLogoutSessionRequired": false}, CustomClientMetadataExtra: map[string]any{"rotateRefreshToken": false, "idTokenTtl": float64(0)}})
	if err != nil {
		t.Fatal(err)
	}
	if !patched || result.OidcClientMetadataExtra["backchannelLogoutSessionRequired"] != false || result.CustomClientMetadataExtra["idTokenTtl"] != float64(0) {
		t.Fatal("metadata response was not retained")
	}
}

func TestApplicationMetadataRejectsDuplicateOwnersAndUnknownKeys(t *testing.T) {
	for _, row := range []struct{ kind, body string }{
		{"oidcClientMetadata", `{"redirectUris":[]}`}, {"customClientMetadata", `{"corsAllowedOrigins":[]}`}, {"customClientMetadata", `{"unknown":true}`}, {"oidcClientMetadata", `null`}, {"unknown", `{}`},
	} {
		if _, err := ValidateApplicationMetadata(row.kind, row.body); err == nil {
			t.Fatalf("accepted invalid metadata contract %s", row.kind)
		}
	}
}
