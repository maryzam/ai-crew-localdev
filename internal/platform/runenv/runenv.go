package runenv

import (
	"fmt"
	"os"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

func RequireManagedContainer() error {
	if os.Getenv(paths.EnvContainer) == "1" {
		return nil
	}
	return fmt.Errorf("managed runs are devcontainer-only on the supported path; start the devcontainer with ai-agent up and run ai-agent run inside it")
}
