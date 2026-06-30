package contract

const CredentialType = "langfuse_otlp"

type Credential struct {
	Endpoint  string `json:"endpoint"`
	PublicKey string `json:"public_key"`
	SecretKey string `json:"secret_key"`
}
