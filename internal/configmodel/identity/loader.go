package identity

import (
	"encoding/json"
	"fmt"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/schema"
	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
)

const maxIdentitiesBytes = 1 << 20

func Load(path string) (*IdentitiesFile, error) {
	data, err := securefile.ReadOwnerOnly(path, maxIdentitiesBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to read identities file: %w", err)
	}

	var f IdentitiesFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("failed to parse identities file: %w", err)
	}

	if f.SchemaVersion != schema.IdentitiesSchemaV2 {
		return nil, fmt.Errorf("unsupported schema version %q, expected %q", f.SchemaVersion, schema.IdentitiesSchemaV2)
	}

	return &f, nil
}

func Validate(f *IdentitiesFile) schema.ValidationErrors {
	var errs schema.ValidationErrors

	if f.SchemaVersion == "" {
		errs = append(errs, schema.ValidationError{
			Field:   "schema_version",
			Message: "must not be empty",
		})
	} else if f.SchemaVersion != schema.IdentitiesSchemaV2 {
		errs = append(errs, schema.ValidationError{
			Field:   "schema_version",
			Message: fmt.Sprintf("must be %q, got %q", schema.IdentitiesSchemaV2, f.SchemaVersion),
		})
	}

	if len(f.Agents) == 0 {
		errs = append(errs, schema.ValidationError{
			Field:   "agents",
			Message: "must contain at least one agent",
		})
		return errs
	}

	for name, agent := range f.Agents {
		prefix := fmt.Sprintf("agents.%s", name)

		if agent.AppID == "" {
			errs = append(errs, schema.ValidationError{
				Field:   prefix + ".app_id",
				Message: "must not be empty",
			})
		}
		if agent.GitName == "" {
			errs = append(errs, schema.ValidationError{
				Field:   prefix + ".git_name",
				Message: "must not be empty",
			})
		}
		if agent.GitEmail == "" {
			errs = append(errs, schema.ValidationError{
				Field:   prefix + ".git_email",
				Message: "must not be empty",
			})
		}
	}

	return errs
}
