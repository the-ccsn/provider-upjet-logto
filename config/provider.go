package config

import (
	_ "embed"
	"fmt"
	tfprovider "github.com/Lenstra/terraform-provider-logto/provider"
	ujconfig "github.com/crossplane/upjet/v2/pkg/config"
)

//go:embed schema.json
var providerSchema string

//go:embed provider-metadata.yaml
var providerMetadata string

func providerConfig(root string) *ujconfig.Provider {
	p := ujconfig.NewProvider([]byte(providerSchema), "logto", "github.com/the-ccsn/provider-upjet-logto", []byte(providerMetadata),
		ujconfig.WithRootGroup(root),
		ujconfig.WithIncludeList([]string{}),
		ujconfig.WithTerraformPluginSDKIncludeList([]string{}),
		ujconfig.WithTerraformPluginFrameworkIncludeList(ExternalNameConfigured()),
		ujconfig.WithTerraformPluginFrameworkProvider(tfprovider.New("dev")()),
		ujconfig.WithFeaturesPackage("internal/features"),
		ujconfig.WithDefaultResourceOptions(ExternalNameConfigurations()),
		ujconfig.WithExampleManifestConfiguration(ujconfig.ExampleManifestConfiguration{ManagedResourceNamespace: "crossplane-system"}),
	)
	p.AddResourceConfigurator("logto_application", func(r *ujconfig.Resource) {
		r.ShortGroup = "application"
		r.Kind = "Application"
		r.Sensitive.AdditionalConnectionDetailsFn = applicationDetails
	})
	p.AddResourceConfigurator("logto_api_resource", func(r *ujconfig.Resource) { r.ShortGroup = "api"; r.Kind = "Resource" })
	p.AddResourceConfigurator("logto_api_resource_scope", func(r *ujconfig.Resource) {
		r.ShortGroup = "api"
		r.Kind = "Scope"
		r.References["resource_id"] = ujconfig.Reference{TerraformName: "logto_api_resource"}
	})
	p.AddResourceConfigurator("logto_role", func(r *ujconfig.Resource) {
		r.ShortGroup = "role"
		r.Kind = "Role"
		r.References["scope_ids"] = ujconfig.Reference{TerraformName: "logto_api_resource_scope"}
	})
	p.AddResourceConfigurator("logto_user", func(r *ujconfig.Resource) {
		r.ShortGroup = "user"
		r.Kind = "User"
		r.References["role_ids"] = ujconfig.Reference{TerraformName: "logto_role"}
	})
	p.AddResourceConfigurator("logto_application_secret", func(r *ujconfig.Resource) {
		r.ShortGroup = "application"
		r.Kind = "Secret"
		r.Sensitive.AdditionalConnectionDetailsFn = secretDetails
		r.References["application_id"] = ujconfig.Reference{TerraformName: "logto_application"}
	})
	p.ConfigureResources()
	return p
}
func GetProvider() *ujconfig.Provider           { return providerConfig("logto.crossplane.io") }
func GetProviderNamespaced() *ujconfig.Provider { return providerConfig("logto.m.crossplane.io") }

func applicationDetails(attr map[string]any) (map[string][]byte, error) {
	id, ok := attr["id"].(string)
	if !ok || id == "" {
		return nil, fmt.Errorf("application ID missing from state")
	}
	return map[string][]byte{"clientId": []byte(id)}, nil
}
func secretDetails(attr map[string]any) (map[string][]byte, error) {
	app, ok := attr["application_id"].(string)
	if !ok || app == "" {
		return nil, fmt.Errorf("application ID missing from secret state")
	}
	value, ok := attr["value"].(string)
	if !ok || value == "" {
		return nil, fmt.Errorf("credential missing from secret state")
	}
	return map[string][]byte{"clientId": []byte(app), "clientSecret": []byte(value)}, nil
}
