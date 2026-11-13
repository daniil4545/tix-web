// Command tix-web serves a local board for the tix task tracker on 127.0.0.1.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tix-web:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fl := flag.NewFlagSet("tix-web", flag.ContinueOnError)
	addr := fl.String("addr", "127.0.0.1:8737", "listen address, 127.0.0.1:port")
	bin := fl.String("tix", "tix", "tix binary, a name in PATH or a path")
	if err := fl.Parse(args); err != nil {
		return fmt.Errorf("flags: %w", err)
	}
	path, err := exec.LookPath(*bin)
	if err != nil {
		return fmt.Errorf("tix not found (%s): install tix or pass -tix /path/to/tix", *bin)
	}
	host, _, err := net.SplitHostPort(*addr)
	if err != nil {
		return fmt.Errorf("-addr %q: %w", *addr, err)
	}
	// The Host check and the printed URL assume this exact address.
	if host != "127.0.0.1" {
		return fmt.Errorf("-addr %q: only 127.0.0.1 is allowed", *addr)
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", *addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	fmt.Printf("tix-web: http://127.0.0.1:%s\n", port)
	srv := &http.Server{Handler: newServer(tixCLI{bin: path}, port), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
