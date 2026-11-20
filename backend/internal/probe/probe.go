// Package probe implements the "healthcheck" subcommand of distroless
// binaries: the images have no curl or wget, so Docker / Compose runs
// `<binary> healthcheck` to call the process's own /livez.
package probe

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// URL is the /livez address of a server listening on addr (":8080",
// "0.0.0.0:8080", …) on this machine. TLS is used when TLS_CERT_FILE is set,
// like tlscert.Configure.
func URL(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("listen address %q: %w", addr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	scheme := "http"
	if os.Getenv("TLS_CERT_FILE") != "" {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, port) + "/livez", nil
}

// Check calls url and fails unless it answers 2xx within timeout. The
// certificate is not verified: the probe only talks to its own process.
func Check(url string, timeout time.Duration) error {
	c := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // loopback probe of our own listener
		},
	}
	res, err := c.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("%s answered %s", url, res.Status)
	}
	return nil
}

// Run is the subcommand: it exits 0 when healthy, 1 otherwise.
func Run(addr string) {
	url, err := URL(addr)
	if err == nil {
		err = Check(url, 3*time.Second)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "unhealthy:", err)
		os.Exit(1)
	}
	fmt.Println("ok")
	os.Exit(0)
}
