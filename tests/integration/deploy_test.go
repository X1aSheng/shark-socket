package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerfileExists(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "docker", "Dockerfile")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("Dockerfile not found at deploy/docker/Dockerfile")
	}
}

func TestDockerComposeExists(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "docker", "docker-compose.yml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("docker-compose.yml not found at deploy/docker/docker-compose.yml")
	}
}

func TestDockerignoreExists(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "docker", ".dockerignore")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal(".dockerignore not found at deploy/docker/.dockerignore")
	}
}

func TestDockerfileBuildsCorrectTarget(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "docker", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "./cmd/shark-socket") {
		t.Error("Dockerfile should build ./cmd/shark-socket as the production binary")
	}
}

func TestDockerfileHasAllPorts(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "docker", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	required := []string{
		"18000", "18200", "18400", "18600", "18800",
		"18900", "18650", "9091",
	}
	for _, port := range required {
		if !strings.Contains(content, port) {
			t.Errorf("Dockerfile missing port %s in EXPOSE", port)
		}
	}
}

func TestDockerfileHasHealthcheck(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "docker", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "HEALTHCHECK") {
		t.Error("Dockerfile should have a HEALTHCHECK instruction")
	}
}

func TestDockerComposeHasAllPorts(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "docker", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	required := []string{
		"18000:18000", "18200:18200/udp", "18400:18400",
		"18600:18600", "18800:18800/udp",
		"18900:18900/udp", "18650:18650", "9091:9091",
	}
	for _, port := range required {
		if !strings.Contains(content, port) {
			t.Errorf("docker-compose.yml missing port mapping %s", port)
		}
	}
}

func TestDockerComposeHasHealthcheck(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "docker", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "healthcheck") {
		t.Error("docker-compose.yml should have a healthcheck configuration")
	}
}

func TestDockerignoreExistsAndHasContent(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "docker", ".dockerignore"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	required := []string{".git", "tests", "examples", "*.md"}
	for _, entry := range required {
		if !strings.Contains(content, entry) {
			t.Errorf(".dockerignore missing entry: %s", entry)
		}
	}
}
