package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/NLKASHEI/MIXED-API/internal/gateway"
)

func main() {
	file := flag.String("config", "mixed-api.json", "local JSON config path")
	noBrowser := flag.Bool("no-browser", false, "don't open the browser automatically")
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
	// Desktop: open a real native Win32 window. The gateway still serves APIs
	// inside this EXE. Use --no-browser for headless/automation mode.
	if runtime.GOOS == "windows" && !*noBrowser {
		if err := startNative(cfg, server, *lan); err != nil {
			log.Fatal(err)
		}
		return
	}
	url := "http://" + cfg.Listen
	fmt.Println("MIXED-API running at", url)
	fmt.Println("API base URL: ", url+"/v1")
	fmt.Println("API key:      ", cfg.APIKey)
	fmt.Println("Admin key:    ", cfg.AdminKey)
	fmt.Println("Keys are also saved in", *file, "; keep this file private.")
	if !*noBrowser && runtime.GOOS == "windows" {
		go func() {
			time.Sleep(800 * time.Millisecond)
			_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
		}()
	}
	if *lan {
		const lanAddr = "0.0.0.0:8766"
		listener, e := net.Listen("tcp4", lanAddr)
		if e != nil {
			log.Fatalf("Cannot start LAN API port 8766: %v", e)
		}
		// Only inference routes on the LAN port, no administration.
		apiOnly := gateway.APIOnly(server)
		go func() {
			fmt.Println("LAN API enabled on port 8766; keep it within your trusted private network.")
			fmt.Println("Phone Base URL: http://<YOUR-PC-LAN-IP>:8766/v1")
			lanServer := &http.Server{Handler: apiOnly, ReadHeaderTimeout: 12 * time.Second, IdleTimeout: 90 * time.Second}
			if e := lanServer.Serve(listener); e != nil && e != http.ErrServerClosed {
				log.Printf("LAN API stopped: %v", e)
			}
		}()
	}
	h := &http.Server{Addr: cfg.Listen, Handler: server, ReadHeaderTimeout: 12 * time.Second, IdleTimeout: 90 * time.Second}
	if err := h.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "Server failed:", err)
		os.Exit(1)
	}
}
