package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/7yedeng/muse2api/internal/gateway"
)

func run() error {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println("muse2api 0.1.0-dev")
		return nil
	}
	if len(os.Args) != 2 || os.Args[1] != "serve" {
		return errors.New("usage: muse2api serve | version")
	}
	cfg, err := gateway.ConfigFromEnv()
	if err != nil {
		return err
	}
	backend := gateway.NewSDKBackend(cfg)
	app, err := gateway.New(cfg, backend)
	if err != nil {
		return err
	}
	defer app.Close()
	apiListener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return errors.New("API listener unavailable")
	}
	defer apiListener.Close()
	adminListener, err := net.Listen("tcp", cfg.AdminListen)
	if err != nil {
		return errors.New("admin listener unavailable")
	}
	defer adminListener.Close()
	api := &http.Server{Handler: app.APIHandler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 2 * time.Minute, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16 << 10}
	admin := &http.Server{Handler: app.AdminHandler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 2 * time.Minute, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fail := make(chan error, 2)
	go func() { fail <- api.Serve(apiListener) }()
	go func() { fail <- admin.Serve(adminListener) }()
	fmt.Println("muse2api started; API and local administration use separate listeners")
	select {
	case <-ctx.Done():
	case err = <-fail:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errors.New("HTTP server stopped unexpectedly")
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = api.Shutdown(shutdown)
	_ = admin.Shutdown(shutdown)
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
