package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
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
	gui := runtime.GOOS == "windows" && !*noBrowser
	// Double-clicking the EXE should work regardless of the Explorer or shortcut
	// working directory. Keep the local config next to the portable Windows EXE.
	if runtime.GOOS == "windows" && *file == "mixed-api.json" {
		if exe, e := os.Executable(); e == nil {
			*file = filepath.Join(filepath.Dir(exe), "mixed-api.json")
		}
	}
	cfg, err := gateway.LoadOrCreate(*file)
	if err != nil {
		startupFailure(gui, "无法读取或保存配置，请检查配置文件格式和目录写入权限。")
	}
	server := gateway.NewServer(*file, cfg)
	defer server.CloseResumable()
	if runtime.GOOS == "windows" && !*noBrowser {
		sessions := desktop.NewGoogleSessions(filepath.Join(filepath.Dir(*file), "browser-extension"))
		server.SetGoogleSessions(sessions)
		defer sessions.Close()
	}

	url := "http://" + cfg.Listen
	fmt.Println("MIXED-API running at", url)
	fmt.Println("API base URL: ", url+"/v1")
	fmt.Println("Keys are saved in", *file, "; keep this file private.")
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		// Reopen only an authenticated gateway with the same build version.
		// Otherwise the new EXE would silently display an older implementation.
		if runtime.GOOS == "windows" && !*noBrowser && existingGateway(url, cfg.AdminKey) {
			if err := desktop.Run(url, cfg.AdminKey); err != nil {
				startupFailure(gui, "应用窗口无法启动，请重新安装完整 Windows 版本。")
			}
			return
		}
		startupFailure(gui, "本机服务端口已被占用，或正在运行其他版本。请先完全退出已打开的 MIXED-API，再启动此版本；若仍无法启动，请检查端口占用。")
	}
	defer listener.Close()
	var lanServer *http.Server
	if *lan {
		const lanAddr = "0.0.0.0:8766"
		listener, e := net.Listen("tcp4", lanAddr)
		if e != nil {
			startupFailure(gui, "无法启动局域网服务，端口 8766 已被占用。")
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
			log.Print("应用窗口无法启动")
			desktop.ShowError("应用窗口无法启动，请重新安装完整 Windows 版本。")
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
	contentType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode != 200 || err != nil || contentType != "application/json" {
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return false
	}
	var status struct{ Service, Version string }
	return json.Unmarshal(raw, &status) == nil && status.Service == "MIXED-API" && status.Version == gateway.BuildVersion
}

func startupFailure(gui bool, message string) {
	log.Print(message)
	if gui {
		desktop.ShowError(message)
	}
	os.Exit(1)
}
