package config

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	ujresource "github.com/crossplane/upjet/v2/pkg/resource"
	connector "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/connector/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type connectorSecretClient struct{}

func (connectorSecretClient) GetSecretData(context.Context, *xpv2.SecretReference) (map[string][]byte, error) {
	return nil, fmt.Errorf("unexpected data lookup")
}
func (connectorSecretClient) GetSecretValue(_ context.Context, ref xpv2.SecretKeySelector) ([]byte, error) {
	if ref.Name != "config" || ref.Namespace != "tenant" || ref.Key != "configuration" {
		return nil, fmt.Errorf("unexpected selector")
	}
	return []byte(`{"config":{"clientSecret":"acceptance-placeholder"}}`), nil
}

func TestConnectorUnsetInitSelectorDoesNotMaskConfiguredSecret(t *testing.T) {
	tr := &connector.Connector{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant"}}
	tr.Spec.ForProvider.ConfigurationSecretRef = xpv2.LocalSecretKeySelector{LocalSecretReference: xpv2.LocalSecretReference{Name: "config"}, Key: "configuration"}
	params := map[string]any{}
	if err := ujresource.GetSensitiveParameters(context.Background(), connectorSecretClient{}, tr, params, tr.GetConnectionDetailsMapping()); err != nil {
		t.Fatal(err)
	}
	value, ok := params["configuration"].(string)
	if !ok || !strings.Contains(value, "acceptance-placeholder") {
		t.Fatal("configuration SecretRef was not resolved")
	}
	if err := tr.SetObservation(map[string]any{"id": "connector", "configuration": value}); err != nil {
		t.Fatal(err)
	}
	status, _ := json.Marshal(tr.Status)
	if strings.Contains(string(status), "acceptance-placeholder") {
		t.Fatal("connector credential leaked into status")
	}
}
