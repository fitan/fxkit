package reqx

import (
	"context"
	"fmt"

	"github.com/imroc/req/v3"
)

// PostJSON 发送 POST 请求并将 body 序列化为 JSON，成功时反序列化为 Resp 返回。
// 若服务端返回非 2xx 状态码，返回包含状态码与响应体内容的错误。
// 利用 Go 1.27+ 泛型能力，省去业务方样板式的 SetBody / SetSuccessResult 链式调用。
func PostJSON[Req, Resp any](client *req.Client, ctx context.Context, path string, body Req) (Resp, error) {
	var zero Resp
	var result Resp
	resp, err := client.R().
		SetContext(ctx).
		SetBody(body).
		SetSuccessResult(&result).
		Post(path)
	if err != nil {
		return zero, err
	}
	if !resp.IsSuccessState() {
		return zero, fmt.Errorf("reqx: POST %s returned status %d: %s", path, resp.StatusCode, resp.String())
	}
	return result, nil
}

// GetJSON 发送 GET 请求并将 JSON 响应反序列化为 Resp 返回。
// 利用 Go 1.27+ 泛型能力，免除在外部定义临时接收变量与类型转换。
func GetJSON[Resp any](client *req.Client, ctx context.Context, path string) (Resp, error) {
	var zero Resp
	var result Resp
	resp, err := client.R().
		SetContext(ctx).
		SetSuccessResult(&result).
		Get(path)
	if err != nil {
		return zero, err
	}
	if !resp.IsSuccessState() {
		return zero, fmt.Errorf("reqx: GET %s returned status %d: %s", path, resp.StatusCode, resp.String())
	}
	return result, nil
}

// PutJSON 发送 PUT 请求并将 body 序列化为 JSON，成功时反序列化为 Resp 返回。
func PutJSON[Req, Resp any](client *req.Client, ctx context.Context, path string, body Req) (Resp, error) {
	var zero Resp
	var result Resp
	resp, err := client.R().
		SetContext(ctx).
		SetBody(body).
		SetSuccessResult(&result).
		Put(path)
	if err != nil {
		return zero, err
	}
	if !resp.IsSuccessState() {
		return zero, fmt.Errorf("reqx: PUT %s returned status %d: %s", path, resp.StatusCode, resp.String())
	}
	return result, nil
}

// PatchJSON 发送 PATCH 请求并将 body 序列化为 JSON，成功时反序列化为 Resp 返回。
func PatchJSON[Req, Resp any](client *req.Client, ctx context.Context, path string, body Req) (Resp, error) {
	var zero Resp
	var result Resp
	resp, err := client.R().
		SetContext(ctx).
		SetBody(body).
		SetSuccessResult(&result).
		Patch(path)
	if err != nil {
		return zero, err
	}
	if !resp.IsSuccessState() {
		return zero, fmt.Errorf("reqx: PATCH %s returned status %d: %s", path, resp.StatusCode, resp.String())
	}
	return result, nil
}

// DeleteJSON 发送 DELETE 请求并将 JSON 响应反序列化为 Resp 返回。
func DeleteJSON[Resp any](client *req.Client, ctx context.Context, path string) (Resp, error) {
	var zero Resp
	var result Resp
	resp, err := client.R().
		SetContext(ctx).
		SetSuccessResult(&result).
		Delete(path)
	if err != nil {
		return zero, err
	}
	if !resp.IsSuccessState() {
		return zero, fmt.Errorf("reqx: DELETE %s returned status %d: %s", path, resp.StatusCode, resp.String())
	}
	return result, nil
}
