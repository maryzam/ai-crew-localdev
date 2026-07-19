package manifest

import (
	"slices"
	"strings"
)

func (f *File) AllowsRunMode(mode string) bool {
	if f == nil || len(f.RunModes) == 0 {
		return true
	}
	return slices.Contains(f.RunModes, mode)
}

func (f *File) AllowsAgent(agentName string) bool {
	if f == nil || f.Agents == nil || len(f.Agents.Allowed) == 0 {
		return true
	}
	return slices.Contains(f.Agents.Allowed, agentName)
}

func (f *File) AllowedAgentsText() string {
	if f == nil || f.Agents == nil {
		return ""
	}
	return strings.Join(f.Agents.Allowed, ", ")
}

func (f *File) RunModesText() string {
	if f == nil {
		return ""
	}
	return strings.Join(f.RunModes, ", ")
}

func (f *File) ResourceURIs() []string {
	if f == nil {
		return nil
	}
	resources := make([]string, 0, len(f.Resources))
	for _, resource := range f.Resources {
		resources = append(resources, resource.URI)
	}
	return resources
}

func (f *File) ResourceBudgetsCopy() []ResourceBudget {
	if f == nil {
		return nil
	}
	return append([]ResourceBudget(nil), f.ResourceBudgets...)
}

func (f *File) ServiceNames() []string {
	if f == nil {
		return nil
	}
	services := make([]string, 0, len(f.Services))
	for _, service := range f.Services {
		services = append(services, service.Name)
	}
	return services
}

func (f *File) PortNumbers() []int {
	if f == nil {
		return nil
	}
	ports := make([]int, 0, len(f.Ports))
	for _, port := range f.Ports {
		ports = append(ports, port.Number)
	}
	return ports
}
