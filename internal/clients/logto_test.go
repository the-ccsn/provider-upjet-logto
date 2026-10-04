package clients

import (
	"context"
	"strings"
	"testing"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	apis "github.com/the-ccsn/provider-upjet-logto/apis/namespaced"
	application "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/application/v1alpha1"
	pcapi "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNamespacedCredentials(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := apis.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pc := &pcapi.ProviderConfig{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "tenant"}, Spec: pcapi.ProviderConfigSpec{Credentials: pcapi.ProviderCredentials{Source: xpv2.CredentialsSourceSecret, CommonCredentialSelectors: xpv2.CommonCredentialSelectors{SecretRef: &xpv2.SecretKeySelector{SecretReference: xpv2.SecretReference{Name: "management", Namespace: "ignored"}, Key: "credentials"}}}}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "management", Namespace: "tenant"}, Data: map[string][]byte{"credentials": []byte(`{"hostname":"example.invalid","application_id":"manager","application_secret":"test-credential","resource":"https://default.logto.app/api"}`)}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pc, secret).Build()
	mg := &application.Application{TypeMeta: metav1.TypeMeta{APIVersion: "application.logto.m.crossplane.io/v1alpha1", Kind: "Application"}, ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: "tenant", UID: "uid"}}
	mg.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: "ProviderConfig"})
	setup := TerraformSetupBuilder("", "Lenstra/logto", "0.0.15")
	first, err := setup(context.Background(), kube, mg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := setup(context.Background(), kube, mg)
	if err != nil {
		t.Fatal(err)
	}
	if first.FrameworkProvider == nil || first.FrameworkProvider == second.FrameworkProvider {
		t.Fatal("each reconcile needs an independent Framework provider")
	}
	if first.Configuration["application_secret"] != "test-credential" {
		t.Fatal("credential not loaded from same namespace")
	}
}

func TestCredentialsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		data       string
		source     xpv2.CredentialsSource
		withSecret bool
	}{
		{"missing-secret", "", xpv2.CredentialsSourceSecret, false},
		{"malformed-json", "private-credential", xpv2.CredentialsSourceSecret, true},
		{"missing-field", `{"hostname":"example.invalid","application_secret":"private-credential"}`, xpv2.CredentialsSourceSecret, true},
		{"wrong-type", `{"hostname":123,"application_secret":"private-credential"}`, xpv2.CredentialsSourceSecret, true},
		{"environment", `{}`, xpv2.CredentialsSourceEnvironment, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := apis.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			pc := &pcapi.ProviderConfig{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "tenant"}, Spec: pcapi.ProviderConfigSpec{Credentials: pcapi.ProviderCredentials{Source: tc.source, CommonCredentialSelectors: xpv2.CommonCredentialSelectors{SecretRef: &xpv2.SecretKeySelector{SecretReference: xpv2.SecretReference{Name: "management", Namespace: "another-tenant"}, Key: "credentials"}}}}}
			builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pc)
			// An identically named Secret in another namespace must never satisfy the reference.
			builder.WithObjects(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "management", Namespace: "another-tenant"}, Data: map[string][]byte{"credentials": []byte(`{"hostname":"example.invalid","application_id":"other","application_secret":"private-credential","resource":"https://default.logto.app/api"}`)}})
			if tc.withSecret {
				builder.WithObjects(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "management", Namespace: "tenant"}, Data: map[string][]byte{"credentials": []byte(tc.data)}})
			}
			mg := &application.Application{TypeMeta: metav1.TypeMeta{APIVersion: "application.logto.m.crossplane.io/v1alpha1", Kind: "Application"}, ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: "tenant", UID: "uid"}}
			mg.SetProviderConfigReference(&xpv2.ProviderConfigReference{Name: "default", Kind: "ProviderConfig"})
			_, err := TerraformSetupBuilder("", "Lenstra/logto", "0.0.15")(context.Background(), builder.Build(), mg)
			if err == nil {
				t.Fatal("invalid credentials accepted")
			}
			if strings.Contains(err.Error(), "private-credential") {
				t.Fatal("credential leaked into diagnostics")
			}
		})
	}
}
