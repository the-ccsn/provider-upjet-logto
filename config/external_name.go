package config

import "github.com/crossplane/upjet/v2/pkg/config"

var ExternalNameConfigs = map[string]config.ExternalName{
	"logto_application_secret":  config.IdentifierFromProvider,
	"logto_application":         config.IdentifierFromProvider,
	"logto_user":                config.IdentifierFromProvider,
	"logto_role":                config.IdentifierFromProvider,
	"logto_api_resource":        config.IdentifierFromProvider,
	"logto_api_resource_scope":  config.IdentifierFromProvider,
	"logto_sign_in_experience":  config.IdentifierFromProvider,
	"logto_account_center":      config.IdentifierFromProvider,
	"logto_id_token_config":     config.IdentifierFromProvider,
	"logto_oidc_session_config": config.IdentifierFromProvider,
	"logto_connector":           config.IdentifierFromProvider,
}

func ExternalNameConfigurations() config.ResourceOption {
	return func(r *config.Resource) {
		if e, ok := ExternalNameConfigs[r.Name]; ok {
			r.ExternalName = e
		}
	}
}
func ExternalNameConfigured() []string {
	return []string{"logto_application_secret$", "logto_application$", "logto_user$", "logto_role$", "logto_api_resource$", "logto_api_resource_scope$", "logto_sign_in_experience$", "logto_account_center$", "logto_id_token_config$", "logto_oidc_session_config$", "logto_connector$"}
}
