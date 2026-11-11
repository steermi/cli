# Third-party notices

The steermi CLI is licensed under Apache-2.0. Release binaries statically
link the Go modules below; each keeps its own license, reproduced in the
module source at the listed version. Test-only modules are not linked into
release binaries.

| Module | Version | License |
|---|---|---|
| `github.com/steermi/agent-contracts` | see `go.mod` | Apache-2.0 |
| `github.com/mark3labs/mcp-go` | v0.45.0 | MIT |
| `github.com/yuin/goldmark` | v1.8.2 | MIT |
| `github.com/google/uuid` | v1.6.0 | BSD-3-Clause |
| `golang.org/x/sys` | v0.41.0 | BSD-3-Clause |
| `golang.org/x/text` | v0.39.0 | BSD-3-Clause |
| `github.com/invopop/jsonschema` | v0.13.0 | MIT |
| `github.com/wk8/go-ordered-map/v2` | v2.1.8 | Apache-2.0 |
| `github.com/bahlo/generic-list-go` | v0.2.0 | BSD-3-Clause |
| `github.com/buger/jsonparser` | v1.1.1 | MIT |
| `github.com/mailru/easyjson` | v0.7.7 | MIT |
| `github.com/spf13/cast` | v1.7.1 | MIT |
| `github.com/yosida95/uritemplate/v3` | v3.0.2 | BSD-3-Clause |
| `gopkg.in/yaml.v3` | v3.0.1 | MIT and Apache-2.0 |
| Go standard library | toolchain | BSD-3-Clause |

Test only: `github.com/stretchr/testify` (MIT), `github.com/davecgh/go-spew`
(ISC), `github.com/pmezard/go-difflib` (BSD-3-Clause).

Release archives include the full license text of every linked module under
`licenses/` (`make licenses` collects them with go-licenses) and an SBOM.
Update this table when `go.mod` changes; `go version -m bin/steermi` lists
the linked modules.
