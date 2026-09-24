package effects

import "github.com/goccy/go-yaml"

// effectDef is one effects/<name>.yaml file.
type effectDef struct {
	Stages     []stageDef `yaml:"stages"`
	FinalState string     `yaml:"final_state"`
}

// stageDef is one stage: exactly one of Color, Primitive, Effect; Settings
// only with Primitive.
type stageDef struct {
	Duration  string         `yaml:"duration"` // Go duration string; "" = none
	Color     string         `yaml:"color"`
	Primitive string         `yaml:"primitive"`
	Effect    string         `yaml:"effect"`
	Settings  map[string]any `yaml:"settings"`
}

// stateDef is one state in a templates/<program>.yaml file: exactly one of
// Color or Effect.
type stateDef struct {
	Color  string `yaml:"color"`
	Effect string `yaml:"effect"`
}

// decodeStrict decodes YAML, rejecting unknown fields so typos fail loudly.
func decodeStrict(data []byte, v any) error {
	return yaml.UnmarshalWithOptions(data, v, yaml.DisallowUnknownField())
}
