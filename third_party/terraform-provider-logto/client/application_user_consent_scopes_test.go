package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestApplicationUserConsentScopesReconcile(t *testing.T) {
	current := []string{"phone", "urn:logto:scope:organizations"}
	mutations := []string{}
	c := mockClient(t, func(r *http.Request) (*http.Response, error) {
		base := "/api/applications/app/user-consent-scopes"
		switch {
		case r.Method == "GET" && r.URL.Path == base:
			// Unrelated grants must never be passed back to POST or DELETE.
			return apiConsentReply(current), nil
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, base+"/user-scopes/"):
			scope := strings.TrimPrefix(r.URL.Path, base+"/user-scopes/")
			mutations = append(mutations, "delete:"+scope)
			current = slices.DeleteFunc(current, func(s string) bool { return s == scope })
			return reply(204, ""), nil
		case r.Method == "POST" && r.URL.Path == base:
			body := payload(t, r)
			if len(body) != 1 {
				t.Fatalf("unexpected ownership: %v", body)
			}
			for _, value := range body["userScopes"].([]any) {
				scope := value.(string)
				if slices.Contains(current, scope) {
					t.Fatal("duplicate assignment")
				}
				current = append(current, scope)
				mutations = append(mutations, "add:"+scope)
			}
			return reply(201, ""), nil
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL)
			return nil, nil
		}
	})
	ctx := context.Background()
	desired := []string{"profile", "email"}
	if err := c.ApplicationUserConsentScopesUpdate(ctx, "app", desired); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mutations, []string{"delete:phone", "delete:urn:logto:scope:organizations", "add:profile", "add:email"}) {
		t.Fatal(mutations)
	}
	count := len(mutations)
	if err := c.ApplicationUserConsentScopesUpdate(ctx, "app", desired); err != nil {
		t.Fatal(err)
	}
	if len(mutations) != count {
		t.Fatal("rerun mutated converged grants")
	}
	// Removing and adding grants outside Git is corrected on the next reconciliation.
	current = []string{"email", "roles"}
	if err := c.ApplicationUserConsentScopesUpdate(ctx, "app", desired); err != nil {
		t.Fatal(err)
	}
	slices.Sort(current)
	if !reflect.DeepEqual(current, []string{"email", "profile"}) {
		t.Fatal(current)
	}
	if err := c.ApplicationUserConsentScopesUpdate(ctx, "app", []string{}); err != nil {
		t.Fatal(err)
	}
	if len(current) != 0 {
		t.Fatal("explicit empty set failed to revoke grants")
	}
}

func apiConsentReply(scopes []string) *http.Response {
	// Marshal through the same response helpers as the rest of the client tests.
	encoded, _ := json.Marshal(map[string]any{"userScopes": scopes, "resourceScopes": []any{map[string]any{"resource": map[string]any{"id": "api"}, "scopes": []any{map[string]any{"id": "keep"}}}}, "organizationScopes": []any{map[string]any{"id": "keep-org"}}})
	return reply(200, string(encoded))
}

func TestApplicationUserConsentScopesRejectsControlID(t *testing.T) {
	c := mockClient(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid ID reached HTTP transport")
		return nil, nil
	})
	if _, err := c.ApplicationUserConsentScopesGet(context.Background(), "bad\tapp"); !errors.Is(err, errEmptyID) {
		t.Fatalf("invalid ID was not rejected before constructing the request: %v", err)
	}
	if err := c.ApplicationUserConsentScopesUpdate(context.Background(), "bad\tapp", []string{"email"}); !errors.Is(err, errEmptyID) {
		t.Fatalf("invalid ID was not rejected before reconciliation: %v", err)
	}
}

func TestApplicationUserConsentScopesRejectsUnsupportedScopes(t *testing.T) {
	for _, desired := range [][]string{
		{"urn:logto:scope:trusted_devices"},
		{"email", "urn:logto:scope:trusted_devices"},
	} {
		t.Run(strings.Join(desired, ","), func(t *testing.T) {
			current := []string{"profile", "email"}
			requests := []string{}
			c := mockClient(t, func(r *http.Request) (*http.Response, error) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				switch r.Method {
				case "GET":
					return apiConsentReply(current), nil
				case "DELETE":
					scope := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
					current = slices.DeleteFunc(current, func(s string) bool { return s == scope })
					return reply(204, ""), nil
				case "POST":
					// Logto 1.40.1 rejects this value before assigning any scopes.
					return reply(400, `{}`), nil
				default:
					t.Fatalf("unexpected request %s %s", r.Method, r.URL)
					return nil, nil
				}
			})
			if err := c.ApplicationUserConsentScopesUpdate(context.Background(), "app", desired); err == nil {
				t.Fatal("unsupported scope accepted")
			}
			if len(requests) != 0 || !slices.Equal(current, []string{"profile", "email"}) {
				t.Fatalf("unsupported scopes reached API: requests=%v, remaining scopes=%v", requests, current)
			}
		})
	}
}

func TestApplicationUserConsentScopesFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		missing bool
	}{
		{"missing", 404, `{}`, true},
		{"forbidden", 403, `{}`, false},
		{"malformed", 200, `{`, false},
		{"missing scopes", 200, `{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := mockClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" {
					t.Fatal("mutated after failed read")
				}
				return reply(tc.status, tc.body), nil
			})
			scopes, err := c.ApplicationUserConsentScopesGet(context.Background(), "app")
			if tc.missing {
				if err != nil || scopes != nil {
					t.Fatalf("missing: %v %v", scopes, err)
				}
			} else if err == nil {
				t.Fatal("failed read accepted")
			}
			if err := c.ApplicationUserConsentScopesUpdate(context.Background(), "app", []string{"email"}); err == nil {
				t.Fatal("reconciliation succeeded after failed read")
			}
			if calls != 2 {
				t.Fatal(calls)
			}
		})
	}
	t.Run("validation before requests", func(t *testing.T) {
		c := mockClient(t, func(*http.Request) (*http.Response, error) { t.Fatal("invalid scope reached API"); return nil, nil })
		for _, scope := range []string{"openid", "", "../profile", "unknown"} {
			if err := c.ApplicationUserConsentScopesUpdate(context.Background(), "app", []string{scope}); err == nil {
				t.Fatalf("accepted %q", scope)
			}
		}
	})
	t.Run("partial failure resumes", func(t *testing.T) {
		current := []string{"phone"}
		fail := true
		c := mockClient(t, func(r *http.Request) (*http.Response, error) {
			switch r.Method {
			case "GET":
				return apiConsentReply(current), nil
			case "DELETE":
				current = []string{}
				return reply(204, ""), nil
			case "POST":
				if fail {
					return reply(403, `{}`), nil
				}
				current = []string{"email"}
				return reply(201, ""), nil
			default:
				t.Fatal(r.Method)
				return nil, nil
			}
		})
		if err := c.ApplicationUserConsentScopesUpdate(context.Background(), "app", []string{"email"}); err == nil {
			t.Fatal("failed addition accepted")
		}
		if len(current) != 0 {
			t.Fatal("obsolete permissions not revoked first")
		}
		fail = false
		if err := c.ApplicationUserConsentScopesUpdate(context.Background(), "app", []string{"email"}); err != nil {
			t.Fatal(err)
		}
	})
}
