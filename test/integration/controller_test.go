//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	apis "github.com/the-ccsn/provider-upjet-logto/apis/namespaced"
	application "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/application/v1alpha1"
	pcapi "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// This launches the shipped executable against an isolated real API server.
// It never uses an existing kubeconfig or cluster, even if one is configured.
func TestControllerLifecycle(t *testing.T) {
	kube, configPath, dir := newControlPlane(t)
	ctx := context.Background()

	var mu sync.Mutex
	var entity map[string]any
	creates, writes := 0, 0
	namedSecrets := []map[string]string{}
	secretCreates := 0
	unauthorized := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/oidc/token" {
			json.NewEncoder(w).Encode(map[string]any{"access_token": "test-token", "token_type": "Bearer", "expires_in": 3600})
			return
		}
		if unauthorized {
			w.WriteHeader(403)
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/applications":
			if err := json.NewDecoder(r.Body).Decode(&entity); err != nil {
				w.WriteHeader(400)
				return
			}
			creates++
			writes++
			entity["id"] = "remote-id"
			entity["tenantId"] = "default"
			entity["isAdmin"] = false
			if _, ok := entity["description"]; !ok {
				entity["description"] = ""
			}
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(entity)
		case r.Method == "GET" && r.URL.Path == "/api/applications/remote-id":
			if entity == nil {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(entity)
		case r.Method == "GET" && r.URL.Path == "/api/applications/remote-id/secrets":
			json.NewEncoder(w).Encode(namedSecrets)
		case r.Method == "POST" && r.URL.Path == "/api/applications/remote-id/secrets":
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				w.WriteHeader(400)
				return
			}
			created := map[string]string{"name": request["name"], "value": "integration-only-secret"}
			namedSecrets = append(namedSecrets, created)
			secretCreates++
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(created)
		case r.Method == "PATCH" && r.URL.Path == "/api/applications/remote-id":
			writes++
			var patch map[string]any
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				w.WriteHeader(400)
				return
			}
			for key, value := range patch {
				entity[key] = value
			}
			json.NewEncoder(w).Encode(entity)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	credentials, _ := json.Marshal(map[string]string{"hostname": strings.TrimPrefix(server.URL, "https://"), "application_id": "manager", "application_secret": "test-only", "resource": "https://default.logto.app/api"})
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "management", Namespace: "tenant"}, Data: map[string][]byte{"credentials": credentials}}
	if err := kube.Create(ctx, secret); err != nil {
		t.Fatal(err)
	}
	pc := &pcapi.ProviderConfig{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "tenant"}, Spec: pcapi.ProviderConfigSpec{Credentials: pcapi.ProviderCredentials{Source: xpv2.CredentialsSourceSecret, CommonCredentialSelectors: xpv2.CommonCredentialSelectors{SecretRef: &xpv2.SecretKeySelector{SecretReference: xpv2.SecretReference{Name: "management", Namespace: "tenant"}, Key: "credentials"}}}}}
	if err := kube.Create(ctx, pc); err != nil {
		t.Fatal(err)
	}
	start := func() func() { return startProvider(t, configPath, caPath, dir) }
	stop := start()
	app := &application.Application{ObjectMeta: metav1.ObjectMeta{Name: "managed", Namespace: "tenant"}, Spec: application.ApplicationSpec{ForProvider: application.ApplicationParameters{Name: str("desired"), Type: str("Traditional")}}}
	app.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: "ProviderConfig"})
	app.SetWriteConnectionSecretToReference(&xpv2.LocalSecretReference{Name: "connection"})
	if err := kube.Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	key := types.NamespacedName{Name: app.Name, Namespace: app.Namespace}
	eventually(t, "create and connection secret", func() bool {
		if kube.Get(ctx, key, app) != nil || meta.GetExternalName(app) != "remote-id" || app.Status.AtProvider.ID == nil {
			return false
		}
		connection := &corev1.Secret{}
		return kube.Get(ctx, types.NamespacedName{Name: "connection", Namespace: "tenant"}, connection) == nil && string(connection.Data["clientId"]) == "remote-id"
	})
	named := &application.Secret{ObjectMeta: metav1.ObjectMeta{Name: "named", Namespace: "tenant"}, Spec: application.SecretSpec{ForProvider: application.SecretParameters{Name: str("gitops"), ApplicationIDRef: &xpv2.NamespacedReference{Name: "managed"}}}}
	named.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: "ProviderConfig"})
	named.SetWriteConnectionSecretToReference(&xpv2.LocalSecretReference{Name: "named-connection"})
	if err := kube.Create(ctx, named); err != nil {
		t.Fatal(err)
	}
	namedKey := types.NamespacedName{Name: "named", Namespace: "tenant"}
	eventually(t, "resolve application reference and publish secret", func() bool {
		if kube.Get(ctx, namedKey, named) != nil || meta.GetExternalName(named) != "remote-id/gitops" {
			return false
		}
		connection := &corev1.Secret{}
		return kube.Get(ctx, types.NamespacedName{Name: "named-connection", Namespace: "tenant"}, connection) == nil && string(connection.Data["clientSecret"]) == "integration-only-secret"
	})
	serialized, err := json.Marshal(named.Status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), "integration-only-secret") {
		t.Fatal("secret exposed in resource status")
	}
	mu.Lock()
	entity["name"] = "drift"
	mu.Unlock()
	eventually(t, "repair remote drift", func() bool { mu.Lock(); defer mu.Unlock(); return entity["name"] == "desired" })
	stop()
	mu.Lock()
	entity["name"] = "after-restart"
	mu.Unlock()
	start()
	eventually(t, "restore state after restart", func() bool { mu.Lock(); defer mu.Unlock(); return entity["name"] == "desired" })
	if err := kube.Get(ctx, key, app); err != nil {
		t.Fatal(err)
	}
	app.SetManagementPolicies(xpv2.ManagementPolicies{xpv2.ManagementActionObserve})
	if err := kube.Update(ctx, app); err != nil {
		t.Fatal(err)
	}
	// Wait for the spec update to enter the controller cache before injecting drift.
	time.Sleep(2 * time.Second)
	mu.Lock()
	entity["name"] = "observe-only"
	before := writes
	mu.Unlock()
	time.Sleep(3 * time.Second)
	mu.Lock()
	if writes != before || entity["name"] != "observe-only" {
		t.Error("Observe policy performed a write")
	}
	unauthorized = true
	mu.Unlock()
	eventually(t, "permission failures surface without losing identity", func() bool {
		if kube.Get(ctx, key, app) != nil {
			return false
		}
		condition := app.GetCondition(xpv2.TypeSynced)
		return condition.Status == corev1.ConditionFalse && meta.GetExternalName(app) == "remote-id"
	})
	mu.Lock()
	defer mu.Unlock()
	if secretCreates != 1 {
		t.Fatalf("created %d named secrets across restart", secretCreates)
	}
	if creates != 1 {
		t.Fatalf("created %d applications; expected exactly one across restart", creates)
	}
}

