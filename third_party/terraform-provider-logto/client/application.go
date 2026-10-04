package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

func (c *Client) ApplicationGet(ctx context.Context, id string) (*ApplicationModel, error) {
	if !validID(id) {
		return nil, errEmptyID
	}

	req := &request{
		method: http.MethodGet,
		path:   "api/applications/" + id,
	}
	res, err := expect(200, 404)(c.do(ctx, req))
	if err != nil {
		return nil, err
	}

	if res.StatusCode == 404 {
		res.Body.Close()
		return nil, nil
	}

	var application ApplicationModel
	if err := decode(res.Body, &application); err != nil {
		return nil, err
	}
	return &application, nil
}

func (c *Client) ApplicationCreate(ctx context.Context, app *ApplicationModel) (*ApplicationModel, error) {
	if app == nil {
		return nil, fmt.Errorf("application configuration is required")
	}
	copyApp := *app
	if app.OidcClientMetadata != nil {
		metadata := *app.OidcClientMetadata
		metadata.RedirectUris = nonNil(metadata.RedirectUris)
		metadata.PostLogoutRedirectUris = nonNil(metadata.PostLogoutRedirectUris)
		copyApp.OidcClientMetadata = &metadata
	}
	req := &request{
		method: http.MethodPost,
		path:   "api/applications",
		body:   &copyApp,
	}

	res, err := expect(200)(c.do(ctx, req))
	if err != nil {
		return nil, err
	}

	var application ApplicationModel
	if err := decode(res.Body, &application); err != nil {
		return nil, err
	}
	return &application, nil
}

func (c *Client) ApplicationDelete(ctx context.Context, id string) error {
	if !validID(id) {
		return errEmptyID
	}

	req := &request{
		method: http.MethodDelete,
		path:   "api/applications/" + id,
	}
	return c.discard(ctx, req, 204, 404)
}

func (c *Client) ApplicationUpdate(ctx context.Context, app *ApplicationModel) (*ApplicationModel, error) {
	if app == nil || !validID(app.ID) {
		return nil, errEmptyID
	}

	// These JSON objects are replaced by Logto, not merged. Preserve fields
	// that this provider does not own, including future fields unknown to Go.
	currentRes, err := expect(200)(c.do(ctx, &request{method: http.MethodGet, path: "api/applications/" + url.PathEscape(app.ID)}))
	if err != nil {
		return nil, err
	}
	current := map[string]any{}
	if err := decode(currentRes.Body, &current); err != nil {
		return nil, err
	}
	patch := map[string]any{"name": app.Name, "description": app.Description, "isThirdParty": app.IsThirdParty}
	if app.OidcClientMetadata != nil {
		metadata, _ := current["oidcClientMetadata"].(map[string]any)
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata["redirectUris"] = nonNil(app.OidcClientMetadata.RedirectUris)
		metadata["postLogoutRedirectUris"] = nonNil(app.OidcClientMetadata.PostLogoutRedirectUris)
		patch["oidcClientMetadata"] = metadata
	}
	if app.CustomClientMetadata != nil {
		metadata, _ := current["customClientMetadata"].(map[string]any)
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata["corsAllowedOrigins"] = nonNil(app.CustomClientMetadata.CorsAllowedOrigins)
		patch["customClientMetadata"] = metadata
	}
	// isAdmin is computed/read-only in our schema; never alter management grants.
	req := &request{method: http.MethodPatch, path: "api/applications/" + url.PathEscape(app.ID), body: patch}

	res, err := expect(200)(c.do(ctx, req))
	if err != nil {
		return nil, err
	}

	var application ApplicationModel
	if err := decode(res.Body, &application); err != nil {
		return nil, err
	}
	return &application, nil
}

func (c *Client) GetApplicationSecrets(ctx context.Context, id string) ([]Secret, error) {
	if !validID(id) {
		return nil, errEmptyID
	}

	req := &request{
		method: http.MethodGet,
		path:   fmt.Sprintf("api/applications/%s/secrets", id),
	}

	res, err := expect(200)(c.do(ctx, req))
	if err != nil {
		return nil, err
	}

	var secrets []Secret
	if err := decode(res.Body, &secrets); err != nil {
		return nil, err
	}
	return secrets, nil
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
