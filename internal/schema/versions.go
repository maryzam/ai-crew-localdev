package schema

const (
	IdentitiesSchemaV2 = "ai-agent-identities/v2"

	// PolicySchemaCurrent is the only accepted policy schema version.
	// The historical v1 ("ai-agent-policy/v1") was retired in the
	// credential-generic refactor; see ADR 0001.
	PolicySchemaCurrent = "2"
)
