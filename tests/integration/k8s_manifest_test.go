package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestK8sDeploymentHasAllPorts(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "k8s", "app", "deployment.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	required := map[string]string{
		"tcp":       "18000",
		"udp":       "18200",
		"http":      "18400",
		"websocket": "18600",
		"coap":      "18800",
		"quic":      "18900",
		"grpcweb":   "18650",
		"metrics":   "9091",
	}
	for name, port := range required {
		if !strings.Contains(content, "name: "+name) || !strings.Contains(content, "containerPort: "+port) {
			t.Errorf("deployment.yaml missing container port %s (%s)", name, port)
		}
	}
}

func TestK8sDeploymentHasSecurityContext(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "k8s", "app", "deployment.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "runAsNonRoot") {
		t.Error("deployment.yaml should have PodSecurityContext with runAsNonRoot")
	}
	if !strings.Contains(content, "allowPrivilegeEscalation") {
		t.Error("deployment.yaml should have ContainerSecurityContext with allowPrivilegeEscalation")
	}
}

func TestK8sDeploymentHasEnvVars(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "k8s", "app", "deployment.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	required := []string{"SHARK_LOG_LEVEL", "SHARK_QUIC_ENABLED", "SHARK_GRPCWEB_ENABLED"}
	for _, env := range required {
		if !strings.Contains(content, env) {
			t.Errorf("deployment.yaml missing env var: %s", env)
		}
	}
}

func TestK8sServiceHasAllProtocols(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "k8s", "app", "service.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	services := []string{
		"shark-socket-tcp", "shark-socket-udp", "shark-socket-http",
		"shark-socket-ws", "shark-socket-coap",
		"shark-socket-quic", "shark-socket-grpcweb", "shark-socket-metrics",
	}
	for _, svc := range services {
		if !strings.Contains(content, "name: "+svc) {
			t.Errorf("service.yaml missing service: %s", svc)
		}
	}
}

func TestK8sNetworkPolicyAllowsGrpcWeb(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "k8s", "app", "networkpolicy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "18650") {
		t.Error("networkpolicy.yaml should allow gRPC-Web port 18650")
	}
}

func TestK8sAppConfigmapExists(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "k8s", "app", "configmap.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("configmap.yaml not found at deploy/k8s/app/configmap.yaml")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "shark-socket-config") {
		t.Error("App configmap should be named shark-socket-config")
	}
}

func TestK8sManifestsExist(t *testing.T) {
	base := filepath.Join("..", "..", "deploy", "k8s", "app")
	files := []string{
		"namespace.yaml", "configmap.yaml", "deployment.yaml",
		"service.yaml", "hpa.yaml", "networkpolicy.yaml",
		"kustomization.yaml", "ingress.yaml",
	}
	for _, f := range files {
		path := filepath.Join(base, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Missing K8s manifest: %s", f)
		}
	}
}

func TestK8sInfraPrometheusExists(t *testing.T) {
	base := filepath.Join("..", "..", "deploy", "k8s", "infra", "prometheus")
	files := []string{"deployment.yaml", "service.yaml", "configmap.yaml"}
	for _, f := range files {
		path := filepath.Join(base, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Missing prometheus manifest: %s", f)
		}
	}
}

func TestProdEntryPointExists(t *testing.T) {
	path := filepath.Join("..", "..", "cmd", "shark-socket", "main.go")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("Production entry point not found at cmd/shark-socket/main.go")
	}
}
