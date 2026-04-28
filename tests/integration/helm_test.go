package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const helmChartDir = "../../deploy/k8s/helm/shark-socket"

func TestHelmChartExists(t *testing.T) {
	files := []string{
		"Chart.yaml", "values.yaml", "values-prod.yaml", "README.md",
	}
	for _, f := range files {
		path := filepath.Join(helmChartDir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Helm chart missing file: %s", f)
		}
	}
}

func TestHelmTemplatesExist(t *testing.T) {
	templates := []string{
		"_helpers.tpl", "deployment.yaml", "service.yaml",
		"configmap.yaml", "ingress.yaml", "hpa.yaml",
		"networkpolicy.yaml", "servicemonitor.yaml", "NOTES.txt",
	}
	for _, f := range templates {
		path := filepath.Join(helmChartDir, "templates", f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Helm chart missing template: %s", f)
		}
	}
}

func TestHelmChartYamlValid(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(helmChartDir, "Chart.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "name: shark-socket") {
		t.Error("Chart.yaml should have name: shark-socket")
	}
	if !strings.Contains(content, "apiVersion: v2") {
		t.Error("Chart.yaml should have apiVersion: v2")
	}
}

func TestHelmValuesHasAllProtocols(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(helmChartDir, "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	protocols := []string{"tcp", "udp", "http", "websocket", "coap", "quic", "grpcweb"}
	for _, p := range protocols {
		if !strings.Contains(content, p+":") {
			t.Errorf("values.yaml missing protocol section: %s", p)
		}
	}
}

func TestHelmValuesHasMetrics(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(helmChartDir, "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "metrics:") {
		t.Error("values.yaml missing metrics section")
	}
	if !strings.Contains(content, "serviceMonitor") {
		t.Error("values.yaml missing serviceMonitor section")
	}
}

func TestHelmHelpersDefinesEnv(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(helmChartDir, "templates", "_helpers.tpl"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "shark-socket.env") {
		t.Error("_helpers.tpl should define shark-socket.env template")
	}
	if !strings.Contains(content, "SHARK_TCP_ENABLED") {
		t.Error("_helpers.tpl env template should include SHARK_TCP_ENABLED")
	}
}

func TestHelmNotesHasEndpoints(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(helmChartDir, "templates", "NOTES.txt"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "shark-socket") {
		t.Error("NOTES.txt should reference service names")
	}
}

func TestK8sOldDirRemoved(t *testing.T) {
	path := filepath.Join("..", "..", "k8s")
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		t.Error("Old k8s/ directory should have been removed")
	}
}

func TestDeployReadmeExists(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "README.md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("deploy/README.md not found")
	}
}
