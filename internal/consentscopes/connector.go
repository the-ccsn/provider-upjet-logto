// Package consentscopes preserves creation-only user scope initialization around
// Upjet's Framework client, which otherwise merges initProvider on every plan.
package consentscopes

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/crossplane/upjet/v2/pkg/resource"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// InitializedAnnotation records the application whose initial grants converged.
// An empty value means initialization is still pending, including failed writes.
const InitializedAnnotation = "logto.crossplane.io/user-consent-scopes-initialized"

// HasInitializedParent also recognizes adoption before its marker is persisted.
// A durable pending marker always keeps failed initial writes retryable.
func HasInitializedParent(tr resource.Terraformed) bool {
	name := meta.GetExternalName(tr)
	if name == "" {
		return false
	}
	if initialized, present := tr.GetAnnotations()[InitializedAnnotation]; present {
		return initialized == name
	}
	return meta.GetExternalCreatePending(tr).IsZero() ||
		(tr.GetID() == name && tr.GetCondition(xpv2.TypeReady).Status == corev1.ConditionTrue && tr.GetCondition(xpv2.TypeSynced).Status == corev1.ConditionTrue)
}

// WrapConnector adds initialization lifecycle handling around the synchronous
// Framework connector. Each successful Observe must include a fresh read.
func WrapConnector(kube client.Client, enabled bool, next managed.ExternalConnector) managed.ExternalConnector {
	if !enabled {
		return next
	}
	return managed.ExternalConnectorFn(func(ctx context.Context, mg xpresource.Managed) (managed.ExternalClient, error) {
		tr, ok := mg.(resource.Terraformed)
		if !ok {
			return nil, fmt.Errorf("consent resource does not expose Terraform parameters")
		}
		if _, present := mg.GetAnnotations()[InitializedAnnotation]; !present && !meta.WasDeleted(mg) {
			before := mg.DeepCopyObject().(client.Object)
			state := ""
			// Adopted resources and resources already reconciled by an older
			// controller must not acquire a new initialization target.
			if HasInitializedParent(tr) {
				state = meta.GetExternalName(mg)
			}
			meta.AddAnnotations(mg, map[string]string{InitializedAnnotation: state})
			if err := persistAnnotations(ctx, kube, before, mg); err != nil {
				return nil, err
			}
		}
		ec, err := next.Connect(ctx, mg)
		if err != nil {
			return nil, err
		}
		return &externalClient{ExternalClient: ec, kube: kube, connector: next}, nil
	})
}

type externalClient struct {
	managed.ExternalClient
	kube      client.Client
	connector managed.ExternalConnector
}

func (e *externalClient) Observe(ctx context.Context, mg xpresource.Managed) (managed.ExternalObservation, error) {
	before := mg.DeepCopyObject().(client.Object)
	o, err := e.ExternalClient.Observe(ctx, mg)
	if err != nil {
		return o, err
	}
	tr := mg.(resource.Terraformed)
	params, err := tr.GetParameters()
	if err != nil {
		return o, err
	}
	initial, err := tr.GetInitParameters()
	if err != nil {
		return o, err
	}
	id := tr.GetID()
	parent := params["application_id"]
	if parent == nil {
		parent = mg.GetAnnotations()[InitializedAnnotation]
		if parent != id || id == "" {
			parent = initial["application_id"]
		}
	}
	// Never mask a missing parent or a requested change to the parent identity.
	if o.ResourceExists && id != "" && id == meta.GetExternalName(mg) && (parent == nil || parent == id) {
		if mg.GetAnnotations()[InitializedAnnotation] == id && params["user_scopes"] == nil && initial["user_scopes"] != nil {
			// Observe still refreshes status and surfaces read errors. Only the
			// creation-only scope difference is excluded from subsequent writes.
			o.ResourceUpToDate = true
			resource.SetUpToDateCondition(tr, true)
			tr.SetConditions(resource.LastAsyncOperationCondition(nil))
		} else if o.ResourceUpToDate {
			observed, err := tr.GetObservation()
			if err != nil {
				return o, err
			}
			desired := params["user_scopes"]
			if desired == nil {
				desired = initial["user_scopes"]
			}
			// The synchronous connector has refreshed the grants. Complete
			// initialization only when that observation matches the target.
			if desired == nil || sameScopes(observed["user_scopes"], desired) {
				meta.AddAnnotations(mg, map[string]string{InitializedAnnotation: id})
				tr.SetConditions(resource.LastAsyncOperationCondition(nil))
			}
		}
	}
	if o.ResourceExists && id != "" && mg.GetAnnotations()[InitializedAnnotation] != id {
		// An existing parent is not enough for readiness, including when a
		// changed parent requires replacement before initialization completes.
		mg.SetConditions(xpv2.Creating())
	}
	// Persist identity recovery even when LateInitialize is disabled. Patch a
	// separate object so the server response cannot replace fresh in-memory status.
	if err := persistAnnotations(ctx, e.kube, before, mg); err != nil {
		// A fresh read may report Available before completion is durable.
		if before.GetAnnotations()[InitializedAnnotation] != id {
			mg.SetConditions(xpv2.Creating())
		}
		return o, err
	}
	return o, nil
}

