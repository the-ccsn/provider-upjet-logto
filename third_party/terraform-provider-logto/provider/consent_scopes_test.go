package provider_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Lenstra/terraform-provider-logto/client"
	"github.com/Lenstra/terraform-provider-logto/provider"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestUserConsentScopesRejectsUnsupportedConfig(t *testing.T) {
	ctx := context.Background()
	const name = "logto_application_user_consent_scopes"
	server := providerserver.NewProtocol6(provider.New("test")())()
	schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	checkDiagnostics(t, schemas.Diagnostics)
	obj := schemas.ResourceSchemas[name].ValueType().(tftypes.Object)
	config, err := tfprotov6.NewDynamicValue(obj, tftypes.NewValue(obj, map[string]tftypes.Value{
		"id":             tftypes.NewValue(tftypes.String, nil),
		"application_id": tftypes.NewValue(tftypes.String, "app"),
		"user_scopes":    tftypes.NewValue(obj.AttributeTypes["user_scopes"], []tftypes.Value{tftypes.NewValue(tftypes.String, "urn:logto:scope:trusted_devices")}),
	}))
	if err != nil {
		t.Fatal(err)
	}
	validated, err := server.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{TypeName: name, Config: &config})
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range validated.Diagnostics {
		if diagnostic.Severity == tfprotov6.DiagnosticSeverityError && diagnostic.Attribute.Equal(tftypes.NewAttributePath().WithAttributeName("user_scopes")) {
			return
		}
	}
	t.Fatalf("unsupported scope did not produce a user_scopes validation error: %v", validated.Diagnostics)
}

