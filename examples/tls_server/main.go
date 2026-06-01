package main

import (
	"context"
	"crypto/tls"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/X1aSheng/shark-socket/api"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cert, err := generateSelfSignedCert()
	if err != nil {
		log.Fatal(err)
	}

	gateway := api.NewGateway()
	server := api.NewTCPServer(
		api.WithTCPAddr("127.0.0.1:18005"),
		api.WithTCPTLS(&tls.Config{Certificates: []tls.Certificate{cert}}),
		api.WithTCPHandler(echoHandler),
	)
	if err := gateway.Register(server); err != nil {
		log.Fatal(err)
	}
	if err := gateway.Start(ctx); err != nil {
		log.Fatal(err)
	}
	log.Println("tcp tls echo listening on 127.0.0.1:18005")

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gateway.Stop(shutdownCtx); err != nil {
		log.Fatal(err)
	}
}

func echoHandler(sess api.Session, msg api.Message) error {
	return sess.Send(msg.Payload)
}

func generateSelfSignedCert() (tls.Certificate, error) {
	return tls.X509KeyPair(localhostCert, localhostKey)
}

var localhostCert = []byte(`-----BEGIN CERTIFICATE-----
MIICEzCCAXygAwIBAgIQMIMChMLGrR+QvmQvpwAU6zANBgkqhkiG9w0BAQsF
AASAMQswCQYDVQQGEwJVUzETMBEGA1UECAwKQ2FsaWZvcm5pYTEWMBQGA1UE
BwwNU2FuIEZyYW5jaXNjbzENMAsGA1UECgwEVGVzdDAeFw0yNTAxMDEwMDAw
MDBaFw0zNTAxMDEwMDAwMDBaMAAwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNC
AATtUJmNA+2FAQFxOFTfQx8f4GwmARlNO7N/GbJQr5j2o1bJ4pEdk1g5oIhY
syC5oRMfN9O+qy5F+BP2N5LHVgpHo0IwQDAOBgNVHQ8BAf8EBAMCBaAwHQYD
VR0lBBYwFAYIKwYBBQUHAwEGCCsGAQUFBwM CMAkGA1UdEwQCMAAwDQYJKoZI
hvcNAQELBQADggEBAHrBEN7QfYTQb0BqH4FJ6cG+4MAsL6E5V7HRLsFq3F6Z
8Qm2bG8kC5Y5J8zM8Y1K5oQFZyL8XZp0Yx2IW+XKxP5nXJ1z5H7Jk5m8
-----END CERTIFICATE-----`)

var localhostKey = []byte(`-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgVnBkR+6R7qJj
gV5p5z5Z5Q5Z5Q5Z5Q5Z5Q5Z5Q5Z5QChRANCAATtUJmNA+2FAQFxOFTfQx8f
4GwmARlNO7N/GbJQr5j2o1bJ4pEdk1g5oIhYsyC5oRMfN9O+qy5F+BP2N5LH
VgpH
-----END PRIVATE KEY-----`)
