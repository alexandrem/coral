# Testing Coral

Run these checks from the repository root:

```bash
make test
make lint
make docs-check
```

`make test` generates required assets before running the Go tests. `make lint`
checks Go code, and `make docs-check` validates the user documentation and CLI
reference. Each command returns a nonzero exit status when its checks fail.

## Flamegraph interaction tests

Flamegraph interaction tests execute the generated JavaScript against a small
DOM model using the Deno runtime already downloaded by `make generate`. These
tests also run in CI short mode and require no browser installation.

To additionally check the standalone SVG in a real Chromium browser, set
`CORAL_FLAMEGRAPH_BROWSER` to its executable path:

```bash
CORAL_FLAMEGRAPH_BROWSER=/path/to/chromium go test ./internal/flamegraph -run '^TestBrowser$' -count=1
```

This optional test runs headlessly and reports a failure if the interaction
checks fail. Without the environment variable, it is skipped.
