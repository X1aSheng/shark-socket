package lwm2m

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/coap"
)

func TestCoAPResponderLifecycle(t *testing.T) {
	lwm := NewServer()
	server := coap.NewServer(
		coap.WithAddr("127.0.0.1:0"),
		coap.WithResponder(NewCoAPResponder(lwm)),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	conn, err := net.Dial("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	ack := exchangeCoAP(t, conn, coap.Message{
		Type:      coap.TypeCON,
		Code:      coap.CodePost,
		MessageID: 1,
		Payload:   []byte("register device-1 60 /3/0/1"),
	})
	if ack.Code != coap.CodeCreated || string(ack.Payload) != "registered device-1" {
		t.Fatalf("register ack = %#v", ack)
	}
	if _, ok := lwm.Registration("device-1"); !ok {
		t.Fatal("registration missing")
	}

	ack = exchangeCoAP(t, conn, coap.Message{
		Type:      coap.TypeCON,
		Code:      coap.CodePut,
		MessageID: 2,
		Payload:   []byte("write device-1 /3/0/1 online"),
	})
	if ack.Code != coap.CodeChanged || string(ack.Payload) != "changed /3/0/1" {
		t.Fatalf("write ack = %#v", ack)
	}

	ack = exchangeCoAP(t, conn, coap.Message{
		Type:      coap.TypeCON,
		Code:      coap.CodeGet,
		MessageID: 3,
		Payload:   []byte("read device-1 /3/0/1"),
	})
	if ack.Code != coap.CodeContent || string(ack.Payload) != "online" {
		t.Fatalf("read ack = %#v", ack)
	}

	ack = exchangeCoAP(t, conn, coap.Message{
		Type:      coap.TypeCON,
		Code:      coap.CodeDelete,
		MessageID: 4,
		Payload:   []byte("deregister device-1"),
	})
	if ack.Code != coap.CodeDeleted || string(ack.Payload) != "deregistered device-1" {
		t.Fatalf("deregister ack = %#v", ack)
	}
	if _, ok := lwm.Registration("device-1"); ok {
		t.Fatal("registration still exists")
	}
}

func exchangeCoAP(t *testing.T, conn net.Conn, req coap.Message) coap.Message {
	t.Helper()
	data, err := req.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := coap.Parse(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if ack.MessageID != req.MessageID {
		t.Fatalf("message id = %d, want %d", ack.MessageID, req.MessageID)
	}
	return ack
}

func stopGateway(t *testing.T, gateway *runtime.Gateway) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := gateway.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
