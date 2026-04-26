package coap

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

func TestIntegration_CoAP_Echo(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find port: %v", err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	conn.Close()

	var handlerCalled atomic.Int32
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		handlerCalled.Add(1)
		return sess.Send(msg.Payload)
	}

	srv := NewServer(handler, WithAddr("127.0.0.1", port))
	if err := srv.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Stop(ctx)
	}()
	waitForUDPServer(t, fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)

	// Send a NON message with payload
	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer clientConn.Close()

	msg := &CoAPMessage{
		Version:   1,
		Type:      NON,
		Code:      CodePost,
		MessageID: 0x0001,
		Payload:   []byte("hello-coap"),
	}
	data, err := msg.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if _, err := clientConn.Write(data); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Wait for handler to be called
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if handlerCalled.Load() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if handlerCalled.Load() == 0 {
		t.Fatal("handler was not called")
	}
}

func TestIntegration_CoAP_CON_ACK(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find port: %v", err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	conn.Close()

	handler := func(sess types.RawSession, msg types.RawMessage) error {
		return nil
	}

	srv := NewServer(handler, WithAddr("127.0.0.1", port))
	if err := srv.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Stop(ctx)
	}()
	waitForUDPServer(t, fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)

	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer clientConn.Close()

	// Send CON message
	msg := &CoAPMessage{
		Version:   1,
		Type:      CON,
		Code:      CodeGet,
		MessageID: 0x0042,
		Payload:   []byte("test"),
	}
	data, err := msg.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if _, err := clientConn.Write(data); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read response (should be ACK)
	clientConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 1500)
	n, err := clientConn.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	resp, err := ParseMessage(buf[:n])
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	if resp.Type != ACK {
		t.Fatalf("response type = %v, want ACK", resp.Type)
	}
	if resp.MessageID != 0x0042 {
		t.Fatalf("response MessageID = 0x%04X, want 0x0042", resp.MessageID)
	}
}

func TestIntegration_CoAP_Dedup(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find port: %v", err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	conn.Close()

	var callCount atomic.Int32
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		callCount.Add(1)
		return nil
	}

	srv := NewServer(handler, WithAddr("127.0.0.1", port))
	if err := srv.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Stop(ctx)
	}()
	waitForUDPServer(t, fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)

	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer clientConn.Close()

	// Send same NON message twice with same MessageID
	msg := &CoAPMessage{
		Version:   1,
		Type:      NON,
		Code:      CodePost,
		MessageID: 0x0099,
		Payload:   []byte("dedup-test"),
	}
	data, _ := msg.Serialize()
	clientConn.Write(data)
	time.Sleep(50 * time.Millisecond)
	clientConn.Write(data)

	// Wait for processing
	time.Sleep(200 * time.Millisecond)

	// Handler should only be called once (dedup prevents second call)
	if callCount.Load() != 1 {
		t.Fatalf("handler called %d times, want 1 (dedup)", callCount.Load())
	}
}
