//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	application "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/application/v1alpha1"
	"github.com/the-ccsn/provider-upjet-logto/internal/consentscopes"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestControllerInitializationDuringWrite(t *testing.T) {
	for _, parent := range []string{"forProvider", "initProvider"} {
		t.Run(parent, func(t *testing.T) { testControllerInitializationDuringWrite(t, parent) })
	}
}

func testControllerInitializationDuringWrite(t *testing.T, parent string) {
	desired := []string{}
	ctx := context.Background()
	api := &consentScopesTestAPI{scopes: []string{}}
	blocked, allowWrite := false, false
	readStarted, releaseRead := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseRead) })
	defer release()
	api.beforeWrite = func() bool {
		api.mu.Lock()
		if !allowWrite {
			api.mu.Unlock()
			return false
		}
		block := !blocked
		blocked = true
		api.mu.Unlock()
		if block {
			close(readStarted)
			<-releaseRead
		}
		return true
	}
	kube := startConsentScopesTestProvider(t, api)
	item := &application.UserConsentScopes{
		ObjectMeta: metav1.ObjectMeta{Name: "concurrent-consent", Namespace: "tenant"},
		Spec: application.UserConsentScopesSpec{
			ForProvider:  application.UserConsentScopesParameters{ApplicationID: str("app")},
			InitProvider: application.UserConsentScopesInitParameters{UserScopes: []*string{str("email")}},
		},
	}
	item.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: "ProviderConfig"})
	if parent == "initProvider" {
		item.Spec.ForProvider.ApplicationID = nil
		item.Spec.InitProvider.ApplicationID = str("app")
	}
	if err := kube.Create(ctx, item); err != nil {
		t.Fatal(err)
	}
	eventually(t, "failed initial grants observed and persisted", func() bool {
		return kube.Get(ctx, client.ObjectKeyFromObject(item), item) == nil && item.Status.AtProvider.ID != nil && *item.Status.AtProvider.ID == "app"
	})
	if parent == "initProvider" {
		if err := kube.Patch(ctx, item, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"initProvider":{"applicationId":null}}}`))); err != nil {
			t.Fatal(err)
		}
		eventually(t, "removed pending parent remains creation-only", func() bool {
			if kube.Get(ctx, client.ObjectKeyFromObject(item), item) != nil {
				return false
			}
			if item.Spec.ForProvider.ApplicationID != nil {
				t.Fatal("removed pending parent was promoted into forProvider")
			}
			synced := item.GetCondition(xpv2.TypeSynced)
			return synced.Status == corev1.ConditionFalse && synced.ObservedGeneration == item.Generation
		})
		if item.GetAnnotations()[consentscopes.InitializedAnnotation] != "" ||
			item.GetCondition(xpv2.TypeReady).Status != corev1.ConditionFalse {
			t.Fatal("removing pending parent completed initialization", item.GetAnnotations(), item.GetCondition(xpv2.TypeReady))
		}
		if err := kube.Patch(ctx, item, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"initProvider":{"applicationId":"app"}}}`))); err != nil {
			t.Fatal(err)
		}
	}
	api.mu.Lock()
	allowWrite = true
	api.mu.Unlock()
	select {
	case <-readStarted:
	case <-time.After(35 * time.Second):
		t.Fatal("grant retry did not start")
	}
	// Change desired scopes while the controller holds its older snapshot.
	patch, err := json.Marshal(map[string]any{"spec": map[string]any{"initProvider": map[string]any{"userScopes": desired}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := kube.Patch(ctx, item, client.RawPatch(types.MergePatchType, patch)); err != nil {
		t.Fatal(err)
	}
	key := client.ObjectKeyFromObject(item)
	// A write is still in progress. A matching older status must not
	// complete initialization before the final grant is read back.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := kube.Get(ctx, key, item); err != nil {
			t.Fatal(err)
		}
		if item.GetAnnotations()[consentscopes.InitializedAnnotation] == "app" {
			t.Fatal("initialization completed while an older write was still running")
		}
		time.Sleep(100 * time.Millisecond)
	}
	release()
	api.assertConverges(t, kube, item, desired, func() []*string { return item.Spec.InitProvider.UserScopes })
	if parent == "initProvider" && item.Spec.ForProvider.ApplicationID != nil {
		t.Fatal("recovered initial parent was promoted into forProvider")
	}
}

