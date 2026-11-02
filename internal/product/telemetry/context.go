package telemetry

import (
	"errors"
	"fmt"

	"github.com/maryzam/ai-crew-localdev/internal/product/evidence"
)

const maxValueBytes = 512

type Context struct {
	RunID       string
	Task        string
	Project     string
	Repository  string
	Branch      string
	ImageDigest string
	EgressMode  string
	Agents      map[string]string
}

type attribute struct {
	key      string
	value    string
	required bool
}

func (c Context) attributes(agent string) []attribute {
	return []attribute{
		{"ai.run.id", c.RunID, true},
		{"ai.task", c.Task, true},
		{"ai.agent", agent, true},
		{"ai.agent.version", c.Agents[agent], false},
		{"ai.project", c.Project, true},
		{"ai.repository", c.Repository, false},
		{"ai.branch", c.Branch, false},
		{"ai.image.digest", c.ImageDigest, false},
		{"ai.egress.mode", c.EgressMode, false},
	}
}

func contextKeys() []string {
	var keys []string
	for _, a := range (Context{}).attributes("") {
		keys = append(keys, a.key)
	}
	return keys
}

func (c Context) Validate() error {
	if err := evidence.ValidateRun(c.RunID); err != nil {
		return err
	}
	if len(c.Agents) == 0 {
		return errors.New("telemetry context names no agent")
	}
	for agent := range c.Agents {
		for _, a := range c.attributes(agent) {
			if a.required && a.value == "" {
				return fmt.Errorf("%s is required", a.key)
			}
			if !printableASCII(a.value) {
				return fmt.Errorf("%s must be printable ASCII of at most %d bytes", a.key, maxValueBytes)
			}
		}
	}
	return nil
}

func printableASCII(value string) bool {
	if len(value) > maxValueBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}
