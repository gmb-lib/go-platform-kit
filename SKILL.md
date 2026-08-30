---
name: go-platform-kit
description: Conventions for using github.com/gmb-lib/go-platform-kit — the thin glue over Azugo that every backend service imports so config, telemetry, errors, correlation, and broker access are wired identically. Use when bootstrapping a service (platform.Setup), defining the base configuration, adding the correlation model, mapping DB result codes to HTTP errors, propagating correlation on outbound HTTP, or publishing/consuming broker events with the event envelope. Complements the azugo-framework skill (it does not replace it).
---

# go-platform-kit — Project Glue Over Azugo

`go-platform-kit` is a **library** (no runtime of its own). It standardizes how every
service turns on **Azugo's own** telemetry and adds the project glue Azugo cannot know
about: the correlation model, PII/secret log redaction, the broker event envelope, and
the error taxonomy. It **re-implements none** of Azugo's logger, metrics, or tracer — it
configures and wraps them.

> Read the **azugo-framework** skill first for app/route/config/handler structure. This
> skill only covers the `go-platform-kit` delta on top of it.

Module: `github.com/gmb-lib/go-platform-kit` · Pinned to `azugo.io/azugo` **v0.38.x** +
`azugo.io/core` **v0.38.x** + `azugo.io/opentelemetry` **v0.38.x** (bumped here once, inherited
transitively). The `go` directive is a **floor**, not a pin — the module builds with any newer
toolchain, and a consumer's own Go version is unaffected by it.

---

## Packages

| Import | Owns |
|---|---|
| `…/platform` | `Setup(app, Options)` — the single bootstrap entrypoint (`PublicErrors`, `TracingOptions`, `OnFailure`) |
| `…/config` | `BaseConfiguration` (embeds Azugo config) + the standard env |
| `…/observability` | logger redaction, metric naming helpers, `EnableTracing`, outbound HTTP-client tracing |
| `…/correlation` | `correlation_id`/`trace_id` middleware + context helpers, and the forwarded `app_instance_id` (§3.1) |
| `…/errors` | the RFC 9457 `problem+json` envelope (`Problem`/`PublicProblem`), produce (`NewProblem`) + relay (`ParseProblem`/`Relay`), the uniform renderer, the `err:domain:reason` taxonomy, and the `FailureHook` error-audit seam (§4.5) |
| `…/broker` | `Publisher`/`Consumer` over the frozen event envelope |
| `…/httpclient` | outbound defaults + correlation/app-instance header propagation (`CorrelationOptions`, `SetCorrelationHeaders`) |
| `…/propagation` | dependency-free leaf (stdlib only): carries the correlation id **and the app instance id** across a hop that only sees a `context.Context` — the on-behalf/DPoP client and background jobs |

---

## 1. Bootstrap — `platform.Setup`

A service makes **one call** in its `App.init()`, right after `server.New(...)`. After it
returns, the service has standardized logging+redaction, metrics, tracing, and the
correlation middleware installed — no copy-paste.

```go
import (
    "azugo.io/azugo"
    "azugo.io/azugo/server"
    "github.com/gmb-lib/go-platform-kit/platform"
)

func New(cmd *cobra.Command, version string) (*App, error) {
    config := NewConfiguration() // embeds *pkconfig.BaseConfiguration
    a, err := server.New(cmd, server.Options{
        AppName:       "Document Service",
        AppVer:        version,
        Configuration: config,
    })
    if err != nil {
        return nil, err
    }

    instance := &App{App: a, config: config}
    if err := instance.init(); err != nil {
        return nil, err
    }
    return instance, nil
}

func (a *App) init() error {
    // FIRST thing after server.New — before any service routes/middleware.
    if err := platform.Setup(a.App, platform.Options{
        Config: a.config.BaseConfiguration,
        // Redaction: customPolicy, // optional; defaults to the fleet policy
    }); err != nil {
        return err
    }

    // …service-specific wiring (stores, routes, go-authbyte, audit emitters)…
    return nil
}
```

`Setup` wires, in order: **(1)** OpenTelemetry tracing (so trace ids exist), **(2)** log
redaction, **(3)** the correlation middleware, and **(4)** the uniform error renderer (§4) — so
every error response is one `application/problem+json` envelope, logged once and correlated. Call
it **before** registering service routes so correlation and the renderer wrap them.

