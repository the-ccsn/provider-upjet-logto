package provider_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Lenstra/terraform-provider-logto/client"
	"github.com/Lenstra/terraform-provider-logto/provider"
	"github.com/hashicorp/terraform-plugin-framework/path"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type lifecycleTransport func(*http.Request) (*http.Response, error)

func (f lifecycleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func apiResponse(code int, body any) *http.Response {
	b, _ := json.Marshal(body)
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}
}

type protocolProvider struct {
	fwprovider.Provider
	api *client.Client
}

func (p *protocolProvider) Configure(_ context.Context, _ fwprovider.ConfigureRequest, resp *fwprovider.ConfigureResponse) {
	resp.ResourceData = p.api
}
func protocolCreate(t *testing.T, ctx context.Context, p fwprovider.Provider, c *client.Client, name string, plan tfsdk.Plan, entity map[string]any) {
	t.Helper()
	server := providerserver.NewProtocol6(&protocolProvider{Provider: p, api: c})()
	if _, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{}); err != nil {
		t.Fatal(err)
	}
	obj := plan.Schema.Type().TerraformType(ctx).(tftypes.Object)
	var config map[string]tftypes.Value
	if err := plan.Raw.As(&config); err != nil {
		t.Fatal(err)
	}
	for key, a := range plan.Schema.(resourceschema.Schema).Attributes {
		if a.IsComputed() && !a.IsOptional() {
			config[key] = tftypes.NewValue(obj.AttributeTypes[key], nil)
		}
	}
	cfg, err := tfprotov6.NewDynamicValue(obj, tftypes.NewValue(obj, config))
	if err != nil {
		t.Fatal(err)
	}
	prior, err := tfprotov6.NewDynamicValue(obj, tftypes.NewValue(obj, nil))
	if err != nil {
		t.Fatal(err)
	}
	proposed, err := tfprotov6.NewDynamicValue(obj, plan.Raw)
	if err != nil {
		t.Fatal(err)
	}
	var providerSchema fwprovider.SchemaResponse
	p.Schema(ctx, fwprovider.SchemaRequest{}, &providerSchema)
	providerType := providerSchema.Schema.Type().TerraformType(ctx).(tftypes.Object)
	providerValues := map[string]tftypes.Value{}
	for name, typ := range providerType.AttributeTypes {
		providerValues[name] = tftypes.NewValue(typ, "test")
	}
	providerConfig, err := tfprotov6.NewDynamicValue(providerType, tftypes.NewValue(providerType, providerValues))
	if err != nil {
		t.Fatal(err)
	}
	configured, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: &providerConfig})
	if err != nil {
		t.Fatal(err)
	}
	checkDiagnostics(t, configured.Diagnostics)
	validated, err := server.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{TypeName: name, Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	checkDiagnostics(t, validated.Diagnostics)
	planned, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: name, PriorState: &prior, ProposedNewState: &proposed, Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	checkDiagnostics(t, planned.Diagnostics)
	applied, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: name, PriorState: &prior, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate, Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	checkDiagnostics(t, applied.Diagnostics)
	if applied.NewState == nil {
		t.Fatal("protocol apply returned no state")
	}
	if name == "logto_application" && entity != nil {
		entity["oidcClientMetadata"].(map[string]any)["redirectUris"] = []any{"https://callback.example"}
	}
	read, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: name, CurrentState: applied.NewState})
	if err != nil {
		t.Fatal(err)
	}
	checkDiagnostics(t, read.Diagnostics)
	importedValue, err := read.NewState.Unmarshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	var importedAttributes map[string]tftypes.Value
	if err := importedValue.As(&importedAttributes); err != nil {
		t.Fatal(err)
	}
	var importID string
	if err := importedAttributes["id"].As(&importID); err != nil {
		t.Fatal(err)
	}
	if name == "logto_api_resource_scope" {
		var parent string
		if err := importedAttributes["resource_id"].As(&parent); err != nil {
			t.Fatal(err)
		}
		importID = parent + "/" + importID
	}
	imported, err := server.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: name, ID: importID})
	if err != nil {
		t.Fatal(err)
	}
	checkDiagnostics(t, imported.Diagnostics)
	if len(imported.ImportedResources) != 1 {
		t.Fatal("import did not return exactly one resource")
	}
	refreshed, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: name, CurrentState: imported.ImportedResources[0].State})
	if err != nil {
		t.Fatal(err)
	}
	checkDiagnostics(t, refreshed.Diagnostics)
	refreshedValue, err := refreshed.NewState.Unmarshal(obj)
	if err != nil || refreshedValue.IsNull() {
		t.Fatalf("imported resource absent: %v", err)
	}
	if name != "logto_application_secret" {
		current, err := read.NewState.Unmarshal(obj)
		if err != nil {
			t.Fatal(err)
		}
		var updated map[string]tftypes.Value
		if err := current.As(&updated); err != nil {
			t.Fatal(err)
		}
		if _, ok := obj.AttributeTypes["name"]; ok {
			updated["name"] = tftypes.NewValue(tftypes.String, "protocol-updated")
			config["name"] = updated["name"]
		} else if _, ok := obj.AttributeTypes["configuration"]; ok {
			var encoded string
			if err := updated["configuration"].As(&encoded); err != nil {
				t.Fatal(err)
			}
			var desired map[string]any
			if err := json.Unmarshal([]byte(encoded), &desired); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "logto_sign_in_experience":
				desired["supportWebsiteUrl"] = "https://updated.example.invalid"
			case "logto_account_center":
				desired["enabled"] = true
			case "logto_id_token_config":
				desired["enabledExtendedClaims"] = []string{"roles"}
			case "logto_oidc_session_config":
				desired["ttl"] = 1801
			case "logto_connector":
				desired["syncProfile"] = true
			}
			payload, _ := json.Marshal(desired)
			updated["configuration"] = tftypes.NewValue(tftypes.String, string(payload))
			config["configuration"] = updated["configuration"]
		}
		for _, field := range []string{"is_default", "is_third_party", "description", "username", "primary_email", "profile", "redirect_uris", "post_logout_redirect_uris", "cors_allowed_origins"} {
			if typ, ok := obj.AttributeTypes[field]; ok && !plan.Schema.(resourceschema.Schema).Attributes[field].IsRequired() {
				updated[field] = tftypes.NewValue(typ, tftypes.UnknownValue)
				config[field] = tftypes.NewValue(typ, nil)
			}
		}
		newConfig, err := tfprotov6.NewDynamicValue(obj, tftypes.NewValue(obj, config))
		if err != nil {
			t.Fatal(err)
		}
		proposed, err := tfprotov6.NewDynamicValue(obj, tftypes.NewValue(obj, updated))
		if err != nil {
			t.Fatal(err)
		}
		change, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: name, PriorState: read.NewState, ProposedNewState: &proposed, Config: &newConfig})
		if err != nil {
			t.Fatal(err)
		}
		checkDiagnostics(t, change.Diagnostics)
		result, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: name, PriorState: read.NewState, PlannedState: change.PlannedState, PlannedPrivate: change.PlannedPrivate, Config: &newConfig})
		if err != nil {
			t.Fatal(err)
		}
		checkDiagnostics(t, result.Diagnostics)
		read.NewState = result.NewState
	}
	deleted, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: name, PriorState: read.NewState, PlannedState: &prior, Config: &prior})
	if err != nil {
		t.Fatal(err)
	}
	checkDiagnostics(t, deleted.Diagnostics)
	state, err := deleted.NewState.Unmarshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	if !state.IsNull() {
		t.Fatal("protocol deletion retained state")
	}
}
func checkDiagnostics(t *testing.T, diags []*tfprotov6.Diagnostic) {
	t.Helper()
	for _, d := range diags {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("protocol error: %s: %s", d.Summary, d.Detail)
		}
	}
}

