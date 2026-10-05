package config

import (
	"context"
	"fmt"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	ujconfig "github.com/crossplane/upjet/v2/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// InitialExternalNameAnnotation seeds adoption without making Git own the live ID.
const InitialExternalNameAnnotation = "logto.crossplane.io/initial-external-name"

func adoptionInitializer(kube client.Client) managed.Initializer {
	return managed.InitializerFn(func(ctx context.Context, mg resource.Managed) error {
		if meta.GetExternalName(mg) != "" {
			return nil
		}
		initial := mg.GetAnnotations()[InitialExternalNameAnnotation]
		if initial == "" {
			return nil
		}
		before := mg.DeepCopyObject().(client.Object)
		meta.SetExternalName(mg, initial)
		if err := kube.Patch(ctx, mg, client.MergeFrom(before)); err != nil {
			return fmt.Errorf("initialize external identity for adoption: %w", err)
		}
		return nil
	})
}

var initialExternalName ujconfig.NewInitializerFn = adoptionInitializer
