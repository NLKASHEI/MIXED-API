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
	"strings"
	"time"

	"github.com/NLKASHEI/MIXED-API/internal/gateway"
)

func main() {
	file := flag.String("config", "mixed-api.json", "local JSON config path")
	noBrowser := flag.Bool("no-browser", false, "don't open the browser automatically")
	lan := flag.Bool("lan", false, "opt-in read-only LAN API listener at :8766; never exposes admin panel")
	importEngine := flag.String("import-engine", "", "maintenance: prepare a local APS-family runtime without starting it")
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
	if *importEngine != "" {
		if err := server.ImportEngine(*importEngine); err != nil {
			log.Fatal(err)
		}
		fmt.Println("无头V组件已准备，统一客户端地址不变。")
		return
	}
	url := "http://" + cfg.Listen
	fmt.Println("MIXED-API running at", url)
	fmt.Println("API base URL: ", url+"/v1")
	fmt.Println("API key:      ", cfg.APIKey)
	fmt.Println("Admin key:    ", cfg.AdminKey)
	fmt.Println("Keys are also saved in", *file, "; keep this file private.")
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		// Reopening the EXE focuses the existing dashboard only after checking
		// the saved admin key against that process, never an unrelated listener.
		if runtime.GOOS == "windows" && !*noBrowser && existingGateway(url, cfg.AdminKey) {
			openDashboard(url, cfg.AdminKey)
			return
		}
		log.Fatalf("Cannot listen on %s: %v", cfg.Listen, err)
	}
	defer listener.Close()
	if !*noBrowser && runtime.GOOS == "windows" {
		openDashboard(url, cfg.AdminKey)
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
	if err := h.Serve(listener); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "Server failed:", err)
		os.Exit(1)
	}
}

func openDashboard(base, admin string) {
	// Fragments never travel in HTTP requests or access logs. The page removes
	// this fragment immediately and keeps the credential in sessionStorage.
	_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", base+"/#admin="+admin).Start()
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
