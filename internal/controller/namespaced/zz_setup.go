// SPDX-FileCopyrightText: 2024 The Crossplane Authors <https://crossplane.io>
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/crossplane/upjet/v2/pkg/controller"

	resource "github.com/the-ccsn/provider-upjet-logto/internal/controller/namespaced/api/resource"
	scope "github.com/the-ccsn/provider-upjet-logto/internal/controller/namespaced/api/scope"
	application "github.com/the-ccsn/provider-upjet-logto/internal/controller/namespaced/application/application"
	secret "github.com/the-ccsn/provider-upjet-logto/internal/controller/namespaced/application/secret"
	providerconfig "github.com/the-ccsn/provider-upjet-logto/internal/controller/namespaced/providerconfig"
	role "github.com/the-ccsn/provider-upjet-logto/internal/controller/namespaced/role/role"
	user "github.com/the-ccsn/provider-upjet-logto/internal/controller/namespaced/user/user"
)

// Setup creates all controllers with the supplied logger and adds them to
// the supplied manager.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	for _, setup := range []func(ctrl.Manager, controller.Options) error{
		resource.Setup,
		scope.Setup,
		application.Setup,
		secret.Setup,
		providerconfig.Setup,
		role.Setup,
		user.Setup,
	} {
		if err := setup(mgr, o); err != nil {
			return err
		}
	}
	return nil
}

// SetupGated creates all controllers with the supplied logger and adds them to
// the supplied manager gated.
func SetupGated(mgr ctrl.Manager, o controller.Options) error {
	for _, setup := range []func(ctrl.Manager, controller.Options) error{
		resource.SetupGated,
		scope.SetupGated,
		application.SetupGated,
		secret.SetupGated,
		providerconfig.SetupGated,
		role.SetupGated,
		user.SetupGated,
	} {
		if err := setup(mgr, o); err != nil {
			return err
		}
	}
	return nil
}

// SetupWebhookWithManager registers conversion webhooks for all resource kinds in the group.
func SetupWebhookWithManager(mgr ctrl.Manager) error {
	for _, setup := range []func(ctrl.Manager) error{
		resource.SetupWebhookWithManager,
		scope.SetupWebhookWithManager,
		application.SetupWebhookWithManager,
		secret.SetupWebhookWithManager,
		providerconfig.SetupWebhookWithManager,
		role.SetupWebhookWithManager,
		user.SetupWebhookWithManager,
	} {
		if err := setup(mgr); err != nil {
			return err
		}
	}
	return nil
}
