# muse2api public candidate

Date: 2026-11-18.

Build a standalone, single-user Muse HTTP adapter with browser-consented session handoff, encrypted local persistence, OpenAI-compatible chat/image/video routes, deployment examples, relay integration instructions and reusable skills. Existing private deployments are outside this project and must remain unchanged.

The protocol SDK is an external dependency pinned to a reviewed commit. Do not vendor old workspaces, browser profiles, private configuration, session snapshots, account identifiers, media, receipts, logs or production deployment files.

Acceptance: fresh configuration bootstrap; two listeners with administration restricted to loopback; one-time browser handoff; authenticated API; encrypted session storage and restart restoration; media submission deduplication and unknown delivery handling; portable configuration and examples; offline tests; extension/skill validation; source allowlist and secret scan; reviewed Git upload manifest. Live upstream calls require a separately authorized user login and are not implied by offline tests.

Publication is a separate final action after the candidate and its upload manifest can be reviewed. Upstream attribution and unresolved license provenance must be stated accurately.

Publication checklist: provide a copyable AI SOP prompt, contributor guidance, a maintainer CODEOWNERS rule and a pull request template. Configure required pull request review and passing offline checks on the public default branch, require approval before running workflows from outside contributors, then read back the actual GitHub settings and first CI result. Source changes and configuration files must pass the exact-index privacy audit before upload; private deployments remain outside this release.

Documentation follow-up: clarify the community project's relationship to Meta, its personal learning and self-hosted workflow focus, and the distinction between the MIT code license and upstream account/service entitlements. Preserve third-party attribution and avoid claiming official approval, legal compliance, unlimited access or an extra noncommercial license restriction. Publish this bounded documentation change through a reviewed PR without changing runtime or existing deployments.
