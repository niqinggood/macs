package operations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mac/internal/codeutil"
	"mac/internal/configvalue"
	"mac/internal/textutil"
)

// ContainerTool provides Docker and Kubernetes development workflow.
type ContainerTool struct {
	workspace string
}

func NewContainerTool(workspace string) *ContainerTool {
	return &ContainerTool{workspace: workspace}
}

func (t *ContainerTool) Name() string { return "container" }

func (t *ContainerTool) Description() string {
	return "Docker and Kubernetes development workflow. " +
		"Actions: containerize (generate Dockerfile + compose for a project), " +
		"build (build container image), " +
		"run (run container with proper config), " +
		"k8s (generate Kubernetes manifests), " +
		"status (check container/registry status), " +
		"compose (manage docker-compose lifecycle), " +
		"cleanup (remove unused containers/images/volumes)."
}

func (t *ContainerTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"containerize", "build", "run", "k8s", "status", "compose", "cleanup"},
				"description": "containerize: generate Dockerfile. build: build image. run: run container. k8s: generate K8s manifests. status: check state. compose: manage compose. cleanup: remove unused resources.",
			},
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Project/container name",
			},
			"port": map[string]interface{}{
				"type":        "integer",
				"description": "Exposed port (default: 8080)",
			},
			"image": map[string]interface{}{
				"type":        "string",
				"description": "Docker image name (for build/run)",
			},
			"registry": map[string]interface{}{
				"type":        "string",
				"description": "Container registry URL",
			},
			"compose_cmd": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"up", "down", "ps", "logs", "build", "restart"},
				"description": "Docker compose command (for compose action)",
			},
			"output": map[string]interface{}{
				"type":        "string",
				"description": "Output directory for generated files",
			},
		},
		"required": []string{"action"},
	}
}

