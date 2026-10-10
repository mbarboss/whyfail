package netguard

import (
	"context"
	"errors"
	"net"
	"testing"
)

// fakeResolver answers from a fixed table and fails for unknown names.
type fakeResolver map[string][]string

func (f fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	ips, ok := f[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	addrs := make([]net.IPAddr, len(ips))
	for i, s := range ips {
		addrs[i] = net.IPAddr{IP: net.ParseIP(s)}
	}
	return addrs, nil
}

var resolver = fakeResolver{
	"localhost":         {"127.0.0.1", "::1"},
	"ollama.localhost":  {"127.0.0.1"},
	"hijacked":          {"203.0.113.7"},
	"ollama.lan":        {"192.168.1.10"},
	"ollama.example":    {"93.184.216.34", "2606:2800:220:1::1"},
	"mixed":             {"127.0.0.1", "10.0.0.5"},
	"empty":             {},
	"mapped-v4-in-v6":   {"::ffff:127.0.0.1"},
	"loopback-range-v4": {"127.42.0.1"},
}

func TestCheckHostAllowsLoopback(t *testing.T) {
	for _, host := range []string{
		"127.0.0.1", "127.255.255.254", "::1", "::ffff:127.0.0.1",
		"localhost", "ollama.localhost", "mapped-v4-in-v6", "loopback-range-v4",
	} {
		t.Run(host, func(t *testing.T) {
			if err := CheckHost(context.Background(), resolver, host); err != nil {
				t.Errorf("CheckHost: %v", err)
			}
		})
	}
}

func TestCheckHostRefusesRemote(t *testing.T) {
	for _, host := range []string{
		"10.0.0.5", "172.16.0.1", "192.168.1.10", "203.0.113.7", "2001:db8::1", "fe80::1",
		"0.0.0.0", "::", "169.254.169.254",
		"hijacked", "ollama.lan", "ollama.example", "mixed",
	} {
		t.Run(host, func(t *testing.T) {
			err := CheckHost(context.Background(), resolver, host)

			if !errors.Is(err, ErrNotLoopback) {
				t.Errorf("err = %v, want ErrNotLoopback", err)
			}
		})
	}
}

func TestCheckHostUnresolved(t *testing.T) {
	for _, host := range []string{"no-such-host", "empty", ""} {
		t.Run(host, func(t *testing.T) {
			err := CheckHost(context.Background(), resolver, host)

			if !errors.Is(err, ErrUnresolved) {
				t.Errorf("err = %v, want ErrUnresolved", err)
			}
		})
	}
}

func TestCheckHostIPLiteralSkipsResolver(t *testing.T) {
	// A nil resolver would panic if it were used.
	if err := CheckHost(context.Background(), nil, "127.0.0.1"); err != nil {
		t.Errorf("CheckHost: %v", err)
	}
}

func TestControlAllowsLoopback(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:11434", "[::1]:11434", "127.9.9.9:80", "[::ffff:127.0.0.1]:11434"} {
		if err := Control("tcp", addr, nil); err != nil {
			t.Errorf("Control(%q): %v", addr, err)
		}
	}
}

func TestControlRefusesOthers(t *testing.T) {
	for _, addr := range []string{
		"10.0.0.5:11434", "[2001:db8::1]:11434", "0.0.0.0:11434", "93.184.216.34:443",
		"localhost:11434", "not-an-address", "",
	} {
		if err := Control("tcp", addr, nil); !errors.Is(err, ErrNotLoopback) {
			t.Errorf("Control(%q) = %v, want ErrNotLoopback", addr, err)
		}
	}
}
