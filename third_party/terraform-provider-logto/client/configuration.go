package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
)

// ConfigurationDefinition binds a resource to one specific Logto API contract.
// Callers cannot select an arbitrary URL or write read-only response fields.
type ConfigurationDefinition struct {
	Endpoint  string
	Method    string
	Sensitive bool
	Fields    []string
}

func ConfigurationContract(kind string) (ConfigurationDefinition, bool) {
	definitions := map[string]ConfigurationDefinition{
		"sign_in_experience":  {Endpoint: "api/sign-in-exp", Method: http.MethodPatch, Fields: []string{"color", "branding", "hideLogtoBranding", "languageInfo", "termsOfUseUrl", "privacyPolicyUrl", "agreeToTermsPolicy", "signIn", "signUp", "socialSignIn", "socialSignInConnectorTargets", "signInMode", "customCss", "customContent", "customUiAssets", "customUiCsp", "passwordPolicy", "mfa", "adaptiveMfa", "singleSignOnEnabled", "supportEmail", "supportWebsiteUrl", "unknownSessionRedirectUrl", "captchaPolicy", "sentinelPolicy", "emailBlocklistPolicy", "forgotPasswordMethods", "passkeySignIn", "signUpProfileFields"}},
		"account_center":      {Endpoint: "api/account-center", Method: http.MethodPatch, Fields: []string{"enabled", "fields", "webauthnRelatedOrigins", "deleteAccountUrl", "customCss", "profileFields"}},
		"id_token_config":     {Endpoint: "api/configs/id-token", Method: http.MethodPut, Fields: []string{"enabledExtendedClaims"}},
		"oidc_session_config": {Endpoint: "api/configs/oidc/session", Method: http.MethodPatch, Fields: []string{"ttl"}},
		"connector":           {Endpoint: "api/connectors", Method: http.MethodPatch, Sensitive: true, Fields: []string{"config", "metadata", "syncProfile"}},
	}
	d, ok := definitions[kind]
	return d, ok
}

func ValidateConfiguration(kind, configuration string) (map[string]any, error) {
	d, ok := ConfigurationContract(kind)
	if !ok {
		return nil, errors.New("unsupported Logto configuration resource")
	}
	var body map[string]any
	if json.Unmarshal([]byte(configuration), &body) != nil || body == nil {
		return nil, errors.New("configuration must be a JSON object")
	}
	for key := range body {
		if !slices.Contains(d.Fields, key) {
			return nil, errors.New("configuration contains unsupported or read-only fields")
		}
	}
	return body, nil
}

func configurationPath(kind, id string) (string, error) {
	d, ok := ConfigurationContract(kind)
	if !ok {
		return "", errors.New("unsupported Logto configuration resource")
	}
	if kind == "connector" {
		if !validID(id) {
			return "", errEmptyID
		}
		return d.Endpoint + "/" + url.PathEscape(id), nil
	}
	if id != "default" {
		return "", errors.New("singleton configuration ID must be default")
	}
	return d.Endpoint, nil
}

func (c *Client) ConfigurationGet(ctx context.Context, kind, id string) (map[string]any, error) {
	endpoint, err := configurationPath(kind, id)
	if err != nil {
		return nil, err
	}
	res, err := expect(http.StatusOK, http.StatusNotFound)(c.do(ctx, &request{method: http.MethodGet, path: endpoint}))
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusNotFound {
		res.Body.Close()
		return nil, nil
	}
	var body map[string]any
	if err := decode(res.Body, &body); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, errors.New("configuration response must be an object")
	}
	return body, nil
}

func (c *Client) ConfigurationWrite(ctx context.Context, kind, id, connectorID, configuration string, create bool) (map[string]any, error) {
	body, err := ValidateConfiguration(kind, configuration)
	if err != nil {
		return nil, err
	}
	d, _ := ConfigurationContract(kind)
	endpoint, method := d.Endpoint, d.Method
	if kind == "connector" && create {
		if !validID(connectorID) {
			return nil, errEmptyID
		}
		if _, ok := body["config"].(map[string]any); !ok {
			return nil, errors.New("connector configuration requires a config object")
		}
		body["connectorId"] = connectorID
		method = http.MethodPost
	} else {
		endpoint, err = configurationPath(kind, id)
		if err != nil {
			return nil, err
		}
	}
	res, err := expect(http.StatusOK, http.StatusCreated)(c.do(ctx, &request{method: method, path: endpoint, body: body}))
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := decode(res.Body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) ConfigurationDelete(ctx context.Context, kind, id string) error {
	endpoint, err := configurationPath(kind, id)
	if err != nil {
		return err
	}
	if kind != "connector" {
		// Tenant settings exist independently of IaC ownership. Removal only releases ownership.
		return nil
	}
	return c.discard(ctx, &request{method: http.MethodDelete, path: endpoint}, http.StatusNoContent, http.StatusNotFound)
}
