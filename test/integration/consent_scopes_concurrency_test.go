//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	application "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/application/v1alpha1"
	"github.com/the-ccsn/provider-upjet-logto/internal/consentscopes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestControllerConcurrentUserConsentScopes(t *testing.T) {
	for _, row := range []struct {
		name   string
		scopes []string
	}{{"replace_scopes", []string{"phone"}}, {"revoke_scopes", []string{}}} {
		t.Run(row.name, func(t *testing.T) {
			ctx := context.Background()
			api := &consentScopesTestAPI{scopes: []string{}}
			blocked := false
			readStarted, releaseRead := make(chan struct{}), make(chan struct{})
			release := sync.OnceFunc(func() { close(releaseRead) })
			defer release()
			api.beforeRead = func() {
				api.mu.Lock()
				block := len(api.scopes) > 0 && !blocked
				blocked = blocked || block
				api.mu.Unlock()
				if block {
					close(readStarted)
					<-releaseRead
				}
			}
			kube := startConsentScopesTestProvider(t, api)
			item := &application.UserConsentScopes{
				ObjectMeta: metav1.ObjectMeta{Name: "concurrent-consent", Namespace: "tenant"},
				Spec: application.UserConsentScopesSpec{ForProvider: application.UserConsentScopesParameters{
					ApplicationID: str("app"), UserScopes: []*string{str("email")},
				}},
			}
			item.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: "ProviderConfig"})
			if err := kube.Create(ctx, item); err != nil {
				t.Fatal(err)
			}
			select {
			case <-readStarted:
			case <-time.After(35 * time.Second):
				t.Fatal("post-creation Read did not start")
			}
			// Change desired scopes while the controller holds its older snapshot.
			patch, err := json.Marshal(map[string]any{"spec": map[string]any{"forProvider": map[string]any{"userScopes": row.scopes}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := kube.Patch(ctx, item, client.RawPatch(types.MergePatchType, patch)); err != nil {
				t.Fatal(err)
			}
			release()
			api.assertConverges(t, kube, item, row.scopes, func() []*string { return item.Spec.ForProvider.UserScopes })
		})
	}
}

func TestControllerRecreationConflictPreservesInitialGrants(t *testing.T) {
	ctx := context.Background()
	api := &consentScopesTestAPI{scopes: []string{}}
	item := &application.UserConsentScopes{
		ObjectMeta: metav1.ObjectMeta{Name: "recreation-conflict", Namespace: "tenant"},
		Spec: application.UserConsentScopesSpec{
			ForProvider:  application.UserConsentScopesParameters{ApplicationID: str("app")},
			InitProvider: application.UserConsentScopesInitParameters{UserScopes: []*string{str("email")}},
		},
	}
	key := client.ObjectKeyFromObject(item)
	const oldPending = "2020-01-01T00:00:00Z"
	var reader client.Client
	armed, blocked := false, false
	preparedRead, releaseRead := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseRead) })
	defer release()
	api.readStatus = func() int {
		api.mu.Lock()
		active, kube := armed && !blocked, reader
		api.mu.Unlock()
		if !active {
			return 200
		}
		current := &application.UserConsentScopes{}
		if err := kube.Get(ctx, key, current); err != nil {
			return 500
		}
		// Block the preparation read only after the managed reconciler has
		// persisted its new creation-pending timestamp.
		if current.GetAnnotations()["crossplane.io/external-create-pending"] != oldPending {
			api.mu.Lock()
			blocked = true
			api.mu.Unlock()
			close(preparedRead)
			<-releaseRead
		}
		return 404
	}
	kube := startConsentScopesTestProvider(t, api)
	api.mu.Lock()
	reader = kube
	api.mu.Unlock()
	item.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: "ProviderConfig"})
	if err := kube.Create(ctx, item); err != nil {
		t.Fatal(err)
	}
	ready := func() bool {
		return kube.Get(ctx, key, item) == nil &&
			item.GetAnnotations()[consentscopes.InitializedAnnotation] == "app" &&
			item.GetCondition(xpv2.TypeReady).Status == corev1.ConditionTrue &&
			item.GetCondition(xpv2.TypeSynced).Status == corev1.ConditionTrue
	}
	eventually(t, "initial grants completed", ready)
	// An established resource is outside the generic creation grace period.
	if err := kube.Patch(ctx, item, client.RawPatch(types.MergePatchType, []byte(`{"metadata":{"annotations":{"crossplane.io/external-create-pending":"2020-01-01T00:00:00Z","crossplane.io/external-create-succeeded":"2020-01-01T00:00:00Z"}}}`))); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	armed = true
	previousReads := api.reads
	api.mu.Unlock()
	select {
	case <-preparedRead:
	case <-time.After(40 * time.Second):
		t.Fatal("recreation preparation read did not start")
	}
	if err := kube.Get(ctx, key, item); err != nil {
		t.Fatal(err)
	}
	if item.GetAnnotations()[consentscopes.InitializedAnnotation] != "app" {
		t.Fatal("preparation discarded completion before the conflict")
	}
	if err := kube.Patch(ctx, item, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"initProvider":{"userScopes":["phone"]}}}`))); err != nil {
		t.Fatal(err)
	}
	release()
	eventually(t, "fresh observations after preparation conflict", func() bool {
		if !ready() {
			return false
		}
		api.mu.Lock()
		defer api.mu.Unlock()
		return api.reads >= previousReads+4 && item.GetCondition(xpv2.TypeSynced).ObservedGeneration == item.Generation
	})
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.scopes) != 1 || api.scopes[0] != "email" {
		t.Fatalf("aborted recreation reapplied changed initialization grants: %v", api.scopes)
	}
}