// Exercise the Framework state boundary, rather than only the HTTP client.
func TestResourceLifecycle(t *testing.T) {
	ctx := context.Background()
	p := provider.New("test")()
	for _, factory := range p.Resources(ctx) {
		r := factory()
		var meta resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "logto"}, &meta)
		if _, ok := client.ConfigurationContract(strings.TrimPrefix(meta.TypeName, "logto_")); ok {
			continue
		}
		t.Run(meta.TypeName, func(t *testing.T) {
			removed, secondaryFailure := false, false
			entity := map[string]any{"id": "id", "tenantId": "tenant", "name": "example", "description": "description", "type": "Traditional", "isDefault": true, "isAdmin": false, "isThirdParty": true, "indicator": "https://api.example", "accessTokenTtl": 3600, "scopes": []any{}, "resourceId": "parent", "oidcClientMetadata": map[string]any{"redirectUris": []any{}, "postLogoutRedirectUris": []any{}}, "customClientMetadata": map[string]any{"corsAllowedOrigins": []any{}}, "username": "example", "primaryEmail": "example@example.com", "profile": map[string]any{"familyName": "", "givenName": "Existing", "middleName": "", "nickname": ""}}
			if meta.TypeName == "logto_role" {
				entity["type"] = "User"
			}
			c, err := client.NewClient(&client.Config{Hostname: "example.invalid", ApplicationID: "test", ApplicationSecret: "test", HttpClient: &http.Client{Transport: lifecycleTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/oidc/token" {
					return apiResponse(200, map[string]any{"access_token": "test", "token_type": "Bearer", "expires_in": 3600}), nil
				}
				if req.Method == "DELETE" {
					removed = true
					return apiResponse(204, nil), nil
				}
				if removed {
					return apiResponse(404, nil), nil
				}
				if strings.HasSuffix(req.URL.Path, "/secrets") {
					if secondaryFailure && req.Method == "GET" {
						return apiResponse(403, nil), nil
					}
					if req.Method == "POST" {
						return apiResponse(201, map[string]any{"name": "gitops", "value": "test-only"}), nil
					}
					return apiResponse(200, []any{map[string]any{"name": "gitops", "value": "test-only"}}), nil
				}
				if strings.Contains(req.URL.Path, "/roles/") && strings.HasSuffix(req.URL.Path, "/scopes") {
					if secondaryFailure {
						return apiResponse(403, nil), nil
					}
					return apiResponse(200, []any{map[string]any{"id": "scope-a"}, map[string]any{"id": "scope-b"}}), nil
				}
				if strings.HasSuffix(req.URL.Path, "/roles") && req.URL.Path != "/api/roles" {
					if req.Method == "GET" {
						return apiResponse(200, []any{}), nil
					}
					if req.Method == "POST" {
						return apiResponse(201, nil), nil
					}
					return apiResponse(200, nil), nil
				}
				if meta.TypeName == "logto_api_resource_scope" && req.Method == "GET" {
					return apiResponse(200, []any{entity}), nil
				}
				if req.Method == "PATCH" {
					var body map[string]any
					if e := json.NewDecoder(req.Body).Decode(&body); e != nil {
						t.Fatal(e)
					}
					if (meta.TypeName == "logto_role" || meta.TypeName == "logto_api_resource") && body["isDefault"] != true {
						t.Fatal("unrelated update cleared default flag")
					}
					if meta.TypeName == "logto_application" && body["isThirdParty"] != true {
						t.Fatal("unrelated update cleared third-party flag")
					}
					if meta.TypeName == "logto_user" {
						if body["username"] != "example" || body["primaryEmail"] != "example@example.com" || body["profile"].(map[string]any)["givenName"] != "Existing" {
							t.Fatal("unrelated update cleared user identity or profile")
						}
					}
					for k, v := range body {
						entity[k] = v
					}
				}
				code := 200
				if req.Method == "POST" && (meta.TypeName == "logto_api_resource_scope" || meta.TypeName == "logto_api_resource") {
					code = 201
				}
				return apiResponse(code, entity), nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			var configured resource.ConfigureResponse
			r.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: c}, &configured)
			if configured.Diagnostics.HasError() {
				t.Fatal(configured.Diagnostics)
			}
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
			set("name", "example")
			set("description", "description")
			set("type", entity["type"])
			set("indicator", "https://api.example")
			set("access_token_ttl", 3600)
			set("resource_id", "parent")
			set("application_id", "app")
			// Known empty collections must remain known empty after API refresh.
			for _, name := range []string{"redirect_uris", "post_logout_redirect_uris", "cors_allowed_origins", "scope_ids", "role_ids", "scopes"} {
				set(name, []tftypes.Value{})
			}
			if meta.TypeName == "logto_role" {
				set("scope_ids", []tftypes.Value{tftypes.NewValue(tftypes.String, "scope-b"), tftypes.NewValue(tftypes.String, "scope-a")})
			}
			if meta.TypeName == "logto_application_secret" {
				set("name", "gitops")
			}
			plan := tfsdk.Plan{Schema: schema.Schema, Raw: tftypes.NewValue(obj, values)}
			protocolCreate(t, ctx, p, c, meta.TypeName, plan, entity)
			removed = false
			entity["name"] = "example"
			create := resource.CreateResponse{State: tfsdk.State{Schema: schema.Schema}}
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &create)
			if create.Diagnostics.HasError() {
				t.Fatal(create.Diagnostics)
			}
			var id string
			if d := create.State.GetAttribute(ctx, path.Root("id"), &id); d.HasError() || id == "" {
				t.Fatalf("created ID missing: %v", d)
			}
			read := resource.ReadResponse{State: create.State}
			r.Read(ctx, resource.ReadRequest{State: create.State}, &read)
			if read.Diagnostics.HasError() {
				t.Fatal(read.Diagnostics)
			}
			if meta.TypeName != "logto_application_secret" {
				updated := tfsdk.Plan{Schema: schema.Schema, Raw: read.State.Raw}
				if d := updated.SetAttribute(ctx, path.Root("name"), "renamed"); d.HasError() {
					t.Fatal(d)
				}
				out := resource.UpdateResponse{State: read.State}
				r.Update(ctx, resource.UpdateRequest{Plan: updated, State: read.State}, &out)
				if out.Diagnostics.HasError() {
					t.Fatal(out.Diagnostics)
				}
				var name string
				if d := out.State.GetAttribute(ctx, path.Root("name"), &name); d.HasError() || name != "renamed" {
					t.Fatalf("updated state wrong: %v", d)
				}
				read.State = out.State
			}
			imported := resource.ImportStateResponse{State: tfsdk.State{Schema: schema.Schema, Raw: tftypes.NewValue(obj, nil)}}
			importID := id
			if meta.TypeName == "logto_api_resource_scope" {
				importID = "parent/" + id
			}
			r.(resource.ResourceWithImportState).ImportState(ctx, resource.ImportStateRequest{ID: importID}, &imported)
			if imported.Diagnostics.HasError() {
				t.Fatal(imported.Diagnostics)
			}
			refresh := resource.ReadResponse{State: imported.State}
			r.Read(ctx, resource.ReadRequest{State: imported.State}, &refresh)
			if refresh.Diagnostics.HasError() {
				t.Fatal(refresh.Diagnostics)
			}
			del := resource.DeleteResponse{}
			r.Delete(ctx, resource.DeleteRequest{State: read.State}, &del)
			if del.Diagnostics.HasError() {
				t.Fatal(del.Diagnostics)
			}
			// Repeated delete and an externally removed object must converge successfully.
			r.Delete(ctx, resource.DeleteRequest{State: read.State}, &del)
			if del.Diagnostics.HasError() {
				t.Fatal(del.Diagnostics)
			}
			missing := resource.ReadResponse{State: read.State}
			r.Read(ctx, resource.ReadRequest{State: read.State}, &missing)
			if missing.Diagnostics.HasError() || !missing.State.Raw.IsNull() {
				t.Fatalf("absence not reflected: %v", missing.Diagnostics)
			}
			if meta.TypeName == "logto_role" || meta.TypeName == "logto_application" {
				removed = false
				secondaryFailure = true
				partial := resource.CreateResponse{State: tfsdk.State{Schema: schema.Schema}}
				r.Create(ctx, resource.CreateRequest{Plan: plan}, &partial)
				if !partial.Diagnostics.HasError() {
					t.Fatal("expected secondary request error")
				}
				id = ""
				if d := partial.State.GetAttribute(ctx, path.Root("id"), &id); d.HasError() || id == "" {
					t.Fatalf("partial creation orphaned resource: %v", d)
				}
			}
		})
	}
}

