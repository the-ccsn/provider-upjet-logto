# Terraform provider quality review

> Historical 2026-10-04 audit snapshot for the original six resources. New local
> configuration contracts and migration validation are recorded separately in
> the [Upjet migration validation report](https://github.com/the-ccsn/provider-upjet-logto/blob/main/docs/migration-validation.md).

Reviewed and hardened locally on 2026-10-04. The original audit coverage baseline
was 60.6% overall and 76.4% handwritten runtime; generated code is kept separate.

The six resources pass real Logto 1.40.1 protocol-v6 create/read/update/delete and
import/read acceptance. Additional real tests verify 101 scopes and user roles,
metadata preservation (TTL, refresh token settings and unmanaged profile), and
empty-collection contracts. Offline tests cover uninitialized reads, partial
creation state, bounded retry, invalid IDs, authentication and configuration.

CI now provisions pinned disposable Logto/PostgreSQL and generates temporary
loopback-only credentials. It never requires production credentials. The companion
Upjet project tests all six CRDs against a real local API server, the shipped
provider executable and real Logto, including references, drift, restart, Secrets
and revocation. Embedded OCI package and SPDX SBOM verification are implemented.

Publishing still requires configured ownership, required checks, signed fork
releases and a real Crossplane Core package installation/upgrade gate. Ambiguous
creation response loss requires inspection and Observe adoption before retry;
write retries cannot supply a missing server-side idempotency guarantee.
No production mutation, remote Git write or publication occurred.

Maintain source here first, then sync the reviewed snapshot and regenerate the
Upjet project. The joint evidence and operational procedures are in that project's
`docs/quality-review.md` and `docs/operations.md`.