func TestUserConsentScopesLifecycle(t *testing.T) {
	ctx := context.Background()
	p := provider.New("test")()
	var r resource.Resource
	const resourceName = "logto_application_user_consent_scopes"
	for _, factory := range p.Resources(ctx) {
		candidate := factory()
		var m resource.MetadataResponse
		candidate.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "logto"}, &m)
		if m.TypeName == resourceName {
			r = candidate
			break
		}
	}
	if r == nil {
		t.Fatal("consent scopes resource missing")
	}
	scopes := []string{}
	missing, fail := false, false
	requests := 0
	api, err := client.NewClient(&client.Config{Hostname: "example.invalid", ApplicationID: "test", ApplicationSecret: "test", HttpClient: &http.Client{Transport: lifecycleTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.Path == "/oidc/token" {
			return apiResponse(200, map[string]any{"access_token": "test", "token_type": "Bearer", "expires_in": 3600}), nil
		}
		if missing {
			return apiResponse(404, nil), nil
		}
		base := "/api/applications/app/user-consent-scopes"
		switch {
		case req.Method == "GET" && req.URL.Path == base:
			return apiResponse(200, map[string]any{"userScopes": scopes}), nil
		case req.Method == "POST" && req.URL.Path == base:
			if fail {
				return apiResponse(403, nil), nil
			}
			var body struct {
				UserScopes []string `json:"userScopes"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			scopes = append(scopes, body.UserScopes...)
			return apiResponse(201, nil), nil
		case req.Method == "DELETE" && strings.HasPrefix(req.URL.Path, base+"/user-scopes/"):
			scope := strings.TrimPrefix(req.URL.Path, base+"/user-scopes/")
			scopes = slices.DeleteFunc(scopes, func(s string) bool { return s == scope })
			return apiResponse(204, nil), nil
		default:
			t.Fatalf("unexpected %s %s", req.Method, req.URL)
			return nil, nil
		}
	})}})
	if err != nil {
		t.Fatal(err)
	}
	var configured resource.ConfigureResponse
	r.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: api}, &configured)
	if configured.Diagnostics.HasError() {
		t.Fatal(configured.Diagnostics)
	}
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	obj := schema.Schema.Type().TerraformType(ctx).(tftypes.Object)
	values := map[string]tftypes.Value{
		"id":             tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"application_id": tftypes.NewValue(tftypes.String, "app"),
		"user_scopes":    tftypes.NewValue(obj.AttributeTypes["user_scopes"], []tftypes.Value{tftypes.NewValue(tftypes.String, "profile"), tftypes.NewValue(tftypes.String, "email")}),
	}
	plan := tfsdk.Plan{Schema: schema.Schema, Raw: tftypes.NewValue(obj, values)}
	// Upjet can apply without a separate ValidateResourceConfig RPC. Invalid
	// input must not adopt an application or leave an unreadable identity.
	type invalidInput struct {
		name, attribute string
		value           any
	}
	inputs := []invalidInput{
		{"id/empty", "application_id", ""}, {"id/dot", "application_id", "."}, {"id/parent", "application_id", ".."},
		{"id/slash", "application_id", "bad/app"}, {"id/backslash", "application_id", `bad\app`},
		{"id/query", "application_id", "bad?app"}, {"id/fragment", "application_id", "bad#app"}, {"id/percent", "application_id", "bad%app"},
		{"scope/implicit", "user_scopes", []string{"openid"}},
		{"scope/unsupported", "user_scopes", []string{"urn:logto:scope:trusted_devices"}},
		{"scope/null", "user_scopes", []string(nil)},
	}
	for code := rune(0); code < 0x20; code++ {
		inputs = append(inputs, invalidInput{fmt.Sprintf("id/control_%02x", code), "application_id", "bad" + string(code) + "app"})
	}
	inputs = append(inputs, invalidInput{"id/control_7f", "application_id", "bad\x7fapp"})
	for _, row := range inputs {
		t.Run("invalid_creation_input/"+row.name, func(t *testing.T) {
			scopes = []string{"phone"}
			beforeRequests := requests
			invalidPlan := plan
			if d := invalidPlan.SetAttribute(ctx, path.Root(row.attribute), row.value); d.HasError() {
				t.Fatal(d)
			}
			invalid := resource.CreateResponse{State: tfsdk.State{Schema: schema.Schema}}
			r.Create(ctx, resource.CreateRequest{Plan: invalidPlan}, &invalid)
			if !invalid.Diagnostics.HasError() {
				t.Fatal("invalid input was accepted during creation")
			}
			if !invalid.State.Raw.IsNull() {
				t.Fatal("invalid input was saved as partial creation state")
			}
			if requests != beforeRequests || !slices.Equal(scopes, []string{"phone"}) {
				t.Fatal("invalid creation input read or changed existing grants", scopes)
			}
		})
	}
	scopes = []string{}
	// Verify schema validation, planning, apply, read, import and delete through protocol v6.
	protocolCreate(t, ctx, p, api, resourceName, plan, nil)
	if len(scopes) != 0 {
		t.Fatal("protocol deletion did not revoke scopes")
	}
	create := resource.CreateResponse{State: tfsdk.State{Schema: schema.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &create)
	if create.Diagnostics.HasError() {
		t.Fatal(create.Diagnostics)
	}
	// Drift includes both an out-of-band addition and a revoked permission.
	scopes = []string{"email", "phone"}
	read := resource.ReadResponse{State: create.State}
	r.Read(ctx, resource.ReadRequest{State: create.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	var observed []string
	if d := read.State.GetAttribute(ctx, path.Root("user_scopes"), &observed); d.HasError() {
		t.Fatal(d)
	}
	slices.Sort(observed)
	if !slices.Equal(observed, []string{"email", "phone"}) {
		t.Fatal("drift hidden", observed)
	}
	updatePlan := plan
	if d := updatePlan.SetAttribute(ctx, path.Root("id"), "app"); d.HasError() {
		t.Fatal(d)
	}
	// An apply without config validation must still distinguish omitted scopes
	// from an explicitly configured empty set before touching existing grants.
	t.Run("null_update_scopes", func(t *testing.T) {
		invalidPlan := updatePlan
		if d := invalidPlan.SetAttribute(ctx, path.Root("user_scopes"), []string(nil)); d.HasError() {
			t.Fatal(d)
		}
		beforeRequests := requests
		invalid := resource.UpdateResponse{State: read.State}
		r.Update(ctx, resource.UpdateRequest{State: read.State, Plan: invalidPlan}, &invalid)
		if !invalid.Diagnostics.HasError() {
			t.Fatal("null scopes were accepted during update")
		}
		if requests != beforeRequests || !slices.Equal(scopes, []string{"email", "phone"}) || !invalid.State.Raw.Equal(read.State.Raw) {
			t.Fatal("null update scopes changed grants or state", scopes)
		}
	})
	update := resource.UpdateResponse{State: read.State}
	r.Update(ctx, resource.UpdateRequest{State: read.State, Plan: updatePlan}, &update)
	if update.Diagnostics.HasError() {
		t.Fatal(update.Diagnostics)
	}
	slices.Sort(scopes)
	if !slices.Equal(scopes, []string{"email", "profile"}) {
		t.Fatal("drift not reconciled", scopes)
	}
	// Empty sets stay present when the parent exists, permitting later updates.
	if d := updatePlan.SetAttribute(ctx, path.Root("user_scopes"), []string{}); d.HasError() {
		t.Fatal(d)
	}
	r.Update(ctx, resource.UpdateRequest{State: update.State, Plan: updatePlan}, &update)
	if update.Diagnostics.HasError() {
		t.Fatal(update.Diagnostics)
	}
	empty := resource.ReadResponse{State: update.State}
	r.Read(ctx, resource.ReadRequest{State: update.State}, &empty)
	if empty.Diagnostics.HasError() || empty.State.Raw.IsNull() {
		t.Fatal("empty scopes treated as absent", empty.Diagnostics)
	}
	fail = true
	partial := resource.CreateResponse{State: tfsdk.State{Schema: schema.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &partial)
	if !partial.Diagnostics.HasError() {
		t.Fatal("expected partial assignment error")
	}
	var id string
	if d := partial.State.GetAttribute(ctx, path.Root("id"), &id); d.HasError() || id != "app" {
		t.Fatal("partial assignment lost identity", d)
	}
	missing = true
	absent := resource.ReadResponse{State: empty.State}
	r.Read(ctx, resource.ReadRequest{State: empty.State}, &absent)
	if absent.Diagnostics.HasError() || !absent.State.Raw.IsNull() {
		t.Fatal("missing parent not reflected", absent.Diagnostics)
	}
	deleted := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: empty.State}, &deleted)
	if deleted.Diagnostics.HasError() {
		t.Fatal(deleted.Diagnostics)
	}
}
