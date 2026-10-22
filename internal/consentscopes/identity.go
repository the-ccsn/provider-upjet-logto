package consentscopes

import (
	"context"
	"errors"
	"fmt"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/crossplane/upjet/v2/pkg/controller"
	"github.com/crossplane/upjet/v2/pkg/resource"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// PreserveCreateIdentity exposes a validated partial creation ID to Crossplane's
// critical annotation updater before a restart can discard the Framework cache.
func PreserveCreateIdentity(next managed.ExternalConnector, r fwresource.Resource, trackers *controller.OperationTrackerStore) managed.ExternalConnector {
	return managed.ExternalConnectorFn(func(ctx context.Context, mg xpresource.Managed) (managed.ExternalClient, error) {
		ec, err := next.Connect(ctx, mg)
		if err != nil {
			return nil, err
		}
		return &identityClient{ExternalClient: ec, resource: r, tracker: trackers.Tracker(mg.(resource.Terraformed))}, nil
	})
}

type identityClient struct {
	managed.ExternalClient
	resource fwresource.Resource
	tracker  *controller.AsyncTracker
}

func (e *identityClient) Create(ctx context.Context, mg xpresource.Managed) (managed.ExternalCreation, error) {
	creation, err := e.ExternalClient.Create(ctx, mg)
	if err == nil {
		return creation, nil
	}
	if recoveryErr := e.recoverIdentity(ctx, mg); recoveryErr != nil {
		return creation, errors.Join(err, recoveryErr)
	}
	return creation, err
}

func (e *identityClient) recoverIdentity(ctx context.Context, mg xpresource.Managed) error {
	state := e.tracker.GetFrameworkTFState()
	if state == nil {
		return nil
	}
	var schema fwresource.SchemaResponse
	e.resource.Schema(ctx, fwresource.SchemaRequest{}, &schema)
	if schema.Diagnostics.HasError() {
		return fmt.Errorf("read partial consent state schema: %v", schema.Diagnostics)
	}
	value, err := state.Unmarshal(schema.Schema.Type().TerraformType(ctx))
	if err != nil {
		return fmt.Errorf("decode partial consent state: %w", err)
	}
	if value.IsNull() {
		return nil
	}
	var fields map[string]tftypes.Value
	if err := value.As(&fields); err != nil {
		return fmt.Errorf("read partial consent state: %w", err)
	}
	id, present := fields["id"]
	if !present || id.IsNull() || !id.IsKnown() {
		return nil
	}
	var externalName string
	if err := id.As(&externalName); err != nil {
		return fmt.Errorf("read partial consent identity: %w", err)
	}
	if externalName != "" {
		meta.SetExternalName(mg, externalName)
	}
	return nil
}
