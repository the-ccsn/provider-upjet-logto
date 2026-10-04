// Package provider exposes the Logto Framework provider for embedded consumers.
package provider

import (
	"github.com/Lenstra/terraform-provider-logto/internal/provider/provider_logto"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
)

// New returns an independent provider instance for each configuration.
func New(version string) func() fwprovider.Provider { return provider_logto.New(version) }
