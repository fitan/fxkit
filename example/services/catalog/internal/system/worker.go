package system

import "github.com/fitan/fxkit/hatchetx"

// RegisterWorker 注册 system feature 的 Hatchet actor：
//   - catalog-counter：并发/队列语义演示；
//   - catalog-heartbeat：reminder 定时 tick。
func RegisterWorker(svc *Service) hatchetx.Registrar {
	return func(client *hatchetx.Client) ([]hatchetx.WorkflowBase, error) {
		a, err := svc.counter.Register(client)
		if err != nil {
			return nil, err
		}
		b, err := svc.heartbeat.Register(client)
		if err != nil {
			return nil, err
		}
		return append(a, b...), nil
	}
}
