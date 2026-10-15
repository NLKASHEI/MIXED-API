package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/NLKASHEI/MIXED-API/internal/gateway"
)

func main() {
	file := flag.String("config", "mixed-api.json", "local JSON config path")
	noBrowser := flag.Bool("no-browser", false, "don't open the browser automatically")
	flag.Parse()
	cfg, err := gateway.LoadOrCreate(*file)
	if err != nil {
		log.Fatal(err)
	}
	server := gateway.NewServer(*file, cfg)
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
	h := &http.Server{Addr: cfg.Listen, Handler: server, ReadHeaderTimeout: 12 * time.Second, IdleTimeout: 90 * time.Second}
	if err := h.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "Server failed:", err)
		os.Exit(1)
	}
}
