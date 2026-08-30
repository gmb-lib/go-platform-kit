# Changelog

Notable changes to this library, newest first. Versions are git tags; this file is written
for whoever bumps the dependency.

## v1.10.0

Additive: existing code compiles unchanged. One behaviour arrives on bump without any opt-in,
listed under *Changed*.

### Added

- **A size cap for a JetStream stream.** `natsbroker.StreamConfig` gains `MaxBytes int64`
  (0 = unlimited, unchanged from today's behaviour), so a sink whose durable record lives
  somewhere else — its database — can bound the stream that is only its replay buffer. An
  unbounded stream otherwise grows until it fills the node it runs on.

  ```go
  _ = conn.EnsureStream(ctx, natsbroker.StreamConfig{
      Name: "AUDIT", Subjects: []string{"audit.>"}, Duplicates: 2 * time.Minute,
      MaxBytes: 128 << 20, // at the cap the OLDEST messages are discarded
  })
  ```

  Size it from the replay window you want, not from a guess at total volume: measure the bytes
  your events actually take and multiply. Leave it 0 where the stream itself is the record.

### Changed

- **`EnsureStream` now sets the discard policy explicitly to old-first.** At the cap the oldest
  messages are dropped and publishing keeps succeeding; the alternative — refusing the newest
  events once the stream is full — is the failure mode a bounded copy of an audit trail must not
  have. This matches what an unconfigured stream already did, so nothing changes for a stream
  without a cap.

- **The framework and telemetry dependencies move a minor: `azugo.io/azugo`, `azugo.io/core` and
  `azugo.io/opentelemetry` → v0.38.x, OpenTelemetry → v1.46.0 (contrib v0.71.0), gRPC → v1.83.2.**
  Nothing in this library's own surface changes with them — build, vet, tests, linter and
  `go mod tidy -diff` all pass unchanged. Two things in the framework are worth knowing before you
  bump, since you use it directly too:

  - `user.Basic`'s `MarshalJSON` **moved to a pointer receiver**. A `Basic` *value* therefore no
    longer satisfies `json.Marshaler`, so marshalling one by value silently produces default field
    JSON instead of the custom form. Marshal `*Basic`. (Nothing in this library or in any service
    we build uses that type — checked — but the failure is silent, not a compile error, which is
    why it is called out.)
  - `azugo.io/core` gains a **`password` package** (argon2id hashing with rehash detection). New
    capability, nothing removed.

- **`EnsureStream` was already create-*or-update*, and this is now stated and tested:** a limit
  added to a stream that already exists takes effect at the sink's next start, without deleting
  the stream. Verified against a real server, including that the cap holds, that the oldest
  messages are the ones discarded, and that the newest survive.

## v1.9.0

Additive: existing code compiles unchanged. Three behaviours do arrive on bump without any
opt-in, listed under *Changed*.

### Added

- **App instance id propagation.** A caller may send `X-App-Instance-Id` to identify the
  installation of the calling application. Where the correlation id answers "which request
  chain", this answers "which installation" — so one installation's calls stay findable
  across services and over time, which a per-request id cannot do.

  ```go
  iid := correlation.AppInstanceID(ctx)           // "" when the caller sent none
  iid = propagation.AppInstanceID(someContext)    // from a hop that only has a context
  ctx = propagation.WithAppInstanceID(ctx, iid)   // seed background work
  ```

  New symbols: `correlation.HeaderAppInstanceID`, `correlation.AppInstanceID`,
  `correlation.LogKeyAppInstanceID`, `propagation.HeaderAppInstanceID`,
  `propagation.WithAppInstanceID`, `propagation.AppInstanceID`,
  `propagation.AppInstanceRequestValueName`.

  Three properties worth knowing before you use it:

  - **Never minted locally.** An absent or invalid header simply means no value. It is
    validated exactly like the correlation id (bounded length, safe charset —
    `correlation.ValidID`), and an invalid inbound value is dropped rather than passed on.
  - **Telemetry, not identity.** Opaque, client-supplied and unauthenticated: any caller can
    send anything, and a well-formed value proves nothing about the sender. It must never be
    an input to an authorization, quota, or trust decision, and it is not a device
    attestation.
  - **Never a metric or log *label*.** It is unbounded per installation, so as a Prometheus
    label it multiplies every series by the number of installations. It belongs in the log
    body, as a span attribute, and in a table column — which is where this library puts it.

- **`errors.FailureHook` and `errors.FailureEvent`**, wired through the new
  `platform.Options.OnFailure`. A service that must keep its own record of the errors it
  returns sets one hook; this library calls it and stores nothing itself, so the service keeps
  its own store and taxonomy.

  ```go
  platform.Setup(a.App, platform.Options{
      Config:    a.config.BaseConfiguration,
      OnFailure: func(ctx *azugo.Context, evt errors.FailureEvent) { audit.Queue(evt) },
  })
  ```

  It fires **only for errors this service originates** — a problem relayed from downstream
  keeps its original `source`, so one downstream failure is audited once, where it happened,
  not once per hop that passes it on. `FailureEvent` is self-contained (`Service`, `Endpoint`,
  `Method`, `Code`, `ErrorMessage`, `StatusCode`, `AppInstanceID`, `CorrelationID`, `TraceID`,
  `OccurredAt`), so a hook can hand the struct to a worker and never touch the request.

  The hook contract, all of it a consequence of *where* it runs:

  - It is **synchronous, before the response is written**, so its latency is added to every
    error response. Hand the event to a bounded queue; dropping on a full queue beats delaying
    an error response. The most common cause of the 5xx you most want recorded is the
    database — write inline and the audit write is slow or down exactly when the service is
    already failing, on the same exhausted pool.
  - The `*azugo.Context` **must not outlive the call**: it is pooled and released when the
    request completes. Never retain it or hand it to a goroutine — copy from `FailureEvent`.
  - It **must not panic** and should swallow its own errors. A panicking hook is recovered and
    logged, but that guard exists because this code runs inside the framework's own panic
    recovery, where a second panic would take the process down. It is a backstop, not a
    contract to lean on.

  With `OnFailure` left nil the whole path is inert.

- **`httpclient.SetCorrelationHeaders(ctx, *fasthttp.RequestHeader)`** — sets the correlation
  id and, when present, the app instance id directly on a raw request header. For a client
  that builds its own request rather than passing options, so both paths forward the same
  headers and cannot drift. A hop that forwards ids by hand and does not use this will drop
  the instance id, and the id then stops at that service.

- **`errors.Handler` accepts hooks variadically** — `Handler(source string, public bool,
  hooks ...FailureHook)`. Existing two-argument calls compile unchanged.

### Changed

Three deltas arrive on bump even with `OnFailure` nil and no caller sending the new header:

- **Every root span now carries a `correlation_id` attribute.** This fires on every request,
  in every service, and there is no opt-out short of not installing the correlation
  middleware. The framework's server-span builder does not capture request headers, so
  without this the id was visible only on whichever outbound call happened to carry it.
- **Log lines gain an `app_instance_id` field** when the inbound request carried a valid
  `X-App-Instance-Id`. With no caller sending it, nothing changes.
- **`httpclient.CorrelationOptions` may now return two options** instead of at most one. Both
  documented call shapes spread it variadically, so this is safe; code that indexes the slice
  or asserts its length is not.

Also:

- `errors.FailureEvent.OccurredAt` is **UTC** by construction, matching the convention the
  broker already applies to event timestamps.
- **`azugo.io/azugo`, `azugo.io/core` → v0.37.x, `azugo.io/opentelemetry` → v0.37.x.** This
  release of the framework fixes a context-lifecycle panic, which is why the bump rides along
  here. Transitively it also replaces the Redis client with a Valkey one; a service that
  addresses a TLS Redis endpoint should confirm it uses a `rediss://` URL.
- The `go` directive is **1.26.6** — a floor, not a pin. Your own toolchain is otherwise
  unaffected by it, and with the default `GOTOOLCHAIN=auto` an older toolchain upgrades itself.
- `README.md` and `SKILL.md` document the two features above; `SKILL.md`'s framework version
  line was stale by three minors and is now correct.

---

The entries below were reconstructed from git history rather than written at the time, so they
say what each tag contains, not why.

## v1.8.0

- `web.PathParam` — decodes a captured route parameter.
- `.gitattributes` for consistent line endings across platforms.

## v1.7.0

- CI: dependency management and testing workflows reworked; a dependency-review workflow for
  pull requests; release workflow checks and summaries.
- Go version and dependencies updated.

## v1.6.0

- Dependency and import updates.

## v1.5.0

- `errors`: `AllowBuiltinOverride` for reason registration, so a service can intentionally
  shadow a built-in reason.

## v1.4.0

- `errors.RegisterReason` — service-specific taxonomy reasons, with custom rendering.
- `platform.Options.TracingOptions` — forwards custom OpenTelemetry instrumentation options
  through `Setup`.
- `errors`: `toProblem` honours a `ResponseStatusCode` alongside `Coder`.
- Framework dependencies updated.

## v1.3.0

- `errors`: test coverage for error handling and status codes; README and SKILL documentation
  for the RFC 9457 envelope.

## v1.2.1

- `errors`: problem title handling and error logging improved.

## v1.2.0

- `errors`: the RFC 9457 `application/problem+json` envelope.
- Dependency updates.

## v1.1.0

- `broker`: `PublishStamped`.

## v1.0.0

First release under `github.com/gmb-lib/go-platform-kit`, carrying the append-only guard on
the broker event envelope. Earlier history lives under the module's previous import path.
