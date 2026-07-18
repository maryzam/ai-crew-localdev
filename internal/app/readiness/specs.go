package readiness

import (
	"fmt"
	"sort"
	"strings"
)

type Severity string

const (
	SeverityRequired Severity = "required"
	SeverityAdvisory Severity = "advisory"
)

type Owner string

const (
	OwnerHost    Owner = "host"
	OwnerConfig  Owner = "config"
	OwnerBroker  Owner = "broker"
	OwnerRuntime Owner = "runtime"
)

type Spec struct {
	Key      string
	Owner    Owner
	Severity Severity
	Verifies string
}

const binaryFamilyKey = "binary-*"

const (
	DocBeginMarker = "<!-- BEGIN generated: readiness-checks (regenerate with `make readiness-docs`) -->"
	DocEndMarker   = "<!-- END generated: readiness-checks -->"
)

var specs = []Spec{
	{"runtime-dir", OwnerHost, SeverityRequired, "XDG_RUNTIME_DIR exists and is a directory"},
	{"broker-socket", OwnerBroker, SeverityRequired, "The broker socket exists and is a Unix domain socket"},
	{"broker-reachability", OwnerBroker, SeverityRequired, "The broker answers a health check on its socket"},
	{"broker-socket-env", OwnerBroker, SeverityRequired, "The configured broker socket path is absolute and valid"},
	{"repo-remote", OwnerConfig, SeverityRequired, "The repository has an HTTPS origin remote (not SSH)"},
	{"broker-configuration-recovery", OwnerConfig, SeverityRequired, "Governance configuration loads with owner-only access"},
	{"broker-identities", OwnerConfig, SeverityRequired, "The identities file exists and is valid"},
	{"broker-policy", OwnerConfig, SeverityRequired, "The policy file exists and is valid"},
	{"broker-pem-files", OwnerBroker, SeverityRequired, "Each agent PEM is a key the broker can load"},
	{"broker-pem-rotation", OwnerBroker, SeverityAdvisory, "No agent PEM is past the rotation reminder age"},
	{"broker-policy-providers", OwnerConfig, SeverityRequired, "Policy provider configs parse for every provider"},
	{"broker-provider-fields", OwnerConfig, SeverityRequired, "Required provider readiness fields are set"},
	{binaryFamilyKey, OwnerHost, SeverityRequired, "Required binaries are installed and executable"},
	{"container-workspace", OwnerRuntime, SeverityRequired, "The workspace directory is set and mountable"},
	{"container-runtime", OwnerRuntime, SeverityRequired, "The container runtime and devcontainer CLI are present"},
}

func lookup(key string) Spec {
	for _, sp := range specs {
		if sp.Key == key {
			return sp
		}
	}
	if strings.HasPrefix(key, "binary-") {
		return Spec{Key: key, Owner: OwnerHost, Severity: SeverityRequired}
	}
	return Spec{Key: key}
}

func Classify(checks []Check) {
	for i := range checks {
		sp := lookup(checks[i].Name)
		checks[i].Owner = sp.Owner
		checks[i].Severity = sp.Severity
	}
}

func DocMarkdown() string {
	rows := make([]Spec, len(specs))
	copy(rows, specs)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	var builder strings.Builder
	builder.WriteString("| Check | Owner | Severity | What it verifies |\n")
	builder.WriteString("|-------|-------|----------|------------------|\n")
	for _, sp := range rows {
		builder.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s |\n", sp.Key, sp.Owner, sp.Severity, sp.Verifies))
	}
	return builder.String()
}
