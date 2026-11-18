package catalog

import (
	"fmt"

	"github.com/fitan/fxkit/reqx"
)

// NewFromFactory 用 reqx 的 transport 构建生成的 OpenAPI client。
// reqx 负责 Consul 服务发现、均衡/failover 与 OTel；不要在业务代码里硬编码实例 IP。
//
// ClientInput.Name 填 Consul 服务名；本地/无 Consul 时用 Seeds 指定直连地址。
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
