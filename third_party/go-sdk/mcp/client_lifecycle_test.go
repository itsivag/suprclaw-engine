package mcp

import (
	"context"
	"testing"
	"time"
)

func TestClientConnectWithSessionContext_SessionSurvivesBootstrapCancellation(t *testing.T) {
	server := NewServer(&Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	clientTransport, serverTransport := NewInMemoryTransports()

	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect() failed: %v", err)
	}
	t.Cleanup(func() {
		_ = serverSession.Close()
	})

	client := NewClient(&Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	bootstrapCtx, cancelBootstrap := context.WithCancel(context.Background())
	sessionCtx, cancelSession := context.WithCancel(context.Background())
	defer cancelSession()

	clientSession, err := client.ConnectWithSessionContext(bootstrapCtx, sessionCtx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.ConnectWithSessionContext() failed: %v", err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
	})

	cancelBootstrap()
	time.Sleep(20 * time.Millisecond)

	if err := clientSession.Ping(context.Background(), nil); err != nil {
		t.Fatalf("Ping() failed after bootstrap cancellation: %v", err)
	}
}
