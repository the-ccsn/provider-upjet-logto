package consentscopes_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/crossplane/upjet/v2/pkg/resource"
	clusterapplication "github.com/the-ccsn/provider-upjet-logto/apis/cluster/application/v1alpha1"
	application "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/application/v1alpha1"
	"github.com/the-ccsn/provider-upjet-logto/internal/consentscopes"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func consentFixture(t *testing.T, factory func() resource.Terraformed, forProvider, initial string) (resource.Terraformed, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := application.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := clusterapplication.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	item := factory()
	data := `{"metadata":{"name":"consent"},"spec":{"forProvider":{"applicationId":"app","userScopes":` + forProvider + `},"initProvider":{"userScopes":` + initial + `}}}`
	if err := json.Unmarshal([]byte(data), item); err != nil {
		t.Fatal(err)
	}
	meta.SetExternalName(item, "app")
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(item).Build()
	if err := kube.Get(context.Background(), client.ObjectKeyFromObject(item), item); err != nil {
		t.Fatal(err)
	}
	return item, kube
}

func TestInitializationLifecycle(t *testing.T) {
	for scope, factory := range map[string]func() resource.Terraformed{
		"namespaced": func() resource.Terraformed { return &application.UserConsentScopes{} },
		"cluster":    func() resource.Terraformed { return &clusterapplication.UserConsentScopes{} },
	} {
		for _, row := range []struct {
			name, forProvider, initial, observed string
			complete, exists, upToDate, want     bool
			wantComplete                         bool
		}{
			{"failed initial write", `null`, `["profile","email"]`, `[]`, false, true, false, false, false},
			{"partial initial write", `null`, `["profile","email"]`, `["profile"]`, false, true, false, false, false},
			{"mismatched observation", `null`, `["profile","email"]`, `[]`, false, true, true, true, false},
			{"successful initialization", `null`, `["profile","email"]`, `["email","profile"]`, false, true, true, true, true},
			{"successful empty initialization", `null`, `[]`, `[]`, false, true, true, true, true},
			{"initial defaults edited", `null`, `["phone"]`, `["email","profile"]`, true, true, false, true, true},
			{"initial defaults externally changed", `null`, `["email","profile"]`, `["phone"]`, true, true, false, true, true},
			{"explicit empty remains managed", `[]`, `["email","profile"]`, `["phone"]`, true, true, false, false, true},
			{"explicit scopes remain managed", `["email"]`, `["profile"]`, `["phone"]`, true, true, false, false, true},
			{"missing parent remains absent", `null`, `["email"]`, `[]`, true, false, false, false, true},
		} {
			t.Run(scope+"/"+row.name, func(t *testing.T) {
				item, kube := consentFixture(t, factory, row.forProvider, row.initial)
				state := ""
				if row.complete {
					state = "app"
				}
				meta.AddAnnotations(item, map[string]string{consentscopes.InitializedAnnotation: state})
				if err := kube.Update(context.Background(), item); err != nil {
					t.Fatal(err)
				}
				next := managed.ExternalConnectorFn(func(context.Context, xpresource.Managed) (managed.ExternalClient, error) {
					return managed.ExternalClientFns{ObserveFn: func(_ context.Context, mg xpresource.Managed) (managed.ExternalObservation, error) {
						if row.exists {
							mg.SetConditions(xpv2.Available())
						}
						var scopes []any
						if err := json.Unmarshal([]byte(row.observed), &scopes); err != nil {
							t.Fatal(err)
						}
						if err := mg.(resource.Terraformed).SetObservation(map[string]any{"id": "app", "application_id": "app", "user_scopes": scopes}); err != nil {
							t.Fatal(err)
						}
						return managed.ExternalObservation{ResourceExists: row.exists, ResourceUpToDate: row.upToDate}, nil
					}}, nil
				})
				external, err := consentscopes.WrapConnector(kube, true, next).Connect(context.Background(), item)
				if err != nil {
					t.Fatal(err)
				}
				got, err := external.Observe(context.Background(), item)
				if err != nil {
					t.Fatal(err)
				}
				if got.ResourceExists != row.exists || got.ResourceUpToDate != row.want {
					t.Fatalf("observation=%+v, want exists=%t upToDate=%t", got, row.exists, row.want)
				}
				if item.GetID() != "app" {
					t.Fatal("annotation persistence replaced fresh in-memory status")
				}
				if ready := item.GetCondition(xpv2.TypeReady).Status == corev1.ConditionTrue; ready != (row.exists && (row.complete || row.wantComplete)) {
					t.Fatalf("Ready=%t before initialization complete=%t", ready, row.wantComplete)
				}
				persisted := factory()
				if err := kube.Get(context.Background(), client.ObjectKeyFromObject(item), persisted); err != nil {
					t.Fatal(err)
				}
				if complete := persisted.GetAnnotations()[consentscopes.InitializedAnnotation] == "app"; complete != row.wantComplete {
					t.Fatalf("persisted initialization complete=%t, want %t", complete, row.wantComplete)
				}
			})
		}
	}
}