> **⚠ HARD RULE — never call `opentelemetry.Use` yourself.** `platform.Setup` already enables tracing
> (step 1); a second call double-registers the trace middleware. Configure tracing via `OTEL_*` env vars
> only. Holds for every `go-platform-kit` service.

**Need custom instrumentation** (e.g. DB query tracing) beyond the built-in router/HTTP-client/cache
spans? Pass it through `Options.TracingOptions` — `Setup` forwards the `opentelemetry.Option` values to
the one `opentelemetry.Use` it owns, so you keep `Setup` instead of dropping to a hand-rolled `Use`
(the hard rule above). Most services leave it nil.

```go
platform.Setup(a.App, platform.Options{
    Config:         a.config.BaseConfiguration,
    TracingOptions: []opentelemetry.Option{opentelemetry.InstrumentationRecorder("db", dbRecorder)},
})
```

Set `Options.PublicErrors: true` on the **one** public-facing boundary service (e.g. a BFF) so its
error responses are projected to the public shape (`source`/`chain` dropped); leave it `false` on
internal services so they return the full envelope for relay + logging (§4).

Set `Options.OnFailure` only if the service keeps its own record of the errors it returns (§4.5).
Left nil — the normal case — no hook runs and the path is inert.

---

## 2. Base configuration — `config.BaseConfiguration`

Embed `*config.BaseConfiguration` instead of Azugo's `*config.Configuration`. It carries
the standard fleet env and already satisfies Azugo's `Configurable` (promoted
`ServerCore`). Always call `c.BaseConfiguration.Bind("", v)` from your `Bind`.

```go
import (
    pkconfig "github.com/gmb-lib/go-platform-kit/config"
    "azugo.io/core/validation"
    "github.com/spf13/viper"
)

type Configuration struct {
    *pkconfig.BaseConfiguration `mapstructure:",squash"`

    PostgresDSN string `mapstructure:"postgres_dsn" validate:"required"`
}

func NewConfiguration() *Configuration {
    return &Configuration{BaseConfiguration: pkconfig.New()}
}

func (c *Configuration) Bind(_ string, v *viper.Viper) {
    c.BaseConfiguration.Bind("", v)            // standard env first
    _ = v.BindEnv("postgres_dsn", "POSTGRES_DSN")
}

func (c *Configuration) Validate(valid *validation.Validate) error {
    if err := c.BaseConfiguration.Validate(valid); err != nil {
        return err
    }
    return valid.Struct(c)
}
```

### Standard env contributed by the base config

| Env | Purpose |
|---|---|
| `SERVICE_NAME` | broker client id + default project-metric label (**required**) |
| `ENVIRONMENT` | **Azugo's own** var: `development`/`test`/`staging`/`production`. Drives the `service.environment` log field and the OTel `deployment.environment` via `app.Env()`. The kit does **not** re-declare it — set Azugo's vocabulary, not `local`/`prod` |
| `LOG_LEVEL`, `LOG_FORMAT` | Azugo log policy (`ecsjson` default outside `development`) |
| `METRICS_ENABLED` | Azugo metrics toggle |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP collector — **unset ⇒ tracing off** |
| `OTEL_SDK_DISABLED`, `OTEL_RESOURCE_ATTRIBUTES` | Standard OTel SDK knobs |
| `ELASTIC_APM_SECRET_TOKEN(_FILE)` | Only if exporting to Elastic APM (secret) |
| `BROKER_URL` | Broker endpoint |
| `BROKER_TLS_CERT/KEY/CA(_FILE)` | Broker client TLS material (secret, via Vault) |

Secrets follow the Vault-agent convention: `<NAME>_FILE` points at the secret file
(`config.LoadRemoteSecret`). Each service still owns its own sub-config.

> `service.name`/`service.version` in logs come from Azugo's `server.Options.AppName`
> /`AppVer`, not `SERVICE_NAME` — set them consistently if you want them to match.

---

## 3. Correlation — the project-only piece

`platform.Setup` installs `correlation.Middleware()`. For every request it resolves the
`correlation_id` — the inbound `X-Correlation-ID` header, else **Azugo's own per-request
id (`ctx.ID()`)** rather than a parallel ULID — adopts the OTel `trace_id`/`span_id`,
binds all three to the context, **adds them to every log line emitted via `ctx.Log()`**,
and echoes `X-Correlation-ID` on the response.

