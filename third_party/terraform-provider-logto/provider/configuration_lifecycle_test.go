package provider_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Lenstra/terraform-provider-logto/client"
	"github.com/Lenstra/terraform-provider-logto/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestConfigurationProtocolLifecycle(t *testing.T) {
	ctx := context.Background()
	p := provider.New("test")()
	valuesByKind := map[string]string{
		"sign_in_experience":  `{ "supportWebsiteUrl": "https://support.example.invalid" }`,
		"account_center":      `{ "enabled": false }`,
		"id_token_config":     `{ "enabledExtendedClaims": [] }`,
		"oidc_session_config": `{ "ttl": 1800 }`,
		"connector":           `{ "config": {"clientId":"test-id","clientSecret":"test-only"}, "syncProfile": false }`,
	}
	for _, factory := range p.Resources(ctx) {
		r := factory()
		var meta resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "logto"}, &meta)
		kind := strings.TrimPrefix(meta.TypeName, "logto_")
		configuration, ok := valuesByKind[kind]
		if !ok {
			continue
		}
		t.Run(kind, func(t *testing.T) {
			contract, _ := client.ConfigurationContract(kind)
			remote := map[string]any{"id": "default", "tenantId": "default", "unmanaged": "keep"}
			if kind == "connector" {
				remote["id"] = "connector-id"
				remote["connectorId"] = "github-universal"
			}
			deleted := false
			api, err := client.NewClient(&client.Config{Hostname: "example.invalid", ApplicationID: "test", ApplicationSecret: "test", HttpClient: &http.Client{Transport: lifecycleTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/oidc/token" {
					return apiResponse(200, map[string]any{"access_token": "test", "token_type": "Bearer", "expires_in": 3600}), nil
				}
				if !strings.HasPrefix(req.URL.Path, "/"+contract.Endpoint) {
					t.Fatalf("unexpected API path: %s", req.URL.Path)
				}
				if req.Method == http.MethodDelete {
					deleted = true
					return apiResponse(204, nil), nil
				}
				if req.Method != http.MethodGet {
					var patch map[string]any
					if err := json.NewDecoder(req.Body).Decode(&patch); err != nil {
						t.Fatal(err)
					}
					if _, ok := patch["tenantId"]; ok {
						t.Fatal("sent read-only field")
					}
					for k, v := range patch {
						remote[k] = v
					}
				}
				return apiResponse(200, remote), nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			var schema resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schema)
			obj := schema.Schema.Type().TerraformType(ctx).(tftypes.Object)
			values := map[string]tftypes.Value{}
			for name, typ := range obj.AttributeTypes {
				values[name] = tftypes.NewValue(typ, nil)
			}
			values["id"] = tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
			values["configuration"] = tftypes.NewValue(tftypes.String, configuration)
			if kind == "connector" {
				values["connector_id"] = tftypes.NewValue(tftypes.String, "github-universal")
				if !schema.Schema.Attributes["configuration"].IsSensitive() {
					t.Fatal("connector credentials are not sensitive")
				}
			}
			protocolCreate(t, ctx, p, api, meta.TypeName, tfsdk.Plan{Schema: schema.Schema, Raw: tftypes.NewValue(obj, values)}, nil)
			if remote["unmanaged"] != "keep" {
				t.Fatal("unmanaged field was overwritten")
			}
			if deleted != (kind == "connector") {
				t.Fatal("singleton deletion must only release ownership; connector deletion must call API")
			}
		})
	}
}