func TestAdoptionAndReadErrors(t *testing.T) {
	item, kube := consentFixture(t, func() resource.Terraformed { return &application.UserConsentScopes{} }, `null`, `["phone"]`)
	readError := errors.New("read forbidden")
	next := managed.ExternalConnectorFn(func(context.Context, xpresource.Managed) (managed.ExternalClient, error) {
		return managed.ExternalClientFns{ObserveFn: func(context.Context, xpresource.Managed) (managed.ExternalObservation, error) {
			return managed.ExternalObservation{}, readError
		}}, nil
	})
	external, err := consentscopes.WrapConnector(kube, true, next).Connect(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	if item.GetAnnotations()[consentscopes.InitializedAnnotation] != "app" {
		t.Fatal("adopted application acquired a new initialization target")
	}
	if _, err := external.Observe(context.Background(), item); !errors.Is(err, readError) {
		t.Fatalf("observation error=%v, want %v", err, readError)
	}
}

func TestCreateResetsInitializationBeforeMutation(t *testing.T) {
	item, kube := consentFixture(t, func() resource.Terraformed { return &application.UserConsentScopes{} }, `null`, `["email"]`)
	writeError := errors.New("assignment forbidden")
	connections, observations := 0, 0
	next := managed.ExternalConnectorFn(func(context.Context, xpresource.Managed) (managed.ExternalClient, error) {
		connections++
		return managed.ExternalClientFns{DisconnectFn: func(context.Context) error { return nil }, ObserveFn: func(_ context.Context, candidate xpresource.Managed) (managed.ExternalObservation, error) {
			observations++
			if candidate.GetAnnotations()[consentscopes.InitializedAnnotation] != "" {
				t.Fatal("new creation was planned with an initialized parent")
			}
			if item.GetAnnotations()[consentscopes.InitializedAnnotation] != "app" {
				t.Fatal("preparation changed the original resource")
			}
			return managed.ExternalObservation{}, nil
		}, CreateFn: func(context.Context, xpresource.Managed) (managed.ExternalCreation, error) {
			persisted := &application.UserConsentScopes{}
			if err := kube.Get(context.Background(), client.ObjectKeyFromObject(item), persisted); err != nil {
				t.Fatal(err)
			}
			if state, exists := persisted.GetAnnotations()[consentscopes.InitializedAnnotation]; !exists || state != "" {
				t.Fatal("creation started without a durable pending initialization marker")
			}
			return managed.ExternalCreation{}, writeError
		}}, nil
	})
	external, err := consentscopes.WrapConnector(kube, true, next).Connect(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := external.Create(context.Background(), item); !errors.Is(err, writeError) {
		t.Fatalf("creation error=%v, want %v", err, writeError)
	}
	if connections != 2 || observations != 1 {
		t.Fatal("new initialization did not prepare a fresh plan")
	}
}

func TestInitializedScopesDoNotHideParentChanges(t *testing.T) {
	item, kube := consentFixture(t, func() resource.Terraformed { return &application.UserConsentScopes{} }, `null`, `["email"]`)
	meta.AddAnnotations(item, map[string]string{consentscopes.InitializedAnnotation: "app"})
	if err := kube.Update(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if err := item.SetParameters(map[string]any{"application_id": "other"}); err != nil {
		t.Fatal(err)
	}
	next := managed.ExternalConnectorFn(func(context.Context, xpresource.Managed) (managed.ExternalClient, error) {
		return managed.ExternalClientFns{ObserveFn: func(context.Context, xpresource.Managed) (managed.ExternalObservation, error) {
			if err := item.SetObservation(map[string]any{"id": "app", "application_id": "app", "user_scopes": []any{"email"}}); err != nil {
				t.Fatal(err)
			}
			return managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: false}, nil
		}}, nil
	})
	external, err := consentscopes.WrapConnector(kube, true, next).Connect(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	o, err := external.Observe(context.Background(), item)
	if err != nil || o.ResourceUpToDate {
		t.Fatalf("parent change was hidden: observation=%+v, error=%v", o, err)
	}
}

func TestRecreationPreparationPreservesInitialization(t *testing.T) {
	readError := errors.New("read forbidden")
	for _, row := range []struct {
		name            string
		exists, connect bool
		readError       error
	}{
		{name: "parent reappeared", exists: true},
		{name: "second read failed", readError: readError},
		{name: "reconnect failed", connect: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			item, kube := consentFixture(t, func() resource.Terraformed { return &application.UserConsentScopes{} }, `null`, `["phone"]`)
			connections := 0
			next := managed.ExternalConnectorFn(func(context.Context, xpresource.Managed) (managed.ExternalClient, error) {
				connections++
				if connections > 1 && row.connect {
					return nil, errors.New("provider unavailable")
				}
				return managed.ExternalClientFns{
					DisconnectFn: func(context.Context) error { return nil },
					ObserveFn: func(context.Context, xpresource.Managed) (managed.ExternalObservation, error) {
						return managed.ExternalObservation{ResourceExists: row.exists}, row.readError
					},
					CreateFn: func(context.Context, xpresource.Managed) (managed.ExternalCreation, error) {
						t.Fatal("preparation failure started a write")
						return managed.ExternalCreation{}, nil
					},
				}, nil
			})
			external, err := consentscopes.WrapConnector(kube, true, next).Connect(context.Background(), item)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := external.Create(context.Background(), item); err == nil {
				t.Fatal("creation proceeded without confirming an absent parent")
			}
			if item.GetAnnotations()[consentscopes.InitializedAnnotation] != "app" {
				t.Fatal("preparation failure changed in-memory initialization state")
			}
			if err := kube.Get(context.Background(), client.ObjectKeyFromObject(item), item); err != nil {
				t.Fatal(err)
			}
			if item.GetAnnotations()[consentscopes.InitializedAnnotation] != "app" {
				t.Fatal("preparation failure reset durable initialization state")
			}
		})
	}
}

func TestCreateConflictPreservesInitializationThroughCriticalUpdate(t *testing.T) {
	for scope, factory := range map[string]func() resource.Terraformed{
		"namespaced": func() resource.Terraformed { return &application.UserConsentScopes{} },
		"cluster":    func() resource.Terraformed { return &clusterapplication.UserConsentScopes{} },
	} {
		t.Run(scope, func(t *testing.T) {
			ctx := context.Background()
			item, kube := consentFixture(t, factory, `null`, `["phone"]`)
			next := managed.ExternalConnectorFn(func(context.Context, xpresource.Managed) (managed.ExternalClient, error) {
				return managed.ExternalClientFns{
					DisconnectFn: func(context.Context) error { return nil },
					ObserveFn: func(context.Context, xpresource.Managed) (managed.ExternalObservation, error) {
						fresh := factory()
						if err := kube.Get(ctx, client.ObjectKeyFromObject(item), fresh); err != nil {
							t.Fatal(err)
						}
						if err := json.Unmarshal([]byte(`{"spec":{"initProvider":{"userScopes":["address"]}}}`), fresh); err != nil {
							t.Fatal(err)
						}
						if err := kube.Update(ctx, fresh); err != nil {
							t.Fatal(err)
						}
						return managed.ExternalObservation{ResourceExists: false}, nil
					},
					CreateFn: func(context.Context, xpresource.Managed) (managed.ExternalCreation, error) {
						t.Fatal("conflicting preparation reached mutation")
						return managed.ExternalCreation{}, nil
					},
				}, nil
			})
			external, err := consentscopes.WrapConnector(kube, true, next).Connect(ctx, item)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := external.Create(ctx, item); !apierrors.IsConflict(err) {
				t.Fatalf("expected a preparation conflict, got %v", err)
			}
			// Reproduce the managed reconciler's annotation retry after Create fails.
			meta.SetExternalCreateFailed(item, time.Now())
			if err := managed.NewRetryingCriticalAnnotationUpdater(kube).UpdateCriticalAnnotations(ctx, item); err != nil {
				t.Fatal(err)
			}
			persisted := factory()
			if err := kube.Get(ctx, client.ObjectKeyFromObject(item), persisted); err != nil {
				t.Fatal(err)
			}
			if marker := persisted.GetAnnotations()[consentscopes.InitializedAnnotation]; marker != "app" {
				t.Fatalf("aborted creation reset initialization through critical annotation retry: %q", marker)
			}
			initial, err := persisted.GetInitParameters()
			if err != nil {
				t.Fatal(err)
			}
			if scopes, ok := initial["user_scopes"].([]any); !ok || len(scopes) != 1 || scopes[0] != "address" {
				t.Fatal("critical annotation retry overwrote concurrent initialization edit", initial)
			}
		})
	}
}

func TestDisabledManagementPoliciesLeaveInitializationUntouched(t *testing.T) {
	item, kube := consentFixture(t, func() resource.Terraformed { return &application.UserConsentScopes{} }, `null`, `["email"]`)
	next := managed.ExternalConnectorFn(func(context.Context, xpresource.Managed) (managed.ExternalClient, error) {
		return managed.ExternalClientFns{}, nil
	})
	if _, err := consentscopes.WrapConnector(kube, false, next).Connect(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if _, exists := item.GetAnnotations()[consentscopes.InitializedAnnotation]; exists {
		t.Fatal("disabled management policies acquired initialization metadata")
	}
}

func TestConcurrentScopesUpdateDuringConnect(t *testing.T) {
	for scope, factory := range map[string]func() resource.Terraformed{
		"namespaced": func() resource.Terraformed { return &application.UserConsentScopes{} },
		"cluster":    func() resource.Terraformed { return &clusterapplication.UserConsentScopes{} },
	} {
		t.Run(scope, func(t *testing.T) {
			ctx := context.Background()
			item, kube := consentFixture(t, factory, `["profile"]`, `null`)
			stale := item.DeepCopyObject().(resource.Terraformed)
			version := stale.GetResourceVersion()
			// GitOps revokes scopes after the reconciler reads its snapshot.
			if err := item.SetParameters(map[string]any{"application_id": "app", "user_scopes": []any{}}); err != nil {
				t.Fatal(err)
			}
			if err := kube.Update(ctx, item); err != nil {
				t.Fatal(err)
			}
			connected := false
			next := managed.ExternalConnectorFn(func(context.Context, xpresource.Managed) (managed.ExternalClient, error) {
				connected = true
				return managed.ExternalClientFns{}, nil
			})
			connector := consentscopes.WrapConnector(kube, true, next)
			if _, err := connector.Connect(ctx, stale); !apierrors.IsConflict(err) {
				t.Fatalf("stale initialization error=%v, want conflict", err)
			}
			if connected || stale.GetResourceVersion() != version {
				t.Fatal("conflicting metadata write advanced the stale reconcile")
			}

			// A fresh reconcile can persist metadata and perform Crossplane's
			// full-object finalizer update without restoring the old grants.
			if err := kube.Get(ctx, client.ObjectKeyFromObject(item), stale); err != nil {
				t.Fatal(err)
			}
			if _, present := stale.GetAnnotations()[consentscopes.InitializedAnnotation]; present {
				t.Fatal("conflicting initialization metadata was persisted")
			}
			if _, err := connector.Connect(ctx, stale); err != nil {
				t.Fatal(err)
			}
			if err := xpresource.NewAPIFinalizer(kube, managed.FinalizerName).AddFinalizer(ctx, stale); err != nil {
				t.Fatal(err)
			}
			if err := kube.Get(ctx, client.ObjectKeyFromObject(item), item); err != nil {
				t.Fatal(err)
			}
			params, err := item.GetParameters()
			if err != nil {
				t.Fatal(err)
			}
			scopes, ok := params["user_scopes"].([]any)
			if !ok || len(scopes) != 0 {
				t.Fatalf("GitOps revocation overwritten: user_scopes=%v", params["user_scopes"])
			}
			if !connected || item.GetAnnotations()[consentscopes.InitializedAnnotation] != "app" {
				t.Fatal("fresh reconcile did not finish initialization metadata")
			}
		})
	}
}
