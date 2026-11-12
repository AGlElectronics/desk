package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestBlocked(t *testing.T) {
	for addr, want := range map[string]bool{
		"10.1.2.3": true, "172.20.0.1": true, "192.168.1.1": true, "127.0.0.1": true,
		"169.254.169.254": true, "100.64.0.1": true, "0.0.0.0": true, "::1": true,
		"fe80::1": true, "fd00::1": true, "::ffff:10.0.0.1": true, "224.0.0.1": true,
		"8.8.8.8": false, "142.250.74.37": false, "2a00:1450:4001::1": false,
	} {
		if got := Blocked(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Blocked(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestAllowList(t *testing.T) {
	g := New([]string{"Mail.Internal.", "10.20.0.0/16"})
	cases := []struct {
		host, ip string
		want     bool
	}{
		{"mail.internal", "10.9.9.9", true},
		{"other.internal", "10.20.1.1", true},
		{"other.internal", "10.21.1.1", false},
		{"imap.example.com", "93.184.216.34", true},
		{"imap.example.com", "127.0.0.1", false},
	}
	for _, c := range cases {
		if got := g.Permitted(c.host, netip.MustParseAddr(c.ip)); got != c.want {
			t.Errorf("Permitted(%s, %s) = %v, want %v", c.host, c.ip, got, c.want)
		}
	}
}

func TestDialRefusesLoopback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	_, err = New(nil).Dial(context.Background(), "127.0.0.1", port, time.Second)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected ErrBlocked, got %v", err)
	}
	if msg := Describe(err); !strings.Contains(msg, "DESK_ALLOWED_MAIL_HOSTS") || strings.Contains(msg, port) {
		t.Fatalf("message %q", msg)
	}

	conn, err := New([]string{"127.0.0.1/32"}).Dial(context.Background(), "127.0.0.1", port, time.Second)
	if err != nil {
		t.Fatalf("allow-listed CIDR: %v", err)
	}
	_ = conn.Close()
	conn, err = New([]string{"localhost"}).Dial(context.Background(), "localhost", port, time.Second)
	if err != nil {
		t.Fatalf("allow-listed name: %v", err)
	}
	_ = conn.Close()
}

func TestDescribeHidesDetails(t *testing.T) {
	_, err := New([]string{"127.0.0.1/32"}).Dial(context.Background(), "127.0.0.1", "1", time.Second)
	if err == nil {
		t.Skip("something listens on port 1")
	}
	msg := Describe(err)
	if strings.Contains(msg, "127.0.0.1") || strings.Contains(msg, ":1") {
		t.Fatalf("leaks the address: %q", msg)
	}
	if Describe(&net.DNSError{Err: "no such host", Name: "x.invalid"}) != "the server name was not found" {
		t.Fatal("dns")
	}
}
