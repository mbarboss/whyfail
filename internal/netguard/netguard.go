// Package netguard keeps captured output on the local machine by allowing
// connections to loopback addresses only.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
)

// Errors returned when a host or address is refused.
var (
	ErrNotLoopback = errors.New("not a loopback address")
	ErrUnresolved  = errors.New("host did not resolve")
)

// Resolver looks up the addresses of a host. *net.Resolver implements it.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// CheckHost returns nil when host is a loopback IP or a name that resolves
// only to loopback addresses. A name with any other address is refused, since
// the connection could go to either.
func CheckHost(ctx context.Context, r Resolver, host string) error {
	if ip, err := netip.ParseAddr(host); err == nil {
		if !isLoopback(ip) {
			return fmt.Errorf("%s: %w", host, ErrNotLoopback)
		}
		return nil
	}
	if host == "" {
		return fmt.Errorf("empty host: %w", ErrUnresolved)
	}

	addrs, err := r.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("%s: %w: %w", host, ErrUnresolved, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("%s: %w", host, ErrUnresolved)
	}
	for _, a := range addrs {
		ip, ok := netip.AddrFromSlice(a.IP)
		if !ok || !isLoopback(ip) {
			return fmt.Errorf("%s resolves to an address that is %w", host, ErrNotLoopback)
		}
	}
	return nil
}

// Control is a net.Dialer Control function that refuses to connect to any
// address that is not loopback. It runs after name resolution, for every
// connection attempt, so it also covers DNS rebinding and redirects.
func Control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !isLoopback(ap.Addr()) {
		return fmt.Errorf("connection to %q refused: %w", address, ErrNotLoopback)
	}
	return nil
}

func isLoopback(ip netip.Addr) bool {
	return ip.Unmap().IsLoopback()
}
