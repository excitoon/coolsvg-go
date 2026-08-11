# Contributing

`coolsvg-go` requires Go 1.26 or newer. Before submitting a change, run:

```sh
gofmt -w .
go mod tidy
go vet ./...
go test -race ./...
```

On AMD64, also exercise the optional vector backend:

```sh
GOEXPERIMENT=simd go test ./...
```

New parser or renderer behavior should include a focused synthetic fixture.
Keep confidential, proprietary, and application-specific SVGs out of the
repository and its history.

Commit subjects must be one-line Conventional Commits, such as
`fix: preserve gradient coordinates across tiles`. Do not add co-author
trailers. Contributions are licensed under the terms in [LICENSE.md](LICENSE.md).
