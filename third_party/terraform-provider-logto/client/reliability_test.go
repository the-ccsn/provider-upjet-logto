package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func mockClient(t *testing.T, fn roundTripFunc) *Client {
	t.Helper()
	c, err := NewClient(&Config{Hostname: "example.invalid", ApplicationID: "test", ApplicationSecret: "test", HttpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/oidc/token" {
			return reply(200, `{"access_token":"test","token_type":"Bearer","expires_in":3600}`), nil
		}
		return fn(r)
	})}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func reply(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func payload(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestApplicationPatchPreservesUnmanagedConfiguration(t *testing.T) {
	c := mockClient(t, func(r *http.Request) (*http.Response, error) {
		switch r.Method {
		case "GET":
			return reply(200, `{"id":"app","isAdmin":true,"oidcClientMetadata":{"redirectUris":["https://old"],"backchannelLogoutUri":"https://logout","futureFlag":true},"customClientMetadata":{"idTokenTtl":7200,"rotateRefreshToken":true,"futureSetting":"keep"}}`), nil
		case "PATCH":
			m := payload(t, r)
			if _, ok := m["isAdmin"]; ok {
				t.Fatal("PATCH must not change computed admin permissions")
			}
			if m["description"] != "" {
				t.Fatal("description cannot be cleared")
			}
			oidc := m["oidcClientMetadata"].(map[string]any)
			custom := m["customClientMetadata"].(map[string]any)
			if oidc["futureFlag"] != true || oidc["backchannelLogoutUri"] != "https://logout" || custom["idTokenTtl"] != float64(7200) || custom["futureSetting"] != "keep" {
				t.Fatal("unmanaged metadata lost")
			}
			if len(oidc["redirectUris"].([]any)) != 0 || len(custom["corsAllowedOrigins"].([]any)) != 0 {
				t.Fatal("lists cannot be cleared")
			}
			return reply(200, `{"id":"app","type":"Traditional"}`), nil
		}
		t.Fatalf("unexpected method %s", r.Method)
		return nil, nil
	})
	_, err := c.ApplicationUpdate(context.Background(), &ApplicationModel{ID: "app", Name: "new", OidcClientMetadata: &OidcClientMetadata{}, CustomClientMetadata: &CustomClientMetadata{}})
	if err != nil {
		t.Fatal(err)
	}
}
func TestClearRoleAndUserAssignments(t *testing.T) {
	c := mockClient(t, func(r *http.Request) (*http.Response, error) {
		m := payload(t, r)
		switch r.URL.Path {
		case "/api/users/u/roles":
			if ids, ok := m["roleIds"].([]any); !ok || len(ids) != 0 {
				t.Fatal("empty roles must be a JSON array")
			}
			return reply(200, "[]"), nil
		case "/api/roles/r":
			if m["isDefault"] != false {
				t.Fatal("false default omitted")
			}
			if _, ok := m["type"]; ok {
				t.Fatal("immutable type sent in PATCH")
			}
			return reply(200, `{"id":"r"}`), nil
		}
		t.Fatal("unexpected path")
		return nil, nil
	})
	if err := c.UpdateRolesForUser(context.Background(), &RoleIdsModel{}, "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RoleUpdate(context.Background(), &RoleModel{ID: "r", Name: "new"}); err != nil {
		t.Fatal(err)
	}
}
func TestScopeReadBeyondFirstPage(t *testing.T) {
	calls := 0
	c := mockClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("page_size") != "100" {
			t.Fatal("page size missing")
		}
		if calls == 1 {
			scopes := make([]ScopeModel, 100)
			for i := range scopes {
				scopes[i].ID = fmt.Sprint(i)
			}
			b, _ := json.Marshal(scopes)
			return reply(200, string(b)), nil
		}
		if r.URL.Query().Get("page") != "2" {
			t.Fatal("second page missing")
		}
		return reply(200, `[{"id":"target"}]`), nil
	})
	scope, err := c.ApiResourceScopeGet(context.Background(), "resource", "target")
	if err != nil || scope == nil || scope.ID != "target" {
		t.Fatalf("scope lost beyond page one: %v", err)
	}
}
func TestScopePaginationFailureRetainsState(t *testing.T) {
	c := mockClient(t, func(r *http.Request) (*http.Response, error) {
		scopes := make([]ScopeModel, 100)
		for i := range scopes {
			scopes[i].ID = fmt.Sprint(i)
		}
		b, _ := json.Marshal(scopes)
		return reply(200, string(b)), nil
	})
	if _, err := c.ApiResourceScopeGet(context.Background(), "resource", "missing"); err == nil {
		t.Fatal("repeating page must be an error, not absence")
	}
}
func TestBoundedReadRetryAndNoMutationRetry(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PATCH", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			calls := 0
			c := mockClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				res := reply(503, "")
				res.Header.Set("Retry-After", "0")
				return res, nil
			})
			_, err := expect(200)(c.do(context.Background(), &request{method: method, path: "api/test"}))
			if err == nil {
				t.Fatal("expected failure")
			}
			want := 1
			if method == "GET" {
				want = 3
			}
			if calls != want {
				t.Fatalf("got %d calls, want %d", calls, want)
			}
		})
	}
}
func TestUnauthorizedRefreshIsBounded(t *testing.T) {
	tokens, requests := 0, 0
	c, err := NewClient(&Config{Hostname: "example.invalid", ApplicationID: "test", ApplicationSecret: "test", HttpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/oidc/token" {
			tokens++
			return reply(200, fmt.Sprintf(`{"access_token":"token%d","token_type":"Bearer","expires_in":3600}`, tokens)), nil
		}
		requests++
		if requests == 2 && r.Header.Get("Authorization") != "Bearer token2" {
			t.Fatal("new token not used")
		}
		return reply(401, ""), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := expect(200)(c.do(context.Background(), &request{method: "GET", path: "api/test"})); err == nil {
		t.Fatal("401 must fail after one refresh")
	}
	if tokens != 2 || requests != 2 {
		t.Fatal("unbounded refresh")
	}
}
func TestRetryHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := mockClient(t, func(r *http.Request) (*http.Response, error) {
		cancel()
		res := reply(429, "")
		res.Header.Set("Retry-After", "30")
		return res, nil
	})
	start := time.Now()
	_, err := c.do(ctx, &request{method: "GET", path: "api/test"})
	if !errors.Is(err, context.Canceled) || time.Since(start) > time.Second {
		t.Fatalf("cancellation not honored: %v", err)
	}
}
func TestConcurrentTokenRequestsShareCache(t *testing.T) {
	calls := 0
	c, err := NewClient(&Config{Hostname: "example.invalid", ApplicationID: "test", ApplicationSecret: "test", HttpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return reply(200, `{"access_token":"test","token_type":"Bearer","expires_in":3600}`), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.getAccessToken(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls != 1 {
		t.Fatalf("concurrent refreshes: %d", calls)
	}
}

type closeTracker struct {
	io.Reader
	closed bool
}

func (b *closeTracker) Close() error { b.closed = true; return nil }
func TestDeleteIsIdempotentAndClosesResponses(t *testing.T) {
	for _, code := range []int{204, 404} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			body := &closeTracker{Reader: strings.NewReader("")}
			c := mockClient(t, func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: code, Body: body}, nil
			})
			if err := c.RoleDelete(context.Background(), "r"); err != nil {
				t.Fatal(err)
			}
			if !body.closed {
				t.Fatal("body not closed")
			}
		})
	}
}
func TestCredentialsNeverFollowRedirects(t *testing.T) {
	leaked := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true; w.WriteHeader(200) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	c, err := NewClient(&Config{Hostname: "localhost", Endpoint: origin.URL, ApplicationID: "test", ApplicationSecret: "private"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.getAccessToken(context.Background()); err == nil {
		t.Fatal("redirect accepted")
	}
	if leaked {
		t.Fatal("credentials followed redirect")
	}
}

func TestRoleScopeReconciliationPreservesRole(t *testing.T) {
	var mutations []string
	c := mockClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return reply(200, `[{"id":"keep"},{"id":"old"}]`), nil
		}
		mutations = append(mutations, r.Method+" "+r.URL.Path)
		if r.Method == "POST" {
			m := payload(t, r)
			ids := m["scopeIds"].([]any)
			if len(ids) != 1 || ids[0] != "new" {
				t.Fatal("additions not deduplicated")
			}
			return reply(201, "[]"), nil
		}
		return reply(204, ""), nil
	})
	if err := c.RoleScopesUpdate(context.Background(), "r", []string{"keep", "new", "new"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(mutations, ",") != "DELETE /api/roles/r/scopes/old,POST /api/roles/r/scopes" {
		t.Fatalf("unexpected relation mutations: %v", mutations)
	}
	mutations = nil
	if err := c.RoleScopesUpdate(context.Background(), "r", nil); err != nil {
		t.Fatal(err)
	}
	if len(mutations) != 2 {
		t.Fatal("all scopes not revoked")
	}
}

func TestRoleScopeQuotaAndPartialFailureRecovery(t *testing.T) {
	current := map[string]bool{"keep": true, "old": true}
	fail := true
	c := mockClient(t, func(r *http.Request) (*http.Response, error) {
		switch r.Method {
		case "GET":
			scopes := []ScopeModel{}
			for id := range current {
				scopes = append(scopes, ScopeModel{ID: id})
			}
			b, _ := json.Marshal(scopes)
			return reply(200, string(b)), nil
		case "DELETE":
			delete(current, strings.TrimPrefix(r.URL.Path, "/api/roles/r/scopes/"))
			return reply(204, ""), nil
		case "POST":
			ids := payload(t, r)["scopeIds"].([]any)
			if len(current)+len(ids) > 2 {
				t.Fatal("scope quota exceeded during replacement")
			}
			if fail {
				fail = false
				return reply(403, ""), nil
			}
			for _, id := range ids {
				current[id.(string)] = true
			}
			return reply(201, "[]"), nil
		}
		t.Fatal("unexpected method")
		return nil, nil
	})
	desired := []string{"keep", "new"}
	if err := c.RoleScopesUpdate(context.Background(), "r", desired); err == nil {
		t.Fatal("expected partial failure")
	}
	if current["old"] || current["new"] || !current["keep"] {
		t.Fatal("partial failure must revoke obsolete grants without losing retained grants")
	}
	if err := c.RoleScopesUpdate(context.Background(), "r", desired); err != nil {
		t.Fatal(err)
	}
	if len(current) != 2 || !current["keep"] || !current["new"] {
		t.Fatal("retry did not converge")
	}
}

func TestUserRolesReadAllPages(t *testing.T) {
	c := mockClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("page") == "1" {
			roles := make([]RoleModel, 100)
			for i := range roles {
				roles[i].ID = fmt.Sprint(i)
			}
			b, _ := json.Marshal(roles)
			return reply(200, string(b)), nil
		}
		return reply(200, `[{"id":"last-role"}]`), nil
	})
	roles, err := c.GetRolesForUser(context.Background(), "user")
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 101 || roles[100].ID != "last-role" {
		t.Fatal("user roles were truncated")
	}
}

