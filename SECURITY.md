# Security policy

Forge is a personal project maintained for a specific deployment. Security work
is handled on a best-effort basis; no response or remediation service level is
promised.

## Supported versions

Only the current default branch and the latest published Forge release are
eligible for security fixes. Older releases and inherited Gitea releases are not
supported.

## Reporting a vulnerability

Do not disclose a suspected vulnerability, exploit, secret, or sensitive log in
a public issue or pull request.

Forge is establishing a private reporting channel. Until one is published here,
open a public issue containing only the title `Private security contact
requested`. Include no vulnerability details. The maintainer will arrange a
private channel before requesting technical information.

A useful private report includes the affected version or commit, impact,
reproduction steps, and any suggested mitigation. Reports may be acknowledged
publicly only with the reporter's permission.

## Inherited vulnerabilities

Forge originated from Gitea 1.27.2 but does not automatically ingest later Gitea
changes. Gitea advisories remain a source of security intelligence for inherited
code. Forge evaluates and implements relevant remedies independently.
