package telemetry

type exporterFunc func([]byte) error

func (f exporterFunc) Export(payload []byte) error {
	return f(payload)
}