// Upjet performs Read before Create with an object-shaped, uninitialized state.
func TestReadBeforeCreateIsAbsentWithoutAPIRequests(t *testing.T) {
	ctx := context.Background()
	p := provider.New("test")()
	api, err := client.NewClient(&client.Config{Hostname: "example.invalid", HttpClient: &http.Client{Transport: lifecycleTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("uninitialized Read reached API")
		return nil, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	for _, factory := range p.Resources(ctx) {
		r := factory()
		var metadata resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "logto"}, &metadata)
		t.Run(metadata.TypeName, func(t *testing.T) {
			var schema resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schema)
			if configurable, ok := r.(resource.ResourceWithConfigure); ok {
				response := resource.ConfigureResponse{}
				configurable.Configure(ctx, resource.ConfigureRequest{ProviderData: api}, &response)
				if response.Diagnostics.HasError() {
					t.Fatal(response.Diagnostics)
				}
			}
			typ := schema.Schema.Type().TerraformType(ctx).(tftypes.Object)
			values := map[string]tftypes.Value{}
			for name, attrType := range typ.AttributeTypes {
				values[name] = tftypes.NewValue(attrType, nil)
			}
			state := tfsdk.State{Schema: schema.Schema, Raw: tftypes.NewValue(typ, values)}
			response := resource.ReadResponse{State: state}
			r.Read(ctx, resource.ReadRequest{State: state}, &response)
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
			if !response.State.Raw.IsNull() {
				t.Fatal("new resource must be observed as absent")
			}
		})
	}
}
