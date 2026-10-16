package operations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerToolContainerizeWritesRelativeOutputInsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.invalid/app\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}

	report, err := NewContainerTool(workspace).Execute(context.Background(), map[string]interface{}{
		"action": "containerize",
		"name":   "demo",
		"port":   9090,
		"output": filepath.Join("deploy", "docker"),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	outputDirectory := filepath.Join(workspace, "deploy", "docker")
	for _, filename := range []string{"Dockerfile", "docker-compose.yml", ".dockerignore"} {
		if _, err := os.Stat(filepath.Join(outputDirectory, filename)); err != nil {
			t.Fatalf("generated file %s is missing: %v", filename, err)
		}
	}
	dockerfile, err := os.ReadFile(filepath.Join(outputDirectory, "Dockerfile"))
	if err != nil {
		t.Fatalf("ReadFile(Dockerfile) error = %v", err)
	}
	for _, expected := range []string{"FROM golang:", "EXPOSE 9090"} {
		if !strings.Contains(string(dockerfile), expected) {
			t.Fatalf("Dockerfile does not contain %q:\n%s", expected, dockerfile)
		}
	}
	if !strings.Contains(report, "**Language:** Go") {
		t.Fatalf("report does not identify Go project:\n%s", report)
	}
}

func TestContainerToolGenerateK8sWritesCompleteManifestSet(t *testing.T) {
	workspace := t.TempDir()
	_, err := NewContainerTool(workspace).Execute(context.Background(), map[string]interface{}{
		"action": "k8s",
		"name":   "api",
		"image":  "registry.invalid/api:v1",
		"port":   float64(8088),
		"output": "manifests",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	outputDirectory := filepath.Join(workspace, "manifests")
	for _, filename := range []string{"deployment.yaml", "service.yaml", "configmap.yaml", "kustomization.yaml"} {
		if _, err := os.Stat(filepath.Join(outputDirectory, filename)); err != nil {
			t.Fatalf("generated file %s is missing: %v", filename, err)
		}
	}
	deployment, err := os.ReadFile(filepath.Join(outputDirectory, "deployment.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(deployment.yaml) error = %v", err)
	}
	for _, expected := range []string{"image: registry.invalid/api:v1", "containerPort: 8088"} {
		if !strings.Contains(string(deployment), expected) {
			t.Fatalf("deployment does not contain %q:\n%s", expected, deployment)
		}
	}
}

func TestContainerToolRejectsUnknownComposeCommandBeforeDockerExecution(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "docker-compose.yml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(docker-compose.yml) error = %v", err)
	}

	_, err := NewContainerTool(workspace).Execute(context.Background(), map[string]interface{}{
		"action":      "compose",
		"compose_cmd": "destroy",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown compose command") {
		t.Fatalf("Execute() error = %v, want unknown compose command", err)
	}
}
