package client

import (
	"context"
	"errors"
	"net/http"
	"path"
)

func (c *Client) UserGet(ctx context.Context, id string) (*UserModel, error) {
	if !validID(id) {
		return nil, errEmptyID
	}

	req := &request{
		method: http.MethodGet,
		path:   "api/users/" + id,
	}
	res, err := expect(200, 404)(c.do(ctx, req))
	if err != nil {
		return nil, err
	}

	if res.StatusCode == 404 {
		res.Body.Close()
		return nil, nil
	}

	var user UserModel
	if err := decode(res.Body, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (c *Client) UserCreate(ctx context.Context, user *UserModel) (*UserModel, error) {
	req := &request{
		method: http.MethodPost,
		path:   "api/users",
		body:   user,
	}

	res, err := expect(200)(c.do(ctx, req))
	if err != nil {
		return nil, err
	}

	var User UserModel
	if err := decode(res.Body, &User); err != nil {
		return nil, err
	}
	return &User, nil
}

func (c *Client) UserDelete(ctx context.Context, id string) error {
	if !validID(id) {
		return errEmptyID
	}

	req := &request{
		method: http.MethodDelete,
		path:   "api/users/" + id,
	}
	return c.discard(ctx, req, 204, 404)
}

func (c *Client) UserUpdate(ctx context.Context, user *UserModel) (*UserModel, error) {
	if user == nil || !validID(user.ID) {
		return nil, errEmptyID
	}

	patch := map[string]any{"username": user.Username, "primaryEmail": user.PrimaryEmail, "name": user.Name}
	if user.Profile != nil {
		res, err := expect(200)(c.do(ctx, &request{method: http.MethodGet, path: "api/users/" + user.ID}))
		if err != nil {
			return nil, err
		}
		var current map[string]any
		if err := decode(res.Body, &current); err != nil {
			return nil, err
		}
		profile, _ := current["profile"].(map[string]any)
		if profile == nil {
			profile = map[string]any{}
		}
		profile["familyName"] = user.Profile.FamilyName
		profile["givenName"] = user.Profile.GivenName
		profile["middleName"] = user.Profile.MiddleName
		profile["nickname"] = user.Profile.Nickname
		patch["profile"] = profile
	}

	req := &request{
		method: http.MethodPatch,
		path:   "api/users/" + user.ID,
		body:   patch,
	}

	res, err := expect(200)(c.do(ctx, req))
	if err != nil {
		return nil, err
	}

	var returnUser UserModel
	if err := decode(res.Body, &returnUser); err != nil {
		return nil, err
	}
	return &returnUser, nil
}

func (c *Client) GetRolesForUser(ctx context.Context, userId string) ([]RoleModel, error) {
	if !validID(userId) {
		return nil, errEmptyID
	}

	return listPages(ctx, c, path.Join("api/users", userId, "roles"), func(role RoleModel) string { return role.ID })

}

func (c *Client) AssignRolesForUser(ctx context.Context, roleIds *RoleIdsModel, userId string) error {
	if !validID(userId) || roleIds == nil {
		return errEmptyID
	}

	req := &request{
		method: http.MethodPost,
		path:   path.Join("api/users", userId, "roles"),
		body:   &RoleIdsModel{RoleIds: nonNil(roleIds.RoleIds)},
	}

	return c.discard(ctx, req, 201)
}

func (c *Client) UpdateRolesForUser(ctx context.Context, roleIds *RoleIdsModel, userId string) error {
	if !validID(userId) || roleIds == nil {
		return errEmptyID
	}
	// Logto's replacement endpoint does not enforce RoleType.User. Its read
	// endpoint filters other types, which would otherwise cause perpetual drift.
	for _, id := range roleIds.RoleIds {
		if !validID(id) {
			return errEmptyID
		}
	}
	checked := map[string]bool{}
	for _, id := range roleIds.RoleIds {
		if checked[id] {
			continue
		}
		role, err := c.RoleGet(ctx, id)
		if err != nil {
			return err
		}
		if role == nil || role.Type != "User" {
			return errors.New("user role assignments require existing roles of type User")
		}
		checked[id] = true
	}

	req := &request{
		method: http.MethodPut,
		path:   path.Join("api/users", userId, "roles"),
		body:   &RoleIdsModel{RoleIds: nonNil(roleIds.RoleIds)},
	}

	return c.discard(ctx, req, 200)
}

func (c *Client) DeleteRolesForUser(ctx context.Context, roleId string, userId string) error {
	if !validID(userId) || !validID(roleId) {
		return errEmptyID
	}

	req := &request{
		method: http.MethodDelete,
		path:   path.Join("api/users", userId, "roles", roleId),
	}

	return c.discard(ctx, req, 204, 404)
}
