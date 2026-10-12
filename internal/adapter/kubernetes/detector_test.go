package kubernetes_test

import (
	"slices"
	"testing"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/kubernetes"
)

func TestDetector_Match(t *testing.T) {
	det := &kubernetes.Detector{}
	cases := []struct {
		path string
		want bool
	}{
		{"deploy.yaml", true},
		{"k8s/base/Deployment.YML", true},
		{"values.yaml", true}, // by path only; the Helm detector claims it first
		{".github/workflows/ci.yml", false},
		{".github/actions/build/action.yaml", false},
		{"deploy.json", false},
		{"Dockerfile", false},
		{"yaml", false},
	}
	for _, tc := range cases {
		if got := det.Match(tc.path); got != tc.want {
			t.Errorf("Match(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestDetector_MatchContent(t *testing.T) {
	det := &kubernetes.Detector{}
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "object",
			content: "apiVersion: v1\nkind: Pod\n",
			want:    true,
		},
		{
			name:    "object in a later document",
			content: "foo: bar\n---\napiVersion: apps/v1\nkind: Deployment\n",
			want:    true,
		},
		{
			name:    "apiVersion without kind (a Helm Chart.yaml)",
			content: "apiVersion: v2\nname: chart\n",
			want:    false,
		},
		{
			name:    "keys present but not at the top level",
			content: "spec:\n  apiVersion: v1\n  kind: Pod\n",
			want:    false,
		},
		{
			name:    "empty kind",
			content: "apiVersion: v1\nkind:\n",
			want:    false,
		},
		{
			name:    "plain YAML",
			content: "services:\n  web:\n    image: nginx\n",
			want:    false,
		},
		{
			name:    "top level is a sequence",
			content: "- apiVersion: v1\n  kind: Pod\n",
			want:    false,
		},
		{
			name:    "Helm template that is not valid YAML",
			content: "{{- if .Values.enabled }}\napiVersion: v1\nkind: Service\n{{- end }}\n",
			want:    false,
		},
		{
			name:    "empty file",
			content: "",
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := det.MatchContent("deploy.yaml", []byte(tc.content)); got != tc.want {
				t.Errorf("MatchContent() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDetector_Detect(t *testing.T) {
	det := &kubernetes.Detector{}

	tests := []struct {
		name      string
		content   string
		wantRaws  []string
		wantLines []uint
	}{
		{
			name: "pod with every container list",
			content: `apiVersion: v1
kind: Pod
spec:
  initContainers:
    - name: init
      image: busybox
  containers:
    - name: app
      image: nginx:1.25
    - name: sidecar
      image: ghcr.io/org/sidecar@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1
  ephemeralContainers:
    - name: debug
      image: org/debug:v2
`,
			wantRaws: []string{
				"busybox",
				"nginx:1.25",
				"ghcr.io/org/sidecar@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1",
				"org/debug:v2",
			},
			wantLines: []uint{6, 9, 11, 14},
		},
		{
			name: "deployment and cronjob pod templates",
			content: `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - image: nginx:1.25
---
apiVersion: batch/v1
kind: CronJob
spec:
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - image: alpine:3
`,
			wantRaws:  []string{"nginx:1.25", "alpine:3"},
			wantLines: []uint{7, 17},
		},
		{
			name: "custom resource embedding a pod template",
			content: `apiVersion: argoproj.io/v1alpha1
kind: Rollout
spec:
  strategy:
    canary: {}
  template:
    spec:
      containers:
        - image: org/app:v3
`,
			wantRaws:  []string{"org/app:v3"},
			wantLines: []uint{9},
		},
		{
			name: "list of objects",
			content: `apiVersion: v1
kind: List
items:
  - apiVersion: v1
    kind: Pod
    spec:
      containers:
        - image: redis:7
`,
			wantRaws:  []string{"redis:7"},
			wantLines: []uint{8},
		},
		{
			name: "fully-qualified references outside container images",
			content: `apiVersion: apps/v1
kind: Deployment
metadata:
  annotations:
    example.com/builder: ghcr.io/org/builder:v1
spec:
  template:
    spec:
      containers:
        - image: operator:v1
          args: ["--sidecar-image=quay.io/org/sidecar:v2"]
          env:
            - name: RELATED_IMAGE_DB
              value: registry.example.com/db/postgres:16
            - name: SHORT_FORM
              value: nginx:1.25
`,
			wantRaws: []string{
				"ghcr.io/org/builder:v1",
				"operator:v1",
				"quay.io/org/sidecar:v2",
				"registry.example.com/db/postgres:16",
			},
			wantLines: []uint{5, 10, 11, 14},
		},
		{
			name: "reference used as a mapping key",
			content: `apiVersion: v1
kind: ConfigMap
data:
  digests:
    ghcr.io/org/app:v1: sha256:abc
`,
			wantRaws:  []string{"ghcr.io/org/app:v1"},
			wantLines: []uint{5},
		},
		{
			name: "literal block is attributed line by line",
			content: `apiVersion: v1
kind: ConfigMap
data:
  script.sh: |
    #!/bin/sh
    docker pull ghcr.io/org/tool:v1
    docker pull ghcr.io/org/skipped:v1  # oci-image-detector:ignore
    docker pull quay.io/org/other:v2
`,
			wantRaws:  []string{"ghcr.io/org/tool:v1", "quay.io/org/other:v2"},
			wantLines: []uint{6, 8},
		},
		{
			name: "document that is not an object is scanned strictly only",
			content: `containers:
  - image: nginx:1.25
  - image: ghcr.io/org/app:v1
---
apiVersion: v1
kind: Pod
spec:
  containers:
    - image: busybox
`,
			wantRaws:  []string{"ghcr.io/org/app:v1", "busybox"},
			wantLines: []uint{3, 9},
		},
		{
			name: "image keys outside container lists are not short-form images",
			content: `apiVersion: example.com/v1
kind: Widget
spec:
  image: nginx:1.25
  containers:
    image: redis:7
  template:
    image: ghcr.io/org/app:v1
`,
			wantRaws:  []string{"ghcr.io/org/app:v1"},
			wantLines: []uint{8},
		},
		{
			name: "unresolved placeholder is reported raw",
			content: `apiVersion: v1
kind: Pod
spec:
  containers:
    - image: "${REGISTRY}/app:${TAG}"
`,
			wantRaws:  []string{"${REGISTRY}/app:${TAG}"},
			wantLines: []uint{5},
		},
		{
			name: "Go template actions mark a chart template, not an image",
			content: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "app.name" . }}
spec:
  template:
    spec:
      containers:
        - image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
        - image: ghcr.io/org/pinned:v1
`,
			wantRaws:  []string{"ghcr.io/org/pinned:v1"},
			wantLines: []uint{10},
		},
		{
			name: "empty and null images name no image",
			content: `apiVersion: v1
kind: Pod
spec:
  containers:
    - image:
    - image: ""
    - image: ~
    - name: no-image
`,
			wantRaws: nil,
		},
		{
			name: "ignore annotation suppresses the value it sits on",
			content: `apiVersion: v1
kind: Pod
spec:
  containers:
    - image: nginx:1.25  # oci-image-detector:ignore
      env:
        - name: A
          value: ghcr.io/org/a:v1  # oci-image-detector:ignore
        - name: B
          value: ghcr.io/org/b:v1
    - image: redis:7
`,
			wantRaws:  []string{"ghcr.io/org/b:v1", "redis:7"},
			wantLines: []uint{10, 11},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := det.Detect("deploy.yaml", []byte(tc.content))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var raws []string
			var lines []uint
			for _, f := range findings {
				raws = append(raws, f.Ref.Raw)
				lines = append(lines, f.Line)
				if f.Strategy != kubernetes.Strategy {
					t.Errorf("Strategy = %q, want %q", f.Strategy, kubernetes.Strategy)
				}
				if f.FilePath != "deploy.yaml" {
					t.Errorf("FilePath = %q, want %q", f.FilePath, "deploy.yaml")
				}
			}
			if !slices.Equal(raws, tc.wantRaws) {
				t.Errorf("raws = %q, want %q", raws, tc.wantRaws)
			}
			if tc.wantLines != nil && !slices.Equal(lines, tc.wantLines) {
				t.Errorf("lines = %v, want %v", lines, tc.wantLines)
			}
		})
	}
}

func TestDetector_Detect_malformedLaterDocument(t *testing.T) {
	content := "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n    - image: nginx:1.25\n---\nkey: [unclosed\n"

	findings, err := (&kubernetes.Detector{}).Detect("deploy.yaml", []byte(content))
	if err == nil {
		t.Error("expected a parse error for the malformed document, got nil")
	}
	if len(findings) != 1 || findings[0].Ref.Raw != "nginx:1.25" {
		t.Errorf("expected the finding of the first document, got %v", findings)
	}
}
