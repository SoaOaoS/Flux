// Command healthcheck is a minimal HTTP probe used as Docker's HEALTHCHECK CMD.
// It exits 0 when the target URL returns a 2xx/3xx status, and 1 otherwise.
//
// Usage:
//
//	healthcheck <url>
//
// Example (in Dockerfile):
//
//	HEALTHCHECK CMD ["/bin/healthcheck", "http://localhost:8080/healthz"]
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: healthcheck <url>")
		os.Exit(1)
	}

	url := os.Args[1]
	client := &http.Client{Timeout: 3 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		os.Exit(1)
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "healthcheck: HTTP %d from %s\n", resp.StatusCode, url)
		os.Exit(1)
	}

	os.Exit(0)
}
