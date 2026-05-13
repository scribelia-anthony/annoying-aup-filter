package main

import (
	"context"
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed static
var staticFS embed.FS

func main() {
	proxyAddr := flag.String("proxy-addr", "127.0.0.1:8080", "address the proxy listens on (point ANTHROPIC_BASE_URL here)")
	uiAddr := flag.String("ui-addr", "127.0.0.1:8888", "address the UI/admin API listens on")
	upstream := flag.String("upstream", "https://api.anthropic.com", "upstream API base URL")
	maxCaptures := flag.Int("max-captures", 1000, "max captures kept in memory (ring buffer)")
	flag.Parse()

	store := NewStore(*maxCaptures)
	rules := NewRules()
	interceptor := NewInterceptor()
	fallback := NewFallback()

	proxy, err := NewProxy(*upstream, store, rules, interceptor, fallback)
	if err != nil {
		log.Fatalf("invalid upstream URL: %v", err)
	}

	proxyMux := http.NewServeMux()
	proxyMux.Handle("/", proxy)
	proxySrv := &http.Server{
		Addr:    *proxyAddr,
		Handler: proxyMux,
	}

	uiMux := http.NewServeMux()
	api := NewAPI(store, rules, interceptor, fallback, proxy)
	api.ProxyAddr = *proxyAddr
	api.RegisterRoutes(uiMux)
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("embed sub: %v", err)
	}
	uiMux.Handle("/", http.FileServer(http.FS(sub)))
	uiSrv := &http.Server{
		Addr:    *uiAddr,
		Handler: uiMux,
	}

	go func() {
		log.Printf("[proxy] listening on http://%s  -> forwards to %s", *proxyAddr, *upstream)
		if err := proxySrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("proxy server error: %v", err)
		}
	}()
	go func() {
		log.Printf("[ui]    listening on http://%s", *uiAddr)
		if err := uiSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ui server error: %v", err)
		}
	}()

	log.Println()
	log.Println(">>> To pipe Claude Code through this proxy, in another shell run:")
	log.Printf("    export ANTHROPIC_BASE_URL=http://%s", *proxyAddr)
	log.Printf("    Then open http://%s in your browser.", *uiAddr)
	log.Println()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = proxySrv.Shutdown(ctx)
	_ = uiSrv.Shutdown(ctx)
}
