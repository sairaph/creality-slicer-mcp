package mcpserver

import (
	"context"
	"net"
	"testing"
	"time"
)

// A port that is already taken makes the http transport fail at once, and the
// shutdown goroutine ends with it instead of waiting on the context forever.
func TestRunHTTPReturnsWhenThePortIsTaken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	srv := newServer(Config{Transport: "http", HTTPAddr: ln.Addr().String()})
	errc := make(chan error, 1)
	go func() { errc <- srv.Run(context.Background()) }()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("Run succeeded on a taken port")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return")
	}
}
