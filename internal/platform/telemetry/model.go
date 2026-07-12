package telemetry

import "github.com/maryzam/ai-crew-localdev/internal/platform/modelattrib"

type AgentMetadata = modelattrib.AgentMetadata
type ModelResolution = modelattrib.ModelResolution
type ModelAttribution = modelattrib.ModelAttribution

func ResolveAgentModelWithConfig(agentName, configuredModel string, command []string) (AgentMetadata, ModelAttribution) {
	agent, model := modelattrib.Resolve(agentName, configuredModel, command, modelattrib.StandardProfiles())
	agent.Type = boundedField("ai_agent.agent.type", agent.Type)
	agent.Identity = boundedField("ai_agent.agent.identity", agent.Identity)
	model.Provider = boundedField("gen_ai.provider.name", model.Provider)
	model.Family = boundedField("ai_agent.model.family", model.Family)
	model.Requested = bounded(model.Requested, MaxPropagatedValueLength)
	return agent, model
}
