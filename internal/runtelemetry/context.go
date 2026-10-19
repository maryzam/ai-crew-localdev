package runtelemetry

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type Agent string

const (
	Claude Agent = "claude"
	Codex  Agent = "codex"
)

const (
	RunIDAttribute = "ai.run.id"
	maxValueBytes  = 512
)

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

type Context struct {
	RunID       string
	Task        string
	Project     string
	Repository  string
	Branch      string
	ImageDigest string
	EgressMode  string
	Agents      map[Agent]string
}

type attribute struct {
	key      string
	value    string
	required bool
}

func (c Context) attributes(agent Agent) []attribute {
	return []attribute{
		{RunIDAttribute, c.RunID, true},
		{"ai.task", c.Task, true},
		{"ai.agent", string(agent), true},
		{"ai.agent.version", c.Agents[agent], true},
		{"ai.project", c.Project, true},
		{"ai.repository", c.Repository, false},
		{"ai.branch", c.Branch, false},
		{"ai.image.digest", c.ImageDigest, true},
		{"ai.egress.mode", c.EgressMode, true},
	}
}

func contextKeys() []string {
	var keys []string
	for _, a := range (Context{}).attributes("") {
		keys = append(keys, a.key)
	}
	return keys
}

func headerName(key string) string {
	return "x-" + strings.ReplaceAll(key, ".", "-")
}

func (c Context) Validate() error {
	if !runIDPattern.MatchString(c.RunID) {
		return fmt.Errorf("run ID %q must be 1 to 64 letters, digits, or hyphens", c.RunID)
	}
	if len(c.Agents) == 0 {
		return errors.New("telemetry context names no agent")
	}
	for agent := range c.Agents {
		if agent != Claude && agent != Codex {
			return fmt.Errorf("unsupported agent %q", agent)
		}
		for _, a := range c.attributes(agent) {
			if a.required && a.value == "" {
				return fmt.Errorf("%s is required", a.key)
			}
			if !headerSafe(a.value) {
				return fmt.Errorf("%s must be printable ASCII of at most %d bytes", a.key, maxValueBytes)
			}
		}
	}
	return nil
}

func headerSafe(value string) bool {
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
