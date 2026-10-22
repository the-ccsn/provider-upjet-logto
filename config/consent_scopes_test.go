package config

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	v2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	ujconfig "github.com/crossplane/upjet/v2/pkg/config"
	ujresource "github.com/crossplane/upjet/v2/pkg/resource"
	clusterapplication "github.com/the-ccsn/provider-upjet-logto/apis/cluster/application/v1alpha1"
	application "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/application/v1alpha1"
	"github.com/the-ccsn/provider-upjet-logto/internal/consentscopes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestUserConsentScopesApplicationReferenceAndEmptyScopes(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := application.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	app := &application.Application{ObjectMeta: metav1.ObjectMeta{Name: "cloudflare-access", Namespace: "prod"}}
	meta.SetExternalName(app, "application-id")
	// A same-named Application in another namespace must not be selected.
	other := &application.Application{ObjectMeta: metav1.ObjectMeta{Name: "cloudflare-access", Namespace: "other"}}
	meta.SetExternalName(other, "wrong-id")
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app, other).Build()
	consent := &application.UserConsentScopes{
		ObjectMeta: metav1.ObjectMeta{Name: "cloudflare-access", Namespace: "prod"},
		Spec: application.UserConsentScopesSpec{ForProvider: application.UserConsentScopesParameters{
			ApplicationIDRef: &v2.NamespacedReference{Name: "cloudflare-access"},
			UserScopes:       []*string{},
		}},
	}
	if err := consent.ResolveReferences(ctx, kube); err != nil {
		t.Fatal(err)
	}
	if consent.Spec.ForProvider.ApplicationID == nil || *consent.Spec.ForProvider.ApplicationID != "application-id" {
		t.Fatal("application reference did not resolve in resource namespace")
	}
	params, err := consent.GetParameters()
	if err != nil {
		t.Fatal(err)
	}
	if params["application_id"] != "application-id" {
		t.Fatal(params)
	}
	if scopes, ok := params["user_scopes"].([]any); !ok || len(scopes) != 0 {
		t.Fatal("explicit empty scopes lost at Terraform boundary", params)
	}
	if _, err := consent.LateInitialize([]byte(`{"user_scopes":["phone"]}`)); err != nil {
		t.Fatal(err)
	}
	if consent.Spec.ForProvider.UserScopes == nil || len(consent.Spec.ForProvider.UserScopes) != 0 {
		t.Fatal("late initialization overwrote explicit empty scopes")
	}
}

func TestUserConsentScopesMergePreservesExplicitValues(t *testing.T) {
	type parameters interface {
		GetMergedParameters(bool) (map[string]any, error)
	}
	for name, factory := range map[string]func() parameters{
		"namespaced": func() parameters { return &application.UserConsentScopes{} },
		"cluster":    func() parameters { return &clusterapplication.UserConsentScopes{} },
	} {
		for _, row := range []struct {
			name, forProvider, initProvider string
			merge                           bool
			want                            []any
		}{
			{"empty overrides defaults", `[]`, `["profile","email"]`, true, []any{}},
			{"nonempty overrides defaults", `["email"]`, `["profile"]`, true, []any{"email"}},
			{"omitted uses defaults", `null`, `["profile"]`, true, []any{"profile"}},
			{"omitted uses empty defaults", `null`, `[]`, true, []any{}},
			{"empty without defaults", `[]`, `null`, true, []any{}},
			{"disabled init merge", `[]`, `["profile"]`, false, []any{}},
		} {
			t.Run(name+"/"+row.name, func(t *testing.T) {
				item := factory()
				data := `{"spec":{"forProvider":{"applicationId":"app","userScopes":` + row.forProvider + `},"initProvider":{"userScopes":` + row.initProvider + `}}}`
				if err := json.Unmarshal([]byte(data), item); err != nil {
					t.Fatal(err)
				}
				got, err := item.GetMergedParameters(row.merge)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got["user_scopes"], row.want) {
					t.Fatalf("user_scopes = %#v, want %#v", got["user_scopes"], row.want)
				}
			})
		}
	}
}