> Note: Azugo's built-in **access log** (`middleware.RequestLogger`) writes through the *app*
> logger, not `ctx.Log()`, so it carries only its own `http.request.id`, never a
> `correlation_id`/`trace_id` field. On the **entry** service that id *equals* `correlation_id`
> (the kit adopts `ctx.ID()`), so it joins the access line to the handler/audit lines. On a
> **downstream** hop — where an inbound `X-Correlation-ID` is present — `correlation_id` is that
> threaded value while the access line keeps the service's *own* `ctx.ID()`, so the two
> **diverge** and the access line is not joinable by the propagated id there. The reliable
> cross-service key is a `ctx.Log()` line (handler or the rendered error), which carries **both**
> the local `http.request.id` and the threaded `correlation_id`/`trace_id`.

In handlers, read the ids and pass them onward:

```go
import "github.com/gmb-lib/go-platform-kit/correlation"

func (r *router) handler(ctx *azugo.Context) {
    cid := correlation.ID(ctx)          // the correlation id
    ids := correlation.FromContext(ctx) // {CorrelationID, TraceID, SpanID}
    _ = cid; _ = ids
}
```

The same ids ride outbound HTTP (§5), broker events (§6), and the audit envelope
(stamped by the emitter libraries) — so one incident is one correlated trail across logs,
traces, and all three audit regimes. **Do not** build your own request-id scheme.

### 3.1 App instance id — `X-App-Instance-Id`

A caller may identify the **installation of the calling application** with an
`X-App-Instance-Id` header. Where the correlation id answers "which request chain", this
answers "which installation" — so one device's calls are findable across services and over
time, which a per-request id cannot do.

The middleware validates it exactly like the correlation id (bounded length, safe charset —
`correlation.ValidID`) and then binds it once. Read it the same way:

```go
iid := correlation.AppInstanceID(ctx)              // "" when absent
iid = propagation.AppInstanceID(someContext)       // from a context-only hop
ctx2 := propagation.WithAppInstanceID(ctx, iid)    // for background work
```

Three rules that matter:

- **Never minted locally.** No inbound header (or an invalid one) means *no* value — absent
  is a supported state, and a service that does not install the middleware reports an empty
  id, which is the correct fail-closed behaviour.
- **Telemetry, not identity.** The value is opaque, client-supplied and unauthenticated.
  Any caller can send anything. It must **never** be an input to an authorization, quota, or
  trust decision, and it is not a device attestation.
- **Never a metric or log label.** It is per-installation and unbounded, so as a Prometheus
  label it multiplies every series by the number of devices. It belongs in the **log body**,
  as a **span attribute**, and in a **table column** — which is exactly where the kit puts
  it. Query it in Loki with `| json | app_instance_id="…"`.

What the middleware puts where, none of it individually opt-out: `correlation_id` on every
log line, echoed as a response header, and as a span attribute; `trace_id`/`span_id` on
every log line when tracing is active; `app_instance_id` on every log line and as a span
attribute **when the header was present**. With no caller sending it, the behaviour is
invisible.

---

## 4. Errors — one uniform envelope (RFC 9457 problem+json)

`platform.Setup` installs a renderer so **every** error response — your own or one relayed from
downstream — is a single `application/problem+json` envelope (RFC 9457). There is exactly one
wire shape; never hand-roll an error body or a second envelope. The envelope carries a stable
machine `code` (`err:domain:reason`), a human `title`, the `status`, an optional safe `detail`,
the originating `source`, the `trace_id`, and a bounded hop `chain`. The renderer also **logs
every error once, correlated** (code + status + trace id, via `ctx.Log()`; 5xx at `Error`, 4xx at
`Warn`), so a failure is always joinable to its request even when the handler logged nothing.

```go
import pkerrors "github.com/gmb-lib/go-platform-kit/errors"
```

### 4.1 Produce your own error — `NewProblem`

Status and title are **derived from the code's reason** (the taxonomy is the single source of
truth for status), so you give the code and hand it to `ctx.Error`:

