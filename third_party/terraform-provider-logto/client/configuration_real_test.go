//go:build integration

package client

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestRealConfigurationContracts(t *testing.T) {
	c := disposableClient(t)
	ctx := context.Background()
	for kind, payload := range map[string]string{
		"sign_in_experience":  `{"supportWebsiteUrl":"https://support.example.invalid"}`,
		"account_center":      `{"enabled":true}`,
		"id_token_config":     `{"enabledExtendedClaims":["roles"]}`,
		"oidc_session_config": `{"ttl":1800}`,
	} {
		t.Run(kind, func(t *testing.T) {
			before, err := c.ConfigurationGet(ctx, kind, "default")
			if err != nil {
				t.Fatal(err)
			}
			var desired map[string]any
			if err := json.Unmarshal([]byte(payload), &desired); err != nil {
				t.Fatal(err)
			}
			restore := map[string]any{}
			for key := range desired {
				restore[key] = before[key]
			}
			t.Cleanup(func() {
				encoded, _ := json.Marshal(restore)
				if _, err := c.ConfigurationWrite(ctx, kind, "default", "", string(encoded), false); err != nil {
					t.Error(err)
				}
			})
			if _, err := c.ConfigurationWrite(ctx, kind, "default", "", payload, true); err != nil {
				t.Fatal(err)
			}
			after, err := c.ConfigurationGet(ctx, kind, "default")
			if err != nil {
				t.Fatal(err)
			}
			for key, want := range desired {
				if !reflect.DeepEqual(after[key], want) {
					t.Fatalf("configuration %s did not persist", key)
				}
			}
		})
	}

	t.Run("connector", func(t *testing.T) {
		payload := `{"config":{"clientId":"acceptance-only","clientSecret":"acceptance-only"},"syncProfile":false}`
		created, err := c.ConfigurationWrite(ctx, "connector", "", "github-universal", payload, true)
		if err != nil {
			t.Fatal(err)
		}
		id, ok := created["id"].(string)
		if !ok || id == "" {
			t.Fatal("connector ID missing")
		}
		t.Cleanup(func() {
			if err := c.ConfigurationDelete(ctx, "connector", id); err != nil {
				t.Error(err)
			}
		})
		read, err := c.ConfigurationGet(ctx, "connector", id)
		if err != nil || read["connectorId"] != "github-universal" {
			t.Fatalf("connector read: %v", err)
		}
		if _, err := c.ConfigurationWrite(ctx, "connector", id, "", `{"syncProfile":true}`, false); err != nil {
			t.Fatal(err)
		}
		read, err = c.ConfigurationGet(ctx, "connector", id)
		if err != nil || read["syncProfile"] != true {
			t.Fatalf("connector update: %v", err)
		}
		if err := c.ConfigurationDelete(ctx, "connector", id); err != nil {
			t.Fatal(err)
		}
		read, err = c.ConfigurationGet(ctx, "connector", id)
		if err != nil || read != nil {
			t.Fatalf("connector was not deleted: %v", err)
		}
	})
}
