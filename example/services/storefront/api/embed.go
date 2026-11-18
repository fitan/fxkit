// Package api 暴露服务的手写 OpenAPI 基座。
//
// go:embed 不能引用父目录，所以基座 YAML 必须和本文件同目录。
package api

import _ "embed"

//go:embed openapi.base.yaml
var Base []byte
