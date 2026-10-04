package provider_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Lenstra/terraform-provider-logto/client"
	"github.com/Lenstra/terraform-provider-logto/provider"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestProviderConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name        string
		missing     string
		unknown     bool
		hostname    string
		environment bool
		wantError   bool
	}{
		{name: "valid", hostname: "example.invalid"},
		{name: "environment", environment: true},
		{name: "missing-hostname", missing: "hostname", wantError: true},
		{name: "missing-app", missing: "application_id", hostname: "example.invalid", wantError: true},
		{name: "missing-secret", missing: "application_secret", hostname: "example.invalid", wantError: true},
		{name: "unknown-hostname", missing: "hostname", unknown: true, wantError: true},
		{name: "unknown-resource", missing: "resource", unknown: true, hostname: "example.invalid", wantError: true},
		{name: "unknown-app", missing: "application_id", unknown: true, hostname: "example.invalid", wantError: true},
		{name: "unknown-secret", missing: "application_secret", unknown: true, hostname: "example.invalid", wantError: true},
		{name: "invalid-origin", hostname: "https://example.invalid/path", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"LOGTO_HOSTNAME", "LOGTO_RESOURCE", "LOGTO_APPLICATION_ID", "LOGTO_APPLICATION_SECRET"} {
				t.Setenv(key, "")
			}
			ctx := context.Background()
			p := provider.New("test")()
			var sch fwprovider.SchemaResponse
			p.Schema(ctx, fwprovider.SchemaRequest{}, &sch)
			typ := sch.Schema.Type().TerraformType(ctx).(tftypes.Object)
			input := map[string]string{"hostname": tc.hostname, "resource": "https://default.logto.app/api", "application_id": "test", "application_secret": "private-credential"}
			vals := map[string]tftypes.Value{}
			for key, value := range input {
				var v any = value
				if key == tc.missing {
					v = nil
					if tc.unknown {
						v = tftypes.UnknownValue
					}
				}
				if tc.environment {
					v = nil
				}
				vals[key] = tftypes.NewValue(tftypes.String, v)
			}
			if tc.environment {
				t.Setenv("LOGTO_HOSTNAME", "example.invalid")
				t.Setenv("LOGTO_RESOURCE", input["resource"])
				t.Setenv("LOGTO_APPLICATION_ID", "test")
				t.Setenv("LOGTO_APPLICATION_SECRET", "private-credential")
			}
			var out fwprovider.ConfigureResponse
			p.Configure(ctx, fwprovider.ConfigureRequest{Config: tfsdk.Config{Schema: sch.Schema, Raw: tftypes.NewValue(typ, vals)}}, &out)
			if out.Diagnostics.HasError() != tc.wantError {
				t.Fatalf("unexpected diagnostics: %v", out.Diagnostics)
			}
			for _, d := range out.Diagnostics {
				if strings.Contains(d.Detail(), "private-credential") {
					t.Fatal("credential exposed")
				}
			}
			if !tc.wantError {
				if _, ok := out.ResourceData.(*client.Client); !ok {
					t.Fatal("configured client missing")
				}
			}
		})
	}
}
