package governance

import (
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/store"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

type Paths struct {
	Identities string
	Policy     string
}

type Snapshot struct {
	Identities      *identity.IdentitiesFile
	Policy          *policy.PolicyFile
	IdentitiesError error
	PolicyError     error
}

type FileStore struct{}

func DefaultPaths() Paths {
	return Paths{Identities: paths.DefaultIdentitiesPath(), Policy: paths.PolicyPath()}
}

func (FileStore) Load(paths Paths) (Snapshot, error) {
	snapshot, err := store.Load(paths.Identities, paths.Policy)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Identities: snapshot.Identities, Policy: snapshot.Policy, IdentitiesError: snapshot.IdentitiesError, PolicyError: snapshot.PolicyError}, nil
}

func (FileStore) Publish(paths Paths, identities *identity.IdentitiesFile, policyFile *policy.PolicyFile) error {
	return store.Publish(paths.Identities, identities, paths.Policy, policyFile)
}
