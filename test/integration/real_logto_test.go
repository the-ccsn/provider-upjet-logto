//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	logto "github.com/Lenstra/terraform-provider-logto/client"
	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	api "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/api/v1alpha1"
	application "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/application/v1alpha1"
	role "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/role/v1alpha1"
	user "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/user/v1alpha1"
	pcapi "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestRealControllerGraph(t *testing.T) {
	credentialPath := os.Getenv("LOGTO_TEST_CREDENTIALS")
	if credentialPath == "" {
		t.Skip("set LOGTO_TEST_CREDENTIALS to enable disposable real Logto acceptance")
	}
	data, err := os.ReadFile(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	var credentials map[string]string
	if json.Unmarshal(data, &credentials) != nil {
		t.Fatal("invalid credentials JSON")
	}
	endpoint, err := url.Parse(credentials["endpoint"])
	if err != nil || net.ParseIP(endpoint.Hostname()) == nil || !net.ParseIP(endpoint.Hostname()).IsLoopback() {
		t.Fatal("real acceptance requires an explicit loopback IP")
	}
	remote, err := logto.NewClient(&logto.Config{Endpoint: endpoint.String(), Hostname: credentials["hostname"], ApplicationID: credentials["application_id"], ApplicationSecret: credentials["application_secret"], Resource: credentials["resource"]})
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(endpoint))
	t.Cleanup(proxy.Close)
	kube, configPath, dir := newControlPlane(t)
	caPath := filepath.Join(dir, "logto-ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	kubeCredentials := map[string]string{}
	for key, value := range credentials {
		kubeCredentials[key] = value
	}
	delete(kubeCredentials, "endpoint")
	kubeCredentials["hostname"] = strings.TrimPrefix(proxy.URL, "https://")
	encoded, _ := json.Marshal(kubeCredentials)
	ctx := context.Background()
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "management", Namespace: "tenant"}, Data: map[string][]byte{"credentials": encoded}}
	if err := kube.Create(ctx, secret); err != nil {
		t.Fatal(err)
	}
	pc := &pcapi.ProviderConfig{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "tenant"}, Spec: pcapi.ProviderConfigSpec{Credentials: pcapi.ProviderCredentials{Source: xpv2.CredentialsSourceSecret, CommonCredentialSelectors: xpv2.CommonCredentialSelectors{SecretRef: &xpv2.SecretKeySelector{SecretReference: xpv2.SecretReference{Name: "management", Namespace: "tenant"}, Key: "credentials"}}}}}
	if err := kube.Create(ctx, pc); err != nil {
		t.Fatal(err)
	}
	stop := startProvider(t, configPath, caPath, dir)
	prefix := "graph_" + time.Now().Format("150405")
	objectMeta := func(name string) metav1.ObjectMeta { return metav1.ObjectMeta{Name: name, Namespace: "tenant"} }
	app := &application.Application{ObjectMeta: objectMeta("application"), Spec: application.ApplicationSpec{ForProvider: application.ApplicationParameters{Name: str(prefix), Type: str("Traditional")}}}
	named := &application.Secret{ObjectMeta: objectMeta("named"), Spec: application.SecretSpec{ForProvider: application.SecretParameters{Name: str(prefix), ApplicationIDRef: &xpv2.NamespacedReference{Name: "application"}}}}
	resource := &api.Resource{ObjectMeta: objectMeta("resource"), Spec: api.ResourceSpec{ForProvider: api.ResourceParameters{Name: str(prefix), Indicator: str("https://" + prefix + ".example.invalid")}}}
	scope := &api.Scope{ObjectMeta: objectMeta("scope"), Spec: api.ScopeSpec{ForProvider: api.ScopeParameters{Name: str("read"), ResourceIDRef: &xpv2.NamespacedReference{Name: "resource"}}}}
	grant := &role.Role{ObjectMeta: objectMeta("role"), Spec: role.RoleSpec{ForProvider: role.RoleParameters{Name: str(prefix), Description: str("Acceptance role"), Type: str("User"), ScopeIdsRefs: []xpv2.NamespacedReference{{Name: "scope"}}}}}
	account := &user.User{ObjectMeta: objectMeta("user"), Spec: user.UserSpec{ForProvider: user.UserParameters{Username: str(prefix), Name: str(prefix), RoleIdsRefs: []xpv2.NamespacedReference{{Name: "role"}}}}}
	managed := []interface {
		client.Object
		SetProviderConfigReference(*xpv2.ProviderConfigReference)
		GetCondition(xpv2.ConditionType) xpv2.Condition
	}{app, named, resource, scope, grant, account}
	ids := map[string]string{}
	t.Cleanup(func() {
		stop()
		// Capture IDs even if a dependency never became ready, before deleting owned objects.
		for _, object := range managed {
			if kube.Get(ctx, client.ObjectKeyFromObject(object), object) == nil {
				ids[object.GetName()] = meta.GetExternalName(object)
				if t.Failed() {
					condition := object.GetCondition(xpv2.TypeSynced)
					t.Logf("resource %s: externalName=%q Synced=%s reason=%s message=%s", object.GetName(), ids[object.GetName()], condition.Status, condition.Reason, condition.Message)
				}
			}
		}
		for _, owned := range []struct {
			name   string
			remove func(context.Context, string) error
		}{{"user", remote.UserDelete}, {"role", remote.RoleDelete}, {"resource", remote.ApiResourceDelete}, {"application", remote.ApplicationDelete}} {
			if id := ids[owned.name]; id != "" {
				if err := owned.remove(ctx, id); err != nil {
					t.Error(err)
				}
			}
		}
	})
	// Create the dependency graph together; references must wait for their parents.
	for _, object := range managed {
		object.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: "ProviderConfig"})
		if err := kube.Create(ctx, object); err != nil {
			t.Fatal(err)
		}
	}
	named.SetWriteConnectionSecretToReference(&xpv2.LocalSecretReference{Name: "named-connection"})
	if err := kube.Update(ctx, named); err != nil {
		t.Fatal(err)
	}
	for _, object := range managed {
		eventually(t, "real graph ready: "+object.GetName(), func() bool {
			if kube.Get(ctx, client.ObjectKeyFromObject(object), object) != nil {
				return false
			}
			return object.GetCondition(xpv2.TypeReady).Status == corev1.ConditionTrue && object.GetCondition(xpv2.TypeSynced).Status == corev1.ConditionTrue && meta.GetExternalName(object) != ""
		})
		ids[object.GetName()] = meta.GetExternalName(object)
	}
	connection := &corev1.Secret{}
	eventually(t, "real named secret connection", func() bool {
		return kube.Get(ctx, types.NamespacedName{Name: "named-connection", Namespace: "tenant"}, connection) == nil && len(connection.Data["clientSecret"]) > 0
	})
	serialized, _ := json.Marshal(named.Status)
	if strings.Contains(string(serialized), string(connection.Data["clientSecret"])) {
		t.Fatal("real secret leaked into status")
	}
	adopted := &application.Application{ObjectMeta: objectMeta("adopted")}
	adopted.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: "ProviderConfig"})
	adopted.SetManagementPolicies(xpv2.ManagementPolicies{xpv2.ManagementActionObserve})
	meta.SetExternalName(adopted, ids["application"])
	if err := kube.Create(ctx, adopted); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Observe adoption with no creation parameters", func() bool {
		return kube.Get(ctx, client.ObjectKeyFromObject(adopted), adopted) == nil && adopted.GetCondition(xpv2.TypeSynced).Status == corev1.ConditionTrue && meta.GetExternalName(adopted) == ids["application"]
	})
	legacyPC := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "logto.crossplane.io/v1beta1", "kind": "ProviderConfig", "metadata": map[string]any{"name": "legacy"},
		"spec": map[string]any{"credentials": map[string]any{"source": "Secret", "secretRef": map[string]any{"namespace": "tenant", "name": "management", "key": "credentials"}}},
	}}
	if err := kube.Create(ctx, legacyPC); err != nil {
		t.Fatal(err)
	}
	legacy := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "application.logto.crossplane.io/v1alpha1", "kind": "Application", "metadata": map[string]any{"name": "legacy"},
		"spec": map[string]any{"providerConfigRef": map[string]any{"name": "legacy"}, "forProvider": map[string]any{"name": prefix + "_legacy", "type": "SPA"}},
	}}
	if err := kube.Create(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	var legacyID string
	eventually(t, "legacy cluster-scoped reconcile", func() bool {
		if kube.Get(ctx, client.ObjectKeyFromObject(legacy), legacy) != nil {
			return false
		}
		legacyID, _, _ = unstructured.NestedString(legacy.Object, "status", "atProvider", "id")
		conditions, _, _ := unstructured.NestedSlice(legacy.Object, "status", "conditions")
		for _, raw := range conditions {
			condition := raw.(map[string]any)
			if condition["type"] == "Synced" && condition["status"] == "True" {
				return legacyID != ""
			}
		}
		return false
	})
	t.Cleanup(func() {
		if err := remote.ApplicationDelete(ctx, legacyID); err != nil {
			t.Error(err)
		}
	})
	observed, err := remote.ApplicationGet(ctx, ids["application"])
	if err != nil {
		t.Fatal(err)
	}
	observed.Name = "drifted"
	if _, err := remote.ApplicationUpdate(ctx, observed); err != nil {
		t.Fatal(err)
	}
	eventually(t, "real API drift correction", func() bool {
		current, err := remote.ApplicationGet(ctx, ids["application"])
		return err == nil && current.Name == prefix
	})
	stop()
	stop = startProvider(t, configPath, caPath, dir)
	for _, object := range managed {
		eventually(t, "restart identity: "+object.GetName(), func() bool {
			return kube.Get(ctx, client.ObjectKeyFromObject(object), object) == nil && meta.GetExternalName(object) == ids[object.GetName()] && object.GetCondition(xpv2.TypeSynced).Status == corev1.ConditionTrue
		})
	}
	// Explicit [] must revoke all relationships, including after a restart.
	if err := kube.Patch(ctx, account, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"forProvider":{"roleIds":[],"roleIdsRefs":[]}}}`))); err != nil {
		t.Fatal(err)
	}
	eventually(t, "revoke all user roles", func() bool {
		roles, err := remote.GetRolesForUser(ctx, ids["user"])
		return err == nil && len(roles) == 0
	})
	if err := kube.Patch(ctx, grant, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"forProvider":{"scopeIds":[],"scopeIdsRefs":[]}}}`))); err != nil {
		t.Fatal(err)
	}
	eventually(t, "revoke all role scopes", func() bool {
		scopes, err := remote.RoleScopesGet(ctx, ids["role"])
		return err == nil && len(scopes) == 0
	})
	// Adopt an existing user using only its ID and role relation, as the GitOps
	// migration does. Transfer write ownership before adding the second object.
	if err := kube.Patch(ctx, account, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"managementPolicies":["Observe"]}}`))); err != nil {
		t.Fatal(err)
	}
	profileBefore, err := remote.UserGet(ctx, ids["user"])
	if err != nil {
		t.Fatal(err)
	}
	profileBefore.PrimaryEmail = prefix + "@example.invalid"
	profileBefore.Profile = &logto.Profile{FamilyName: "Preserve", GivenName: "Existing", Nickname: "Account"}
	if profileBefore, err = remote.UserUpdate(ctx, profileBefore); err != nil {
		t.Fatal(err)
	}
	adoptedUser := &user.User{ObjectMeta: objectMeta("adopted-user"), Spec: user.UserSpec{ForProvider: user.UserParameters{RoleIdsRefs: []xpv2.NamespacedReference{{Name: "role"}}}}}
	adoptedUser.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: "ProviderConfig"})
	adoptedUser.SetManagementPolicies(xpv2.ManagementPolicies{xpv2.ManagementActionObserve, xpv2.ManagementActionUpdate})
	meta.SetExternalName(adoptedUser, ids["user"])
	if err := kube.Create(ctx, adoptedUser); err != nil {
		t.Fatal(err)
	}
	eventually(t, "ID-only user adoption updates roles", func() bool {
		actual, err := remote.GetRolesForUser(ctx, ids["user"])
		return err == nil && len(actual) == 1 && actual[0].ID == ids["role"]
	})
	profileAfter, err := remote.UserGet(ctx, ids["user"])
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(profileBefore)
	afterJSON, _ := json.Marshal(profileAfter)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("role-only adoption changed existing user profile")
	}
}

func TestRealControllerConfigurations(t *testing.T) {
	credentialPath := os.Getenv("LOGTO_TEST_CREDENTIALS")
	if credentialPath == "" {
		t.Skip("set LOGTO_TEST_CREDENTIALS to enable disposable real Logto acceptance")
	}
	data, err := os.ReadFile(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	var credentials map[string]string
	if json.Unmarshal(data, &credentials) != nil {
		t.Fatal("invalid credentials JSON")
	}
	endpoint, err := url.Parse(credentials["endpoint"])
	if err != nil || net.ParseIP(endpoint.Hostname()) == nil || !net.ParseIP(endpoint.Hostname()).IsLoopback() {
		t.Fatal("real acceptance requires an explicit loopback IP")
	}
	remote, err := logto.NewClient(&logto.Config{Endpoint: endpoint.String(), Hostname: credentials["hostname"], ApplicationID: credentials["application_id"], ApplicationSecret: credentials["application_secret"], Resource: credentials["resource"]})
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(endpoint))
	t.Cleanup(proxy.Close)
	kube, configPath, dir := newControlPlane(t)
	caPath := filepath.Join(dir, "logto-ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	kubeCredentials := map[string]string{}
	for key, value := range credentials {
		kubeCredentials[key] = value
	}
	delete(kubeCredentials, "endpoint")
	kubeCredentials["hostname"] = strings.TrimPrefix(proxy.URL, "https://")
	encoded, _ := json.Marshal(kubeCredentials)
	ctx := context.Background()
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "management", Namespace: "tenant"}, Data: map[string][]byte{"credentials": encoded}}
	if err := kube.Create(ctx, secret); err != nil {
		t.Fatal(err)
	}
	pc := &pcapi.ProviderConfig{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "tenant"}, Spec: pcapi.ProviderConfigSpec{Credentials: pcapi.ProviderCredentials{Source: xpv2.CredentialsSourceSecret, CommonCredentialSelectors: xpv2.CommonCredentialSelectors{SecretRef: &xpv2.SecretKeySelector{SecretReference: xpv2.SecretReference{Name: "management", Namespace: "tenant"}, Key: "credentials"}}}}}
	if err := kube.Create(ctx, pc); err != nil {
		t.Fatal(err)
	}
	stop := startProvider(t, configPath, caPath, dir)
	testRealConfigurations(t, kube, remote, stop)
}