func TestUserConsentScopesLateInitializationPreservesDefaults(t *testing.T) {
	type parameters interface {
		GetParameters() (map[string]any, error)
		GetMergedParameters(bool) (map[string]any, error)
		LateInitialize([]byte) (bool, error)
	}
	for name, factory := range map[string]func() parameters{
		"namespaced": func() parameters { return &application.UserConsentScopes{} },
		"cluster":    func() parameters { return &clusterapplication.UserConsentScopes{} },
	} {
		for _, row := range []struct {
			name, forProvider, initProvider, observed string
			wantForProvider                           any
			wantMerged                                []any
			wantChanged                               bool
		}{
			{"failed initial grant", `null`, `["profile","email"]`, `[]`, nil, []any{"profile", "email"}, false},
			{"partial initial grant", `null`, `["profile","email"]`, `["profile"]`, nil, []any{"profile", "email"}, false},
			{"empty defaults", `null`, `[]`, `["phone"]`, nil, []any{}, false},
			{"adoption without defaults", `null`, `null`, `["phone"]`, []any{"phone"}, []any{"phone"}, true},
			{"empty adoption without defaults", `null`, `null`, `[]`, []any{}, []any{}, true},
			{"explicit empty overrides defaults", `[]`, `["profile"]`, `["phone"]`, []any{}, []any{}, false},
			{"explicit value overrides defaults", `["email"]`, `["profile"]`, `["phone"]`, []any{"email"}, []any{"email"}, false},
		} {
			t.Run(name+"/"+row.name, func(t *testing.T) {
				item := factory()
				data := `{"spec":{"forProvider":{"applicationId":"app","userScopes":` + row.forProvider + `},"initProvider":{"userScopes":` + row.initProvider + `}}}`
				if err := json.Unmarshal([]byte(data), item); err != nil {
					t.Fatal(err)
				}
				changed, err := item.LateInitialize([]byte(`{"application_id":"app","user_scopes":` + row.observed + `}`))
				if err != nil {
					t.Fatal(err)
				}
				if changed != row.wantChanged {
					t.Errorf("late initialization changed = %t, want %t", changed, row.wantChanged)
				}
				got, err := item.GetParameters()
				if err != nil {
					t.Fatal(err)
				}
				if scopes := got["user_scopes"]; !reflect.DeepEqual(scopes, row.wantForProvider) {
					t.Errorf("forProvider.userScopes = %#v, want %#v", scopes, row.wantForProvider)
				}
				merged, err := item.GetMergedParameters(true)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(merged["user_scopes"], row.wantMerged) {
					t.Errorf("effective user_scopes = %#v, want %#v", merged["user_scopes"], row.wantMerged)
				}
			})
		}
	}
}

func TestUserConsentScopesUsesGeneratedMergeAtRuntime(t *testing.T) {
	for _, provider := range []func() *ujconfig.Provider{GetProvider, GetProviderNamespaced} {
		resource := provider().Resources["logto_application_user_consent_scopes"]
		//lint:ignore SA1019 Verify the runtime selects the generated merge, rather than the compatibility path.
		if resource.ControllerReconcileVersion != resource.Version {
			t.Fatal("consent controller would bypass the generated parameter merge")
		}
	}
}

