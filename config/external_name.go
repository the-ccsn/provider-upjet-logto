package config

import "github.com/crossplane/upjet/v2/pkg/config"

var ExternalNameConfigs = map[string]config.ExternalName{
	"logto_application_secret": config.IdentifierFromProvider,
	"logto_application":        config.IdentifierFromProvider,
	"logto_user":               config.IdentifierFromProvider,
	"logto_role":               config.IdentifierFromProvider,
	"logto_api_resource":       config.IdentifierFromProvider,
	"logto_api_resource_scope": config.IdentifierFromProvider,
}

func ExternalNameConfigurations() config.ResourceOption {
	return func(r *config.Resource) {
		if e, ok := ExternalNameConfigs[r.Name]; ok {
			r.ExternalName = e
		}
	}
}
func ExternalNameConfigured() []string {
	return []string{"logto_application_secret$", "logto_application$", "logto_user$", "logto_role$", "logto_api_resource$", "logto_api_resource_scope$"}
}
