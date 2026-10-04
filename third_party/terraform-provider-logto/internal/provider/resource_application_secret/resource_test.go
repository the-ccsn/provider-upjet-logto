package resource_application_secret

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Lenstra/terraform-provider-logto/client"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSecretLifecycle(t *testing.T) {
	ctx := context.Background()
	exists := false
	tokenCalls := 0
	c, err := client.NewClient(&client.Config{Hostname: "example.invalid", ApplicationID: "test", ApplicationSecret: "test", HttpClient: &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		status := 200
		body := ""
		switch req.Method + " " + req.URL.Path {
		case "POST /oidc/token":
			tokenCalls++
			body = `{"access_token":"mock","token_type":"Bearer","expires_in":3600}`
		case "POST /api/applications/app/secrets":
			exists = true
			status = 201
			body = `{"name":"gitops","value":"test-secret"}`
		case "GET /api/applications/app":
			body = `{"id":"app","name":"example","type":"Traditional"}`
		case "GET /api/applications/app/secrets":
			if exists {
				body = `[{"name":"gitops","value":"test-secret"}]`
			} else {
				body = `[]`
			}
		case "DELETE /api/applications/app/secrets/gitops":
			exists = false
			status = 204
		default:
			t.Fatalf("unexpected API request %s %s", req.Method, req.URL.Path)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	r := &secretResource{client: c}
	var sch resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sch)
	plan := tfsdk.Plan{Schema: sch.Schema}
	if d := plan.Set(ctx, &model{ID: types.StringUnknown(), ApplicationID: types.StringValue("app"), Name: types.StringValue("gitops"), Value: types.StringUnknown()}); d.HasError() {
		t.Fatal(d)
	}
	create := resource.CreateResponse{State: tfsdk.State{Schema: sch.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &create)
	if create.Diagnostics.HasError() {
		t.Fatal(create.Diagnostics)
	}
	var m model
	if d := create.State.Get(ctx, &m); d.HasError() {
		t.Fatal(d)
	}
	if m.ID.ValueString() != "app/gitops" || m.Value.ValueString() != "test-secret" {
		t.Fatal("created state missing identifier or credential")
	}
	// Import and refresh reconstruct the generated credential from the API.
	imported := resource.ImportStateResponse{State: tfsdk.State{Schema: sch.Schema}}
	if d := imported.State.Set(ctx, &model{ID: types.StringNull(), ApplicationID: types.StringNull(), Name: types.StringNull(), Value: types.StringNull()}); d.HasError() {
		t.Fatal(d)
	}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "app/gitops"}, &imported)
	if imported.Diagnostics.HasError() {
		t.Fatal(imported.Diagnostics)
	}
	read := resource.ReadResponse{State: imported.State}
	r.Read(ctx, resource.ReadRequest{State: imported.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	if d := read.State.Get(ctx, &m); d.HasError() {
		t.Fatal(d)
	}
	if m.Value.ValueString() != "test-secret" {
		t.Fatal("import did not recover credential")
	}
	del := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: read.State}, &del)
	if del.Diagnostics.HasError() {
		t.Fatal(del.Diagnostics)
	}
	missing := resource.ReadResponse{State: read.State}
	r.Read(ctx, resource.ReadRequest{State: read.State}, &missing)
	if missing.Diagnostics.HasError() {
		t.Fatal(missing.Diagnostics)
	}
	if !missing.State.Raw.IsNull() {
		t.Fatal("removed secret must be removed from state")
	}
	if tokenCalls != 1 {
		t.Fatalf("token cache not shared across operations: %d", tokenCalls)
	}
}