func str(s string) *string { return &s }
func eventually(t *testing.T, stage string, fn func() bool) {
	t.Helper()
	// Dependency failures use Crossplane's exponential backoff capped at 60s.
	// Allow more than one capped retry on slower CI workers.
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", stage)
}

func newControlPlane(t *testing.T) (client.Client, string, string) {
	t.Helper()
	useExisting := false
	environment := &envtest.Environment{UseExistingCluster: &useExisting, CRDDirectoryPaths: []string{"../../package/crds"}, ErrorIfCRDPathMissing: true}
	cfg, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Error(err)
		}
	})
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := apis.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kube, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := kube.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant"}}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	kubeconfig := clientcmdapi.Config{
		Clusters:  map[string]*clientcmdapi.Cluster{"local": {Server: cfg.Host, CertificateAuthorityData: cfg.CAData}},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{"local": {ClientCertificateData: cfg.CertData, ClientKeyData: cfg.KeyData}},
		Contexts:  map[string]*clientcmdapi.Context{"local": {Cluster: "local", AuthInfo: "local"}}, CurrentContext: "local",
	}
	configPath := filepath.Join(dir, "kubeconfig")
	if err := clientcmd.WriteToFile(kubeconfig, configPath); err != nil {
		t.Fatal(err)
	}

	return kube, configPath, dir
}

func startProvider(t *testing.T, configPath, caPath, dir string) func() {
	t.Helper()
	binary, err := filepath.Abs("../../.work/bin/provider")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	healthAddress := listener.Addr().String()
	listener.Close()
	process := exec.Command(binary, "--poll=1s", "--sync=10s", "--metrics-bind-address=0", "--health-bind-address="+healthAddress, "--certs-dir=", "--debug")
	process.Env = append(os.Environ(), "KUBECONFIG="+configPath, "SSL_CERT_FILE="+caPath)
	log, err := os.Create(filepath.Join(dir, fmt.Sprintf("provider-%d.log", time.Now().UnixNano())))
	if err != nil {
		t.Fatal(err)
	}
	process.Stdout = log
	process.Stderr = log
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait(); log.Close() }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			process.Process.Signal(os.Interrupt)
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				process.Process.Kill()
				<-done
			}
		})
	}
	t.Cleanup(stop)
	probeClient := &http.Client{Timeout: 3 * time.Second}
	eventually(t, "runtime health and readiness probes", func() bool {
		for _, path := range []string{"/healthz", "/readyz"} {
			response, err := probeClient.Get("http://" + healthAddress + path)
			if err != nil {
				return false
			}
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				return false
			}
		}
		return true
	})
	return stop
}
