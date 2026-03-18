// Package main provides the entry point for Kiro API Proxy.
//
// Kiro API Proxy is a reverse proxy service that translates Kiro API requests
// into OpenAI and Anthropic (Claude) compatible formats. Key features include:
//   - Multi-account pool with round-robin load balancing
//   - Automatic OAuth token refresh
//   - Streaming response support for real-time AI interactions
//   - Admin panel for account and configuration management
//
// The service exposes the following endpoints:
//   - /v1/messages - Claude API compatible endpoint
//   - /v1/chat/completions - OpenAI API compatible endpoint
//   - /admin - Web-based administration panel
package main

import (
	"fmt"
	"kiro-api-proxy/config"
	"kiro-api-proxy/pool"
	"kiro-api-proxy/proxy"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// startConfigWatcher starts watching the config file for changes
// and triggers hot reload when the file is modified
func startConfigWatcher() {
	configPath := config.GetConfigPath()
	if configPath == "" {
		return
	}

	// Get the directory to watch (fsnotify watches directories)
	configDir := filepath.Dir(configPath)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("Warning: failed to create config watcher: %v", err)
		return
	}

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}

				// Only handle write events for the config file
				if event.Has(fsnotify.Write) && event.Name == configPath {
					log.Printf("Config file changed, reloading...")
					if err := config.Reload(); err != nil {
						log.Printf("Failed to reload config: %v", err)
					} else {
						log.Printf("Config reloaded successfully")
					}
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("Config watcher error: %v", err)
			}
		}
	}()

	if err := watcher.Add(configDir); err != nil {
		log.Printf("Warning: failed to watch config directory: %v", err)
		return
	}

	log.Printf("Config hot reload enabled, watching: %s", configPath)
}

func main() {
	// CONFIG_URL: remote config (GitHub Gist, raw file, etc.)
	// CONFIG_PATH: local config file path (default: data/config.json)
	configPath := "data/config.json"
	if envPath := os.Getenv("CONFIG_PATH"); envPath != "" {
		configPath = envPath
	}

	// Initialize Gist sync (checks for GITHUB_TOKEN and GIST_ID)
	config.SetGistConfig()

	// If Gist sync is configured, load from Gist API (handles changing commit hashes)
	if config.IsGistConfigured() {
		log.Printf("Loading config from Gist: %s", os.Getenv("GIST_ID"))
		if err := config.LoadFromGistAPI(); err != nil {
			log.Fatalf("Failed to load config from Gist: %v", err)
		}
	} else {
		// Local file mode
		if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
			log.Fatalf("Failed to create data directory: %v", err)
		}
		if err := config.Init(configPath); err != nil {
			log.Fatalf("Failed to load config: %v", err)
		}
	}

	// 环境变量覆盖密码
	if envPassword := os.Getenv("ADMIN_PASSWORD"); envPassword != "" {
		config.SetPassword(envPassword)
	}

	// Initialize Gist sync
	config.SetGistConfig()

	// Start config file watcher for hot reload
	startConfigWatcher()

	// 初始化账号池
	pool.GetPool()

	// 创建 HTTP 处理器（包含后台刷新任务）
	handler := proxy.NewHandler()

	// 启动服务器
	addr := fmt.Sprintf("%s:%d", config.GetHost(), config.GetPort())
	log.Printf("Kiro-Go starting on http://%s", addr)
	log.Printf("Admin panel: http://%s/admin", addr)
	log.Printf("Claude API: http://%s/v1/messages", addr)
	log.Printf("OpenAI API: http://%s/v1/chat/completions", addr)

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
