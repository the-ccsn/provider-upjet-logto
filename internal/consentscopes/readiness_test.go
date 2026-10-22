package consentscopes_test

import (
	"context"
	"errors"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	fakeManager "github.com/crossplane/crossplane-runtime/v2/pkg/resource/fake"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/crossplane/upjet/v2/pkg/resource"
	clusterapplication "github.com/the-ccsn/provider-upjet-logto/apis/cluster/application/v1alpha1"
	application "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/application/v1alpha1"
	"github.com/the-ccsn/provider-upjet-logto/internal/consentscopes"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type completionPatchClient struct {
	client.Client
	patchError error
	failures   int
}

func (c *completionPatchClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if c.patchError != nil && obj.GetAnnotations()[consentscopes.InitializedAnnotation] == "app" {
		c.failures++
		return c.patchError
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func TestInitializationReadyRequiresPersistedCompletion(t *testing.T) {
	for _, row := range []struct {
		name    string
		factory func() resource.Terraformed
		kind    xpresource.ManagedKind
	}{
		{"namespaced", func() resource.Terraformed { return &application.UserConsentScopes{} }, xpresource.ManagedKind(application.UserConsentScopes_GroupVersionKind)},
		{"cluster", func() resource.Terraformed { return &clusterapplication.UserConsentScopes{} }, xpresource.ManagedKind(clusterapplication.UserConsentScopes_GroupVersionKind)},
	} {
		t.Run(row.name, func(t *testing.T) {
			ctx := context.Background()
			item, fixture := consentFixture(t, row.factory, `null`, `["email"]`)
			if row.name == "namespaced" {
				item.SetNamespace("tenant")
			}
			item.SetManagementPolicies(xpv2.ManagementPolicies{xpv2.ManagementActionAll})
			meta.AddAnnotations(item, map[string]string{consentscopes.InitializedAnnotation: ""})
			item.SetConditions(xpv2.Creating(), xpv2.ReconcileSuccess())
			kube := &completionPatchClient{
				Client:     fake.NewClientBuilder().WithScheme(fixture.Scheme()).WithStatusSubresource(item).WithObjects(item).Build(),
				patchError: errors.New("completion patch unavailable"),
			}
			next := managed.ExternalConnectorFn(func(context.Context, xpresource.Managed) (managed.ExternalClient, error) {
				return managed.ExternalClientFns{
					ObserveFn: func(_ context.Context, mg xpresource.Managed) (managed.ExternalObservation, error) {
						if err := mg.(resource.Terraformed).SetObservation(map[string]any{"id": "app", "application_id": "app", "user_scopes": []any{"email"}}); err != nil {
							return managed.ExternalObservation{}, err
						}
						mg.SetConditions(xpv2.Available())
						return managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: true}, nil
					},
					DisconnectFn: func(context.Context) error { return nil },
				}, nil
			})
			newReconciler := func() *managed.Reconciler {
				return managed.NewReconciler(&fakeManager.Manager{Client: kube, Scheme: kube.Scheme()}, row.kind,
					managed.WithManagementPolicies(), managed.WithExternalConnector(consentscopes.WrapConnector(kube, true, next)))
			}
			r := newReconciler()
			request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(item)}
			if _, err := r.Reconcile(ctx, request); err != nil {
				t.Fatal(err)
			}
			if err := kube.Get(ctx, request.NamespacedName, item); err != nil {
				t.Fatal(err)
			}
			if kube.failures != 1 || item.GetAnnotations()[consentscopes.InitializedAnnotation] != "" {
				t.Fatal("did not exercise completion persistence failure", kube.failures, item.GetAnnotations())
			}
			if ready := item.GetCondition(xpv2.TypeReady); ready.Status != corev1.ConditionFalse || ready.Reason != xpv2.Creating().Reason {
				t.Fatal("completion was not persisted before readiness", ready)
			}
			if item.GetCondition(xpv2.TypeSynced).Status != corev1.ConditionFalse || item.GetID() != "app" {
				t.Fatal("metadata failure was hidden or lost observed identity", item.GetCondition(xpv2.TypeSynced), item.GetID())
			}

			// Retry through a new reconciler after metadata writes recover.
			kube.patchError = nil
			r = newReconciler()
			for i := 0; i < 3; i++ {
				if _, err := r.Reconcile(ctx, request); err != nil {
					t.Fatal(err)
				}
			}
			if err := kube.Get(ctx, request.NamespacedName, item); err != nil {
				t.Fatal(err)
			}
			if item.GetAnnotations()[consentscopes.InitializedAnnotation] != "app" ||
				item.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue ||
				item.GetCondition(xpv2.TypeSynced).Status != corev1.ConditionTrue {
				t.Fatal("initialization did not become ready after persistence recovered", item.GetAnnotations(), item.GetCondition(xpv2.TypeReady), item.GetCondition(xpv2.TypeSynced))
			}
		})
	}
}
