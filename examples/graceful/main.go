// Command graceful is what a production server actually looks like: your own
// http.Server with timeouts, and a shutdown that lets in-flight requests finish.
//
// Engine.Run exists for examples and small tools. It leaves every timeout at
// zero, which means a slow or malicious client can hold a connection open
// indefinitely. Anything facing the internet should be wired like this instead.
//
//	go run ./examples/graceful
//	curl localhost:8080/slow &   # then press Ctrl+C: the request still finishes
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gozarp/zarp"
	"github.com/gozarp/zarp/middleware"
)

func main() {
	r := zarp.New()
	r.Use(middleware.Default()...)

	r.GET("/ping", func(c *zarp.Context) {
		c.Text(http.StatusOK, "pong")
	})

	// A request that outlives the shutdown signal, to show it is waited for.
	r.GET("/slow", func(c *zarp.Context) {
		select {
		case <-time.After(5 * time.Second):
			c.Text(http.StatusOK, "finished")
		case <-c.Done():
			// The request context is cancelled if the client goes away.
			return
		}
	})

	// Engine is an http.Handler, so it drops straight into http.Server.
	srv := &http.Server{
		Addr:    ":8080",
		Handler: r,

		// Timeouts are the reason to build the server yourself. Without them a
		// stalled client holds a connection and a goroutine for ever.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	// Ctrl+C or SIGTERM (what a container runtime sends) cancels this context.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Println("listening on", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	stop() // restore the default handler, so a second Ctrl+C kills it outright
	log.Println("shutting down; waiting for in-flight requests")

	// Shutdown stops accepting new connections and waits for open ones, up to
	// this deadline. Past it, Close cuts off whatever is left.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed, closing: %v", err)
		if err := srv.Close(); err != nil {
			log.Printf("close: %v", err)
		}
	}
	log.Println("stopped")
}
