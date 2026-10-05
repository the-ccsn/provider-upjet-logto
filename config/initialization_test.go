package config

import (
	"context"
	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	application "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/application/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func TestInitialExternalNameIsOnlyAnAdoptionHint(t *testing.T) {
	for _, row := range []struct{ name, hint, external, want string }{
		{"adopt", "old-id", "", "old-id"},
		{"rebuilt", "old-id", "new-id", "new-id"},
		{"no-hint", "", "", ""},
	} {
		t.Run(row.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := application.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			app := &application.Application{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "tenant", Annotations: map[string]string{"logto.crossplane.io/initial-external-name": row.hint}}}
			if row.external != "" {
				meta.SetExternalName(app, row.external)
			}
			kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build()
			initializers := GetProviderNamespaced().Resources["logto_application"].InitializerFns
			if len(initializers) == 0 {
				t.Fatal("missing adoption initializer")
			}
			for twice := 0; twice < 2; twice++ {
				for _, init := range initializers {
					if err := init(kube).Initialize(context.Background(), app); err != nil {
						t.Fatal(err)
					}
				}
				if err := kube.Get(context.Background(), client.ObjectKeyFromObject(app), app); err != nil {
					t.Fatal(err)
				}
				if got := meta.GetExternalName(app); got != row.want {
					t.Fatalf("external ID=%q, want %q", got, row.want)
				}
			}
		})
	}
}
