package models

import "testing"

func TestDatadogEndpointValidation(t *testing.T) {
	tests := []struct {
		name        string
		cfg         DatadogConfig
		wantNetwork string
		wantAddr    string
		wantErr     bool
	}{
		{name: "host:port", cfg: DatadogConfig{Endpoint: "localhost:8125"}, wantNetwork: "udp", wantAddr: "localhost:8125"},
		{name: "udp scheme", cfg: DatadogConfig{Endpoint: "udp://10.0.0.1:8125"}, wantNetwork: "udp", wantAddr: "10.0.0.1:8125"},
		{name: "ipv6", cfg: DatadogConfig{Endpoint: "udp://[::1]:8125"}, wantNetwork: "udp", wantAddr: "[::1]:8125"},
		{name: "unixgram", cfg: DatadogConfig{Endpoint: "unixgram:///var/run/dd.sock"}, wantNetwork: "unixgram", wantAddr: "/var/run/dd.sock"},
		{name: "endpoint overrides agent", cfg: DatadogConfig{Endpoint: "a:1", AgentHost: "b", AgentPort: 2}, wantNetwork: "udp", wantAddr: "a:1"},
		{name: "agent fields", cfg: DatadogConfig{AgentHost: "b", AgentPort: 2}, wantNetwork: "udp", wantAddr: "b:2"},
		{name: "bad endpoint ignores valid agent", cfg: DatadogConfig{Endpoint: "nope", AgentHost: "b", AgentPort: 2}, wantErr: true},
		{name: "no port", cfg: DatadogConfig{Endpoint: "localhost"}, wantErr: true},
		{name: "no host", cfg: DatadogConfig{Endpoint: ":8125"}, wantErr: true},
		{name: "port zero", cfg: DatadogConfig{Endpoint: "localhost:0"}, wantErr: true},
		{name: "port too large", cfg: DatadogConfig{Endpoint: "localhost:65536"}, wantErr: true},
		{name: "port not numeric", cfg: DatadogConfig{Endpoint: "localhost:http"}, wantErr: true},
		{name: "tcp scheme", cfg: DatadogConfig{Endpoint: "tcp://localhost:8125"}, wantErr: true},
		{name: "unix scheme", cfg: DatadogConfig{Endpoint: "unix:///var/run/dd.sock"}, wantErr: true},
		{name: "unixgram empty", cfg: DatadogConfig{Endpoint: "unixgram://"}, wantErr: true},
		{name: "unixgram root only", cfg: DatadogConfig{Endpoint: "unixgram:///"}, wantErr: true},
		{name: "unixgram relative", cfg: DatadogConfig{Endpoint: "unixgram://dd.sock"}, wantErr: true},
		{name: "udp empty", cfg: DatadogConfig{Endpoint: "udp://"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			network, addr, err := tt.cfg.ResolveEndpoint()
			if err != nil {
				t.Fatal(err)
			}
			if network != tt.wantNetwork || addr != tt.wantAddr {
				t.Fatalf("ResolveEndpoint() = %q, %q; want %q, %q", network, addr, tt.wantNetwork, tt.wantAddr)
			}
			if got := tt.cfg.Address(); got != tt.wantAddr {
				t.Fatalf("Address() = %q, want %q", got, tt.wantAddr)
			}
		})
	}
}

func TestBufferSizeCapped(t *testing.T) {
	base := DatadogConfig{Endpoint: "localhost:8125"}
	tests := []struct {
		name     string
		size     int
		wantErr  bool
		wantSize int
	}{
		{"default udp", 0, false, DefaultDatadogUDPBufferSize},
		{"explicit", 512, false, 512},
		{"max", MaxDatadogBufferSize, false, MaxDatadogBufferSize},
		{"above max", MaxDatadogBufferSize + 1, true, 0},
		{"negative", -1, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			cfg.BufferSize = tt.size
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && cfg.PacketSize() != tt.wantSize {
				t.Fatalf("PacketSize() = %d, want %d", cfg.PacketSize(), tt.wantSize)
			}
		})
	}

	unix := DatadogConfig{Endpoint: "unixgram:///tmp/dd.sock"}
	if got := unix.PacketSize(); got != DefaultDatadogUnixgramBufferSize {
		t.Fatalf("unixgram PacketSize() = %d, want %d", got, DefaultDatadogUnixgramBufferSize)
	}
	unix.BufferSize = 100
	if got := unix.PacketSize(); got != 100 {
		t.Fatalf("explicit unixgram PacketSize() = %d, want 100", got)
	}
}
