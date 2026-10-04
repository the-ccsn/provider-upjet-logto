package config

import (
	"encoding/json"
	"testing"

	application "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/application/v1alpha1"
	role "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/role/v1alpha1"
	user "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/user/v1alpha1"
)

func TestExplicitEmptyCollectionsSurviveSerialization(t *testing.T) {
	for _, tc := range []struct {
		name           string
		empty, omitted any
		field          string
	}{
		{"user", user.UserParameters{RoleIds: []*string{}}, user.UserParameters{}, "roleIds"},
		{"role", role.RoleParameters{ScopeIds: []*string{}}, role.RoleParameters{}, "scopeIds"},
		{"application", application.ApplicationParameters{RedirectUris: []*string{}}, application.ApplicationParameters{}, "redirectUris"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.empty)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if values, ok := fields[tc.field].([]any); !ok || len(values) != 0 {
				t.Fatal("explicit [] was lost")
			}
			data, err = json.Marshal(tc.omitted)
			if err != nil {
				t.Fatal(err)
			}
			fields = map[string]any{}
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if _, exists := fields[tc.field]; exists {
				t.Fatal("omitted collection was serialized")
			}
		})
	}
	account := &user.User{Spec: user.UserSpec{ForProvider: user.UserParameters{RoleIds: []*string{}}}}
	params, err := account.GetParameters()
	if err != nil {
		t.Fatal(err)
	}
	if values, ok := params["role_ids"].([]any); !ok || len(values) != 0 {
		t.Fatal("explicit [] lost at Terraform boundary")
	}
	if _, err := account.LateInitialize([]byte(`{"role_ids":["foreign-role"]}`)); err != nil {
		t.Fatal(err)
	}
	if account.Spec.ForProvider.RoleIds == nil || len(account.Spec.ForProvider.RoleIds) != 0 {
		t.Fatal("late initialization overwrote explicitly empty roles")
	}
}
