package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// ValidApplicationID reports whether an ID can be used as one URL path segment.
func ValidApplicationID(id string) bool {
	return validID(id) && !strings.ContainsFunc(id, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// ApplicationUserConsentScopesGet returns nil only when the application is absent.
// API and organization consent scopes are deliberately outside this resource's ownership.
func (c *Client) ApplicationUserConsentScopesGet(ctx context.Context, applicationID string) ([]string, error) {
	if !ValidApplicationID(applicationID) {
		return nil, errEmptyID
	}
	res, err := expect(200, 404)(c.do(ctx, &request{method: http.MethodGet, path: path.Join("api/applications", applicationID, "user-consent-scopes")}))
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusNotFound {
		res.Body.Close()
		return nil, nil
	}
	var result struct {
		UserScopes []string `json:"userScopes"`
	}
	if err := decode(res.Body, &result); err != nil {
		return nil, err
	}
	if result.UserScopes == nil {
		return nil, fmt.Errorf("consent scopes response is missing userScopes")
	}
	return result.UserScopes, nil
}

// ApplicationUserConsentScopesUpdate authoritatively reconciles user scopes in place.
// Reads precede mutations, obsolete scopes are revoked first, and retries are idempotent.
func (c *Client) ApplicationUserConsentScopesUpdate(ctx context.Context, applicationID string, desired []string) error {
	if !ValidApplicationID(applicationID) {
		return errEmptyID
	}
	wanted := map[string]bool{}
	for _, scope := range desired {
		if !ValidUserConsentScope(scope) {
			return fmt.Errorf("invalid user consent scope %q", scope)
		}
		wanted[scope] = true
	}
	current, err := c.ApplicationUserConsentScopesGet(ctx, applicationID)
	if err != nil {
		return err
	}
	if current == nil {
		return fmt.Errorf("application not found while reconciling consent scopes")
	}
	existing := map[string]bool{}
	for _, scope := range current {
		existing[scope] = true
		if !wanted[scope] {
			// A URI scope contains ':'; escape it as one path segment.
			if err := c.discard(ctx, &request{method: http.MethodDelete, path: path.Join("api/applications", applicationID, "user-consent-scopes/user-scopes", url.PathEscape(scope))}, 204, 404); err != nil {
				return err
			}
		}
	}
	additions := []string{}
	for _, scope := range desired {
		if !existing[scope] {
			additions = append(additions, scope)
			existing[scope] = true
		}
	}
	if len(additions) == 0 {
		return nil
	}
	return c.discard(ctx, &request{method: http.MethodPost, path: path.Join("api/applications", applicationID, "user-consent-scopes"), body: map[string]any{"userScopes": additions}}, 201)
}

func ValidUserConsentScope(scope string) bool {
	// Match UserScope in Logto 1.40.1, the supported API contract. Validate
	// newer scopes here before reconciliation can revoke existing grants.
	switch scope {
	case "profile", "email", "phone", "address", "custom_data", "identities", "roles",
		"urn:logto:scope:organizations", "urn:logto:scope:organization_roles", "urn:logto:scope:sessions":
		return true
	default:
		return false
	}
}
