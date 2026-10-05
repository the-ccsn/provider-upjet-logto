package config

import (
	"encoding/json"
	"strings"
	"testing"

	ujresource "github.com/crossplane/upjet/v2/pkg/resource"
	application "github.com/the-ccsn/provider-upjet-logto/apis/namespaced/application/v1alpha1"
)

func TestSecretConnectionDetails(t *testing.T) {
	cfg := GetProviderNamespaced()
	if len(cfg.Resources) != 11 {
		t.Fatalf("got %d resources", len(cfg.Resources))
	}
	tr := &application.Secret{}
	state := map[string]any{"id": "app/gitops", "application_id": "app", "name": "gitops", "value": "test-credential"}
	conn, err := ujresource.GetConnectionDetails(state, tr, cfg.Resources["logto_application_secret"])
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"clientId": "app", "clientSecret": "test-credential", "attribute.value": "test-credential"} {
		if string(conn[key]) != want {
			t.Errorf("unexpected connection detail %s", key)
		}
	}
	if err := tr.SetObservation(state); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(tr.Status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "test-credential") {
		t.Fatal("credential leaked into resource status")
	}
	if cfg.Resources["logto_application_secret"].References["application_id"].TerraformName != "logto_application" {
		t.Fatal("application reference missing")
	}
}

func TestApplicationCredentialsStayOutOfStatus(t *testing.T) {
	cfg := GetProviderNamespaced()
	tr := &application.Application{}
	state := map[string]any{"id": "app", "name": "example", "type": "Traditional", "client_secrets": map[string]any{"gitops": "private-credential"}}
	conn, err := ujresource.GetConnectionDetails(state, tr, cfg.Resources["logto_application"])
	if err != nil {
		t.Fatal(err)
	}
	if string(conn["clientId"]) != "app" {
		t.Fatal("client ID not exported")
	}
	if err := tr.SetObservation(state); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(tr.Status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-credential") {
		t.Fatal("application credential leaked into status")
	}
	if _, err := secretDetails(map[string]any{"application_id": "app"}); err == nil {
		t.Fatal("missing secret must fail")
	}
	if _, err := applicationDetails(map[string]any{}); err == nil {
		t.Fatal("missing application ID must fail")
	}
}
