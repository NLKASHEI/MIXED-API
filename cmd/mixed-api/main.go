package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/NLKASHEI/MIXED-API/internal/desktop"
	"github.com/NLKASHEI/MIXED-API/internal/gateway"
)

func main() {
	file := flag.String("config", "mixed-api.json", "local JSON config path")
	noBrowser := flag.Bool("no-browser", false, "headless mode: run only the local gateway")
	lan := flag.Bool("lan", false, "opt-in read-only LAN API listener at :8766; never exposes admin panel")
	flag.Parse()
	// Double-clicking the EXE should work regardless of the Explorer or shortcut
	// working directory. Keep the local config next to the portable Windows EXE.
	if runtime.GOOS == "windows" && *file == "mixed-api.json" {
		if exe, e := os.Executable(); e == nil {
			*file = filepath.Join(filepath.Dir(exe), "mixed-api.json")
		}
	}
	cfg, err := gateway.LoadOrCreate(*file)
	if err != nil {
		log.Fatal(err)
	}
	server := gateway.NewServer(*file, cfg)
	if runtime.GOOS == "windows" && !*noBrowser {
		sessions := desktop.NewGoogleSessions(filepath.Join(filepath.Dir(*file), "browser-profiles"))
		server.SetGoogleSessions(sessions)
		defer sessions.Close()
	}

	url := "http://" + cfg.Listen
	fmt.Println("MIXED-API running at", url)
	fmt.Println("API base URL: ", url+"/v1")
	fmt.Println("Keys are saved in", *file, "; keep this file private.")
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		// Reopening the EXE focuses the existing dashboard only after checking
		// the saved admin key against that process, never an unrelated listener.
		if runtime.GOOS == "windows" && !*noBrowser && existingGateway(url, cfg.AdminKey) {
			if err := desktop.Run(url, cfg.AdminKey); err != nil {
				log.Print(err)
			}
			return
		}
		log.Fatalf("Cannot listen on %s: %v", cfg.Listen, err)
	}
	defer listener.Close()
	var lanServer *http.Server
	if *lan {
		const lanAddr = "0.0.0.0:8766"
		listener, e := net.Listen("tcp4", lanAddr)
		if e != nil {
			log.Fatalf("Cannot start LAN API port 8766: %v", e)
		}
		// Only inference routes on the LAN port, no administration.
		apiOnly := gateway.APIOnly(server)
		lanServer = &http.Server{Handler: apiOnly, ReadHeaderTimeout: 12 * time.Second, IdleTimeout: 90 * time.Second}
		go func() {
			fmt.Println("LAN API enabled on port 8766; keep it within your trusted private network.")
			fmt.Println("Phone Base URL: http://<YOUR-PC-LAN-IP>:8766/v1")
			if e := lanServer.Serve(listener); e != nil && e != http.ErrServerClosed {
				log.Printf("LAN API stopped: %v", e)
			}
		}()
	}
	h := &http.Server{Addr: cfg.Listen, Handler: server, ReadHeaderTimeout: 12 * time.Second, IdleTimeout: 90 * time.Second}
	if !*noBrowser && runtime.GOOS == "windows" {
		go func() {
			if e := h.Serve(listener); e != nil && e != http.ErrServerClosed {
				log.Print("Gateway stopped")
			}
		}()
		err := desktop.Run(url, cfg.AdminKey)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = h.Shutdown(ctx)
		if lanServer != nil {
			_ = lanServer.Shutdown(ctx)
		}
		if err != nil {
			log.Print(err)
		}
		return
	}
	if err := h.Serve(listener); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "Server failed:", err)
		os.Exit(1)
	}
}

func existingGateway(base, admin string) bool {
	req, err := http.NewRequest("GET", strings.TrimSuffix(base, "/")+"/api/status", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+admin)
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200 && strings.Contains(resp.Header.Get("Content-Type"), "application/json")
}
