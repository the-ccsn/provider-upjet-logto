package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"sigs.k8s.io/yaml"
)

// Use the API server's structural and CEL validators on every shipped CRD.
func TestGeneratedCRDsValidate(t *testing.T) {
	files, err := filepath.Glob("../package/crds/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 17 {
		t.Fatalf("expected 17 CRDs, got %d", len(files))
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var external apiextensionsv1.CustomResourceDefinition
			if err := yaml.UnmarshalStrict(data, &external); err != nil {
				t.Fatal(err)
			}
			var internal apiextensions.CustomResourceDefinition
			if err := apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(&external, &internal, nil); err != nil {
				t.Fatal(err)
			}
			for _, version := range internal.Spec.Versions {
				if version.Storage {
					internal.Status.StoredVersions = []string{version.Name}
				}
			}
			if errs := validation.ValidateCustomResourceDefinition(context.Background(), &internal); len(errs) > 0 {
				t.Fatal(errs.ToAggregate())
			}
		})
	}
}
