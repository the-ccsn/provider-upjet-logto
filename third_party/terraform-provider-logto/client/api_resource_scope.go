// Permissions contains in an api_resource and used with their Id's by roles directly

package client

import (
	"context"
	"net/http"
	"path"
)

func (c *Client) ApiResourceScopesList(ctx context.Context, resourceId string, query_params map[string]string) ([]ScopeModel, error) {
	if !validID(resourceId) {
		return nil, errEmptyID
	}
	req := &request{
		method:          http.MethodGet,
		path:            path.Join("api/resources", resourceId, "scopes"),
		queryParameters: query_params,
	}

	res, err := expect(200)(c.do(ctx, req))
	if err != nil {
		return nil, err
	}

	var returnScope []ScopeModel
	if err := decode(res.Body, &returnScope); err != nil {
		return nil, err
	}

	return returnScope, nil
}

func (c *Client) ApiResourceScopeCreate(ctx context.Context, resourceId string, scope *ScopeModel) (*ScopeModel, error) {
	if !validID(resourceId) {
		return nil, errEmptyID
	}
	req := &request{
		method: http.MethodPost,
		path:   path.Join("api/resources", resourceId, "scopes"),
		body:   map[string]any{"name": scope.Name, "description": scope.Description},
	}

	res, err := expect(201)(c.do(ctx, req))
	if err != nil {
		return nil, err
	}

	var returnScope ScopeModel
	if err := decode(res.Body, &returnScope); err != nil {
		return nil, err
	}

	return &returnScope, nil
}

func (c *Client) ApiResourceScopeDelete(ctx context.Context, resourceId string, scopeId string) error {
	if !validID(resourceId) || !validID(scopeId) {
		return errEmptyID
	}

	req := &request{
		method: http.MethodDelete,
		path:   path.Join("api/resources", resourceId, "scopes", scopeId),
	}

	return c.discard(ctx, req, 204, 404)
}

func (c *Client) ApiResourceScopeUpdate(ctx context.Context, scope *ScopeModel) (*ScopeModel, error) {
	if scope == nil || !validID(scope.ResourceId) || !validID(scope.ID) {
		return nil, errEmptyID
	}

	req := &request{
		method: http.MethodPatch,
		path:   path.Join("api/resources", scope.ResourceId, "scopes", scope.ID),
		body:   map[string]any{"name": scope.Name, "description": scope.Description},
	}

	res, err := expect(200)(c.do(ctx, req))
	if err != nil {
		return nil, err
	}

	var returnScope ScopeModel
	if err := decode(res.Body, &returnScope); err != nil {
		return nil, err
	}
	return &returnScope, nil
}

// ApiResourceScopeGet scans all pages. A truncated list must never imply deletion.
func (c *Client) ApiResourceScopeGet(ctx context.Context, resourceID, scopeID string) (*ScopeModel, error) {
	if !validID(resourceID) || !validID(scopeID) {
		return nil, errEmptyID
	}
	scopes, err := listPages(ctx, c, path.Join("api/resources", resourceID, "scopes"), func(scope ScopeModel) string { return scope.ID })
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, scope := range scopes {
		if scope.ID == scopeID {
			return &scope, nil
		}
	}
	return nil, nil
}
