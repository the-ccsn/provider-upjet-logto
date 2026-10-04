//go:build integration

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

func disposableClient(t *testing.T) *Client {
	t.Helper()
	data, err := os.ReadFile(os.Getenv("LOGTO_TEST_CREDENTIALS"))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]string
	if err := json.Unmarshal(data, &values); err != nil {
		t.Fatal("invalid credentials JSON")
	}
	endpoint, err := url.Parse(values["endpoint"])
	if err != nil || net.ParseIP(endpoint.Hostname()) == nil || !net.ParseIP(endpoint.Hostname()).IsLoopback() {
		t.Fatal("real acceptance requires explicit loopback IP")
	}
	c, err := NewClient(&Config{Endpoint: endpoint.String(), Hostname: values["hostname"], ApplicationID: values["application_id"], ApplicationSecret: values["application_secret"], Resource: values["resource"]})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRealPaginationBeyond100(t *testing.T) {
	c := disposableClient(t)
	ctx := context.Background()
	prefix := fmt.Sprintf("pages_%d", time.Now().UnixNano())
	parent, err := c.ApiResourceCreate(ctx, &ApiResourceModel{Name: prefix, Indicator: "https://" + prefix + ".example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.ApiResourceDelete(ctx, parent.ID); err != nil {
			t.Error(err)
		}
	})
	var scope *ScopeModel
	for i := 0; i < 101; i++ {
		scope, err = c.ApiResourceScopeCreate(ctx, parent.ID, &ScopeModel{Name: fmt.Sprintf("scope_%03d", i)})
		if err != nil {
			t.Fatal(err)
		}
	}
	found, err := c.ApiResourceScopeGet(ctx, parent.ID, scope.ID)
	if err != nil || found == nil || found.Name != scope.Name {
		t.Fatalf("scope on page 2 was lost: %v", err)
	}
	account, err := c.UserCreate(ctx, &UserModel{Username: prefix})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.UserDelete(ctx, account.ID); err != nil {
			t.Error(err)
		}
	})
	roles := []string{}
	t.Cleanup(func() {
		for _, id := range roles {
			if err := c.RoleDelete(ctx, id); err != nil {
				t.Error(err)
			}
		}
	})
	for i := 0; i < 101; i++ {
		role, err := c.RoleCreate(ctx, &RoleModel{Name: fmt.Sprintf("%s_%03d", prefix, i), Description: "Acceptance pagination", Type: "User"})
		if err != nil {
			t.Fatal(err)
		}
		roles = append(roles, role.ID)
	}
	if err := c.UpdateRolesForUser(ctx, &RoleIdsModel{RoleIds: roles}, account.ID); err != nil {
		t.Fatal(err)
	}
	assigned, err := c.GetRolesForUser(ctx, account.ID)
	if err != nil || len(assigned) != 101 {
		t.Fatalf("expected 101 roles, got %d: %v", len(assigned), err)
	}
}

func TestRealUnmanagedMetadataSurvivesUpdates(t *testing.T) {
	c := disposableClient(t)
	ctx := context.Background()
	name := fmt.Sprintf("metadata_%d", time.Now().UnixNano())
	app, err := c.ApplicationCreate(ctx, &ApplicationModel{Name: name, Type: "Traditional", OidcClientMetadata: &OidcClientMetadata{}, CustomClientMetadata: &CustomClientMetadata{IdTokenTtl: 7200, RotateRefreshToken: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.ApplicationDelete(ctx, app.ID); err != nil {
			t.Error(err)
		}
	})
	updated, err := c.ApplicationUpdate(ctx, &ApplicationModel{ID: app.ID, Name: name + "_changed", OidcClientMetadata: &OidcClientMetadata{}, CustomClientMetadata: &CustomClientMetadata{}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.CustomClientMetadata.IdTokenTtl != 7200 || !updated.CustomClientMetadata.RotateRefreshToken {
		t.Fatal("unmanaged token configuration was overwritten")
	}
	response, err := expect(200)(c.do(ctx, &request{method: http.MethodPost, path: "api/users", body: map[string]any{"username": name, "profile": map[string]any{"website": "https://profile.example.invalid", "locale": "zh-CN", "givenName": "Original"}}}))
	if err != nil {
		t.Fatal(err)
	}
	var account UserModel
	if err := decode(response.Body, &account); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.UserDelete(ctx, account.ID); err != nil {
			t.Error(err)
		}
	})
	_, err = c.UserUpdate(ctx, &UserModel{ID: account.ID, Username: name, Name: "Changed", Profile: &Profile{GivenName: "Updated"}})
	if err != nil {
		t.Fatal(err)
	}
	response, err = expect(200)(c.do(ctx, &request{method: http.MethodGet, path: "api/users/" + account.ID}))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := decode(response.Body, &raw); err != nil {
		t.Fatal(err)
	}
	profile := raw["profile"].(map[string]any)
	if profile["website"] != "https://profile.example.invalid" || profile["locale"] != "zh-CN" || profile["givenName"] != "Updated" {
		t.Fatal("unmanaged profile fields were overwritten")
	}
}
