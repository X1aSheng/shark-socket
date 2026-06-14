package deploy_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerfileEntrypoint(t *testing.T) {
	data, err := os.ReadFile("../../deploy/docker/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "go build -o /out/shark-socket ./cmd/shark-socket") {
		t.Fatal("Dockerfile does not build cmd/shark-socket")
	}
	if !strings.Contains(text, "ENTRYPOINT") {
		t.Fatal("Dockerfile missing ENTRYPOINT")
	}
	assertContains(t, text, "ARG GOPROXY=")
	assertContains(t, text, "EXPOSE 18000 18080 18081")
	assertContains(t, text, "HEALTHCHECK")
	assertContains(t, text, "ca-certificates")
	assertContains(t, text, "adduser -u 1000")
}

func TestK8sAndHelmManifestsExist(t *testing.T) {
	paths := []string{
		"../../deploy/k8s/namespace.yaml",
		"../../deploy/k8s/serviceaccount.yaml",
		"../../deploy/k8s/configmap.yaml",
		"../../deploy/k8s/deployment.yaml",
		"../../deploy/k8s/service.yaml",
		"../../deploy/k8s/networkpolicy.yaml",
		"../../deploy/k8s/pdb.yaml",
		"../../deploy/k8s/hpa.yaml",
		"../../deploy/helm/shark-socket/Chart.yaml",
		"../../deploy/helm/shark-socket/templates/deployment.yaml",
		"../../deploy/helm/shark-socket/templates/service.yaml",
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s missing: %v", path, err)
		}
	}
}

func TestK8sManifestSemantics(t *testing.T) {
	deployment := readFile(t, "../../deploy/k8s/deployment.yaml")
	service := readFile(t, "../../deploy/k8s/service.yaml")
	kustomization := readFile(t, "../../deploy/k8s/kustomization.yaml")

	assertContains(t, deployment, "kind: Deployment")
	assertContains(t, deployment, "name: shark-socket")
	assertContains(t, deployment, "app: shark-socket")
	assertContains(t, deployment, "containerPort: 18000")
	assertContains(t, deployment, "containerPort: 18080")
	assertContains(t, deployment, "containerPort: 18081")
	assertContains(t, deployment, "SHARK_TCP_ADDR")
	assertContains(t, deployment, "0.0.0.0:18000")
	assertContains(t, deployment, "readinessProbe:")
	assertContains(t, deployment, "livenessProbe:")
	assertContains(t, deployment, "path: /readyz")
	assertContains(t, deployment, "path: /healthz")
	assertContains(t, deployment, "runAsNonRoot: true")
	assertContains(t, deployment, "allowPrivilegeEscalation: false")
	assertContains(t, deployment, "readOnlyRootFilesystem: true")
	assertContains(t, deployment, "resources:")
	assertContains(t, deployment, "serviceAccountName: shark-socket")
	assertContains(t, deployment, "fsGroup: 1000")

	assertContains(t, service, "kind: Service")
	assertContains(t, service, "app: shark-socket")
	assertContains(t, service, "port: 18000")
	assertContains(t, service, "targetPort: 18000")
	assertContains(t, service, "port: 18080")
	assertContains(t, service, "port: 18081")

	assertContains(t, kustomization, "namespace.yaml")
	assertContains(t, kustomization, "serviceaccount.yaml")
	assertContains(t, kustomization, "configmap.yaml")
	assertContains(t, kustomization, "deployment.yaml")
	assertContains(t, kustomization, "service.yaml")
	assertContains(t, kustomization, "networkpolicy.yaml")
	assertContains(t, kustomization, "pdb.yaml")
	assertContains(t, kustomization, "hpa.yaml")
}

