package config

import (
	"os"
	"testing"
	"time"
)

func TestNewFileReloader(t *testing.T) {
	// Create a temp config file
	tmpFile, err := os.CreateTemp("", "config-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	config := struct {
		Port int `json:"port"`
	}{Port: 8080}

	_, err = tmpFile.WriteString(`{"port": 8080}`)
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	reloader := NewFileReloader(tmpFile.Name(), &config)
	if reloader == nil {
		t.Fatal("expected non-nil reloader")
	}
	if reloader.interval != 5*time.Second {
		t.Errorf("expected default interval 5s, got %v", reloader.interval)
	}
}

func TestFileReloader_Options(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	config := struct {
		Port int `json:"port"`
	}{Port: 8080}

	var reloadCount int
	reloader := NewFileReloader(tmpFile.Name(), &config,
		WithReloadInterval(100*time.Millisecond),
		WithOnReload(func(c any) { reloadCount++ }),
	)

	if reloader.interval != 100*time.Millisecond {
		t.Errorf("expected interval 100ms, got %v", reloader.interval)
	}
	if reloader.onReload == nil {
		t.Error("expected onReload callback to be set")
	}
	_ = reloadCount
}

func TestFileReloader_FileNotFound(t *testing.T) {
	config := struct {
		Port int `json:"port"`
	}{Port: 8080}

	reloader := NewFileReloader("/nonexistent/config.json", &config)
	err := reloader.Start()
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestFileReloader_InvalidJSON(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(`invalid json`)
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	config := struct {
		Port int `json:"port"`
	}{Port: 8080}

	reloader := NewFileReloader(tmpFile.Name(), &config)
	err = reloader.Reload()
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestFileReloader_Reload(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	config := struct {
		Port int `json:"port"`
	}{Port: 8080}

	_, err = tmpFile.WriteString(`{"port": 8080}`)
	if err != nil {
		t.Fatal(err)
	}

	reloader := NewFileReloader(tmpFile.Name(), &config)
	err = reloader.Reload()
	if err != nil {
		t.Errorf("unexpected reload error: %v", err)
	}

	c := reloader.Config()
	if c == nil {
		t.Error("expected non-nil config")
	}

	cfg, ok := c.(*struct {
		Port int `json:"port"`
	})
	if !ok {
		t.Error("unexpected config type")
	}
	if cfg.Port != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Port)
	}
}

func TestFileReloader_Stop(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	config := struct {
		Port int `json:"port"`
	}{Port: 8080}

	_, err = tmpFile.WriteString(`{"port": 8080}`)
	if err != nil {
		t.Fatal(err)
	}

	reloader := NewFileReloader(tmpFile.Name(), &config)
	err = reloader.Start()
	if err != nil {
		t.Fatal(err)
	}

	err = reloader.Stop()
	if err != nil {
		t.Errorf("unexpected stop error: %v", err)
	}
}