func TestControllerConsentScopesInvalidParentRecovery(t *testing.T) {
	for _, row := range []struct{ name, section, id string }{
		{"path/forProvider", "forProvider", "bad/app"},
		{"path/initProvider", "initProvider", "bad/app"},
		{"control/forProvider", "forProvider", "bad\tapp"},
		{"control/initProvider", "initProvider", "bad\tapp"},
	} {
		t.Run(row.name, func(t *testing.T) {
			ctx := context.Background()
			api := &consentScopesTestAPI{scopes: []string{}}
			kube := startConsentScopesTestProvider(t, api)
			item := &application.UserConsentScopes{
				ObjectMeta: metav1.ObjectMeta{Name: "invalid-parent-recovery", Namespace: "tenant"},
				Spec: application.UserConsentScopesSpec{ForProvider: application.UserConsentScopesParameters{
					UserScopes: []*string{str("email")},
				}},
			}
			if row.section == "forProvider" {
				item.Spec.ForProvider.ApplicationID = str(row.id)
			} else {
				item.Spec.InitProvider.ApplicationID = str(row.id)
			}
			item.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: "ProviderConfig"})
			if err := kube.Create(ctx, item); err != nil {
				t.Fatal(err)
			}
			eventually(t, "invalid initial application ID rejected", func() bool {
				if kube.Get(ctx, client.ObjectKeyFromObject(item), item) != nil {
					return false
				}
				message := item.GetCondition(xpv2.TypeSynced).Message
				return strings.Contains(message, "Invalid application ID") || strings.Contains(message, "identifier must be")
			})
			patch, err := json.Marshal(map[string]any{"spec": map[string]any{row.section: map[string]any{"applicationId": "app"}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := kube.Patch(ctx, item, client.RawPatch(types.MergePatchType, patch)); err != nil {
				t.Fatal(err)
			}
			api.assertConverges(t, kube, item, []string{"email"}, func() []*string { return item.Spec.ForProvider.UserScopes })
		})
	}
}

func TestControllerConsentScopesRejectedInitializationCleanup(t *testing.T) {
	for _, section := range []string{"forProvider", "initProvider"} {
		t.Run(section, func(t *testing.T) {
			ctx := context.Background()
			api := &consentScopesTestAPI{scopes: []string{"profile", "email"}}
			kube := startConsentScopesTestProvider(t, api)
			item := &application.UserConsentScopes{
				ObjectMeta: metav1.ObjectMeta{Name: "rejected-initialization-cleanup", Namespace: "tenant"},
				Spec: application.UserConsentScopesSpec{ForProvider: application.UserConsentScopesParameters{
					ApplicationID: str("app"),
				}},
			}
			if section == "forProvider" {
				item.Spec.ForProvider.UserScopes = []*string{str("openid")}
			} else {
				item.Spec.InitProvider.UserScopes = []*string{str("openid")}
			}
			item.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: "ProviderConfig"})
			if err := kube.Create(ctx, item); err != nil {
				t.Fatal(err)
			}
			key := client.ObjectKeyFromObject(item)
			eventually(t, "invalid scope rejected before adopting the parent", func() bool {
				return kube.Get(ctx, key, item) == nil &&
					strings.Contains(strings.ToLower(item.GetCondition(xpv2.TypeSynced).Message), "invalid user consent scope")
			})
			if err := kube.Delete(ctx, item); err != nil {
				t.Fatal(err)
			}
			eventually(t, "failed initialization finalizer removed", func() bool {
				return apierrors.IsNotFound(kube.Get(ctx, key, &application.UserConsentScopes{}))
			})
			api.mu.Lock()
			defer api.mu.Unlock()
			if !slices.Equal(api.scopes, []string{"profile", "email"}) {
				t.Fatalf("deleting rejected initialization revoked preexisting grants: %v", api.scopes)
			}
		})
	}
}