func (e *externalClient) Create(ctx context.Context, mg xpresource.Managed) (managed.ExternalCreation, error) {
	before := mg.DeepCopyObject().(client.Object)
	var prepared managed.ExternalClient
	if before.GetAnnotations()[InitializedAnnotation] != "" {
		// Prepare current initialization defaults on a copy. Neither reference
		// resolution nor a failed existence check may discard durable completion.
		candidate := mg.DeepCopyObject().(resource.Terraformed)
		meta.AddAnnotations(candidate, map[string]string{InitializedAnnotation: ""})
		if resolver, ok := candidate.(interface {
			ResolveReferences(context.Context, client.Reader) error
		}); ok {
			if err := resolver.ResolveReferences(ctx, e.kube); err != nil {
				return managed.ExternalCreation{}, fmt.Errorf("resolve initial parent for recreation: %w", err)
			}
		}
		ec, err := e.connector.Connect(ctx, candidate)
		if err != nil {
			return managed.ExternalCreation{}, err
		}
		prepared = ec
		defer func() {
			if prepared != nil {
				_ = prepared.Disconnect(ctx)
			}
		}()
		o, err := prepared.Observe(ctx, candidate)
		if err != nil {
			return managed.ExternalCreation{}, err
		}
		if o.ResourceExists {
			return managed.ExternalCreation{}, fmt.Errorf("consent parent reappeared while preparing initialization; retry observation")
		}
		if err := e.ExternalClient.Disconnect(ctx); err != nil {
			return managed.ExternalCreation{}, err
		}
	}
	meta.AddAnnotations(mg, map[string]string{InitializedAnnotation: ""})
	if err := persistAnnotations(ctx, e.kube, before, mg); err != nil {
		// The managed reconciler retries annotations after a Create error.
		// It must not commit the initialization reset rejected by this patch.
		mg.SetAnnotations(maps.Clone(before.GetAnnotations()))
		return managed.ExternalCreation{}, err
	}
	if prepared != nil {
		e.ExternalClient = prepared
		prepared = nil
	}
	return e.ExternalClient.Create(ctx, mg)
}

func persistAnnotations(ctx context.Context, kube client.Client, before client.Object, mg xpresource.Managed) error {
	if maps.Equal(before.GetAnnotations(), mg.GetAnnotations()) {
		return nil
	}
	patch := before.DeepCopyObject().(client.Object)
	patch.SetAnnotations(maps.Clone(mg.GetAnnotations()))
	// Reject stale snapshots so later full-object updates cannot overwrite a
	// concurrent spec change using the newer metadata revision.
	if err := kube.Patch(ctx, patch, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("persist consent initialization state: %w", err)
	}
	mg.SetResourceVersion(patch.GetResourceVersion())
	mg.SetAnnotations(patch.GetAnnotations())
	return nil
}

func sameScopes(actual, desired any) bool {
	set := func(value any) ([]string, bool) {
		var result []string
		if value == nil {
			return result, true
		}
		items, ok := value.([]any)
		if !ok {
			return nil, false
		}
		for _, item := range items {
			scope, ok := item.(string)
			if !ok {
				return nil, false
			}
			result = append(result, scope)
		}
		slices.Sort(result)
		return result, true
	}
	actualScopes, validActual := set(actual)
	desiredScopes, validDesired := set(desired)
	return validActual && validDesired && slices.Equal(actualScopes, desiredScopes)
}
