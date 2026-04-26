package main

import (
	"encoding/json"
	"log"
	stdhttp "net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/X1aSheng/shark-socket/api"
	"github.com/X1aSheng/shark-socket/internal/protocol/http"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	srv := api.NewHTTPServer(http.WithAddr("0.0.0.0", 8082))

	// Route: GET /hello
	srv.HandleFunc("/hello", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Hello from shark-socket!",
			"method":  r.Method,
		})
	})

	// Route: GET /health
	srv.HandleFunc("/health", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(stdhttp.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "healthy",
		})
	})

	// Route: GET / (root)
	srv.HandleFunc("/", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.URL.Path != "/" {
			stdhttp.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"service": "shark-socket",
			"routes":  []string{"/hello", "/health", "/"},
		})
	})

	if err := srv.Start(); err != nil {
		log.Fatalf("Failed to start HTTP server: %v", err)
	}

	log.Println("=== Basic HTTP Server (Mode A) ===")
	log.Println("Listening on :8082")
	log.Println("")
	log.Println("Test with:")
	log.Println("  curl http://localhost:8082/")
	log.Println("  curl http://localhost:8082/hello")
	log.Println("  curl http://localhost:8082/health")
	log.Println("")
	log.Println("Press Ctrl+C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
}