func TestNullCollectionsNeverMeanAbsence(t *testing.T) {
	c := mockClient(t, func(r *http.Request) (*http.Response, error) { return reply(200, "null"), nil })
	if _, err := c.ApiResourceScopeGet(context.Background(), "resource", "scope"); err == nil {
		t.Fatal("null scope response treated as deletion")
	}
	if _, err := c.GetRolesForUser(context.Background(), "user"); err == nil {
		t.Fatal("null roles accepted")
	}
	if _, err := c.RoleScopesGet(context.Background(), "role"); err == nil {
		t.Fatal("null role scopes accepted")
	}
	if _, err := c.GetApplicationSecrets(context.Background(), "app"); err == nil {
		t.Fatal("null secrets accepted")
	}
}

func TestInvalidIdentifiersNeverReachAPI(t *testing.T) {
	c := mockClient(t, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("invalid identifier reached %s", r.URL.Path)
		return nil, nil
	})
	for _, id := range []string{"", ".", "..", "../applications/app", "app?query", "app#fragment", "app%2fchild", "app\\child"} {
		for _, call := range []func(context.Context, string) error{c.RoleDelete, c.UserDelete, c.ApplicationDelete, c.ApiResourceDelete} {
			if err := call(context.Background(), id); err == nil {
				t.Fatalf("invalid identifier accepted: %q", id)
			}
		}
		if err := c.ApiResourceScopeDelete(context.Background(), "parent", id); err == nil {
			t.Fatal("invalid scope accepted")
		}
		if err := c.ApplicationSecretDelete(context.Background(), id, "gitops"); err == nil {
			t.Fatal("invalid application accepted")
		}
	}
	if err := c.ApplicationSecretDelete(context.Background(), "app", ".."); err == nil {
		t.Fatal("dot secret name accepted")
	}
}

