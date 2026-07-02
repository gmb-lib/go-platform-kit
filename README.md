# go-platform-kit

The thin, project-specific glue over [Azugo](https://azugo.io) that every backend service
imports so that **config, telemetry, errors, correlation, and broker access are wired
identically** across the fleet.

It does **not** replace Azugo's telemetry — it standardizes how every service turns it on
and adds the project glue Azugo cannot know about (the correlation model linking a trace
to the three audit regimes, PII/secret log redaction, and the frozen broker event
envelope). It re-implements none of Azugo's logger, metrics, or tracer.

See [`SKILL.md`](./SKILL.md) for usage conventions.

**Scope (a v1 commitment):** this kit targets [Azugo](https://azugo.io) services. Its
entrypoints take `*azugo.App` / `*azugo.Context` by design, and it is version-pinned in
lockstep with `azugo.io/*`. It is not a general-purpose Go toolkit; non-Azugo stacks should
implement the (small, documented) envelope contract directly.

**Event envelope stability:** the `broker.Envelope` JSON schema is **append-only** —
new optional fields may be added; existing fields and attribute keys are never renamed
or removed. A schema test pins the struct's field set, so a rename or removal fails CI.
`Envelope.DataSubjects` values must be **pseudonymous internal identity references**,
never national identifiers, names, or e-mail addresses.

**Error envelope:** every service renders one RFC 9457 `application/problem+json` shape
(`errors.Problem`) with a stable machine `code` (`err:domain:reason`); the public boundary projects
it to a topology-free shape (`errors.PublicProblem`, dropping `source`/`chain`). It is
**append-only** in the same spirit as the event envelope. See [`SKILL.md`](./SKILL.md) §4 for the
produce/relay/render API.

## Install

```sh
go get github.com/gmb-lib/go-platform-kit
```

Pinned in lockstep to `azugo.io/azugo`, `azugo.io/core`, and `azugo.io/opentelemetry`.

## One-call bootstrap

```go
func (a *App) init() error {
    if err := platform.Setup(a.App, platform.Options{
        Config: a.config.BaseConfiguration,
    }); err != nil {
        return err
    }
    // …service-specific wiring…
    return nil
}
```

After `Setup` the service has standardized logging + redaction, metrics, OpenTelemetry
tracing, and the correlation middleware installed.

## Packages

| Package | Owns |
|---|---|
| `platform` | `Setup(app, Options)` — the single bootstrap entrypoint |
| `config` | `BaseConfiguration` + the standard fleet env |
| `observability` | log redaction, metric naming, OpenTelemetry enablement (incl. bespoke HTTP-client tracing) |
| `correlation` | `correlation_id`/`trace_id` middleware + context helpers |
| `errors` | the uniform RFC 9457 `application/problem+json` error envelope — produce (`NewProblem`), relay (`ParseProblem`/`Relay`), the one-install renderer, the public projection, and the `err:domain:reason` taxonomy |
| `broker` | `Publisher`/`Dispatch` + `IdempotencyStore` over the frozen event envelope (at-least-once handling, mark-after-success dedup) |
| `broker/natsbroker` | NATS JetStream concrete impl — publish `Transport` + durable pull `Consumer` + `Connect`/`EnsureStream`; **opt-in**, the only package importing `nats.go` |
| `httpclient` | outbound defaults + correlation header propagation |
| `propagation` | dependency-free leaf: carries the correlation id across a `context.Context`-only hop (the on-behalf/DPoP client, background jobs) |

## Develop

```sh
go build ./...
go test ./...
go vet ./...
```

## License

MIT — see [LICENSE](./LICENSE).
