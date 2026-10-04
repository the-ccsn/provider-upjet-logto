package client

import (
	"context"
	"net/http"
	"path"
)

func (c *Client) RoleGet(ctx context.Context, id string) (*RoleModel, error) {
	if !validID(id) {
		return nil, errEmptyID
	}

	req := &request{
		method: http.MethodGet,
		path:   path.Join("api/roles", id),
	}

	res, err := expect(200, 404)(c.do(ctx, req))
	if err != nil {
		return nil, err
	}

	if res.StatusCode == 404 {
		res.Body.Close()
		return nil, nil
	}

	var role RoleModel
	if err := decode(res.Body, &role); err != nil {
		return nil, err
	}
	return &role, nil
}

func (c *Client) RoleScopesGet(ctx context.Context, roleId string) ([]ScopeModel, error) {
	if !validID(roleId) {
		return nil, errEmptyID
	}

	req := &request{
		method: http.MethodGet,
		path:   path.Join("api/roles", roleId, "scopes"),
	}

	res, err := expect(200, 404)(c.do(ctx, req))
	if err != nil {
		return nil, err
	}

	if res.StatusCode == 404 {
		res.Body.Close()
		return nil, nil
	}

	var roleScopes []ScopeModel
	if err := decode(res.Body, &roleScopes); err != nil {
		return nil, err
	}
	return roleScopes, nil
}

func (c *Client) RoleCreate(ctx context.Context, role *RoleModel) (*RoleModel, error) {
	req := &request{
		method: http.MethodPost,
		path:   "api/roles",
		body:   role,
	}

	res, err := expect(200)(c.do(ctx, req))
	if err != nil {
		return nil, err
	}

	var returnRole RoleModel
	if err := decode(res.Body, &returnRole); err != nil {
		return nil, err
	}
	return &returnRole, nil
}

func (c *Client) RoleDelete(ctx context.Context, id string) error {
	if !validID(id) {
		return errEmptyID
	}

	req := &request{
		method: http.MethodDelete,
		path:   path.Join("api/roles", id),
	}

	return c.discard(ctx, req, 204, 404)
}

func (c *Client) RoleUpdate(ctx context.Context, role *RoleModel) (*RoleModel, error) {
	if role == nil || !validID(role.ID) {
		return nil, errEmptyID
	}

	req := &request{
		method: http.MethodPatch,
		path:   path.Join("api/roles", role.ID),
		body:   map[string]any{"name": role.Name, "description": role.Description, "isDefault": role.IsDefault},
	}

	res, err := expect(200)(c.do(ctx, req))
	if err != nil {
		return nil, err
	}

	var returnRole RoleModel
	if err := decode(res.Body, &returnRole); err != nil {
		return nil, err
	}
	return &returnRole, nil
}

// RoleScopesUpdate reconciles relations in place, preserving role identity and memberships.
func (c *Client) RoleScopesUpdate(ctx context.Context, roleID string, desired []string) error {
	if !validID(roleID) {
		return errEmptyID
	}
	current, err := c.RoleScopesGet(ctx, roleID)
	if err != nil {
		return err
	}
	existing, wanted := map[string]bool{}, map[string]bool{}
	for _, scope := range current {
		existing[scope.ID] = true
	}
	additions := []string{}
	for _, id := range desired {
		if !validID(id) {
			return errEmptyID
		}
		if !wanted[id] && !existing[id] {
			additions = append(additions, id)
		}
		wanted[id] = true
	}
	for _, scope := range current {
		if !wanted[scope.ID] {
			if err := c.discard(ctx, &request{method: http.MethodDelete, path: path.Join("api/roles", roleID, "scopes", scope.ID)}, 204, 404); err != nil {
				return err
			}
		}
	}
	// Revoke obsolete grants first to respect quotas and fail closed on partial errors.
	if len(additions) > 0 {
		if err := c.discard(ctx, &request{method: http.MethodPost, path: path.Join("api/roles", roleID, "scopes"), body: map[string]any{"scopeIds": additions}}, 201); err != nil {
			return err
		}
	}
	return nil
}
