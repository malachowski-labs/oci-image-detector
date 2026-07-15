package terraform_test

import (
	"testing"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/terraform"
	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

// findByRaw returns the first finding with the given Raw, or nil.
func findByRaw(findings []domain.Finding, raw string) *domain.Finding {
	for i := range findings {
		if findings[i].Ref.Raw == raw {
			return &findings[i]
		}
	}
	return nil
}

// samplePlan mirrors the shape of `terraform show -json` output: a root module
// with an ECS task whose container_definitions is a resolved JSON string, a
// child module with a nested kubernetes container image, and a resource whose
// image is null (known after apply) that must be skipped.
const samplePlan = `{
  "format_version": "1.2",
  "terraform_version": "1.9.0",
  "planned_values": {
    "root_module": {
      "resources": [
        {
          "address": "aws_ecs_task_definition.app",
          "type": "aws_ecs_task_definition",
          "name": "app",
          "values": {
            "container_definitions": "[{\"name\":\"app\",\"image\":\"ghcr.io/org/app:1.25\"}]"
          }
        },
        {
          "address": "aws_ecs_task_definition.pending",
          "values": { "container_definitions": null }
        }
      ],
      "child_modules": [
        {
          "address": "module.svc",
          "resources": [
            {
              "address": "module.svc.kubernetes_deployment.app",
              "values": {
                "spec": [{"template": [{"spec": [{"container": [
                  {"name": "svc", "image": "gcr.io/proj/svc:v2"}
                ]}]}]}]
              }
            }
          ]
        }
      ]
    }
  }
}`

func TestDetectPlan(t *testing.T) {
	findings, err := terraform.DetectPlan([]byte(samplePlan))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ecs := findByRaw(findings, "ghcr.io/org/app:1.25")
	if ecs == nil {
		t.Fatalf("expected ECS image ghcr.io/org/app:1.25, got: %v", findings)
	}
	if ecs.FilePath != "aws_ecs_task_definition.app" {
		t.Errorf("ECS FilePath = %q, want resource address", ecs.FilePath)
	}
	if ecs.Strategy != terraform.StrategyPlan {
		t.Errorf("Strategy = %q, want %q", ecs.Strategy, terraform.StrategyPlan)
	}

	k8s := findByRaw(findings, "gcr.io/proj/svc:v2")
	if k8s == nil {
		t.Fatalf("expected nested child-module image gcr.io/proj/svc:v2, got: %v", findings)
	}
	if k8s.FilePath != "module.svc.kubernetes_deployment.app" {
		t.Errorf("k8s FilePath = %q, want child-module resource address", k8s.FilePath)
	}
}

func TestDetectPlan_invalidJSON(t *testing.T) {
	if _, err := terraform.DetectPlan([]byte("not json")); err == nil {
		t.Error("expected error for invalid plan JSON")
	}
}

func TestDetectPlan_empty(t *testing.T) {
	findings, err := terraform.DetectPlan([]byte(`{"planned_values":{"root_module":{}}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for empty plan, got: %v", findings)
	}
}
