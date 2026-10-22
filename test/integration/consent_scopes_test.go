//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	application "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/application/v1alpha1"
	pcapi "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/v1beta1"
	"github.com/the-ccsn/provider-upjet-logto/internal/consentscopes"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestControllerUserConsentScopes(t *testing.T) {
	for _, row := range []struct {
		name     string
		lateInit bool
	}{{"with_late_init", true}, {"without_late_init", false}} {
		t.Run(row.name, func(t *testing.T) { testControllerUserConsentScopes(t, row.lateInit) })
	}
}

func testControllerUserConsentScopes(t *testing.T, lateInit bool) {
	kube, configPath, dir := newControlPlane(t)
	ctx := context.Background()
	var mu sync.Mutex
	scopes := []string{}
	fail, missNextRead := true, false
	parent := "app"
	posts, reads, writes := 0, 0, 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/oidc/token" {
			json.NewEncoder(w).Encode(map[string]any{"access_token": "consent-test", "token_type": "Bearer", "expires_in": 3600})
			return
		}
		base := "/api/applications/" + parent + "/user-consent-scopes"
		switch {
		case r.Method == "GET" && r.URL.Path == base:
			reads++
			if missNextRead {
				missNextRead = false
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"userScopes": scopes})
		case r.Method == "POST" && r.URL.Path == base:
			posts++
			writes++
			if fail {
				w.WriteHeader(403)
				return
			}
			var b struct {
				UserScopes []string `json:"userScopes"`
			}
			if json.NewDecoder(r.Body).Decode(&b) != nil {
				w.WriteHeader(400)
				return
			}
			scopes = append(scopes, b.UserScopes...)
			w.WriteHeader(201)
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, base+"/user-scopes/"):
			writes++
			scope := strings.TrimPrefix(r.URL.Path, base+"/user-scopes/")
			scopes = slices.DeleteFunc(scopes, func(s string) bool { return s == scope })
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	caPath := filepath.Join(dir, "consent-test-ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	creds, _ := json.Marshal(map[string]string{"hostname": strings.TrimPrefix(server.URL, "https://"), "application_id": "consent-test", "application_secret": "consent-test", "resource": "https://default.logto.app/api"})
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "management", Namespace: "tenant"}, Data: map[string][]byte{"credentials": creds}}
	if err := kube.Create(ctx, secret); err != nil {
		t.Fatal(err)
	}
	pc := &pcapi.ProviderConfig{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "tenant"}, Spec: pcapi.ProviderConfigSpec{Credentials: pcapi.ProviderCredentials{Source: xpv2.CredentialsSourceSecret, CommonCredentialSelectors: xpv2.CommonCredentialSelectors{SecretRef: &xpv2.SecretKeySelector{SecretReference: xpv2.SecretReference{Name: "management", Namespace: "tenant"}, Key: "credentials"}}}}}
	if err := kube.Create(ctx, pc); err != nil {
		t.Fatal(err)
	}
	stop := startProvider(t, configPath, caPath, dir)
	item := &application.UserConsentScopes{
		ObjectMeta: metav1.ObjectMeta{Name: "consent-test", Namespace: "tenant"},
		Spec: application.UserConsentScopesSpec{
			ForProvider:  application.UserConsentScopesParameters{},
			InitProvider: application.UserConsentScopesInitParameters{ApplicationID: str("app"), UserScopes: []*string{str("profile"), str("email")}},
		},
	}
	item.SetManagementPolicies(xpv2.ManagementPolicies{xpv2.ManagementActionObserve, xpv2.ManagementActionCreate, xpv2.ManagementActionUpdate, xpv2.ManagementActionDelete})
	if lateInit {
		item.SetManagementPolicies(append(item.GetManagementPolicies(), xpv2.ManagementActionLateInitialize))
	}
	item.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: "ProviderConfig"})
	if err := kube.Create(ctx, item); err != nil {
		t.Fatal(err)
	}
	key := types.NamespacedName{Name: item.Name, Namespace: item.Namespace}
	eventually(t, "initial POST failure", func() bool { mu.Lock(); defer mu.Unlock(); return posts > 0 })
	// Let the controller save the deterministic identity, then recover the failed mutation.
	eventually(t, "partial external name recovery", func() bool { return kube.Get(ctx, key, item) == nil && meta.GetExternalName(item) == "app" })
	mu.Lock()
	fail = false
	mu.Unlock()
	eventually(t, "permission recovery", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.Contains(scopes, "profile") && slices.Contains(scopes, "email")
	})
	eventually(t, "consent Ready", func() bool {
		return kube.Get(ctx, key, item) == nil && item.GetCondition(xpv2.TypeReady).Status == corev1.ConditionTrue && item.GetCondition(xpv2.TypeSynced).Status == corev1.ConditionTrue
	})
	if item.Spec.ForProvider.UserScopes != nil {
		t.Fatal("late initialization promoted observed grants over initProvider defaults")
	}
	if item.GetAnnotations()[consentscopes.InitializedAnnotation] != "app" {
		t.Fatal("resource reported Ready before initialization completion was persisted")
	}
	if item.Spec.ForProvider.ApplicationID != nil {
		t.Fatal("initial parent was promoted into a continuously managed field")
	}
	t.Log("Init-only grants recovered after Create failure and resource Ready")
	assertNoWrites := func(stage string, want ...string) {
		t.Helper()
		mu.Lock()
		previousReads, previousWrites := reads, writes
		mu.Unlock()
		eventually(t, stage, func() bool { mu.Lock(); defer mu.Unlock(); return reads >= previousReads+2 })
		mu.Lock()
		actual, currentWrites := slices.Clone(scopes), writes
		mu.Unlock()
		slices.Sort(actual)
		slices.Sort(want)
		if currentWrites != previousWrites || !slices.Equal(actual, want) {
			t.Fatalf("%s: remote scopes=%v, want %v; mutation attempts=%d, want %d", stage, actual, want, currentWrites, previousWrites)
		}
	}
	if err := kube.Patch(ctx, item, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"initProvider":{"applicationId":null}}}`))); err != nil {
		t.Fatal(err)
	}
	assertNoWrites("removing the initial parent preserves its completed binding", "profile", "email")
	eventually(t, "initial parent removal reconciled", func() bool {
		return kube.Get(ctx, key, item) == nil && item.GetCondition(xpv2.TypeSynced).Status == corev1.ConditionTrue &&
			item.GetCondition(xpv2.TypeSynced).ObservedGeneration == item.Generation
	})
	if item.Spec.ForProvider.ApplicationID != nil || item.GetAnnotations()[consentscopes.InitializedAnnotation] != "app" {
		t.Fatal("removing the initial parent promoted it into continuous management", item.Spec.ForProvider.ApplicationID, item.GetAnnotations())
	}
	if err := kube.Patch(ctx, item, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"initProvider":{"applicationId":"other","userScopes":["phone"]}}}`))); err != nil {
		t.Fatal(err)
	}
	assertNoWrites("initProvider edits after creation are ignored", "profile", "email")
	mu.Lock()
	readsBeforeRecovery, writesBeforeRecovery := reads, writes
	missNextRead = true
	mu.Unlock()
	eventually(t, "parent reappeared during recreation preparation", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return reads >= readsBeforeRecovery+4
	})
	if err := kube.Get(ctx, key, item); err != nil {
		t.Fatal(err)
	}
	if item.GetAnnotations()[consentscopes.InitializedAnnotation] != "app" {
		t.Fatal("transient missing parent discarded completed initialization")
	}
	mu.Lock()
	changedDuringRecovery := writes != writesBeforeRecovery || !slices.Contains(scopes, "profile") || !slices.Contains(scopes, "email")
	mu.Unlock()
	if changedDuringRecovery {
		t.Fatal("reappeared parent received initialization grants again")
	}
	if err := kube.Patch(ctx, item, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"initProvider":{"applicationIdRef":{"name":"missing-parent","policy":{"resolve":"Always"}}}}}`))); err != nil {
		t.Fatal(err)
	}
	assertNoWrites("unused initial reference does not block observation", "profile", "email")
	stop()
	mu.Lock()
	scopes = []string{"phone", "email"}
	mu.Unlock()
	startProvider(t, configPath, caPath, dir)
	assertNoWrites("initProvider grants remain unmanaged after restart", "phone", "email")
	// Only forProvider opts back into continuous permission management.
	if err := kube.Patch(ctx, item, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"forProvider":{"userScopes":["profile","email"]}}}`))); err != nil {
		t.Fatal(err)
	}
	eventually(t, "restart and drift repair", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(scopes) == 2 && slices.Contains(scopes, "profile") && slices.Contains(scopes, "email")
	})
	t.Log("Restart preserved initialization; forProvider resumed permission management")
	if err := kube.Get(ctx, key, item); err != nil {
		t.Fatal(err)
	}
	item.Spec.ForProvider.UserScopes = []*string{}
	if err := kube.Update(ctx, item); err != nil {
		t.Fatal(err)
	}
	eventually(t, "explicit empty scope revoke", func() bool { mu.Lock(); defer mu.Unlock(); return len(scopes) == 0 })
	if err := kube.Get(ctx, key, item); err != nil {
		t.Fatal(err)
	}
	item.Spec.ForProvider.UserScopes = []*string{str("email")}
	if err := kube.Update(ctx, item); err != nil {
		t.Fatal(err)
	}
	eventually(t, "grant after explicit clear", func() bool { mu.Lock(); defer mu.Unlock(); return len(scopes) == 1 && scopes[0] == "email" })
	{
		// Recreating a missing parent starts a new initialization with the
		// current defaults; its previous initialized identity must not leak.
		replacement := &application.Application{ObjectMeta: metav1.ObjectMeta{Name: "replacement-parent", Namespace: "tenant"}}
		meta.SetExternalName(replacement, "other")
		replacement.SetManagementPolicies(xpv2.ManagementPolicies{xpv2.ManagementActionObserve})
		replacement.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: "ProviderConfig"})
		if err := kube.Create(ctx, replacement); err != nil {
			t.Fatal(err)
		}
		if err := kube.Patch(ctx, item, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"initProvider":{"applicationId":"stale","applicationIdRef":{"name":"replacement-parent","policy":{"resolve":"Always"}}}}}`))); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		parent, scopes = "other", []string{}
		mu.Unlock()
		eventually(t, "recreate with current initial parent", func() bool {
			if kube.Get(ctx, key, item) != nil || meta.GetExternalName(item) != "other" || item.GetAnnotations()[consentscopes.InitializedAnnotation] != "other" {
				return false
			}
			mu.Lock()
			defer mu.Unlock()
			return len(scopes) == 1 && scopes[0] == "email"
		})
	}
	if err := kube.Get(ctx, key, item); err != nil {
		t.Fatal(err)
	}
	if err := kube.Delete(ctx, item); err != nil {
		t.Fatal(err)
	}
	eventually(t, "finalizer removal", func() bool { return apierrors.IsNotFound(kube.Get(ctx, key, &application.UserConsentScopes{})) })
	mu.Lock()
	remaining := slices.Clone(scopes)
	mu.Unlock()
	if len(remaining) != 0 {
		t.Fatal("deletion left granted user scopes", remaining)
	}
	t.Log("Explicit [] overrides initProvider defaults; deletion revokes grants and removes the finalizer")
}

func TestControllerUserConsentScopesAdoption(t *testing.T) {
	kube, configPath, dir := newControlPlane(t)
	ctx := context.Background()
	var mu sync.Mutex
	scopes := map[string][]string{
		"adopted-reference": {"profile", "email"},
		"adopted-selector":  {"profile", "email"},
	}
	reads, writes := map[string]int{}, map[string]int{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/oidc/token" {
			json.NewEncoder(w).Encode(map[string]any{"access_token": "adoption-test", "token_type": "Bearer", "expires_in": 3600})
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/applications/"), "/")
		if len(parts) < 2 || parts[1] != "user-consent-scopes" {
			w.WriteHeader(404)
			return
		}
		id := parts[0]
		if _, exists := scopes[id]; !exists {
			w.WriteHeader(404)
			return
		}
		switch {
		case r.Method == "GET" && len(parts) == 2:
			reads[id]++
			json.NewEncoder(w).Encode(map[string]any{"userScopes": scopes[id]})
		case r.Method == "DELETE" && len(parts) == 4 && parts[2] == "user-scopes":
			writes[id]++
			scopes[id] = slices.DeleteFunc(scopes[id], func(scope string) bool { return scope == parts[3] })
			w.WriteHeader(204)
		case r.Method == "POST":
			writes[id]++
			w.WriteHeader(403)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	caPath := filepath.Join(dir, "adoption-ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	credentials, _ := json.Marshal(map[string]string{"hostname": strings.TrimPrefix(server.URL, "https://"), "application_id": "adoption-test", "application_secret": "adoption-test", "resource": "https://default.logto.app/api"})
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "management", Namespace: "tenant"}, Data: map[string][]byte{"credentials": credentials}}
	if err := kube.Create(ctx, secret); err != nil {
		t.Fatal(err)
	}
	pc := &pcapi.ProviderConfig{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "tenant"}, Spec: pcapi.ProviderConfigSpec{Credentials: pcapi.ProviderCredentials{Source: xpv2.CredentialsSourceSecret, CommonCredentialSelectors: xpv2.CommonCredentialSelectors{SecretRef: &xpv2.SecretKeySelector{SecretReference: xpv2.SecretReference{Name: "management", Namespace: "tenant"}, Key: "credentials"}}}}}
	if err := kube.Create(ctx, pc); err != nil {
		t.Fatal(err)
	}
	startProvider(t, configPath, caPath, dir)
	for _, selector := range []bool{false, true} {
		name := "adopted-reference"
		if selector {
			name = "adopted-selector"
		}
		t.Run(name, func(t *testing.T) {
			always := xpv2.ResolvePolicyAlways
			initial := application.UserConsentScopesInitParameters{UserScopes: []*string{str("phone")}}
			if selector {
				initial.ApplicationIDSelector = &xpv2.NamespacedSelector{MatchLabels: map[string]string{"parent": "missing"}, Policy: &xpv2.Policy{Resolve: &always}}
			} else {
				initial.ApplicationIDRef = &xpv2.NamespacedReference{Name: "missing-parent", Policy: &xpv2.Policy{Resolve: &always}}
			}
			item := &application.UserConsentScopes{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "tenant"},
				Spec:       application.UserConsentScopesSpec{InitProvider: initial},
			}
			meta.SetExternalName(item, name)
			item.SetManagementPolicies(xpv2.ManagementPolicies{xpv2.ManagementActionObserve})
			item.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: "ProviderConfig"})
			if err := kube.Create(ctx, item); err != nil {
				t.Fatal(err)
			}
			key := client.ObjectKeyFromObject(item)
			eventually(t, "adopt without resolving obsolete initial parent", func() bool {
				if kube.Get(ctx, key, item) != nil || item.GetAnnotations()[consentscopes.InitializedAnnotation] != name ||
					item.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue || item.GetCondition(xpv2.TypeSynced).Status != corev1.ConditionTrue {
					return false
				}
				mu.Lock()
				defer mu.Unlock()
				return reads[name] >= 2
			})
			mu.Lock()
			unchanged := writes[name] == 0 && slices.Equal(scopes[name], []string{"profile", "email"})
			mu.Unlock()
			if !unchanged {
				t.Fatal("adoption applied initialization grants")
			}
			// The obsolete reference remains while explicit scopes take control.
			if err := kube.Patch(ctx, item, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"managementPolicies":["Observe","Update","LateInitialize"],"forProvider":{"userScopes":[]}}}`))); err != nil {
				t.Fatal(err)
			}
			eventually(t, "explicit empty scopes revoke adopted grants", func() bool {
				if kube.Get(ctx, key, item) != nil || item.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue || item.GetCondition(xpv2.TypeSynced).Status != corev1.ConditionTrue {
					return false
				}
				mu.Lock()
				defer mu.Unlock()
				return len(scopes[name]) == 0 && writes[name] == 2
			})
			if item.Spec.ForProvider.ApplicationID != nil {
				t.Fatal("adoption promoted an unresolved initial parent reference into forProvider")
			}
		})
	}
}
