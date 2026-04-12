package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

//go:embed ui/*
var uiAssets embed.FS

var (
	httpdServer *http.Server
)

// UIからの設定保存用の構造体
type AllConfig struct {
	AutoStart  int    `json:"autoStart"`
	Upstream   string `json:"upstream"`
	User       string `json:"user"`
	Pass       string `json:"pass"`
	ProxyPort  string `json:"proxyPort"`
	PacUrl     string `json:"pacUrl"`
	PacPrefix  string `json:"pacPrefix"`
	PacContent string `json:"pacContent"`
}

// 空きポートを探す関数（proxy.goと共用） エラー時は0返却
func findFreePort() int {
	if !ReloadCfg() {
		return 0
	}
	minPort := GetIntSafe(cfg.Section("General").Key("AutoPortMin"), 20000)
	maxPort := GetIntSafe(cfg.Section("General").Key("AutoPortMax"), 29999)
	for p := minPort; p <= maxPort; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
		if err == nil {
			ln.Close()
			return p
		}
	}
	return 0
}

var allOrLocalAddrRegex = regexp.MustCompile(`^127\.(?:0\.){2}1$|^(?:0\.){3}0$|^$`)

func getToaddr(addr string) string {
	if allOrLocalAddrRegex.MatchString(addr) {
		return "127.0.0.1"
	} else if addrRegex.MatchString(addr) {
		return addr
	} else {
		return "127.0.0.1"
	}
}

// Port取得
func getCurrentHttpdPort() int {
	if CurrentHttpdPort > 0 && CurrentHttpdPort <= 65535 {
		return CurrentHttpdPort
	}
	if !ReloadCfg() {
		return CurrentHttpdPort
	}
	portStr := GetStringSafe(cfg.Section("General").Key("HttpdPort"))
	var port int
	if portStr == "" {
		port = findFreePort()
		if port == 0 {
			notify("ERROR", "Httpd", "No available ports were found.")
			return CurrentHttpdPort
		}
	} else {
		port, _ = strconv.Atoi(portStr)
	}
	CurrentHttpdPort = port
	CurrentHttpdAddr = GetStringSafe(cfg.Section("General").Key("HttpdAddr"))
	return CurrentHttpdPort
}