func TestHelmChartSemantics(t *testing.T) {
	chart := readFile(t, "../../deploy/helm/shark-socket/Chart.yaml")
	values := readFile(t, "../../deploy/helm/shark-socket/values.yaml")
	deployment := readFile(t, "../../deploy/helm/shark-socket/templates/deployment.yaml")
	service := readFile(t, "../../deploy/helm/shark-socket/templates/service.yaml")

	assertContains(t, chart, "apiVersion: v2")
	assertContains(t, chart, "name: shark-socket")
	assertContains(t, values, "repository: shark-socket")
	assertContains(t, values, "port: 18000")
	assertContains(t, values, "metricsPort: 18080")
	assertContains(t, values, "healthPort: 18081")
	assertContains(t, values, "tcpAddr: \"0.0.0.0:18000\"")
	assertContains(t, values, "podSecurityContext:")
	assertContains(t, values, "resources:")
	assertContains(t, values, "probes:")
	assertContains(t, deployment, "{{ .Values.replicaCount }}")
	assertContains(t, deployment, "{{ .Values.image.repository }}:{{ .Values.image.tag }}")
	assertContains(t, deployment, "{{ .Values.service.port }}")
	assertContains(t, deployment, "{{ .Values.service.metricsPort }}")
	assertContains(t, deployment, "{{ .Values.service.healthPort }}")
	assertContains(t, deployment, "SHARK_TCP_ADDR")
	assertContains(t, deployment, "readinessProbe:")
	assertContains(t, deployment, "livenessProbe:")
	assertContains(t, deployment, "path: /readyz")
	assertContains(t, deployment, "path: /healthz")
	assertContains(t, deployment, "allowPrivilegeEscalation: {{ .Values.securityContext.allowPrivilegeEscalation }}")
	assertContains(t, deployment, "fsGroup: {{ .Values.podSecurityContext.fsGroup }}")
	assertContains(t, deployment, "serviceAccountName: {{ include \"shark-socket.fullname\" . }}")
	assertContains(t, service, "{{ .Values.service.type }}")
	assertContains(t, service, "{{ .Values.service.port }}")
}

func TestDeployToolRenderingWhenAvailable(t *testing.T) {
	root := projectRoot(t)
	if _, err := exec.LookPath("kubectl"); err == nil {
		out := runCommand(t, root, "kubectl", "kustomize", "deploy/k8s")
		assertContains(t, out, "kind: Deployment")
		assertContains(t, out, "kind: Service")
	} else {
		t.Log("kubectl not found; skipping kustomize render validation")
	}

	if _, err := exec.LookPath("helm"); err == nil {
		out := runCommand(t, root, "helm", "template", "shark-socket", "deploy/helm/shark-socket")
		assertContains(t, out, "kind: Deployment")
		assertContains(t, out, "kind: Service")
	} else {
		t.Log("helm not found; skipping helm template validation")
	}

	if _, err := exec.LookPath("docker"); err == nil {
		out := runCommand(t, root, "docker", "compose", "-f", "deploy/docker/docker-compose.yml", "config")
		assertContains(t, out, "shark-socket")
		assertContains(t, out, "GOPROXY")
	} else {
		t.Log("docker not found; skipping docker compose config validation")
	}
}

func TestGitHubActionsWorkflowSemantics(t *testing.T) {
	workflow := readFile(t, "../../.github/workflows/ci.yml")

	assertContains(t, workflow, "actions/checkout@v6")
	assertContains(t, workflow, "actions/setup-go@v6")
	assertContains(t, workflow, "go-version-file: go.mod")
	assertContains(t, workflow, "windows-latest")
	assertContains(t, workflow, "ubuntu-latest")
	assertContains(t, workflow, "pull_request:")
	assertContains(t, workflow, "concurrency:")
	assertContains(t, workflow, "golangci-lint")
	assertContains(t, workflow, "govulncheck")
	assertContains(t, workflow, `go run scripts/run_tests.go -mode unit -timeout 5m`)
	assertContains(t, workflow, `go run scripts/run_tests.go -mode integration -timeout 5m`)
	assertContains(t, workflow, `go run scripts/run_tests.go -mode race -timeout 5m`)
	assertContains(t, workflow, `go run scripts/run_tests.go -mode cover -timeout 5m`)
	assertContains(t, workflow, "./scripts/validate.ps1")
	assertContains(t, workflow, "./scripts/validate_deploy.ps1")
	assertContains(t, workflow, "actions/upload-artifact@v7")
	assertContains(t, workflow, "validation-logs-ubuntu-latest")
	assertContains(t, workflow, "validation-logs-windows-latest")
	assertContains(t, workflow, "race-logs-ubuntu-latest")
	assertContains(t, workflow, "coverage-logs-ubuntu-latest")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestExamplesCompile(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// deploy_test.go is in tests/deploy/; root is ../../examples
	examplesDir := filepath.Join(root, "..", "..", "examples")
	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(examplesDir, entry.Name())
		// Skip directories without Go files (e.g. config/)
		gos, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		if len(gos) == 0 {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			cmd := exec.Command("go", "build", "-o", os.DevNull, ".")
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go build %s: %v\n%s", entry.Name(), err, out)
			}
		})
	}
}

func assertContains(t *testing.T, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Fatalf("expected %q in:\n%s", want, text)
	}
}

func projectRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func runCommand(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}
