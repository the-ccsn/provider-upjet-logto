package consentscopes_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	logtoclient "github.com/Lenstra/terraform-provider-logto/client"
	tfprovider "github.com/Lenstra/terraform-provider-logto/provider"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	fakeManager "github.com/crossplane/crossplane-runtime/v2/pkg/resource/fake"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	ujcontroller "github.com/crossplane/upjet/v2/pkg/controller"
	"github.com/crossplane/upjet/v2/pkg/resource"
	"github.com/crossplane/upjet/v2/pkg/terraform"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	clusterapplication "github.com/the-ccsn/provider-upjet-logto/apis/cluster/application/v1alpha1"
	application "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/application/v1alpha1"
	"github.com/the-ccsn/provider-upjet-logto/config"
	"github.com/the-ccsn/provider-upjet-logto/internal/consentscopes"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type identityTransport func(*http.Request) (*http.Response, error)

func (f identityTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type identityTestProvider struct {
	fwprovider.Provider
	api *logtoclient.Client
}

func (p identityTestProvider) Configure(_ context.Context, _ fwprovider.ConfigureRequest, resp *fwprovider.ConfigureResponse) {
	resp.ResourceData = p.api
}

func TestDeleteAfterFailedCreateAndRestart(t *testing.T) {
	for _, scope := range []string{"namespaced", "cluster"} {
		for _, input := range []string{"for_provider", "init_provider", "policies_disabled", "invalid_scope", "invalid_id", "policies_disabled_init_scopes", "changed_for_provider_parent", "changed_init_provider_parent", "removed_init_provider_parent"} {
			t.Run(scope+"/"+input, func(t *testing.T) {
				ctx := context.Background()
				scheme := runtime.NewScheme()
				identityMust(t, application.AddToScheme(scheme))
				identityMust(t, clusterapplication.AddToScheme(scheme))
				var item resource.Terraformed = &application.UserConsentScopes{}
				kind := xpresource.ManagedKind(application.UserConsentScopes_GroupVersionKind)
				cfg := config.GetProviderNamespaced().Resources["logto_application_user_consent_scopes"]
				if scope == "cluster" {
					item = &clusterapplication.UserConsentScopes{}
					item.(xpresource.LegacyManaged).SetDeletionPolicy(xpv2.DeletionDelete)
					kind = xpresource.ManagedKind(clusterapplication.UserConsentScopes_GroupVersionKind)
					cfg = config.GetProvider().Resources["logto_application_user_consent_scopes"]
				} else {
					item.SetNamespace("tenant")
				}
				item.SetName("restart-delete-probe")
				item.SetUID("restart-delete-probe")
				item.SetManagementPolicies(xpv2.ManagementPolicies{xpv2.ManagementActionAll})
				parameters := map[string]any{"application_id": "app", "user_scopes": []any{"email"}}
				switch input {
				case "init_provider", "changed_init_provider_parent", "removed_init_provider_parent":
					parameters = map[string]any{}
					identityMust(t, json.Unmarshal([]byte(`{"spec":{"initProvider":{"applicationId":"app","userScopes":["email"]}}}`), item))
				case "invalid_scope":
					parameters["user_scopes"] = []any{"openid"}
				case "invalid_id":
					parameters["application_id"] = "bad/app"
				case "policies_disabled_init_scopes":
					delete(parameters, "user_scopes")
					identityMust(t, json.Unmarshal([]byte(`{"spec":{"initProvider":{"userScopes":["email"]}}}`), item))
				}
				identityMust(t, item.SetParameters(parameters))
				kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(item).WithObjects(item).Build()
				key := client.ObjectKeyFromObject(item)
				grants := []string{}
				valid := input != "invalid_scope" && input != "invalid_id" && input != "policies_disabled_init_scopes"
				if !valid {
					grants = []string{"phone"}
				}
				posts, deletes, reads := 0, 0, 0
				api, err := logtoclient.NewClient(&logtoclient.Config{Hostname: "probe.invalid", ApplicationID: "synthetic", ApplicationSecret: "synthetic", HttpClient: &http.Client{Transport: identityTransport(func(r *http.Request) (*http.Response, error) {
					respond := func(status int, body any) (*http.Response, error) {
						data, err := json.Marshal(body)
						if err != nil {
							return nil, err
						}
						return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(data)))}, nil
					}
					if r.URL.Path == "/oidc/token" {
						return respond(200, map[string]any{"access_token": "synthetic", "token_type": "Bearer", "expires_in": 3600})
					}
					const base = "/api/applications/app/user-consent-scopes"
					switch {
					case r.Method == "GET" && r.URL.Path == base:
						reads++
						return respond(200, map[string]any{"userScopes": grants})
					case r.Method == "POST" && r.URL.Path == base:
						posts++
						var body struct {
							UserScopes []string `json:"userScopes"`
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							return nil, err
						}
						grants = append(grants, body.UserScopes...)
						return nil, errors.New("synthetic response lost after committed grant")
					case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, base+"/user-scopes/"):
						deletes++
						scope := strings.TrimPrefix(r.URL.Path, base+"/user-scopes/")
						grants = slices.DeleteFunc(grants, func(s string) bool { return s == scope })
						return respond(204, nil)
					default:
						t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
						return nil, nil
					}
				})}})
				identityMust(t, err)
				setup := func(context.Context, client.Client, xpresource.Managed) (terraform.Setup, error) {
					return terraform.Setup{FrameworkProvider: identityTestProvider{Provider: tfprovider.New("probe")(), api: api}}, nil
				}
				newReconciler := func() *managed.Reconciler {
					trackers := ujcontroller.NewOperationStore(logging.NewNopLogger())
					enabled := input != "policies_disabled" && input != "policies_disabled_init_scopes"
					base := ujcontroller.NewTerraformPluginFrameworkConnector(kube, setup, cfg, trackers, ujcontroller.WithTerraformPluginFrameworkLogger(logging.NewNopLogger()), ujcontroller.WithTerraformPluginFrameworkManagementPolicies(enabled))
					var initializers []managed.Initializer
					for _, f := range cfg.InitializerFns {
						initializers = append(initializers, f(kube))
					}
					opts := []managed.ReconcilerOption{managed.WithInitializers(initializers...), managed.WithExternalConnector(consentscopes.WrapConnector(kube, enabled, consentscopes.PreserveCreateIdentity(base, cfg.TerraformPluginFrameworkResource, trackers)))}
					if enabled {
						opts = append(opts, managed.WithManagementPolicies())
					}
					return managed.NewReconciler(&fakeManager.Manager{Client: kube, Scheme: scheme}, kind, opts...)
				}
				request := reconcile.Request{NamespacedName: key}
				_, err = newReconciler().Reconcile(ctx, request)
				identityMust(t, err)
				identityMust(t, kube.Get(ctx, key, item))
				t.Logf("failed create: external=%q id=%q marker=%q grants=%v posts=%d reads=%d synced=%s", meta.GetExternalName(item), item.GetID(), item.GetAnnotations()[consentscopes.InitializedAnnotation], grants, posts, reads, item.GetCondition(xpv2.TypeSynced).Status)
				if item.GetCondition(xpv2.TypeSynced).Status != corev1.ConditionFalse {
					t.Fatal("creation error was hidden")
				}
				if valid {
					if meta.GetExternalName(item) != "app" || posts != 1 || !slices.Equal(grants, []string{"email"}) {
						t.Fatal("failed committed create did not preserve identity")
					}
				} else if meta.GetExternalName(item) != "" || item.GetID() != "" || posts != 0 || reads != 0 || !slices.Equal(grants, []string{"phone"}) {
					t.Fatal("invalid creation adopted or changed an application", item.GetAnnotations(), grants)
				}
				if input == "removed_init_provider_parent" {
					identityMust(t, json.Unmarshal([]byte(`{"spec":{"initProvider":{"applicationId":null}}}`), item))
					identityMust(t, kube.Update(ctx, item))
					_, err = newReconciler().Reconcile(ctx, request)
					identityMust(t, err)
					identityMust(t, kube.Get(ctx, key, item))
					params, err := item.GetParameters()
					identityMust(t, err)
					if params["application_id"] != nil {
						t.Fatal("pending parent was promoted after its default was removed", params)
					}
					// Restoring the missing input can finish initialization after restart.
					identityMust(t, json.Unmarshal([]byte(`{"spec":{"initProvider":{"applicationId":"app"}}}`), item))
					identityMust(t, kube.Update(ctx, item))
					for i := 0; i < 2; i++ {
						_, err = newReconciler().Reconcile(ctx, request)
						identityMust(t, err)
					}
					identityMust(t, kube.Get(ctx, key, item))
					if item.GetAnnotations()[consentscopes.InitializedAnnotation] != "app" ||
						item.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue ||
						item.GetCondition(xpv2.TypeSynced).Status != corev1.ConditionTrue {
						t.Fatal("restored parent did not finish initialization", item.GetAnnotations(), item.GetCondition(xpv2.TypeSynced))
					}
					identityMust(t, json.Unmarshal([]byte(`{"spec":{"initProvider":{"applicationId":"other"}}}`), item))
					identityMust(t, kube.Update(ctx, item))
					candidate := item.DeepCopyObject().(resource.Terraformed)
					meta.AddAnnotations(candidate, map[string]string{consentscopes.InitializedAnnotation: ""})
					params, err = candidate.GetMergedParameters(true)
					identityMust(t, err)
					if params["application_id"] != "other" {
						t.Fatal("recreation ignored the current initialization parent", params)
					}
				}
				if input == "changed_for_provider_parent" || input == "changed_init_provider_parent" {
					if input == "changed_init_provider_parent" {
						identityMust(t, json.Unmarshal([]byte(`{"spec":{"initProvider":{"applicationId":"other"}}}`), item))
					} else {
						identityMust(t, item.SetParameters(map[string]any{"application_id": "other", "user_scopes": []any{"email"}}))
					}
					identityMust(t, kube.Update(ctx, item))
					_, err = newReconciler().Reconcile(ctx, request)
					identityMust(t, err)
					identityMust(t, kube.Get(ctx, key, item))
					if item.GetAnnotations()[consentscopes.InitializedAnnotation] != "" ||
						item.GetCondition(xpv2.TypeReady).Status != corev1.ConditionFalse ||
						item.GetCondition(xpv2.TypeSynced).Status != corev1.ConditionFalse ||
						!strings.Contains(item.GetCondition(xpv2.TypeSynced).Message, "require resource replacement") {
						t.Fatal("rejected parent change completed pending initialization", item.GetAnnotations(), item.GetCondition(xpv2.TypeReady), item.GetCondition(xpv2.TypeSynced))
					}
					if meta.GetExternalName(item) != "app" || posts != 1 || deletes != 0 || !slices.Equal(grants, []string{"email"}) {
						t.Fatal("rejected parent change replaced ownership or grants", item.GetAnnotations(), grants)
					}
				}
				identityMust(t, kube.Delete(ctx, item))
				r := newReconciler()
				for i := 0; i < 3; i++ {
					_, err = r.Reconcile(ctx, request)
					identityMust(t, err)
				}
				err = kube.Get(ctx, key, item)
				t.Logf("after restart and delete: objectGone=%v grants=%v deletes=%d reads=%d", apierrors.IsNotFound(err), grants, deletes, reads)
				if !apierrors.IsNotFound(err) {
					t.Fatal("failed creation was not deleted", err)
				}
				if valid {
					if len(grants) != 0 || deletes != 1 {
						t.Fatal("partial creation grants leaked after restart and deletion", grants, deletes)
					}
				} else if deletes != 0 || !slices.Equal(grants, []string{"phone"}) {
					t.Fatal("invalid creation deletion revoked existing grants", grants, deletes)
				}
			})
		}
	}
}

func identityMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
