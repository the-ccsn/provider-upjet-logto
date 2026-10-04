package tfgen

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-codegen-spec/spec"
)

func Load(ctx context.Context) (*spec.Specification, *spec.Specification, error) {
	content, err := os.ReadFile("provider_code_spec.json")
	if err != nil {
		return nil, nil, err
	}
	specification, err := spec.Parse(ctx, content)
	if err != nil {
		return nil, nil, err
	}

	content, err = os.ReadFile("config/provider_code_extra.json")
	if err != nil {
		return nil, nil, err
	}
	extra, err := spec.Parse(ctx, content)
	if err != nil {
		return nil, nil, err
	}

	return &specification, &extra, nil
}
