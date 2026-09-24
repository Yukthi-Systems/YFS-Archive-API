# Security Policy

## Supported versions

Security fixes are applied to the latest release and to `main`.

| Version        | Supported |
|----------------|-----------|
| Latest release | ✅        |
| `main`         | ✅        |
| Older releases | ❌        |

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub
issues, discussions, pull requests or Discord.**

Report them privately instead, in either of these ways:

- **GitHub:** [Report a vulnerability](https://github.com/Yukthi-Systems/YFS-Archive-API/security/advisories/new) (private security advisory)
- **Email:** [connect@yukthi.com](mailto:connect@yukthi.com), with the subject line `[SECURITY] YFS Archive API`

Please include:

- the affected version, commit or image tag,
- a description of the issue and its impact,
- steps to reproduce, or a proof of concept,
- any known mitigations.

## What to expect

| Step | Target |
|------|--------|
| Acknowledgement | within 3 business days |
| Initial assessment | within 7 business days |
| Fix or mitigation | depends on severity. We will keep you updated. |

We follow coordinated disclosure. Once a fix is released, we will publish a
GitHub Security Advisory and credit you, unless you prefer to stay
anonymous.

## Deployment hardening

This service is designed to sit behind a trusted boundary. See
[README → Security notes](README.md#security-notes). In short:

- never expose `POST /internal/archives` to the public internet,
- connect with a `SELECT`-only PostgreSQL role,
- keep `.env` and storage server keys out of version control.