```go
ctx.Error(pkerrors.NewProblem("err:document:notFound"))                    // 404, title "Not found"
ctx.Error(pkerrors.NewProblem("err:document:notFound",
    pkerrors.WithDetail("document "+id+" expired")))                       // + internal-only detail
ctx.Error(pkerrors.NewProblem("err:upload:invalid",
    pkerrors.WithPublicDetail("the file must be a PDF")))                  // detail survives the public boundary
```

Options: `WithDetail` (internal-by-default), `WithPublicDetail` (survives the public boundary),
`WithStatus`, `WithTitle`, `WithSource` (rare — the renderer fills it), `WithType`. The renderer
fills `source` + `trace_id`; never set those by hand for your own errors. An error carrying a
stable code via the `Coder` interface (`ErrorCode() string`) is rendered the same way.

**A reason outside the taxonomy table needs `WithStatus`** — the **title then follows that
status** (e.g. `WithStatus(409)` → "Conflict"), so `WithTitle` is optional; keep the precise code
either way: `NewProblem("err:document:legalHold", pkerrors.WithStatus(409))`.

### 4.2 DB result codes — `FromResultCode` / `HTTP`

Map the DB layer's namespaced result codes (`result_error('err:document:notFound', …)`) to the
mapped HTTP error and pass it to `ctx.Error`. **Never** return a raw DB error to the client.

```go
doc, code, err := r.Store().GetDocument(ctx, ctx.Params.String("id"))
if err != nil {
    ctx.Error(err)                            // unexpected — 500, logged, no leak
    return
}
if code != "" {
    ctx.Error(pkerrors.FromResultCode(code))  // e.g. err:document:notFound → 404
    return
}
```

Use `pkerrors.HTTP(domain, reason)` to classify without a DB code. Auth-specific mappings stay in
`go-authbyte`.

**Taxonomy — reason → status** (case-insensitive; `_`/`-`/space ignored):

| reason (+ synonyms) | status | title |
|---|---|---|
| `notFound` / `missing` / `unknown` | 404 | Not found |
| `unauthorized` / `unauthenticated` | 401 | Unauthorized |
| `forbidden` / `denied` / `notAllowed` | 403 | Forbidden |
| `conflict` / `alreadyExists` / `duplicate` | 409 | Conflict |
| `gone` / `expired` / `revoked` | 410 | No longer available |
| `invalid` / `validation` / `malformed` | 400 | Invalid request |
| `required` / `missingParameter` | 400 | Missing required parameter |
| *(reason outside the table)* | give `WithStatus`; title follows the status | |
| *(unmapped code at the renderer)* | 500 | Internal server error (raw code never leaks) |

Title-follows-status is defined for every standard status a service returns
(400/401/403/404/409/410/413/415/422/429/501/502/503/504), so a `WithStatus` for a
non-taxonomy reason still renders a sensible title without `WithTitle`.

#### 4.2.1 Extend the taxonomy — `RegisterReason`

A service with many fine-grained, per-call-site reasons (`proofInvalid`, `signingCertUnavailable`, …)
would otherwise repeat `WithStatus`/`WithTitle` at every call site — with nothing to stop two sites
drifting to different statuses for the same code. Teach the taxonomy the reason **once**, at startup:

```go
func (a *App) init() error {
    // BEFORE platform.Setup / any request.
    pkerrors.RegisterReason("proofInvalid", pkerrors.ReasonSpec{Status: 400, Title: "Invalid proof"})
    // …platform.Setup(…)…
}
```

After that, all three entry points derive the same status **and** title for the code — and render
identically on the wire, whether produced via `NewProblem("err:credential:proofInvalid")` or returned
via `FromResultCode`/`HTTP` to `ctx.Error`:

```json
{ "code": "err:credential:proofInvalid", "status": 400, "title": "Invalid proof" }
```

Rules:
- **Call once, at startup** (an `init()` or `App.init()` before `platform.Setup`); register each reason
  from a **single site** — re-registering does not panic (last spec wins), so a second site silently
  drifts, the very thing this removes.
