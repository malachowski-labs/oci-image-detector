package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const planJSON = `{
  "planned_values": {
    "root_module": {
      "resources": [
        {
          "address": "aws_ecs_task_definition.app",
          "values": {
            "container_definitions": "[{\"image\":\"ghcr.io/org/app:1.25\"}]"
          }
        }
      ]
    }
  }
}`

// silenceStdout redirects os.Stdout to /dev/null for the duration of the test so
// the stdout reporter does not pollute test output, restoring it on cleanup.
func silenceStdout(t *testing.T) {
	t.Helper()
	orig := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	os.Stdout = devnull
	t.Cleanup(func() {
		os.Stdout = orig
		devnull.Close()
	})
}

// TestPlanCommand_outputFile drives the full subcommand and asserts the JSON
// report contains the resolved image from the plan.
func TestPlanCommand_outputFile(t *testing.T) {
	silenceStdout(t)

	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(planPath, []byte(planJSON), 0o600); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	outPath := filepath.Join(dir, "out.json")

	root := newRootCmd("test")
	root.SetArgs([]string{"plan", planPath, "-o", outPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	out, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(string(out), "ghcr.io/org/app:1.25") {
		t.Errorf("output JSON missing image, got: %s", out)
	}
}

// TestPlanCommand_allowEmpty verifies the exit-code contract: an empty plan
// errors with ErrNoFindings unless --allow-empty is set.
func TestPlanCommand_allowEmpty(t *testing.T) {
	silenceStdout(t)

	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(planPath, []byte(`{"planned_values":{"root_module":{}}}`), 0o600); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	if err := runPlan(planPath, planOptions{}); !errors.Is(err, ErrNoFindings) {
		t.Errorf("expected ErrNoFindings, got: %v", err)
	}
	if err := runPlan(planPath, planOptions{allowEmpty: true}); err != nil {
		t.Errorf("expected nil with allow-empty, got: %v", err)
	}
}

// TestPlanCommand_excludeImages verifies --exclude-images drops findings whose
// image reference matches a doublestar glob, mirroring the directory scan.
func TestPlanCommand_excludeImages(t *testing.T) {
	silenceStdout(t)

	const twoImages = `{
  "planned_values": {
    "root_module": {
      "resources": [
        {"address": "a", "values": {"image": "ghcr.io/org/app:1.25"}},
        {"address": "b", "values": {"image": "localhost:5000/dev:latest"}}
      ]
    }
  }
}`
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(planPath, []byte(twoImages), 0o600); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	outPath := filepath.Join(dir, "out.json")

	root := newRootCmd("test")
	root.SetArgs([]string{"plan", planPath, "-o", outPath, "--exclude-images", "localhost:5000/**"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	out, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if strings.Contains(string(out), "localhost:5000/dev:latest") {
		t.Errorf("excluded image still present, got: %s", out)
	}
	if !strings.Contains(string(out), "ghcr.io/org/app:1.25") {
		t.Errorf("non-excluded image missing, got: %s", out)
	}
}

// TestReadPlanInput_stdin verifies '-' reads the plan from stdin.
func TestReadPlanInput_stdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })

	go func() {
		w.WriteString(planJSON)
		w.Close()
	}()

	content, err := readPlanInput("-")
	if err != nil {
		t.Fatalf("readPlanInput: %v", err)
	}
	if !strings.Contains(string(content), "ghcr.io/org/app:1.25") {
		t.Errorf("stdin content not read, got: %s", content)
	}
}