func TestUserRoleReplacementRejectsInvisibleRolesBeforeWriting(t *testing.T) {
	for _, roleType := range []string{"MachineToMachine", ""} {
		t.Run(roleType, func(t *testing.T) {
			c := mockClient(t, func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || r.URL.Path != "/api/roles/r" {
					t.Fatal("invalid role caused a write")
				}
				return reply(200, fmt.Sprintf(`{"id":"r","type":%q}`, roleType)), nil
			})
			if err := c.UpdateRolesForUser(context.Background(), &RoleIdsModel{RoleIds: []string{"r"}}, "u"); err == nil {
				t.Fatal("non-user role accepted")
			}
		})
	}
	c := mockClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return reply(200, `{"id":"r","type":"User"}`), nil
		}
		if r.Method != "PUT" || r.URL.Path != "/api/users/u/roles" {
			t.Fatal("unexpected request")
		}
		return reply(200, "[]"), nil
	})
	if err := c.UpdateRolesForUser(context.Background(), &RoleIdsModel{RoleIds: []string{"r"}}, "u"); err != nil {
		t.Fatal(err)
	}
}

func TestApplicationCreateEncodesEmptyRedirectsAsArrays(t *testing.T) {
	c := mockClient(t, func(r *http.Request) (*http.Response, error) {
		metadata := payload(t, r)["oidcClientMetadata"].(map[string]any)
		for _, key := range []string{"redirectUris", "postLogoutRedirectUris"} {
			if values, ok := metadata[key].([]any); !ok || len(values) != 0 {
				t.Fatalf("%s must be an empty JSON array", key)
			}
		}
		return reply(200, `{"id":"app","type":"Traditional"}`), nil
	})
	input := &ApplicationModel{Name: "test", Type: "Traditional", OidcClientMetadata: &OidcClientMetadata{}}
	if _, err := c.ApplicationCreate(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if input.OidcClientMetadata.RedirectUris != nil {
		t.Fatal("client mutated caller input")
	}
}

func TestReadRetryAfterFormatsAndBounds(t *testing.T) {
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		header   string
		expected time.Duration
	}{
		{"2", 2 * time.Second},
		{"9223372036854775807", 30 * time.Second},
		{now.Add(10 * time.Second).Format(http.TimeFormat), 10 * time.Second},
		{now.Add(time.Hour).Format(http.TimeFormat), 30 * time.Second},
		{now.Add(-time.Second).Format(http.TimeFormat), 0},
		{"invalid", 100 * time.Millisecond},
		{"-1", 100 * time.Millisecond},
	} {
		if got := readRetryDelay(tc.header, 0, now); got != tc.expected {
			t.Fatalf("Retry-After %q: got %s, want %s", tc.header, got, tc.expected)
		}
	}
}
