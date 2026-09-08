package openapi

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"gopkg.in/yaml.v3"
)

// SpecVersion is an OpenAPI document version to emit.
type SpecVersion string

const (
	// Spec30 is OpenAPI 3.0.3 (Huma Downgrade). Preferred for oapi-codegen.
	Spec30 SpecVersion = "3.0"
	// Spec31 is native Huma OpenAPI 3.1.
	Spec31 SpecVersion = "3.1"
)

// Format is the serialization of an encoded spec.
type Format string

const (
	FormatYAML Format = "yaml"
	FormatJSON Format = "json"
)

// EncodeInput controls [Encode]. Zero value is OpenAPI 3.0 YAML (oapi-codegen).
type EncodeInput struct {
	Version SpecVersion // default [Spec30]
	Format  Format      // default [FormatYAML]
	// Base is an optional static spec merged under the live Huma document
	// (same rules as [MergeBaseHuma]).
	Base []byte
}

// ParseSpecVersion accepts "3.0" / "3.0.3" / "3.1" / "3.1.0".
func ParseSpecVersion(s string) (SpecVersion, error) {
	switch strings.TrimSpace(s) {
	case "", "3.0", "3.0.3":
		return Spec30, nil
	case "3.1", "3.1.0":
		return Spec31, nil
	default:
		return "", fmt.Errorf("openapi spec version %q (want 3.0 or 3.1)", s)
	}
}

// ParseFormat accepts yaml / yml / json.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "yaml", "yml":
		return FormatYAML, nil
	case "json":
		return FormatJSON, nil
	default:
		return "", fmt.Errorf("openapi format %q (want yaml or json)", s)
	}
}

// Encode serializes a live Huma OpenAPI document.
func Encode(api huma.API, in EncodeInput) ([]byte, error) {
	if api == nil {
		return nil, fmt.Errorf("openapi encode: nil Huma API")
	}
	ver := in.Version
	if ver == "" {
		ver = Spec30
	}
	format := in.Format
	if format == "" {
		format = FormatYAML
	}

	raw, err := encodeHumaYAML(api, ver)
	if err != nil {
		return nil, err
	}
	if len(in.Base) > 0 {
		raw, err = MergeYAML(in.Base, raw)
		if err != nil {
			return nil, err
		}
		raw, err = forceOpenAPIVersion(raw, openAPIVersionString(ver))
		if err != nil {
			return nil, err
		}
	}
	if format == FormatJSON {
		return yamlToJSON(raw)
	}
	return raw, nil
}

func encodeHumaYAML(api huma.API, ver SpecVersion) ([]byte, error) {
	doc := api.OpenAPI()
	if doc == nil {
		return nil, fmt.Errorf("openapi encode: nil OpenAPI document")
	}
	switch ver {
	case Spec30:
		b, err := doc.DowngradeYAML()
		if err != nil {
			return nil, fmt.Errorf("openapi encode 3.0: %w", err)
		}
		return b, nil
	case Spec31:
		b, err := doc.YAML()
		if err != nil {
			return nil, fmt.Errorf("openapi encode 3.1: %w", err)
		}
		return b, nil
	default:
		return nil, fmt.Errorf("openapi encode: unsupported version %q", ver)
	}
}

func openAPIVersionString(ver SpecVersion) string {
	if ver == Spec31 {
		return "3.1.0"
	}
	return "3.0.3"
}

func forceOpenAPIVersion(raw []byte, ver string) ([]byte, error) {
	doc, err := parseDoc(raw)
	if err != nil {
		return nil, fmt.Errorf("openapi encode version: %w", err)
	}
	doc["openapi"] = ver
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("openapi encode version marshal: %w", err)
	}
	return out, nil
}

func yamlToJSON(raw []byte) ([]byte, error) {
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("openapi encode json: %w", err)
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("openapi encode json: %w", err)
	}
	b = append(b, '\n')
	return b, nil
}
