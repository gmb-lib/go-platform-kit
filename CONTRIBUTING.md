# Contributing

Thank you for considering a contribution. Bug reports, fixes and improvements are welcome. For
anything that could be exploited, use the private route in [SECURITY.md](SECURITY.md) — never a
public issue.

For anything larger than a small fix, please open an issue first and describe what you want to
change and why. It protects your time: a change that fights the library's design is better
redirected before it is written than after.

## Building and testing

You need the Go toolchain at the version named in [go.mod](go.mod). The gate a change must pass is
the same one CI runs:

```sh
go build ./...
go vet ./...
go test -race -count=1 ./...
```

Three more checks run in CI and are worth running before you push:

- **Lint** — `golangci-lint run`, at the version pinned in
  [.github/workflows/ci.yml](.github/workflows/ci.yml); the repo's [.golangci.yml](.golangci.yml)
  carries the configuration.
- **Vulnerabilities** — `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`.
- **Fuzz** — a short sweep runs every `Fuzz*` target for 30 seconds. This module has none today; if you add a parser for input that comes from outside, add one.

The committed tree must already be tidy: CI runs `go mod tidy -diff` and fails if it would change
anything, so run `go mod tidy` after touching dependencies. All Go code is `gofmt`-formatted, and
`.gitattributes` pins Go files to LF line endings — leave that alone, it keeps the tidy-diff gate
stable across platforms.

## What a change to this library needs

Every service in the fleet imports this module, so a change here lands everywhere at once. Two of
its jobs are security jobs, and they carry the highest bar:

- **Log redaction.** A change to the redactor, or to any type it walks, needs a test that the
  secret or personal-data field does not survive — including through nesting and custom types.
- **The public error projection.** The public shape exists to drop internal source, chain,
  topology and upstream detail. A change to the problem types or the renderer needs a test that the
  public projection still drops them.

Also load-bearing:

- **Both envelopes are append-only.** The broker envelope's field set is pinned by a schema test on
  purpose; adding an optional field is fine, renaming or removing one is not.
- **Correlation identifiers that arrive from outside are untrusted** until this kit decides
  otherwise; a change to propagation should say which side of that line each identifier is on.
- This kit is version-pinned in lockstep with the `azugo.io/*` line, and it deliberately
  re-implements none of Azugo's logger, metrics or tracer. Keep it glue.
- [`CHANGELOG.md`](CHANGELOG.md) is part of the change: consumers read it before bumping the pin.

## Proposing a change

- Work on a branch and open a pull request against `develop`. `develop` is merged into `main` and
  tagged there when a release goes out, so `main` is never committed to directly.
- **Sign off every commit.** This project uses the
  [Developer Certificate of Origin](https://developercertificate.org/): by adding a
  `Signed-off-by: Your Name <you@example.org>` line you certify that you wrote the change or
  otherwise have the right to submit it under this project's licence. `git commit -s` adds the line
  for you; the name and address must match the commit author. A pull request whose commits lack it
  fails the DCO check and cannot be merged.
- Keep the change focused: one concern per pull request.
- A change in behaviour comes with a test that fails without it.
- Match the style around you — naming, error handling, comment density. Comments explain what and
  why in plain domain terms; a reference to a standard is cited in the bracket form already used in
  the code.
- Pull requests also run a dependency review. A new dependency needs a reason the standard library
  or the existing ones cannot cover.

## Releases

A release is a tag on `main`, and for a Go module the tag *is* the publication — the release
workflow re-runs the gate, then checks the tagged version is actually importable from the module
proxy before declaring success. If your change is a breaking one, say so in the pull request: the
version it lands in is decided from that.

## Licence

This project is licensed under the MIT License (see [LICENSE](LICENSE)). By submitting a
contribution you agree that it is provided under the same licence.