func TestUserConsentScopesLateInitializationPreservesInitialParent(t *testing.T) {
	completed := map[string]string{"crossplane.io/external-name": "app", consentscopes.InitializedAnnotation: "app"}
	pending := map[string]string{"crossplane.io/external-name": "app", consentscopes.InitializedAnnotation: ""}
	stale := map[string]string{"crossplane.io/external-name": "app", consentscopes.InitializedAnnotation: "other"}
	type parameters interface {
		GetParameters() (map[string]any, error)
		GetMergedParameters(bool) (map[string]any, error)
		LateInitialize([]byte) (bool, error)
	}
	for scope, factory := range map[string]func() parameters{
		"namespaced": func() parameters { return &application.UserConsentScopes{} },
		"cluster":    func() parameters { return &clusterapplication.UserConsentScopes{} },
	} {
		for _, row := range []struct {
			name, parent, initial, wantMerged string
			wantParent                        any
			wantChanged                       bool
			reference, selector               bool
			annotations                       map[string]string
			removeInitial                     bool
		}{
			{name: "initial parent remains creation-only", parent: `null`, initial: `"app"`, wantMerged: "other"},
			{name: "adoption without defaults fills parent", parent: `null`, initial: `null`, wantMerged: "app", wantParent: "app", wantChanged: true},
			{name: "explicit parent remains managed", parent: `"managed"`, initial: `"app"`, wantMerged: "managed", wantParent: "managed"},
			{name: "unresolved initial reference remains creation-only", parent: `null`, initial: `null`, wantMerged: "other", reference: true},
			{name: "unresolved initial selector remains creation-only", parent: `null`, initial: `null`, wantMerged: "other", selector: true},
			{name: "completed parent default removed", parent: `null`, initial: `"app"`, wantMerged: "other", annotations: completed, removeInitial: true},
			{name: "completed parent reference removed", parent: `null`, initial: `null`, wantMerged: "other", reference: true, annotations: completed, removeInitial: true},
			{name: "completed parent selector removed", parent: `null`, initial: `null`, wantMerged: "other", selector: true, annotations: completed, removeInitial: true},
			{name: "pending parent default removed", parent: `null`, initial: `"app"`, wantMerged: "other", annotations: pending, removeInitial: true},
			{name: "pending parent reference removed", parent: `null`, initial: `null`, wantMerged: "other", reference: true, annotations: pending, removeInitial: true},
			{name: "pending parent selector removed", parent: `null`, initial: `null`, wantMerged: "other", selector: true, annotations: pending, removeInitial: true},
			{name: "pending marker without identity discovers parent", parent: `null`, initial: `null`, wantMerged: "app", wantParent: "app", wantChanged: true, annotations: map[string]string{consentscopes.InitializedAnnotation: ""}},
			{name: "stale marker does not bind parent", parent: `null`, initial: `null`, wantMerged: "app", wantParent: "app", wantChanged: true, annotations: stale},
		} {
			t.Run(scope+"/"+row.name, func(t *testing.T) {
				item := factory()
				data := `{"spec":{"forProvider":{"applicationId":` + row.parent + `,"userScopes":[]},"initProvider":{"applicationId":` + row.initial + `,"userScopes":["email"]}}}`
				if err := json.Unmarshal([]byte(data), item); err != nil {
					t.Fatal(err)
				}
				if row.reference {
					if err := json.Unmarshal([]byte(`{"spec":{"initProvider":{"applicationIdRef":{"name":"missing"}}}}`), item); err != nil {
						t.Fatal(err)
					}
				}
				if row.selector {
					if err := json.Unmarshal([]byte(`{"spec":{"initProvider":{"applicationIdSelector":{"matchLabels":{"parent":"missing"}}}}}`), item); err != nil {
						t.Fatal(err)
					}
				}
				if row.annotations != nil {
					tr := item.(ujresource.Terraformed)
					meta.AddAnnotations(tr, row.annotations)
				}
				if row.removeInitial {
					if err := json.Unmarshal([]byte(`{"spec":{"initProvider":{"applicationId":null,"applicationIdRef":null,"applicationIdSelector":null}}}`), item); err != nil {
						t.Fatal(err)
					}
				}
				changed, err := item.LateInitialize([]byte(`{"application_id":"app","user_scopes":[]}`))
				if err != nil {
					t.Fatal(err)
				}
				if changed != row.wantChanged {
					t.Errorf("late initialization changed=%t, want %t", changed, row.wantChanged)
				}
				params, err := item.GetParameters()
				if err != nil {
					t.Fatal(err)
				}
				if got := params["application_id"]; got != row.wantParent {
					t.Errorf("forProvider.applicationId=%v, want %v", got, row.wantParent)
				}
				// A confirmed absence starts another creation with current defaults.
				if err := json.Unmarshal([]byte(`{"spec":{"initProvider":{"applicationId":"other"}}}`), item); err != nil {
					t.Fatal(err)
				}
				if row.removeInitial {
					meta.AddAnnotations(item.(ujresource.Terraformed), map[string]string{consentscopes.InitializedAnnotation: ""})
				}
				merged, err := item.GetMergedParameters(true)
				if err != nil {
					t.Fatal(err)
				}
				if got := merged["application_id"]; got != row.wantMerged {
					t.Errorf("recreated parent=%v, want %s", got, row.wantMerged)
				}
			})
		}
	}
}

