## What and why

<!-- One or two sentences. Link the issue if there is one. -->

## Integration

- [ ] opnsense
- [ ] wifictl
- [ ] deploy/docker
- [ ] shared code (`internal/`, `cmd/`), CI or catalog

## Evidence

<!-- Hardware and firmware it was run against, and what NetPulse showed.
     New parsing needs a redacted fixture under testdata/ or a test that
     covers the response shape. Keep units, absence and empty results honest
     (docs/external-integrations.md in the NetPulse repo). -->

## Checklist

- [ ] `gofmt -l .` is empty, `go vet ./...` and `go test ./...` pass
- [ ] `python3 .github/validate_vendors.py` passes
- [ ] README and CHANGELOG of the integration updated
- [ ] No credentials, tokens, public IPs or real MACs in code, fixtures or logs