- Matching is case- and separator-insensitive (same as built-ins). Registering a reason that
  collides with a **built-in** reason after normalization **panics** by default (fail-fast against
  accidental shadowing at startup). Pass `AllowBuiltinOverride()` to register it deliberately —
  the registration then **takes precedence** over the built-in bucket (e.g. `not-found` gets its
  own status/title instead of the built-in 404); anything left unregistered still falls through
  to the built-in set (the no-registration fallback). A `Status` outside `100–599` **panics**.
- **`Title` and any safe message are client-facing** (`Title` on the wire, the safe message becomes
  `detail`) — keep them client-safe: no PII, secrets, or internal names, same as any `SafeError`.

### 4.3 Relay a downstream error — never a bare 502

When an outbound call fails, decode the downstream problem and **relay** it: preserve the terminal
`code`/`source`/`trace_id`/`title`, append this service to the chain, and choose the outer status
deliberately. Do **not** collapse a parsed failure into a generic 502.

```go
if down, ok := pkerrors.ParseProblem(respBody); ok {
    ctx.Error(pkerrors.Relay(down, "portal-api", down.Status)) // relay status unchanged, or e.g. 424
    return
}
ctx.Error(pkerrors.Relay(nil, "portal-api", fasthttp.StatusBadGateway)) // non-conforming upstream
```

The `chain` is bounded automatically (the root hop is always kept, the middle elided) — the full
path is reconstructable from the logs by `trace_id`.

### 4.4 The public boundary — `PublicErrors`

The one public-facing service sets `platform.Options.PublicErrors: true` (§1). Its renderer emits
the **public projection** (`PublicProblem`): `source` and `chain` are *structurally absent* and
`detail` is withheld unless marked public-safe — a topology leak is impossible by construction, not
by remembering to clear a field. Internal services leave it `false` and return the full envelope so
a relaying service can attribute and extend it.

### 4.5 Auditing the errors you return — `FailureHook`

A service that must keep a record of every error it returns sets **one** hook at bootstrap. The
kit calls it and stores nothing itself — the service owns its store and its taxonomy:

```go
platform.Setup(a.App, platform.Options{
    Config:    a.config.BaseConfiguration,
    OnFailure: func(ctx *azugo.Context, evt pkerrors.FailureEvent) { audit.Queue(evt) },
})
```

**Fired only at the origin.** The renderer compares the problem's `source` to this service, so a
relayed downstream error is audited where it happened and *not* again at every hop that passes it
on. Without that rule one failure would produce one row per hop in the chain.

**`FailureEvent` is self-contained** — service, endpoint, router path, method, `err:domain:reason`
code, message, status, app instance id, correlation id, trace id, and a UTC timestamp. That is
deliberate: with the correlation and trace ids on the event, an audit row is one click into the
logs and the trace instead of a search over a guessed time window, and a hook can hand the
struct to a worker without ever touching the request context.

Three rules for the hook body, all of them consequences of *where* it runs:

- **It is synchronous, before the response is written.** Its latency is added to every error
  response. Hand the event to a bounded queue and let a worker persist it; dropping on a full
  queue beats delaying an error response. The most common cause of the 5xx you most want
  audited is the database — write inline and the audit insert is slow or down exactly when the
  service is already failing, on the same exhausted connection pool.
- **`ctx` must not outlive the call.** It is pooled and released when the request completes.
  Never retain it, never hand it to a goroutine, never use it as the context of a deferred
  write — copy from the `FailureEvent`, which is sufficient on its own.
- **Do not panic; swallow your own errors.** The renderer recovers a panicking hook and logs it,
  but that guard exists because this code runs inside the framework's own panic recovery where a
  second panic has no net above it — it is a backstop, not a contract to lean on.

Router 404s do not reach the error handler, so unauthenticated scanner traffic does not drive
rows through this path.

---

## 5. Outbound HTTP — correlation propagation

For service-to-service calls use `ctx.HTTPClient()`, not a hand-rolled client.
`go-platform-kit` adds the `correlation_id` header, and the `app_instance_id` header when the
inbound request carried one; W3C `traceparent` is injected automatically by
`azugo.io/opentelemetry`; `go-authbyte` adds the DPoP-bound token.

