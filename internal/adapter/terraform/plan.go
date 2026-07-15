package terraform

import (
	"encoding/json"
	"fmt"

	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

// StrategyPlan is the stable identifier for findings resolved from a Terraform
// plan (the JSON emitted by `terraform show -json <tfplan>`). It is distinct
// from Strategy so consumers can tell source-resolved findings from
// plan-resolved ones.
const StrategyPlan domain.Strategy = "terraform-plan"

// planFile is the minimal subset of the `terraform show -json` schema the
// detector needs: the fully-resolved planned values, grouped by module.
type planFile struct {
	PlannedValues struct {
		RootModule planModule `json:"root_module"`
	} `json:"planned_values"`
}

// planModule is a module in planned_values: its resources and any child
// modules, walked recursively.
type planModule struct {
	Resources    []planResource `json:"resources"`
	ChildModules []planModule   `json:"child_modules"`
}

// planResource is a single resource instance. Address is the fully-qualified
// resource address (e.g. module.app.aws_ecs_task_definition.app); Values holds
// its resolved attributes, which may nest arbitrarily.
type planResource struct {
	Address string         `json:"address"`
	Values  map[string]any `json:"values"`
}

// DetectPlan extracts image-reference findings from the JSON produced by
// `terraform show -json <tfplan>`. Because Terraform has already resolved every
// variable, local, function and module input, each resource's values are
// concrete; the resolved strings are passed through the same imageRefsIn filter
// as the source detector. Findings are attributed to the resource address
// (FilePath) with no line number (plan JSON has none).
func DetectPlan(content []byte) ([]domain.Finding, error) {
	var plan planFile
	if err := json.Unmarshal(content, &plan); err != nil {
		return nil, fmt.Errorf("terraform: parse plan json: %w", err)
	}

	var findings []domain.Finding
	var walk func(m planModule)
	walk = func(m planModule) {
		for _, r := range m.Resources {
			for _, s := range jsonStringLeaves(r.Values) {
				for _, ref := range imageRefsIn(s) {
					findings = append(findings, domain.Finding{
						Ref:      ref,
						FilePath: r.Address,
						Strategy: StrategyPlan,
					})
				}
			}
		}
		for _, child := range m.ChildModules {
			walk(child)
		}
	}
	walk(plan.PlannedValues.RootModule)

	return findings, nil
}

// jsonStringLeaves collects every string value reachable within v, descending
// into maps and slices. It mirrors stringLeaves for the cty world but operates
// on the Go values produced by encoding/json, so it picks image references out
// of nested attributes and jsonencode blobs (which unmarshal to a string) alike.
func jsonStringLeaves(v any) []string {
	var out []string
	collectJSONStringLeaves(v, &out)
	return out
}

func collectJSONStringLeaves(v any, out *[]string) {
	switch t := v.(type) {
	case string:
		*out = append(*out, t)
	case []any:
		for _, e := range t {
			collectJSONStringLeaves(e, out)
		}
	case map[string]any:
		for _, e := range t {
			collectJSONStringLeaves(e, out)
		}
	}
}
