package config

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/v2"
)

// unmarshalYAML 按 yaml tag 解码 path 处的配置；path 不存在时返回 nil 且不改动 out。
func unmarshalYAML(k *koanf.Koanf, path string, out any) error {
	if k == nil {
		return fmt.Errorf("nil koanf")
	}
	if path != "" && !k.Exists(path) {
		return nil
	}
	return k.UnmarshalWithConf(path, out, koanf.UnmarshalConf{
		Tag: "yaml",
		DecoderConfig: &mapstructure.DecoderConfig{
			WeaklyTypedInput: true,
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				durationDecodeHook(),
				mapstructure.StringToSliceHookFunc(","),
			),
		},
	})
}

// durationDecodeHook 从 Go duration 字符串（"5s"、"1h"）解析 time.Duration，
// 或从 YAML 中未加引号的数字解析（按秒处理）。
func durationDecodeHook() mapstructure.DecodeHookFunc {
	target := reflect.TypeOf(time.Duration(0))
	return func(_ reflect.Type, to reflect.Type, data any) (any, error) {
		if to != target {
			return data, nil
		}
		switch v := data.(type) {
		case time.Duration:
			return v, nil
		case string:
			s := strings.TrimSpace(v)
			if s == "" {
				return time.Duration(0), nil
			}
			d, err := time.ParseDuration(s)
			if err != nil {
				return nil, fmt.Errorf("invalid duration %q", s)
			}
			return d, nil
		case int:
			return time.Duration(v) * time.Second, nil
		case int8:
			return time.Duration(v) * time.Second, nil
		case int16:
			return time.Duration(v) * time.Second, nil
		case int32:
			return time.Duration(v) * time.Second, nil
		case int64:
			return time.Duration(v) * time.Second, nil
		case uint:
			return time.Duration(v) * time.Second, nil
		case uint64:
			return time.Duration(v) * time.Second, nil
		case float64:
			return time.Duration(v) * time.Second, nil
		case float32:
			return time.Duration(v) * time.Second, nil
		default:
			return data, nil
		}
	}
}
