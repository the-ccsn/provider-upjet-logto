//go:build integration

package client

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestRealApplicationUserConsentScopes(t *testing.T) {
	c := disposableClient(t)
	ctx := context.Background()
	prefix := fmt.Sprintf("consent_%d", time.Now().UnixNano())
	app, err := c.ApplicationCreate(ctx, &ApplicationModel{Name: prefix, Type: "Traditional", IsThirdParty: true, OidcClientMetadata: &OidcClientMetadata{}, CustomClientMetadata: &CustomClientMetadata{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.ApplicationDelete(ctx, app.ID); err != nil {
			t.Error(err)
		}
	})
	resource, err := c.ApiResourceCreate(ctx, &ApiResourceModel{Name: prefix, Indicator: "https://" + prefix + ".example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.ApiResourceDelete(ctx, resource.ID); err != nil {
			t.Error(err)
		}
	})
	scope, err := c.ApiResourceScopeCreate(ctx, resource.ID, &ScopeModel{Name: "read"})
	if err != nil {
		t.Fatal(err)
	}
	// Seed an API grant through Logto's real endpoint to verify that user-scope
	// reconciliation never replaces permissions outside this resource's ownership.
	if err := c.discard(ctx, &request{method: http.MethodPost, path: path.Join("api/applications", app.ID, "user-consent-scopes"), body: map[string]any{"resourceScopes": []string{scope.ID}}}, 201); err != nil {
		t.Fatal(err)
	}
	apiGrants := func() any {
		t.Helper()
		res, err := expect(200)(c.do(ctx, &request{method: http.MethodGet, path: path.Join("api/applications", app.ID, "user-consent-scopes")}))
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := decode(res.Body, &body); err != nil {
			t.Fatal(err)
		}
		return body["resourceScopes"]
	}
	before := apiGrants()
	check := func(want []string) {
		t.Helper()
		got, err := c.ApplicationUserConsentScopesGet(ctx, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Fatalf("user scopes=%v, want %v", got, want)
		}
		if !reflect.DeepEqual(apiGrants(), before) {
			t.Fatal("API consent grants were changed")
		}
	}
	check([]string{})
	if err := c.ApplicationUserConsentScopesUpdate(ctx, app.ID, []string{"profile", "email"}); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplicationUserConsentScopesUpdate(ctx, app.ID, []string{"urn:logto:scope:trusted_devices"}); err == nil {
		t.Fatal("unsupported scope accepted")
	}
	check([]string{"email", "profile"})
	for _, desired := range [][]string{{"profile", "email"}, {"profile", "email"}, {"email"}, {}} {
		if err := c.ApplicationUserConsentScopesUpdate(ctx, app.ID, desired); err != nil {
			t.Fatal(err)
		}
		expected := slices.Clone(desired)
		slices.Sort(expected)
		check(expected)
	}
	// Exercise a drift grant and a URI-style scope against the actual router.
	if err := c.ApplicationUserConsentScopesUpdate(ctx, app.ID, []string{"phone", "urn:logto:scope:organizations"}); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplicationUserConsentScopesUpdate(ctx, app.ID, []string{"email"}); err != nil {
		t.Fatal(err)
	}
	check([]string{"email"})
	if err := c.ApplicationUserConsentScopesUpdate(ctx, app.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	check([]string{})
	parent, err := c.ApplicationGet(ctx, app.ID)
	if err != nil || parent == nil {
		t.Fatalf("consent update removed application: %v", err)
	}
}
