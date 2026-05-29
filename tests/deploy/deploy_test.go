package deploy_test

import (
	"os"
	"strings"
	"testing"
)

func TestDockerfileEntrypoint(t *testing.T) {
	data, err := os.ReadFile("../../deploy/docker/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "go build -o /out/shark-socket-new ./cmd/shark-socket-new") {
		t.Fatal("Dockerfile does not build cmd/shark-socket-new")
	}
	if !strings.Contains(text, "ENTRYPOINT") {
		t.Fatal("Dockerfile missing ENTRYPOINT")
	}
}

func TestK8sAndHelmManifestsExist(t *testing.T) {
	paths := []string{
		"../../deploy/k8s/deployment.yaml",
		"../../deploy/k8s/service.yaml",
		"../../deploy/helm/shark-socket-new/Chart.yaml",
		"../../deploy/helm/shark-socket-new/templates/deployment.yaml",
		"../../deploy/helm/shark-socket-new/templates/service.yaml",
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s missing: %v", path, err)
		}
	}
}