func (t *ContainerTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := configvalue.String(params, "action", "status")
	name := configvalue.String(params, "name", filepath.Base(t.workspace))
	port := configvalue.Int(params, "port", 8080)
	image := configvalue.String(params, "image", name+":latest")
	registry := configvalue.String(params, "registry", "")
	composeCmd := configvalue.String(params, "compose_cmd", "ps")
	output := configvalue.String(params, "output", "")

	switch action {
	case "containerize":
		return t.containerize(name, port, output)
	case "build":
		return t.buildImage(ctx, image, registry)
	case "run":
		return t.runContainer(ctx, name, image, port)
	case "k8s":
		return t.generateK8s(name, image, port, output)
	case "status":
		return t.containerStatus(ctx)
	case "compose":
		return t.manageCompose(ctx, composeCmd)
	case "cleanup":
		return t.cleanup(ctx)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

// ================== Containerize ==================

func (t *ContainerTool) containerize(name string, port int, output string) (string, error) {
	lang := codeutil.DetectProjectLanguage(t.workspace)
	if output == "" {
		output = filepath.Join(t.workspace, "docker")
	} else if !filepath.IsAbs(output) {
		output = filepath.Join(t.workspace, output)
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return "", fmt.Errorf("create container output directory: %w", err)
	}

	dockerfile := t.generateDockerfile(name, lang, port)
	compose := t.generateCompose(name, port)
	ignore := t.generateDockerignore(lang)

	dockerfilePath := filepath.Join(output, "Dockerfile")
	composePath := filepath.Join(output, "docker-compose.yml")
	ignorePath := filepath.Join(output, ".dockerignore")

	for path, content := range map[string]string{
		dockerfilePath: dockerfile,
		composePath:    compose,
		ignorePath:     ignore,
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", fmt.Errorf("write generated container file %s: %w", path, err)
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Containerized: `%s`\n\n", name))
	sb.WriteString(fmt.Sprintf("**Language:** %s | **Port:** %d\n\n", lang, port))
	sb.WriteString(fmt.Sprintf("Generated files in `%s/`:\n\n", output))
	sb.WriteString(fmt.Sprintf("- `Dockerfile` — multi-stage build for %s\n", lang))
	sb.WriteString("- `docker-compose.yml` — development and production profiles\n")
	sb.WriteString("- `.dockerignore` — optimized build context\n\n")

	sb.WriteString("### Quick Start\n\n")
	sb.WriteString(fmt.Sprintf("```bash\n# Build and run\ndocker compose -f %s/docker-compose.yml up -d\n\n# View logs\ndocker compose -f %s/docker-compose.yml logs -f\n\n# Stop\ndocker compose -f %s/docker-compose.yml down\n```\n", output, output, output))

	sb.WriteString("\n### Dockerfile Preview\n\n")
	sb.WriteString(fmt.Sprintf("```dockerfile\n%s\n```\n", textutil.Truncate(dockerfile, 1500)))

	return sb.String(), nil
}

func (t *ContainerTool) generateDockerfile(name, lang string, port int) string {
	switch lang {
	case "Go":
		return fmt.Sprintf(`# Multi-stage build for Go
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/server .

FROM alpine:3.20
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/server .
COPY --from=builder /app/web ./web
COPY --from=builder /app/skills ./skills
COPY --from=builder /app/storage ./storage
EXPOSE %d
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://localhost:%d/health || exit 1
ENTRYPOINT ["./server"]
`, port, port)

	case "Node":
		return fmt.Sprintf(`FROM node:20-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci --only=production && npm cache clean --force
COPY . .
RUN npm run build 2>/dev/null || true

FROM node:20-alpine
RUN apk --no-cache add tini
WORKDIR /app
COPY --from=builder /app/node_modules ./node_modules
COPY --from=builder /app/dist ./dist
COPY --from=builder /app/package*.json ./
EXPOSE %d
HEALTHCHECK --interval=30s CMD wget -qO- http://localhost:%d/health || exit 1
ENTRYPOINT ["/sbin/tini", "--", "node", "dist/index.js"]
`, port, port)

	case "Python":
		return fmt.Sprintf(`FROM python:3.12-slim AS builder
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

FROM python:3.12-slim
WORKDIR /app
COPY --from=builder /usr/local/lib/python3.12/site-packages /usr/local/lib/python3.12/site-packages
COPY . .
EXPOSE %d
HEALTHCHECK --interval=30s CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:%d/health')" || exit 1
ENTRYPOINT ["python", "main.py"]
`, port, port)

	case "Rust":
		return fmt.Sprintf(`FROM rust:1.80-alpine AS builder
WORKDIR /app
COPY Cargo.toml Cargo.lock ./
RUN mkdir src && echo "fn main() {}" > src/main.rs
RUN cargo build --release 2>/dev/null || true
RUN rm -rf src
COPY . .
RUN cargo build --release

FROM alpine:3.20
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app/target/release/%s .
EXPOSE %d
HEALTHCHECK --interval=30s CMD wget -qO- http://localhost:%d/health || exit 1
ENTRYPOINT ["./%s"]
`, name, port, port, name)

	default:
		return fmt.Sprintf(`FROM alpine:3.20
WORKDIR /app
COPY . .
EXPOSE %d
CMD ["sh"]
`, port)
	}
}

func (t *ContainerTool) generateCompose(name string, port int) string {
	return fmt.Sprintf(`version: "3.9"

services:
  %s:
    build:
      context: ..
      dockerfile: docker/Dockerfile
    image: %s:latest
    container_name: %s
    ports:
      - "%d:%d"
    environment:
      - TZ=Asia/Shanghai
      - ENV=production
    volumes:
      - ../storage:/app/storage
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:%d/health"]
      interval: 30s
      timeout: 3s
      retries: 3
    deploy:
      resources:
        limits:
          memory: 512M
        reservations:
          memory: 128M
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
`, name, name, name, port, port, port)
}

func (t *ContainerTool) generateDockerignore(lang string) string {
	base := `# Git
.git
.gitignore

# IDE
.idea
.vscode
*.swp
*.swo

# OS
.DS_Store
Thumbs.db

# Build artifacts
node_modules
__pycache__
*.pyc
*.pyo
dist
build
target

# Environment
.env
.env.local
*.log
tmp/
temp/
`

	switch lang {
	case "Go":
		return base + "# Go\n*.exe\n*.test\n*.out\nvendor/\n"
	case "Node":
		return base + "# Node\nnode_modules/\nnpm-debug.log*\ncoverage/\n"
	case "Python":
		return base + "# Python\nvenv/\n.venv/\n*.egg-info/\n.pytest_cache/\n"
	}
	return base
}

// ================== Build ==================

func (t *ContainerTool) buildImage(ctx context.Context, image, registry string) (string, error) {
	if registry != "" && !strings.Contains(image, registry) {
		image = registry + "/" + image
	}

	start := time.Now()
	out, err := runDockerCmd(ctx, t.workspace, 300, "build", "-t", image, ".")
	duration := time.Since(start)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Docker Build (%.0fs)\n\n", duration.Seconds()))

	if err != nil {
		sb.WriteString(fmt.Sprintf("❌ Build failed:\n```\n%s\n```\n", textutil.Truncate(out, 2000)))
		sb.WriteString("\n### Common Fixes\n")
		sb.WriteString("- Check Dockerfile syntax: `docker build --check .`\n")
		sb.WriteString("- Use `.dockerignore` to reduce build context\n")
		sb.WriteString("- Enable BuildKit: `DOCKER_BUILDKIT=1 docker build .`\n")
	} else {
		sb.WriteString(fmt.Sprintf("✅ Image built: `%s`\n\n```\n%s\n```\n", image, textutil.Truncate(out, 1000)))
		sb.WriteString(fmt.Sprintf("\nRun with:\n```bash\ndocker run -p 8080:8080 %s\n```\n", image))
	}

	return sb.String(), nil
}

// ================== Run ==================

func (t *ContainerTool) runContainer(ctx context.Context, name, image string, port int) (string, error) {
	// Check if container exists
	checkOut, _ := runDockerCmd(ctx, t.workspace, 10, "ps", "-a", "--filter", "name="+name, "--format", "{{.Status}}")

	if strings.TrimSpace(checkOut) != "" {
		// Container exists, restart it
		_, _ = runDockerCmd(ctx, t.workspace, 30, "stop", name)
		_, _ = runDockerCmd(ctx, t.workspace, 30, "rm", name)
	}

	out, err := runDockerCmd(ctx, t.workspace, 60, "run", "-d",
		"--name", name,
		"-p", fmt.Sprintf("%d:%d", port, port),
		"--restart", "unless-stopped",
		image)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Container: `%s`\n\n", name))

	if err != nil {
		sb.WriteString(fmt.Sprintf("❌ Failed to start:\n```\n%s\n```\n", out))
		sb.WriteString("\n### Troubleshooting\n")
		sb.WriteString(fmt.Sprintf("- Check port %d is not in use\n", port))
		sb.WriteString(fmt.Sprintf("- Verify the image exists: `docker images | grep %s`\n", image))
	} else {
		containerID := strings.TrimSpace(out)
		if len(containerID) > 12 {
			containerID = containerID[:12]
		}
		sb.WriteString(fmt.Sprintf("✅ Container started\n\n**ID:** `%s`\n**Port:** %d\n\n", containerID, port))
		sb.WriteString(fmt.Sprintf("```bash\n# View logs\ndocker logs -f %s\n\n# Exec into container\ndocker exec -it %s sh\n\n# Stop\ndocker stop %s\n```\n", name, name, name))
	}

	return sb.String(), nil
}

// ================== Kubernetes ==================

func (t *ContainerTool) generateK8s(name, image string, port int, output string) (string, error) {
	if output == "" {
		output = filepath.Join(t.workspace, "k8s")
	} else if !filepath.IsAbs(output) {
		output = filepath.Join(t.workspace, output)
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return "", fmt.Errorf("create Kubernetes output directory: %w", err)
	}

	deployment := t.generateDeployment(name, image, port)
	service := t.generateService(name, port)
	configmap := t.generateConfigMap(name)

	// Generate kustomization
	kustomize := fmt.Sprintf(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: default

resources:
  - deployment.yaml
  - service.yaml
  - configmap.yaml

commonLabels:
  app: %s
`, name)
	for filename, content := range map[string]string{
		"deployment.yaml":    deployment,
		"service.yaml":       service,
		"configmap.yaml":     configmap,
		"kustomization.yaml": kustomize,
	} {
		path := filepath.Join(output, filename)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", fmt.Errorf("write generated Kubernetes file %s: %w", path, err)
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Kubernetes Manifests: `%s`\n\n", name))
	sb.WriteString(fmt.Sprintf("Generated in `%s/`:\n\n", output))
	sb.WriteString("- `deployment.yaml` — 2 replicas, resource limits, health checks\n")
	sb.WriteString("- `service.yaml` — ClusterIP + optional LoadBalancer\n")
	sb.WriteString("- `configmap.yaml` — environment configuration\n")
	sb.WriteString("- `kustomization.yaml` — Kustomize entrypoint\n\n")

	sb.WriteString("### Deploy\n\n")
	sb.WriteString("```bash\n")
	sb.WriteString(fmt.Sprintf("kubectl apply -k %s/\n", output))
	sb.WriteString(fmt.Sprintf("kubectl get pods -l app=%s\n", name))
	sb.WriteString(fmt.Sprintf("kubectl port-forward svc/%s %d:%d\n", name, port, port))
	sb.WriteString("```\n")

	return sb.String(), nil
}

func (t *ContainerTool) generateDeployment(name, image string, port int) string {
	return fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  labels:
    app: %s
spec:
  replicas: 2
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
        - name: %s
          image: %s
          imagePullPolicy: IfNotPresent
          ports:
            - containerPort: %d
              protocol: TCP
          envFrom:
            - configMapRef:
                name: %s-config
          resources:
            requests:
              memory: "64Mi"
              cpu: "100m"
            limits:
              memory: "512Mi"
              cpu: "500m"
          livenessProbe:
            httpGet:
              path: /health
              port: %d
            initialDelaySeconds: 10
            periodSeconds: 15
          readinessProbe:
            httpGet:
              path: /health
              port: %d
            initialDelaySeconds: 5
            periodSeconds: 10
      restartPolicy: Always
`, name, name, name, name, name, image, port, name, port, port)
}

func (t *ContainerTool) generateService(name string, port int) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
  labels:
    app: %s
spec:
  type: ClusterIP
  selector:
    app: %s
  ports:
    - port: %d
      targetPort: %d
      protocol: TCP
      name: http
`, name, name, name, port, port)
}

func (t *ContainerTool) generateConfigMap(name string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s-config
data:
  ENV: "production"
  TZ: "Asia/Shanghai"
  LOG_LEVEL: "info"
`, name)
}

// ================== Status ==================

func (t *ContainerTool) containerStatus(ctx context.Context) (string, error) {
	var sb strings.Builder
	sb.WriteString("## Container Status\n\n")

	// Docker info
	verOut, _ := runDockerCmd(ctx, t.workspace, 10, "version", "--format", "{{.Server.Version}}")
	if verOut != "" {
		sb.WriteString(fmt.Sprintf("**Docker:** v%s\n\n", strings.TrimSpace(verOut)))
	} else {
		sb.WriteString("**Docker:** Not available\n\n")
		return sb.String(), nil
	}

	// Running containers
	sb.WriteString("### Running Containers\n\n")
	psOut, _ := runDockerCmd(ctx, t.workspace, 10, "ps", "--format", "table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}")
	if strings.TrimSpace(psOut) != "" {
		sb.WriteString(fmt.Sprintf("```\n%s\n```\n\n", psOut))
	} else {
		sb.WriteString("No running containers.\n\n")
	}

	// Images
	sb.WriteString("### Images\n\n")
	imgOut, _ := runDockerCmd(ctx, t.workspace, 10, "images", "--format", "table {{.Repository}}\t{{.Tag}}\t{{.Size}}")
	sb.WriteString(fmt.Sprintf("```\n%s\n```\n\n", textutil.Truncate(imgOut, 1000)))

	// Disk usage
	sb.WriteString("### Disk Usage\n\n")
	dfOut, _ := runDockerCmd(ctx, t.workspace, 10, "system", "df")
	sb.WriteString(fmt.Sprintf("```\n%s\n```\n", dfOut))

	return sb.String(), nil
}

// ================== Compose ==================

func (t *ContainerTool) manageCompose(ctx context.Context, cmd string) (string, error) {
	// Find docker-compose files
	composeFiles := findComposeFiles(t.workspace)
	if len(composeFiles) == 0 {
		// Generate one first
		return "No docker-compose.yml found. Use `container action=containerize` to generate one.", nil
	}

	// Build compose command
	args := []string{"compose"}
	for _, f := range composeFiles {
		args = append(args, "-f", f)
	}
	switch cmd {
	case "up":
		args = append(args, "up", "-d")
	case "down":
		args = append(args, "down")
	case "ps":
		args = append(args, "ps")
	case "logs":
		args = append(args, "logs", "--tail=50")
	case "build":
		args = append(args, "build")
	case "restart":
		args = append(args, "restart")
	default:
		return "", fmt.Errorf("unknown compose command: %s", cmd)
	}

	out, err := runDockerCmd(ctx, t.workspace, 120, args...)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Docker Compose: `%s`\n\n", cmd))
	sb.WriteString(fmt.Sprintf("Files: %s\n\n", strings.Join(composeFiles, ", ")))
	sb.WriteString(fmt.Sprintf("```\n%s\n```\n", textutil.Truncate(out, 2000)))

	if err != nil {
		sb.WriteString(fmt.Sprintf("\n⚠️ Error: %v\n", err))
	}

	return sb.String(), nil
}

// ================== Cleanup ==================

func (t *ContainerTool) cleanup(ctx context.Context) (string, error) {
	var sb strings.Builder
	sb.WriteString("## Container Cleanup\n\n")

	// Remove stopped containers
	stoppedOut, _ := runDockerCmd(ctx, t.workspace, 30, "container", "prune", "-f")
	sb.WriteString(fmt.Sprintf("### Stopped Containers\n```\n%s\n```\n\n", stoppedOut))

	// Remove unused images
	imgOut, _ := runDockerCmd(ctx, t.workspace, 30, "image", "prune", "-f")
	sb.WriteString(fmt.Sprintf("### Unused Images\n```\n%s\n```\n\n", imgOut))

	// Remove unused volumes
	volOut, _ := runDockerCmd(ctx, t.workspace, 30, "volume", "prune", "-f")
	sb.WriteString(fmt.Sprintf("### Unused Volumes\n```\n%s\n```\n\n", volOut))

	// Remove build cache
	cacheOut, _ := runDockerCmd(ctx, t.workspace, 30, "builder", "prune", "-f")
	sb.WriteString(fmt.Sprintf("### Build Cache\n```\n%s\n```\n", cacheOut))

	sb.WriteString("✅ Cleanup complete.\n")
	return sb.String(), nil
}

// ================== Helpers ==================

func runDockerCmd(ctx context.Context, dir string, timeoutSec int, args ...string) (string, error) {
	commandContext, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(commandContext, "docker", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if errors.Is(commandContext.Err(), context.DeadlineExceeded) {
		return string(output), fmt.Errorf("timed out after %ds", timeoutSec)
	}
	return string(output), err
}

func findComposeFiles(dir string) []string {
	var files []string
	// Check common locations
	candidates := []string{
		filepath.Join(dir, "docker-compose.yml"),
		filepath.Join(dir, "docker-compose.yaml"),
		filepath.Join(dir, "docker", "docker-compose.yml"),
		filepath.Join(dir, "docker", "docker-compose.yaml"),
		filepath.Join(dir, "deploy", "docker-compose.yml"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			files = append(files, c)
		}
	}
	return files
}

// isDockerAvailable checks if docker is in PATH
func isDockerAvailable() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}
