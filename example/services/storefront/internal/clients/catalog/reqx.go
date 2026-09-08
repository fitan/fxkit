package catalog

import (
	"fmt"

	"github.com/fitan/fxkit/reqx"
)

// NewFromFactory builds the generated OpenAPI client on a [reqx] transport
// (Consul watch, failover, OTel). Set ClientInput.Name to the Consul service
// name; use Seeds for local/dev without Consul. Do not hardcode instance IPs.
func NewFromFactory(f *reqx.Factory, in reqx.ClientInput) (*ClientWithResponses, error) {
	if f == nil {
		return nil, fmt.Errorf("catalog: nil reqx factory")
	}
	httpClient, server, err := f.TransportClient(in)
	if err != nil {
		return nil, err
	}
	return NewClientWithResponses(server, WithHTTPClient(httpClient))
}
