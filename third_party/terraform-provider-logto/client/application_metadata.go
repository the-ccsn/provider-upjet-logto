package client

import (
	"encoding/json"
	"errors"
	"slices"
)

var applicationMetadataFields = map[string][]string{
	"oidcClientMetadata":   {"backchannelLogoutUri", "backchannelLogoutSessionRequired", "logoUri"},
	"customClientMetadata": {"idTokenTtl", "refreshTokenTtl", "refreshTokenTtlInDays", "alwaysIssueRefreshToken", "rotateRefreshToken", "allowTokenExchange"},
}

func ValidateApplicationMetadata(kind, encoded string) (map[string]any, error) {
	var value map[string]any
	if json.Unmarshal([]byte(encoded), &value) != nil || value == nil {
		return nil, errors.New("metadata must be a JSON object")
	}
	fields, ok := applicationMetadataFields[kind]
	if !ok {
		return nil, errors.New("unsupported application metadata contract")
	}
	for key := range value {
		if !slices.Contains(fields, key) {
			return nil, errors.New("metadata contains unsupported fields or fields managed by dedicated attributes")
		}
	}
	return value, nil
}

func (m *ApplicationModel) UnmarshalJSON(data []byte) error {
	type wire ApplicationModel
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*m = ApplicationModel(value)
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for kind, fields := range applicationMetadataFields {
		metadata, _ := raw[kind].(map[string]any)
		extra := map[string]any{}
		for _, key := range fields {
			if v, ok := metadata[key]; ok {
				extra[key] = v
			}
		}
		if kind == "oidcClientMetadata" {
			m.OidcClientMetadataExtra = extra
		} else {
			m.CustomClientMetadataExtra = extra
		}
	}
	return nil
}

func mergeApplicationMetadata(payload map[string]any, kind string, extra map[string]any) error {
	if extra == nil {
		return nil
	}
	for key := range extra {
		if !slices.Contains(applicationMetadataFields[kind], key) {
			return errors.New("unsupported application metadata field")
		}
	}
	metadata, _ := payload[kind].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
	}
	for key, value := range extra {
		metadata[key] = value
	}
	payload[kind] = metadata
	return nil
}
