//go:build integration

package integration

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	application "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/application/v1alpha1"
	"github.com/the-ccsn/provider-upjet-logto/internal/consentscopes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestControllerFailedConsentCreationPersistsIdentity(t *testing.T) {
	api := &consentScopesTestAPI{scopes: []string{}}
	api.writeStatus = func() int { return http.StatusServiceUnavailable }
	readStarted, releaseRead := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseRead) })
	defer release()
	blocked := false
	api.beforeRead = func() {
		api.mu.Lock()
		block := len(api.scopes) != 0 && !blocked
		blocked = blocked || block
		api.mu.Unlock()
		if block {
			close(readStarted)
			<-releaseRead
		}
	}
	kube := startConsentScopesTestProvider(t, api)
	item := &application.UserConsentScopes{
		ObjectMeta: metav1.ObjectMeta{Name: "failed-create-identity", Namespace: "tenant"},
		Spec: application.UserConsentScopesSpec{ForProvider: application.UserConsentScopesParameters{
			ApplicationID: str("app"), UserScopes: []*string{str("email")},
		}},
	}
	item.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: "ProviderConfig"})
	if err := kube.Create(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	select {
	case <-readStarted:
	case <-time.After(40 * time.Second):
		t.Fatal("failed creation did not reach its next observation")
	}
	// The follow-up Read is still blocked. Only the failed Create can have
	// recovered the identity for a restart or a subsequent deletion.
	if err := kube.Get(context.Background(), client.ObjectKeyFromObject(item), item); err != nil {
		t.Fatal(err)
	}
	if meta.GetExternalName(item) != "app" || meta.GetExternalCreateFailed(item).IsZero() {
		t.Fatal("failed creation did not persist partial identity", item.GetAnnotations())
	}
	if item.GetAnnotations()[consentscopes.InitializedAnnotation] != "" ||
		item.GetCondition(xpv2.TypeReady).Status != corev1.ConditionFalse ||
		item.GetCondition(xpv2.TypeSynced).Status != corev1.ConditionFalse {
		t.Fatal("partial creation was marked complete", item.GetAnnotations(), item.GetCondition(xpv2.TypeReady), item.GetCondition(xpv2.TypeSynced))
	}
	// A different desired parent is rejected as a replacement. The original
	// parent existing must not make its pending initialization Ready.
	before := item.DeepCopy()
	item.Spec.ForProvider.ApplicationID = str("other")
	if err := kube.Patch(context.Background(), item, client.MergeFrom(before)); err != nil {
		t.Fatal(err)
	}
	release()
	eventually(t, "changed parent rejected after failed creation", func() bool {
		if kube.Get(context.Background(), client.ObjectKeyFromObject(item), item) != nil {
			return false
		}
		synced := item.GetCondition(xpv2.TypeSynced)
		return synced.Status == corev1.ConditionFalse && synced.ObservedGeneration == item.Generation &&
			strings.Contains(synced.Message, "require resource replacement")
	})
	if item.GetAnnotations()[consentscopes.InitializedAnnotation] != "" ||
		item.GetCondition(xpv2.TypeReady).Status != corev1.ConditionFalse || meta.GetExternalName(item) != "app" {
		t.Fatal("rejected parent change completed pending initialization", item.GetAnnotations(), item.GetCondition(xpv2.TypeReady))
	}
	before = item.DeepCopy()
	item.Spec.ForProvider.ApplicationID = str("app")
	if err := kube.Patch(context.Background(), item, client.MergeFrom(before)); err != nil {
		t.Fatal(err)
	}
	api.assertConverges(t, kube, item, []string{"email"}, func() []*string { return item.Spec.ForProvider.UserScopes })
}
