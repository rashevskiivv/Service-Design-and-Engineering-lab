# ADR 0002: SQLite driver is `modernc.org/sqlite` (pure Go, no CGO)

- Status: Accepted, 2026-09-30
- Deciders: Slava (owner), Architect agent

## Context

The PRD asks for a single static binary with state in one SQLite file. Jan builds Docker images and may deploy on linux/amd64 or linux/arm64 GPU hosts, and Slava develops on darwin/arm64. The database is tiny: fewer than 100 keys and a few thousand usage rows per lab. It sees roughly one write per request and a couple of indexed reads per request.

## Decision

Use **`modernc.org/sqlite`** (driver name `"sqlite"`), pinned to v1.60.x, and build with `CGO_ENABLED=0`.

- It is a CGo-free port of SQLite (3.53.4 in v1.60.1). It supports linux/amd64, linux/arm64 and darwin/arm64, among others ([pkg.go.dev](https://pkg.go.dev/modernc.org/sqlite)).
- Pragmas go in the DSN (`_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)`), and `_txlock=immediate` is supported ([sqlite.go](https://gitlab.com/cznic/sqlite/-/raw/master/sqlite.go)).
- Pool rules from the driver docs: bound pools with `SetMaxOpenConns`, and never share one connection between goroutines ([doc.go](https://gitlab.com/cznic/sqlite/-/raw/master/doc.go)). We use one writer pool (max 1 connection) and one reader pool (max 4).

## Alternatives considered

1. **`mattn/go-sqlite3` (CGO).** It is the reference and faster, but "you are required to set the environment variable `CGO_ENABLED=1` and have a `gcc` compiler". Cross-compiling needs a per-target C cross toolchain, and a static binary needs musl plus `-linkmode external -extldflags -static` ([README](https://github.com/mattn/go-sqlite3)). That breaks `GOOS=linux GOARCH=arm64 go build` from the Mac and the `FROM scratch/distroless` image.
2. **`ncruces/go-sqlite3` (WASM + wazero, also CGO-free).** Viable, but it pulls in a WASM runtime. modernc is more widely used with `database/sql`, and we need nothing it lacks.
3. **No SQLite (JSON file or in-memory only).** Loses usage stats and revocations on restart. The PRD requires SQLite.

## Consequences

- `CGO_ENABLED=0 GOOS=linux GOARCH={amd64,arm64} go build` works anywhere. The Docker runtime stage can be distroless-static.
- CPU-bound SQL runs about 1.3–2x slower than C SQLite, per the driver docs. That is irrelevant at our volume, since a request costs one indexed lookup and one insert.
- Keep `modernc.org/libc` at exactly the version in modernc.org/sqlite's `go.mod` ("Fragile modernc.org/libc dependency" in doc.go). Never `go get -u` it on its own.
- The binary grows by roughly 10 MB. That's acceptable.
- In Docker on macOS, put the DB on a **named volume**, not a bind mount. WAL needs shared-memory locking, and host bind mounts go through a VM file-sharing layer (UNVERIFIED on Docker Desktop's virtiofs; a named volume sidesteps it).
