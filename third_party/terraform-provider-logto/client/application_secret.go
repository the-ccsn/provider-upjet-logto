package client

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) ApplicationSecretCreate(ctx context.Context, appID, name string) (*Secret, error) {
	if !validID(appID) || !validSecretName(name) {
		return nil, errEmptyID
	}
	res, err := expect(http.StatusCreated)(c.do(ctx, &request{method: http.MethodPost, path: "api/applications/" + url.PathEscape(appID) + "/secrets", body: map[string]string{"name": name}}))
	if err != nil {
		return nil, err
	}
	var secret Secret
	if err := decode(res.Body, &secret); err != nil {
		return nil, err
	}
	return &secret, nil
}
func (c *Client) ApplicationSecretDelete(ctx context.Context, appID, name string) error {
	if !validID(appID) || !validSecretName(name) {
		return errEmptyID
	}
	res, err := expect(http.StatusNoContent, http.StatusNotFound)(c.do(ctx, &request{method: http.MethodDelete, path: "api/applications/" + url.PathEscape(appID) + "/secrets/" + url.PathEscape(name)}))
	if res != nil && res.Body != nil {
		res.Body.Close()
	}
	return err
}
