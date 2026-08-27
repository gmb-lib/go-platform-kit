# Security policy

This is the glue every backend service in a fleet imports so that configuration, telemetry,
errors, correlation and broker access are wired the same way everywhere. Two of those jobs are
security jobs in their own right: it is the code that **redacts secrets and personal data out of
log lines**, and the code that **projects an internal error into the shape a public boundary is
allowed to return**. A gap in either one does not affect a single endpoint — it affects every
service that imports this module.

Please report security problems privately. Do not open a public issue, pull request or discussion
for anything that could be exploited before a fix exists.

## How to report

Use **[private vulnerability reporting](https://github.com/gmb-lib/go-platform-kit/security/advisories/new)**
on this repository. The report stays visible only to you and the maintainers until an advisory is
published, and it gives us one place to discuss and co-ordinate a fix with you.

Please include, as far as you can establish it:

- what the problem is, and what an attacker or an unintended reader gains from it;
- the smallest set of steps that reproduces it, and against which version or commit;
- the configuration it needs, if it only appears under particular settings;
- whether you have told anyone else, and whether a disclosure date already binds you.

Please redact anything real. If a finding is about a leak, the shape of the leaked value is what we
need, not the value.

## What happens next

- We acknowledge a report within **five working days**.
- We tell you whether we can reproduce it, and what we think its severity is, as soon as we know.
- We keep you updated while a fix is prepared, and we agree a disclosure date with you. Our default
  is to publish an advisory once a fix is available, and in any case within **90 days** of the
  report — earlier if the problem is already public or being exploited.
- We credit you in the advisory unless you would rather stay anonymous.

There is no bug-bounty programme. We are grateful anyway, and we say so publicly.

## What we consider most serious

- The public error projection leaking what it exists to drop: internal source, error chain,
  hostnames, upstream messages or stack detail reaching a public boundary through the public
  problem shape or the uniform renderer.
- Log redaction missing a secret or a personal-data field, so a token, key, password, national
  identifier, name or address survives into a log line — including through a nested structure, a
  custom type, or a value the redactor does not recognise as one of its patterns.
- A configuration value that carries a secret being logged, echoed in an error, or exposed by a
  diagnostic surface.
- A correlation, trace or app-instance identifier that an untrusted caller can set and that is then
  trusted or forwarded as if the service had generated it, or one that carries data it should not.
- The broker envelope losing its append-only guarantee — a field renamed or removed that a consumer
  already chains on — or `DataSubjects` carrying a direct identifier where a pseudonymous reference
  is required.
- The failure hook receiving, recording or forwarding material the error envelope was supposed to
  have stripped.

Denial of service and findings that need an already-compromised host are in scope but lower
priority. Reports about outdated dependencies are welcome where you can show the vulnerable path
is actually reachable.

## Scope

This policy covers the code in this repository. It does not cover Azugo or the `azugo.io/*` line
(report those to their maintainers), the log pipeline, broker or telemetry backends a deployment
points this kit at, or the services that import it. Whether a given service turns on the public
error projection is that service's configuration — but a report that a *default* is unsafe is very
much in scope.

## Supported versions

Security fixes land on the most recent release. Older tags are not patched; if you are pinned to
one, the fix is to move forward. Note that this module is version-pinned in lockstep with the
`azugo.io/*` line, so a fix may require moving that pin too.
