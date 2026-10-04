package provider

import (
	"context"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"testing"
)

func TestSensitiveSchema(t *testing.T) {
	ctx := context.Background()
	p := New("test")()
	var ps fwprovider.SchemaResponse
	p.Schema(ctx, fwprovider.SchemaRequest{}, &ps)
	if !ps.Schema.Attributes["application_secret"].IsSensitive() {
		t.Fatal("provider credential must be sensitive")
	}
	for _, newResource := range p.Resources(ctx) {
		r := newResource()
		var m resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "logto"}, &m)
		if m.TypeName != "logto_application" {
			continue
		}
		var s resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &s)
		a := s.Schema.Attributes["client_secrets"]
		if a == nil || !a.IsSensitive() || !a.IsComputed() {
			t.Fatal("client secrets must be sensitive and computed")
		}
		return
	}
	t.Fatal("application resource missing")
}
