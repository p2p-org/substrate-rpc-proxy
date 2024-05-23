package endpoint

type EndpointStatus int

const (
	EndpointStatusHealthy EndpointStatus = 1 << iota
	EndpointStatusAvailable
	EndpointStatusUnreachable
)

type Provider interface {
	GetAliveEndpoint(string, int) string
	GetEndpoint(string, string, int) string
	GetStatus(string) map[string]EndpointStatus
}
