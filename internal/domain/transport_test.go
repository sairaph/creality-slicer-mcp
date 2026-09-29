package domain

import (
	"strings"
	"testing"
)

func TestTransportAndAddrSettings(t *testing.T) {
	cases := []struct {
		name, transport, addr string
		wantTransport         string
		wantAddr              string
		wantErr               string
	}{
		{"defaults", "", "", "stdio", DefaultHTTPAddr, ""},
		{"stdio", "stdio", "", "stdio", DefaultHTTPAddr, ""},
		{"case and space", "  HTTP ", "", "http", DefaultHTTPAddr, ""},
		{"http with addr", "http", "127.0.0.1:9000", "http", "127.0.0.1:9000", ""},
		{"http with empty host", "http", ":9000", "http", ":9000", ""},
		{"bad transport", "htpp", "", "", "", `TRANSPORT: "htpp" is not stdio or http`},
		{"bad addr without port", "http", "localhost", "", "", "ADDR:"},
		{"bad addr port", "http", "127.0.0.1:99999", "", "", "ADDR:"},
		{"bad addr port text", "http", "127.0.0.1:web", "", "", "ADDR:"},
		// ADDR only matters for http, so a bad one is not an error for stdio.
		{"bad addr ignored for stdio", "stdio", "nonsense", "stdio", DefaultHTTPAddr, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(EnvCmd, "")
			t.Setenv(EnvTransport, c.transport)
			t.Setenv(EnvAddr, c.addr)
			s, err := SettingsFromEnv()
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, c.wantErr)
				}
				return
			}
			if err != nil || s.Transport != c.wantTransport || s.Addr != c.wantAddr {
				t.Fatalf("SettingsFromEnv() = %+v, %v; want transport %s addr %s", s, err, c.wantTransport, c.wantAddr)
			}
		})
	}
}
