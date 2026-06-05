// Command annoying-aup-filter runs an HTTP proxy + admin web UI that sits
// between Claude Code (or any Anthropic SDK) and api.anthropic.com.
// Captures every request and streaming response, supports match-and-
// replace rules, intercept/forward, replay, and AUP-refusal fallback.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/scribelia-anthony/annoying-aup-filter/internal/api"
	"github.com/scribelia-anthony/annoying-aup-filter/internal/fallback"
	"github.com/scribelia-anthony/annoying-aup-filter/internal/intercept"
	"github.com/scribelia-anthony/annoying-aup-filter/internal/proxy"
	"github.com/scribelia-anthony/annoying-aup-filter/internal/rules"
	"github.com/scribelia-anthony/annoying-aup-filter/internal/store"
	"github.com/scribelia-anthony/annoying-aup-filter/internal/web"
)

// Set at link time by goreleaser. Reads VCS info from debug.BuildInfo
// when unset (e.g., `go build` or `go install`).
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	proxyAddr := flag.String("proxy-addr", "127.0.0.1:8080", "address the proxy listens on (point ANTHROPIC_BASE_URL here)")
	uiAddr := flag.String("ui-addr", "127.0.0.1:8888", "address the UI / admin API listens on")
	upstream := flag.String("upstream", "https://api.anthropic.com", "upstream API base URL")
	maxCaptures := flag.Int("max-captures", 1000, "max captures kept in memory (ring buffer)")
	rulesFile := flag.String("rules-file", "", "optional path to a JSON file with rules to load at startup (same format as PUT /admin/rules)")
	showVersion := flag.Bool("version", false, "print version info and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(versionString())
		return
	}

	st := store.New(*maxCaptures)
	rs := rules.New()
	in := intercept.New()
	fb := fallback.New()

	if *rulesFile != "" {
		if data, err := os.ReadFile(*rulesFile); err == nil {
			var loaded []*rules.Rule
			if err := json.Unmarshal(data, &loaded); err != nil {
				log.Fatalf("rules-file: parse: %v", err)
			}
			if err := rs.Replace(loaded); err != nil {
				log.Fatalf("rules-file: compile: %v", err)
			}
			log.Printf("[rules] loaded %d rule(s) from %s", len(loaded), *rulesFile)
		} else if !os.IsNotExist(err) {
			log.Fatalf("rules-file: %v", err)
		} else {
			log.Printf("[rules] %s not present yet — will be created on first save", *rulesFile)
		}
		rs.SetPath(*rulesFile)

		// Persist fallback and intercept toggles next to the rules file so
		// the whole runtime configuration survives a restart.
		stateDir := filepath.Dir(*rulesFile)
		fb.SetPath(filepath.Join(stateDir, "fallback.json"))
		in.SetPath(filepath.Join(stateDir, "intercept.json"))
		fb.Load()
		in.Load()
		if fb.Enabled() {
			log.Printf("[fallback] restored: enabled, model=%s", fb.Model())
		}
		if in.Enabled() {
			log.Printf("[intercept] restored: enabled")
		}
	}

	px, err := proxy.New(*upstream, st, rs, in, fb)
	if err != nil {
		log.Fatalf("invalid upstream URL: %v", err)
	}

	proxyMux := http.NewServeMux()
	proxyMux.Handle("/", px)
	proxySrv := &http.Server{
		Addr:              *proxyAddr,
		Handler:           proxyMux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	uiMux := http.NewServeMux()
	ap := api.New(st, rs, in, fb, px)
	ap.ProxyAddr = *proxyAddr
	ap.RegisterRoutes(uiMux)
	uiMux.Handle("/", http.FileServer(http.FS(web.FS())))
	uiSrv := &http.Server{
		Addr:              *uiAddr,
		Handler:           uiMux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
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

func versionString() string {
	v, c, d := version, commit, date
	if c == "" || d == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					if c == "" {
						c = s.Value
					}
				case "vcs.time":
					if d == "" {
						d = s.Value
					}
				}
			}
		}
	}
	if c == "" {
		c = "unknown"
	}
	if d == "" {
		d = "unknown"
	}
	return fmt.Sprintf("annoying-aup-filter %s (commit %s, built %s)", v, c, d)
}
