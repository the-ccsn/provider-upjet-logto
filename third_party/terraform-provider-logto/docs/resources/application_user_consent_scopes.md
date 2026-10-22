---
page_title: "logto_application_user_consent_scopes Resource - logto"
description: |-
  Manage the allowed user profile consent scopes of a third-party application.
---

# logto_application_user_consent_scopes (Resource)

Authoritatively manages an application's user profile consent scopes. Create one
resource per application. API resource and organization consent scopes are preserved.
This configures what the application may request; users still authorize access on
the consent screen. `openid` is implicit and must not be included in `user_scopes`.

Scope validation follows Logto 1.40.1's `UserScope` contract: `profile`, `email`,
`phone`, `address`, `custom_data`, `identities`, `roles`,
`urn:logto:scope:organizations`, `urn:logto:scope:organization_roles`, and
`urn:logto:scope:sessions`. Unsupported values, including
`urn:logto:scope:trusted_devices`, are rejected before API calls so an invalid
target cannot revoke existing grants.

```terraform
resource "logto_application_user_consent_scopes" "cloudflare_access" {
  application_id = logto_application.cloudflare_access.id
  user_scopes    = ["profile", "email"]
}
```

## Schema

### Required

- `application_id` (String) Application ID. Changing it requires replacement.
- `user_scopes` (Set of String) Allowed user profile scopes. An empty set revokes all user profile scopes.

### Read-Only

- `id` (String) The application ID.

## Import

Import using the application ID. Import adopts all existing user profile scopes.
Updates reconcile grants in place; deletion revokes user profile scopes without
deleting the application. Out-of-band changes are detected on refresh.

```sh
terraform import logto_application_user_consent_scopes.cloudflare_access APPLICATION_ID
```