```go
import "github.com/gmb-lib/go-platform-kit/httpclient"

func (c *DocumentClient) Fetch(ctx *azugo.Context, id string) (*Doc, error) {
    client := httpclient.Outbound(ctx, c.baseURL) // == ctx.HTTPClient().WithBaseURL(...)
    var doc Doc
    opts := httpclient.CorrelationOptions(ctx) // correlation_id + app_instance_id (0-2 options)
    // opts = append(opts, authClient.AttachToken(ctx)) // go-authbyte attaches DPoP + token
    err := client.GetJSON("/v1/documents/"+id, &doc, opts...)
    return &doc, err
}
```

**Forwarding matters more than it looks.** The app instance id is never minted, so a hop that
drops it makes the id stop at the edge service — and a failure audited deeper in the chain
then records an empty instance, which is the one case the feature exists for. Any client that
builds its own request instead of passing options must set both headers, and there is one
helper that knows how so the two paths cannot drift:

```go
req := ctx.HTTPClient().NewRequest(...)          // hand-built: body, content-type, auth by hand
httpclient.SetCorrelationHeaders(ctx, &req.Header) // correlation_id + app_instance_id
```

For work with no inbound request to inherit from — a background job kicked off by a
device-originated call — carry the ids on the context instead
(`propagation.WithCorrelationID` / `propagation.WithAppInstanceID`, §3.1).

### Bespoke clients that bypass `ctx.HTTPClient()`

When a call cannot go through `ctx.HTTPClient()` — a third-party SDK that owns its
`http.Client`, or an external API client built at startup — instrument that client so
its calls still open OpenTelemetry **client spans** and inject the W3C trace context.
Service-to-service hops then continue the same trace; external hops show up in the
service graph.

```go
import "github.com/gmb-lib/go-platform-kit/observability"

// Instrument a service's own client in place (allocates one when nil):
client := observability.InstrumentHTTPClient(&http.Client{Timeout: 10 * time.Second})

// Or wrap only the transport — e.g. for an SDK that takes a RoundTripper
// (nil base ⇒ http.DefaultTransport):
rt := observability.InstrumentedTransport(nil)
```

Safe to apply unconditionally: with no exporter or active span it is a no-op. It
carries the **trace context only** — the `correlation_id` header rides on
`ctx.HTTPClient()` calls (via `CorrelationOptions`), so a bespoke client that must
propagate it has to set that header itself.

---

## 6. Broker — the event envelope

Audit/security emitters (`go-eidas-audit`, `go-gdpr-audit`, `go-sec-events`) build on
these helpers; a service rarely publishes directly. The `Envelope` is the **frozen
schema**; `Publisher.Publish` stamps `event_id` (ULID), `occurred_at`, and
correlation/trace ids, validates, and strips any bearer-token-shaped attributes —
**events carry correlation, never tokens**.

```go
import "github.com/gmb-lib/go-platform-kit/broker"

pub := broker.NewPublisher(transport, cfg.ServiceName) // transport: your broker client

func (r *router) onPreviewed(ctx *azugo.Context, env, doc string) error {
    return pub.Publish(ctx, "signing.events", &broker.Envelope{
        EventType:  "document.previewed",
        Categories: []broker.Category{broker.CategorySigning},
        Actor:      &broker.Actor{ID: ctx.User().ID(), Type: "user"},
        Resource:   &broker.Resource{Type: "document", ID: doc},
        Operation:  broker.OpRead,
        Outcome:    broker.OutcomeSuccess,
        Attributes: map[string]any{"envelope_id": env}, // no PII, no document content
    })
}
```

Consume idempotently (at-least-once delivery assumed). The event id is marked processed
**only after the handler succeeds** — a failed handling is redelivered, so the handler
itself must be idempotent (e.g. `INSERT … ON CONFLICT (event_id) DO NOTHING`):

```go
store := broker.NewMemoryIdempotencyStore() // bounded FIFO; back with Redis for multi-replica
err := broker.Dispatch(ctx, payload, store, func(ctx context.Context, ev *broker.Envelope) error {
    // idempotent handling, keyed on ev.EventID
    return nil
})
```

`Transport` is an interface (`Publish(ctx, topic, key, payload)`) — inject your broker
client; the core `broker` package stays transport-agnostic glue.

### NATS JetStream (`broker/natsbroker`)

The concrete NATS JetStream implementation lives in the **opt-in** subpackage
`broker/natsbroker` — the one place that imports `nats.go`. Import it only in services
that talk to NATS (producers + sinks); services that don't never pull the dependency, so
the core `broker` package stays client-free.