func TestUserConsentScopesInitializedParent(t *testing.T) {
	type parameters interface {
		GetMergedParameters(bool) (map[string]any, error)
	}
	for scope, factory := range map[string]func() parameters{
		"namespaced": func() parameters { return &application.UserConsentScopes{} },
		"cluster":    func() parameters { return &clusterapplication.UserConsentScopes{} },
	} {
		for _, row := range []struct {
			name, parent, initialized, external, want string
		}{
			{"initial parent edit after completion", `null`, "app", "app", "app"},
			{"pending initialization uses current defaults", `null`, "", "app", "other"},
			{"explicit parent remains managed", `"managed"`, "app", "app", "managed"},
			{"different external identity is not initialized", `null`, "app", "new", "other"},
		} {
			t.Run(scope+"/"+row.name, func(t *testing.T) {
				item := factory()
				data, err := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]string{
					consentscopes.InitializedAnnotation: row.initialized,
					"crossplane.io/external-name":       row.external,
				}}})
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, item); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(`{"spec":{"forProvider":{"applicationId":`+row.parent+`,"userScopes":[]},"initProvider":{"applicationId":"other","userScopes":["email"]}}}`), item); err != nil {
					t.Fatal(err)
				}
				merged, err := item.GetMergedParameters(true)
				if err != nil {
					t.Fatal(err)
				}
				if merged["application_id"] != row.want {
					t.Fatalf("merged parent=%v, want %s", merged["application_id"], row.want)
				}
			})
		}
	}
}

func TestUserConsentScopesInitialReferences(t *testing.T) {
	type referenced interface {
		ujresource.Terraformed
		ResolveReferences(context.Context, client.Reader) error
	}
	for scope, factory := range map[string]func() referenced{
		"namespaced": func() referenced { return &application.UserConsentScopes{} },
		"cluster":    func() referenced { return &clusterapplication.UserConsentScopes{} },
	} {
		for _, row := range []struct {
			name                         string
			complete, explicit, selector bool
			managedReference, wantError  bool
			unmarked, creating, ready    bool
		}{
			{name: "completed initial reference is ignored", complete: true},
			{name: "completed initial selector is ignored", complete: true, selector: true},
			{name: "pending initial reference is required", wantError: true},
			{name: "pending initial selector is required", selector: true, wantError: true},
			{name: "explicit parent overrides initial reference", explicit: true},
			{name: "managed reference is still required", complete: true, managedReference: true, wantError: true},
			{name: "adopted initial reference is ignored", unmarked: true},
			{name: "adopted initial selector is ignored", unmarked: true, selector: true},
			{name: "previously ready resource ignores initial reference", unmarked: true, creating: true, ready: true},
			{name: "failed creation still requires initial reference", unmarked: true, creating: true, wantError: true},
			{name: "adopted managed reference is still required", unmarked: true, managedReference: true, wantError: true},
		} {
			t.Run(scope+"/"+row.name, func(t *testing.T) {
				scheme := runtime.NewScheme()
				for _, add := range []func(*runtime.Scheme) error{application.AddToScheme, clusterapplication.AddToScheme} {
					if err := add(scheme); err != nil {
						t.Fatal(err)
					}
				}
				item := factory()
				initial := map[string]any{"applicationId": "app", "userScopes": []string{"email"}}
				ref := map[string]any{"name": "missing", "policy": map[string]string{"resolve": "Always"}}
				if row.selector {
					initial["applicationIdSelector"] = map[string]any{"matchLabels": map[string]string{"parent": "missing"}, "policy": map[string]string{"resolve": "Always"}}
				} else {
					initial["applicationIdRef"] = ref
				}
				managed := map[string]any{"userScopes": []string{}}
				if row.explicit {
					managed["applicationId"] = "app"
				}
				if row.managedReference {
					managed["applicationIdRef"] = ref
				}
				data, err := json.Marshal(map[string]any{"metadata": map[string]any{"name": "consent"}, "spec": map[string]any{"forProvider": managed, "initProvider": initial}})
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, item); err != nil {
					t.Fatal(err)
				}
				state := ""
				if row.complete {
					state = "app"
				}
				meta.SetExternalName(item, "app")
				if !row.unmarked {
					meta.AddAnnotations(item, map[string]string{consentscopes.InitializedAnnotation: state})
				}
				if row.creating {
					meta.SetExternalCreatePending(item, metav1.Now().Time)
				}
				if row.ready {
					if err := item.SetObservation(map[string]any{"id": "app"}); err != nil {
						t.Fatal(err)
					}
					item.SetConditions(v2.Available(), v2.ReconcileSuccess())
				}
				kube := fake.NewClientBuilder().WithScheme(scheme).Build()
				err = item.ResolveReferences(context.Background(), kube)
				if (err != nil) != row.wantError {
					t.Fatalf("reference resolution error=%v, wantError=%t", err, row.wantError)
				}
			})
		}
	}
}
