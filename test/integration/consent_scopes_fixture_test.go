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

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	application "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/application/v1alpha1"
	pcapi "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/v1beta1"
	"github.com/the-ccsn/provider-upjet-logto/internal/consentscopes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type consentScopesTestAPI struct {
	mu     sync.Mutex
	scopes []string
	reads  int

	// Hooks run without mu held so tests can block requests while editing state.
	readStatus  func() int
	beforeRead  func()
	beforeWrite func() bool
	writeStatus func() int
}

func (a *consentScopesTestAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/oidc/token" {
		json.NewEncoder(w).Encode(map[string]any{"access_token": "concurrency-test", "token_type": "Bearer", "expires_in": 3600})
		return
	}
	const base = "/api/applications/app/user-consent-scopes"
	switch {
	case r.Method == "GET" && r.URL.Path == base:
		a.mu.Lock()
		a.reads++
		a.mu.Unlock()
		if a.beforeRead != nil {
			a.beforeRead()
		}
		if a.readStatus != nil {
			if status := a.readStatus(); status != http.StatusOK {
				w.WriteHeader(status)
				return
			}
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"userScopes": a.scopes})
	case r.Method == "POST" && r.URL.Path == base:
		if a.beforeWrite != nil && !a.beforeWrite() {
			w.WriteHeader(403)
			return
		}
		var body struct {
			UserScopes []string `json:"userScopes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(400)
			return
		}
		a.mu.Lock()
		a.scopes = append(a.scopes, body.UserScopes...)
		a.mu.Unlock()
		if a.writeStatus != nil {
			w.WriteHeader(a.writeStatus())
			return
		}
		w.WriteHeader(201)
	case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, base+"/user-scopes/"):
		scope := strings.TrimPrefix(r.URL.Path, base+"/user-scopes/")
		a.mu.Lock()
		a.scopes = slices.DeleteFunc(a.scopes, func(s string) bool { return s == scope })
		a.mu.Unlock()
		w.WriteHeader(204)
	default:
		w.WriteHeader(404)
	}
}

func startConsentScopesTestProvider(t *testing.T, api *consentScopesTestAPI) client.Client {
	t.Helper()
	kube, configPath, dir := newControlPlane(t)
	server := httptest.NewTLSServer(api)
	t.Cleanup(server.Close)
	caPath := filepath.Join(dir, "consent-scopes-ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	creds, err := json.Marshal(map[string]string{"hostname": strings.TrimPrefix(server.URL, "https://"), "application_id": "concurrency-test", "application_secret": "concurrency-test", "resource": "https://default.logto.app/api"})
	if err != nil {
		t.Fatal(err)
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "management", Namespace: "tenant"}, Data: map[string][]byte{"credentials": creds}}
	if err := kube.Create(context.Background(), secret); err != nil {
		t.Fatal(err)
	}
	pc := &pcapi.ProviderConfig{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "tenant"}, Spec: pcapi.ProviderConfigSpec{Credentials: pcapi.ProviderCredentials{Source: xpv2.CredentialsSourceSecret, CommonCredentialSelectors: xpv2.CommonCredentialSelectors{SecretRef: &xpv2.SecretKeySelector{SecretReference: xpv2.SecretReference{Name: "management", Namespace: "tenant"}, Key: "credentials"}}}}}
	if err := kube.Create(context.Background(), pc); err != nil {
		t.Fatal(err)
	}
	startProvider(t, configPath, caPath, dir)
	return kube
}

func (a *consentScopesTestAPI) assertConverges(t *testing.T, kube client.Client, item *application.UserConsentScopes, desired []string, desiredSpec func() []*string) {
	t.Helper()
	key := client.ObjectKeyFromObject(item)
	assertDesiredScopes := func() {
		t.Helper()
		actual := desiredSpec()
		if actual == nil || !slices.EqualFunc(actual, desired, func(scope *string, want string) bool { return scope != nil && *scope == want }) {
			encoded, _ := json.Marshal(actual)
			t.Fatalf("controller overwrote concurrent GitOps scope change: got %s, want %v", encoded, desired)
		}
	}
	eventually(t, "concurrent desired scopes converge", func() bool {
		if kube.Get(context.Background(), key, item) != nil {
			return false
		}
		assertDesiredScopes()
		a.mu.Lock()
		matches := slices.Equal(a.scopes, desired)
		a.mu.Unlock()
		return matches && item.GetAnnotations()[consentscopes.InitializedAnnotation] == "app" &&
			item.GetCondition(xpv2.TypeReady).Status == corev1.ConditionTrue && item.GetCondition(xpv2.TypeSynced).Status == corev1.ConditionTrue
	})
	a.mu.Lock()
	previousReads := a.reads
	a.mu.Unlock()
	eventually(t, "subsequent reconciles preserve concurrent change", func() bool {
		if kube.Get(context.Background(), key, item) != nil {
			return false
		}
		assertDesiredScopes()
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.reads >= previousReads+2 && slices.Equal(a.scopes, desired)
	})
}
