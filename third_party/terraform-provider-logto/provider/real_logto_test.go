//go:build integration

package provider_test

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Lenstra/terraform-provider-logto/client"
	"github.com/Lenstra/terraform-provider-logto/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// Only disposable loopback Logto instances are accepted. A remote deployment
// cannot accidentally become the target of this mutation-bearing test suite.
func TestRealLogtoLifecycle(t *testing.T) {
	credentialPath := os.Getenv("LOGTO_TEST_CREDENTIALS")
	if credentialPath == "" {
		t.Fatal("LOGTO_TEST_CREDENTIALS must point to disposable loopback credentials")
	}
	data, err := os.ReadFile(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	var credentials struct {
		Endpoint, Hostname, Resource string
		ApplicationID                string `json:"application_id"`
		ApplicationSecret            string `json:"application_secret"`
	}
	if err := json.Unmarshal(data, &credentials); err != nil {
		t.Fatal("invalid credential JSON")
	}
	endpoint, err := url.Parse(credentials.Endpoint)
	if err != nil || net.ParseIP(endpoint.Hostname()) == nil || !net.ParseIP(endpoint.Hostname()).IsLoopback() {
		t.Fatal("acceptance tests require an explicit loopback IP")
	}
	api, err := client.NewClient(&client.Config{Endpoint: credentials.Endpoint, Hostname: credentials.Hostname, Resource: credentials.Resource, ApplicationID: credentials.ApplicationID, ApplicationSecret: credentials.ApplicationSecret})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := "acceptance-" + time.Now().Format("150405")
	ttl := float64(3600)
	parent, err := api.ApiResourceCreate(ctx, &client.ApiResourceModel{Name: prefix, Indicator: "https://" + prefix + ".example.invalid", AccessTokenTtl: &ttl})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := api.ApiResourceDelete(ctx, parent.ID); err != nil {
			t.Error(err)
		}
	})
	app, err := api.ApplicationCreate(ctx, &client.ApplicationModel{Name: prefix, Type: "Traditional", OidcClientMetadata: &client.OidcClientMetadata{}, CustomClientMetadata: &client.CustomClientMetadata{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := api.ApplicationDelete(ctx, app.ID); err != nil {
			t.Error(err)
		}
	})
	p := provider.New("acceptance")()
	for _, factory := range p.Resources(ctx) {
		r := factory()
		var metadata resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "logto"}, &metadata)
		t.Run(metadata.TypeName, func(t *testing.T) {
			var schema resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schema)
			obj := schema.Schema.Type().TerraformType(ctx).(tftypes.Object)
			values := map[string]tftypes.Value{}
			for name, typ := range obj.AttributeTypes {
				values[name] = tftypes.NewValue(typ, nil)
			}
			set := func(name string, value any) {
				if typ, ok := obj.AttributeTypes[name]; ok {
					values[name] = tftypes.NewValue(typ, value)
				}
			}
			set("id", tftypes.UnknownValue)
			set("name", prefix)
			set("description", "acceptance")
			set("type", "Traditional")
			set("username", strings.ReplaceAll(prefix, "-", "_"))
			set("primary_email", prefix+"@example.invalid")
			if metadata.TypeName == "logto_role" {
				set("type", "User")
			}
			set("indicator", "https://"+prefix+"-resource.example.invalid")
			set("access_token_ttl", 3600)
			set("resource_id", parent.ID)
			set("application_id", app.ID)
			for _, field := range []string{"redirect_uris", "post_logout_redirect_uris", "cors_allowed_origins", "scope_ids", "role_ids"} {
				set(field, []tftypes.Value{})
			}
			plan := tfsdk.Plan{Schema: schema.Schema, Raw: tftypes.NewValue(obj, values)}
			protocolCreate(t, ctx, p, api, metadata.TypeName, plan, nil)
		})
	}
}