// ログの末尾から一定量だけを取得 1行=max200B、max20行/s、600行=30秒 = 120KB
func getTailLog() ([]byte, error) {
	const chunkSize int64 = 120 * 1000
	file, err := os.Open(LogPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	stat, _ := file.Stat()
	fileSize := stat.Size()
	if fileSize <= chunkSize {
		return os.ReadFile(LogPath)
	}
	buffer := make([]byte, int(chunkSize))
	_, err = file.ReadAt(buffer, fileSize-chunkSize)
	if err != nil {
		return nil, err
	}
	firstNewLine := bytes.IndexByte(buffer, '\n')
	if firstNewLine != -1 {
		return buffer[firstNewLine+1:], nil
	}
	return buffer, nil
}

// Httpd (管理画面/PAC配信) の開始
func startHttpd() bool {
	if IsHttpdRun {
		return true
	}
	if !ReloadCfg() {
		return false
	}
	if getCurrentHttpdPort() == 0 {
		return false
	}
	mux := http.NewServeMux()
	// PAC配信(起動時にPACファイルが置かれていなくても許容)
	pName := GetStringSafe(cfg.Section("Pac").Key("PacName"), "proxy.pac")
	hDir := GetStringSafe(cfg.Section("Pac").Key("HtmlDir"), "./html/")
	mux.HandleFunc("/"+pName, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		http.ServeFile(w, r, filepath.Join(hDir, pName))
	})
	// 管理画面(多重防止)
	withAuth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			token := r.Header.Get("X-Session-ID")
			if !IsValidSession(token) {
				http.Error(w, "Invalid Session", http.StatusForbidden)
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		sessionMu.Lock()
		active := isLaunchPathActive
		path := currentLaunchPath
		sessionMu.Unlock()
		if active && r.URL.Path == path {
			HandleLauncher(w, r)
			return
		}
		uiFS, _ := fs.Sub(uiAssets, "ui")
		targetPath := r.URL.Path
		if targetPath == "/" || targetPath == "/index.html" {
			var embedSessionID string
			if r.Method == http.MethodPost {
				key := r.FormValue("launch_key")
				sID, ok := ExchangeSession(key)
				if ok {
					embedSessionID = sID
				}
			}
			f, err := uiFS.Open("index.html")
			if err == nil {
				defer f.Close()
				content, _ := io.ReadAll(f)
				injected := bytes.Replace(content, []byte("<head>"), []byte(fmt.Sprintf("<head><script>window.SESSION_ID='%s';</script>", embedSessionID)), 1)
				w.Header().Set("Content-Type", "text/html")
				w.Write(injected)
				return
			}
		}
		cleanPath := strings.TrimPrefix(targetPath, "/")
		if cleanPath == "" {
			cleanPath = "index.html"
		}
		f, err := uiFS.Open(cleanPath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		f.Close()
		http.FileServer(http.FS(uiFS)).ServeHTTP(w, r)
	})
	mux.HandleFunc("/api/exchange", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Key string `json:"key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		sID, ok := ExchangeSession(req.Key)
		if !ok {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"sessionID": sID})
	})
	mux.HandleFunc("/api/heartbeat", withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	// 状態取得API(ヘッダステータス)
	mux.HandleFunc("/api/status", withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		res := map[string]interface{}{
			"httpd": IsHttpdRun,
			"proxy": IsProxyRun,
		}
		json.NewEncoder(w).Encode(res)
	}))
	// 制御操作API
	mux.HandleFunc("/api/control", withAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			action := r.URL.Query().Get("action")
			switch action {
			case "httpd-reboot":
				go func() {
					if !stopHttpd() {
						notify("ERROR", "Httpd", "Reboot failed.")
						return
					}
					for i := 0; i < 10; i++ {
						time.Sleep(200 * time.Millisecond)
						if startHttpd() {
							return
						}
					}
					notify("ERROR", "Httpd", "Reboot failed after retries.")
				}()
			case "proxy-toggle":
				if IsProxyRun {
					stopProxyRelay()
				} else {
					startProxyRelay()
				}
			case "start-all":
				go PerformStartAll()
			case "stop-all":
				go PerformStopAll()
			case "quit":
				w.WriteHeader(http.StatusOK)
				logOutput("LOG", "Httpd", "A stop request was received from the WebUI.")
				go func() {
					time.Sleep(500 * time.Millisecond)
					onExit()
					os.Exit(0)
				}()
				return
			}
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	// ログ取得API(オフセット以降を取得)
	mux.HandleFunc("/api/log", withAuth(func(w http.ResponseWriter, r *http.Request) {
		file, err := os.Open(LogPath)
		if err != nil {
			http.Error(w, "Log open error", 500)
			return
		}
		defer file.Close()
		stat, _ := file.Stat()
		fileSize := stat.Size()
		offsetStr := r.URL.Query().Get("offset")
		offset, _ := strconv.ParseInt(offsetStr, 10, 64)
		var data []byte
		if offset == -1 {
			data, _ = getTailLog()
		} else if offset < fileSize {
			data = make([]byte, fileSize-offset)
			_, err = file.ReadAt(data, offset)
			if err != nil {
				http.Error(w, "Read error", 500)
				return
			}
		}
		w.Header().Set("Access-Control-Expose-Headers", "X-Log-Size")
		w.Header().Set("X-Log-Size", strconv.FormatInt(fileSize, 10))
		w.Write(data)
	}))
	// 通知読取API
	mux.HandleFunc("/api/notifications", withAuth(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		lastID, _ := strconv.ParseInt(q.Get("last_id"), 10, 64)
		list := PullNotifications(lastID)
		w.Header().Set("Content-Type", "application/json")
		if list == nil {
			list = []AppNotification{}
		}
		json.NewEncoder(w).Encode(list)
	}))
	// 通信速度取得API(ヘッダステータス)
	mux.HandleFunc("/api/traffic", withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var sVal, rVal uint64
		if IsProxyRun && len(HistorySent) > 0 {
			sVal = HistorySent[len(HistorySent)-1]
			rVal = HistoryRecv[len(HistoryRecv)-1]
		}
		res := map[string]interface{}{
			"sent": sVal,
			"recv": rVal,
		}
		json.NewEncoder(w).Encode(res)
	}))
	// 通信速度履歴取得AP(Statsタブ)
	mux.HandleFunc("/api/traffic-history", withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sent_history": HistorySent,
			"recv_history": HistoryRecv,
		})
	}))
	// 設定読込API(RuleFileは存在しない場合(初回)も空で返却)
	mux.HandleFunc("/api/read-config", withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !ReloadCfg() {
			http.Error(w, "Failed to reload config", http.StatusInternalServerError)
			return
		}
		rFile := GetStringSafe(cfg.Section("Pac").Key("RuleFile"), "mod_rules.pac")
		pacData, _ := os.ReadFile(filepath.Join(CONF_DIR, rFile))
		res := map[string]interface{}{
			"autoStart":  GetIntSafe(cfg.Section("General").Key("AutoStart"), 0),
			"upstream":   GetStringSafe(cfg.Section("Proxy").Key("Upstream")),
			"user":       GetStringSafe(cfg.Section("Proxy").Key("User")),
			"proxyPort":  GetStringSafe(cfg.Section("Proxy").Key("ProxyPort")),
			"pacUrl":     GetStringSafe(cfg.Section("Pac").Key("PacUrl")),
			"pacPrefix":  GetStringSafe(cfg.Section("Pac").Key("PacPrefix")),
			"pacContent": string(pacData),
		}
		json.NewEncoder(w).Encode(res)
	}))
	// 設定保存API(JSON受付)
	mux.HandleFunc("/api/save-config", withAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req AllConfig
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			logOutput("ERROR", "Httpd", "JSON decoding failed. %v", err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		if !ReloadCfg() {
			http.Error(w, "Failed to reload config", http.StatusInternalServerError)
			return
		}
		cfg.Section("General").Key("AutoStart").SetValue(strconv.Itoa(req.AutoStart))
		cfg.Section("Proxy").Key("Upstream").SetValue(req.Upstream)
		cfg.Section("Proxy").Key("ProxyPort").SetValue(req.ProxyPort)
		cfg.Section("Pac").Key("PacUrl").SetValue(req.PacUrl)
		cfg.Section("Pac").Key("PacPrefix").SetValue(req.PacPrefix)
		if req.User != "" {
			cfg.Section("Proxy").Key("User").SetValue(req.User)
			if req.Pass != "" {
				cfg.Section("Proxy").Key("Pass").SetValue(req.Pass)
			}
		} else {
			cfg.Section("Proxy").Key("User").SetValue(req.User)
			cfg.Section("Proxy").Key("Pass").SetValue(req.Pass)
		}
		if err := saveAsSJIS(cfg, CONF_PATH); err != nil {
			logOutput("ERROR", "Httpd", "Settings save failed. %v", err)
			http.Error(w, "Failed to save INI", http.StatusInternalServerError)
			return
		}
		rFile := GetStringSafe(cfg.Section("Pac").Key("RuleFile"), "mod_rules.pac")
		err := os.WriteFile(filepath.Join(CONF_DIR, rFile), []byte(req.PacContent), 0644)
		if err != nil {
			logOutput("ERROR", "Httpd", "PacRuleFile saving failed. %v", err)
			http.Error(w, "Failed to save PAC file", http.StatusInternalServerError)
			return
		}
		logOutput("INFO", "Httpd", "All settings have been saved.")
		w.WriteHeader(http.StatusOK)
	}))
	// Proxy中継ログレベル取得API
	mux.HandleFunc("/api/get-log-level", withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{"level": LogLevel})
	}))
	// Proxy中継ログレベル変更API
	mux.HandleFunc("/api/set-log-level", withAuth(func(w http.ResponseWriter, r *http.Request) {
		levelStr := r.URL.Query().Get("level")
		lvl, err := strconv.Atoi(levelStr)
		if err != nil {
			http.Error(w, "Invalid level", http.StatusBadRequest)
			return
		}
		LogLevel = lvl
		logOutput("INFO", "Httpd", "LogLevel changed to: %d", LogLevel)
		w.WriteHeader(http.StatusOK)
	}))
	// 起動
	httpdServer = &http.Server{Addr: CurrentHttpdAddr + ":" + strconv.Itoa(CurrentHttpdPort), Handler: mux}
	IsHttpdRun = true
	go func() {
		notify("INFO", "Httpd", "Started. ("+CurrentHttpdAddr+":"+strconv.Itoa(CurrentHttpdPort)+")")
		if err := httpdServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			IsHttpdRun = false
			notify("ERROR", "Httpd", err.Error())
		}
	}()
	return IsHttpdRun
}

// Httpdの停止
func stopHttpd() bool {
	if httpdServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		httpdServer.Shutdown(ctx)
		httpdServer = nil
		IsHttpdRun = false
		notify("INFO", "Httpd", "Stopped.")
	}
	return true
}
