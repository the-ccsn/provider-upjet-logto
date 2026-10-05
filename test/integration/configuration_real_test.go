//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	logto "github.com/Lenstra/terraform-provider-logto/client"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Exercise the actual shipped controller and Secret selectors, not direct resource calls.
func testRealConfigurations(t *testing.T, kube client.Client, remote *logto.Client, stop func()) {
	t.Helper()
	ctx := context.Background()
	rows := []struct {
		kind, resource string
		desired        map[string]any
	}{
		{"account_center", "AccountCenter", map[string]any{"enabled": true}},
		{"sign_in_experience", "SignInExperience", map[string]any{"supportWebsiteUrl": "https://acceptance.example.invalid/support"}},
		{"id_token_config", "IDTokenConfiguration", map[string]any{"enabledExtendedClaims": []any{"roles"}}},
		{"oidc_session_config", "OIDCSessionConfiguration", map[string]any{"ttl": float64(1800)}},
	}
	for _, row := range rows {
		before, err := remote.ConfigurationGet(ctx, row.kind, "default")
		if err != nil {
			t.Fatal(err)
		}
		restore := map[string]any{}
		for key := range row.desired {
			restore[key] = before[key]
		}
		t.Cleanup(func() {
			stop()
			encoded, _ := json.Marshal(restore)
			if _, err := remote.ConfigurationWrite(ctx, row.kind, "default", "", string(encoded), false); err != nil {
				t.Error(err)
			}
		})
		encoded, _ := json.Marshal(row.desired)
		object := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "configuration.logto.m.crossplane.io/v1alpha1", "kind": row.resource,
			"metadata": map[string]any{"name": row.kind, "namespace": "tenant", "annotations": map[string]any{"crossplane.io/external-name": "default"}},
			"spec":     map[string]any{"managementPolicies": []any{"Observe"}, "providerConfigRef": map[string]any{"name": "default", "kind": "ProviderConfig"}, "forProvider": map[string]any{"configuration": string(encoded)}},
		}}
		// Kubernetes names cannot contain underscores.
		object.SetName(strings.ReplaceAll(row.kind, "_", "-"))
		if err := kube.Create(ctx, object); err != nil {
			t.Fatal(err)
		}
		eventually(t, "configuration Observe adoption "+row.kind, func() bool {
			return kube.Get(ctx, client.ObjectKeyFromObject(object), object) == nil && unstructuredSynced(object)
		})
		observed, err := remote.ConfigurationGet(ctx, row.kind, "default")
		if err != nil || !reflect.DeepEqual(before, observed) {
			t.Fatal("Observe policy mutated tenant configuration")
		}
		if err := kube.Patch(ctx, object, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"managementPolicies":["Observe","Update"]}}`))); err != nil {
			t.Fatal(err)
		}
		eventually(t, "configuration in-place update "+row.kind, func() bool {
			actual, err := remote.ConfigurationGet(ctx, row.kind, "default")
			if err != nil {
				return false
			}
			for key, value := range row.desired {
				if !reflect.DeepEqual(actual[key], value) {
					return false
				}
			}
			return true
		})
	}
	const marker = "acceptance-only-placeholder"
	data := []byte(`{"config":{"clientId":"acceptance-only","clientSecret":"` + marker + `"},"syncProfile":false}`)
	if err := kube.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "connector-config", Namespace: "tenant"}, Data: map[string][]byte{"configuration": data}}); err != nil {
		t.Fatal(err)
	}
	connector := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "connector.logto.m.crossplane.io/v1alpha1", "kind": "Connector", "metadata": map[string]any{"name": "github-acceptance", "namespace": "tenant"},
		"spec": map[string]any{"managementPolicies": []any{"Observe", "Create", "Update", "LateInitialize"}, "providerConfigRef": map[string]any{"name": "default", "kind": "ProviderConfig"}, "forProvider": map[string]any{"connectorId": "github-universal", "configurationSecretRef": map[string]any{"name": "connector-config", "key": "configuration"}}},
	}}
	if err := unstructured.SetNestedField(connector.Object, map[string]any{"name": "connector-state"}, "spec", "writeConnectionSecretToRef"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop()
		if kube.Get(ctx, client.ObjectKeyFromObject(connector), connector) == nil {
			id := connector.GetAnnotations()["crossplane.io/external-name"]
			if id == "" {
				id, _, _ = unstructured.NestedString(connector.Object, "status", "atProvider", "id")
			}
			if id != "" {
				if err := remote.ConfigurationDelete(ctx, "connector", id); err != nil {
					t.Error(err)
				}
			}
		}
	})
	if err := kube.Create(ctx, connector); err != nil {
		t.Fatal(err)
	}
	lastDiagnostic := ""
	eventually(t, "connector SecretRef creation", func() bool {
		if kube.Get(ctx, client.ObjectKeyFromObject(connector), connector) != nil {
			return false
		}
		conditions, _, _ := unstructured.NestedSlice(connector.Object, "status", "conditions")
		encoded, _ := json.Marshal(conditions)
		if string(encoded) != lastDiagnostic {
			id, _, _ := unstructured.NestedString(connector.Object, "status", "atProvider", "id")
			t.Logf("connector reconcile diagnostic: externalName=%q statusID=%q conditions=%s", connector.GetAnnotations()["crossplane.io/external-name"], id, encoded)
			lastDiagnostic = string(encoded)
		}
		return unstructuredSynced(connector) && connector.GetAnnotations()["crossplane.io/external-name"] != ""
	})
	status, _, _ := unstructured.NestedMap(connector.Object, "status")
	encoded, _ := json.Marshal(status)
	if strings.Contains(string(encoded), marker) {
		t.Fatal("connector credential leaked into status")
	}
	secret := &corev1.Secret{}
	if err := kube.Get(ctx, types.NamespacedName{Name: "connector-config", Namespace: "tenant"}, secret); err != nil {
		t.Fatal(err)
	}
	secret.Data["configuration"] = []byte(`{"config":{"clientId":"acceptance-updated","clientSecret":"` + marker + `"},"syncProfile":true}`)
	if err := kube.Update(ctx, secret); err != nil {
		t.Fatal(err)
	}
	// Requeue explicitly; Secret updates are observed on the provider's next poll.
	if err := kube.Patch(ctx, connector, client.RawPatch(types.MergePatchType, []byte(`{"metadata":{"annotations":{"crossplane.io/reconcile-requested-at":"configuration-updated"}}}`))); err != nil {
		t.Fatal(err)
	}
	eventually(t, "connector SecretRef update", func() bool {
		actual, err := remote.ConfigurationGet(ctx, "connector", connector.GetAnnotations()["crossplane.io/external-name"])
		return err == nil && actual["syncProfile"] == true
	})
}

func unstructuredSynced(object *unstructured.Unstructured) bool {
	conditions, _, _ := unstructured.NestedSlice(object.Object, "status", "conditions")
	for _, value := range conditions {
		condition := value.(map[string]any)
		if condition["type"] == "Synced" && condition["status"] == "True" {
			return true
		}
	}
	return false
}