```go
import "github.com/gmb-lib/go-platform-kit/broker/natsbroker"

// Producer: publish over JetStream (Msg-Id = event id → server-side dedup backstop).
conn, _ := natsbroker.Connect(natsbroker.Config{URL: cfg.Broker.URL, Name: cfg.ServiceName})
pub := broker.NewPublisher(natsbroker.NewTransport(conn), cfg.ServiceName)

// Sink: ensure the stream, then run a durable pull consumer driving broker.Dispatch.
_ = conn.EnsureStream(ctx, natsbroker.StreamConfig{
    Name: "AUDIT", Subjects: []string{"audit.>"}, Duplicates: 2 * time.Minute,
    MaxBytes: 128 << 20, // 0 = unlimited; at the cap the OLDEST messages go
})
cons, _ := natsbroker.NewConsumer(ctx, conn, natsbroker.ConsumerConfig{
    Stream: "AUDIT", Durable: "eidas-audit", FilterSubject: "audit.signing",
}, store, handler, log)
_ = cons.Start(ctx) // success acks; any error naks → JetStream redelivers. Stop() to halt.
```

Connection material comes from the platform `Broker` config (`BROKER_URL` / `BROKER_TLS_*`).
`Consumer` is framework-agnostic (`Start`/`Stop`), so the same code runs standalone or
bundled inside another service's `core.Tasker`.

**Bound the stream where the durable record lives elsewhere.** A stream whose events are also
landed in a database is a replay buffer, and an unbounded one fills the node it runs on: give it
`MaxBytes` (the sink's own env knob, so a deployment can size it) and leave the discard policy at
old-first, which `EnsureStream` sets — the alternative is a full stream that refuses the newest
events, the one failure a bounded audit copy must not have. Leave `MaxBytes` at 0 only where the
stream itself *is* the record. `EnsureStream` **updates** a stream that already exists, so adding a
cap takes effect at the next start of the sink; it does not need the stream deleted.

---

## 7. Logging & redaction — automatic

After `Setup`, redaction is **always on**. Use `ctx.Log()` as normal; the redacting core
**drops** credential/secret/document-content fields and **masks** free-text PII before
they reach the sink — a handler cannot accidentally log a token.

```go
ctx.Log().Info("issued token",
    zap.String("authorization", tok), // DROPPED
    zap.String("email", subjectEmail), // MASKED → "[REDACTED]"
    zap.String("document_id", id),     // kept
)
```

Override the policy via `platform.Options.Redaction` only to **add** keys, never to weaken
the defaults. Metric naming helpers live in `observability` (`IncCounter`,
`ObserveSeconds`) on Azugo's VictoriaMetrics registry.

---

## Non-goals (do not add here)

No business/domain logic; no auth (that's `go-authbyte`); no audit/security **emission**
(those libraries ride this glue); no data access; no forking Azugo's logger/metrics/tracer.
If it is not a genuine every-service concern, it does not belong in `go-platform-kit`.

---

## Summary

| Concern | API | Pattern |
|---|---|---|
| Bootstrap | `platform.Setup(app, Options)` | one call in `App.init()` after `server.New` |
| Base config | `config.New()` / `*config.BaseConfiguration` | embed + `BaseConfiguration.Bind/Validate` |
| Correlation | `correlation.ID/FromContext` | middleware auto-installed by Setup |
| App instance | `correlation.AppInstanceID` / `propagation.WithAppInstanceID` | forwarded, never minted; telemetry, never an authz input (§3.1) |
| Errors | `errors.NewProblem` / `FromResultCode` / `ParseProblem`+`Relay` | one `problem+json` envelope; `ctx.Error(...)` renders + logs it |
| Error audit | `Options.OnFailure` → `errors.FailureHook` | fires only where the error originates; non-blocking, `ctx` must not escape (§4.5) |
| Outbound | `httpclient.Outbound` + `CorrelationOptions` (or `SetCorrelationHeaders`) | over `ctx.HTTPClient()`; forwards both ids |
| Broker | `broker.NewPublisher` / `broker.Dispatch` | `Envelope`, idempotent consume |
| Redaction | automatic | `ctx.Log()`; policy via `Options.Redaction` |
