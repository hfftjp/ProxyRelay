package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"golang.org/x/net/http2"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var (
	proxyServer *http.Server
	internalMux = http.NewServeMux()
	proxyUser   string
	proxyAuth   string
)

// アクセス制御リスト
var (
	AllowList []string
	DenyList  []string
)

// アクセス制限リストファイル読み込み
func loadList(path string) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	var list []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			list = append(list, line)
		}
	}
	return list
}

func loadAccessLists() {
	AllowList = loadList(ALLOW_PATH)
	DenyList = loadList(DENY_PATH)
}

// アクセス制限内容判定
func wildcardMatch(pattern, host string) bool {
	if !strings.Contains(pattern, "*") {
		return host == pattern
	}
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") {
		return strings.Contains(host, pattern[1:len(pattern)-1])
	}
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(host, pattern[1:])
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(host, pattern[:len(pattern)-1])
	}
	return true
}

func isAllowed(rawHost string) bool {
	host, _, err := net.SplitHostPort(rawHost)
	if err != nil {
		host = rawHost
	}
	for _, d := range DenyList {
		if wildcardMatch(d, host) {
			return false
		}
	}
	if len(AllowList) > 0 {
		for _, a := range AllowList {
			if wildcardMatch(a, host) {
				return true
			}
		}
		return false
	}
	return true
}

// Hop-by-hopヘッダ除去
var hopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Transfer-Encoding",
	"Upgrade",
	"TE",
	"Trailer",
}

func removeHopHeaders(h http.Header) {
	for _, k := range hopHeaders {
		h.Del(k)
	}
}

// トラフィック計測(historyは120秒分)
var (
	TotalSentBytes uint64
	TotalRecvBytes uint64
	HistorySent    []uint64
	HistoryRecv    []uint64
	historyMax     = 120
)

type countWriter struct {
	io.Writer
	count *uint64
}

func (w *countWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	atomic.AddUint64(w.count, uint64(n))
	return n, err
}

// 通信量記録＋トレーアイコン反映
func monitorTraffic() {
	if HistorySent == nil {
		HistorySent = make([]uint64, historyMax)
		HistoryRecv = make([]uint64, historyMax)
	}
	ticker := time.NewTicker(1 * time.Second)
	for range ticker.C {
		s := atomic.SwapUint64(&TotalSentBytes, 0)
		r := atomic.SwapUint64(&TotalRecvBytes, 0)
		HistorySent = append(HistorySent[1:], s)
		HistoryRecv = append(HistoryRecv[1:], r)
		if IsProxyRun {
			if s > 1024 || r > 1024 {
				setBusyIcon()
			} else {
				updateIcon()
			}
		}
	}
}

// Proxy中継追加ログ
func writeProxyLog(outputLevel int, level, message string, v ...interface{}) {
	if LogLevel >= outputLevel {
		logOutput(level, "Proxy", message, v...)
	}
}

// Port取得
func getCurrentProxyPort() int {
	if CurrentProxyPort > 0 && CurrentProxyPort <= 65535 {
		return CurrentProxyPort
	}
	if !ReloadCfg() {
		return CurrentProxyPort
	}
	portStr := GetStringSafe(cfg.Section("Proxy").Key("ProxyPort"))
	var port int
	if portStr == "" {
		port = findFreePort()
		if port == 0 {
			notify("ERROR", "Proxy", "No available ports were found.")
			return CurrentProxyPort
		}
	} else {
		port, _ = strconv.Atoi(portStr)
	}
	CurrentProxyPort = port
	return CurrentProxyPort
}

// Proxy中継の開始
func startProxyRelay() bool {
	if IsProxyRun {
		return true
	}
	if !ReloadCfg() {
		return false
	}
	loadAccessLists()
	port := getCurrentProxyPort()
	if port == 0 {
		return false
	}
	upstreamRaw := GetStringSafe(cfg.Section("Proxy").Key("Upstream"))
	if !upsRegex.MatchString(upstreamRaw) {
		notify("ERROR", "Proxy", "Upstream not specified.")
		return false
	}
	proxyUser = GetStringSafe(cfg.Section("Proxy").Key("User"))
	passB64 := GetStringSafe(cfg.Section("Proxy").Key("Pass"))
	decodedPass, _ := base64.StdEncoding.DecodeString(passB64)
	proxyAuth = base64.StdEncoding.EncodeToString([]byte(proxyUser + ":" + string(decodedPass)))
	upstream, _ := url.Parse(upstreamRaw)
	tr := newTransport(upstream)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			handleConnect(w, r, upstream)
			return
		}
		handleHTTP(w, r, tr)
	})
	proxyServer = &http.Server{
		Addr:              ":" + strconv.Itoa(CurrentProxyPort),
		Handler:           handler,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
	}
	IsProxyRun = true
	updateIcon()
	refreshMenu()
	go func() {
		notify("INFO", "Proxy", "Started. (Port:"+strconv.Itoa(CurrentProxyPort)+")")
		err := proxyServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			IsProxyRun = false
			updateIcon()
			refreshMenu()
			notify("ERROR", "Proxy", err.Error())
		}
	}()
	return true
}

// Proxy中継停止
func stopProxyRelay() bool {
	if proxyServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		proxyServer.Shutdown(ctx)
		proxyServer = nil
		IsProxyRun = false
		if !SkipTrayUpdate {
			updateIcon()
			refreshMenu()
		}
		notify("INFO", "Proxy", "Stopped.")
	}
	return true
}

// Transport
func newTransport(upstream *url.URL) *http.Transport {
	tr := &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			host := req.URL.Hostname()
			if host == "127.0.0.1" || host == "localhost" {
				return nil, nil
			}
			return upstream, nil
		},
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   50,
		IdleConnTimeout:       90 * time.Second,
		DisableKeepAlives:     false,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	http2.ConfigureTransport(tr)
	return tr
}

// CONNECT処理
func handleConnect(w http.ResponseWriter, r *http.Request, upstream *url.URL) {
	if !isAllowed(r.Host) {
		writeProxyLog(2, "LOG", "BLOCK: %s", r.Host)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	writeProxyLog(2, "LOG", "CONNECT: %s -> %s", r.RemoteAddr, r.Host)
	destConn, err := net.DialTimeout("tcp", upstream.Host, 10*time.Second)
	if err != nil {
		writeProxyLog(1, "ERROR", "Connect to upstream (%s) failed: %v", upstream.Host, err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	var proxyAuthHeader string
	if proxyUser != "" {
		proxyAuthHeader = "Proxy-Authorization: Basic " + proxyAuth + "\r\n"
		writeProxyLog(2, "LOG", "AUTH: Applied for HTTPS: %s", r.Host)
	} else if h := r.Header.Get("Proxy-Authorization"); h != "" {
		proxyAuthHeader = "Proxy-Authorization: " + h + "\r\n"
		writeProxyLog(2, "LOG", "AUTH: Pass-through for HTTPS: %s", r.Host)
	}
	fmt.Fprintf(destConn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n%s\r\n", r.Host, r.Host, proxyAuthHeader)
	resp, err := http.ReadResponse(bufio.NewReader(destConn), r)
	if err != nil {
		writeProxyLog(1, "ERROR", "Proxy read failed: %v", err)
		destConn.Close()
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		writeProxyLog(1, "WARNING", "Upstream returned status: %d for %s", resp.StatusCode, r.Host)
		hij, _ := w.(http.Hijacker)
		c, _, _ := hij.Hijack()
		resp.Write(c)
		destConn.Close()
		return
	}
	hij, ok := w.(http.Hijacker)
	if !ok {
		destConn.Close()
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hij.Hijack()
	if err != nil {
		destConn.Close()
		return
	}
	clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	writeProxyLog(2, "LOG", "CONNECT: Tunnel: %s <-> %s", r.RemoteAddr, r.Host)
	go func() {
		defer clientConn.Close()
		defer destConn.Close()
		io.Copy(&countWriter{Writer: destConn, count: &TotalSentBytes}, clientConn)
	}()
	go func() {
		defer clientConn.Close()
		defer destConn.Close()
		io.Copy(&countWriter{Writer: clientConn, count: &TotalRecvBytes}, destConn)
	}()
}

// HTTP処理
func handleHTTP(w http.ResponseWriter, r *http.Request, tr *http.Transport) {
	host := r.URL.Hostname()
	if !isAllowed(host) {
		writeProxyLog(2, "LOG", "BLOCK: %s", host)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if !r.URL.IsAbs() {
		writeProxyLog(1, "WARNING", "Loop detected: Blocking request to self (%s)", r.Host)
		internalMux.ServeHTTP(w, r)
		return
	}
	writeProxyLog(2, "LOG", "CONNECT: HTTP: %s -> %s %s", r.RemoteAddr, r.Method, r.URL.String())
	removeHopHeaders(r.Header)
	applyProxyAuth(r)
	resp, err := tr.RoundTrip(r)
	if err != nil {
		writeProxyLog(1, "ERROR", "HTTP RoundTrip failed: %v", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		writeProxyLog(1, "WARNING", "HTTP Status: %d for %s", resp.StatusCode, r.URL.Host)
	}
	removeHopHeaders(resp.Header)
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(&countWriter{Writer: w, count: &TotalRecvBytes}, resp.Body)
}

// 認証
func applyProxyAuth(r *http.Request) {
	if !r.URL.IsAbs() {
		return
	}
	if proxyUser != "" {
		r.Header.Set("Proxy-Authorization", "Basic "+proxyAuth)
		writeProxyLog(2, "LOG", "AUTH: Applied for HTTP: %s", r.Host)
	} else if r.Header.Get("Proxy-Authorization") != "" {
		writeProxyLog(2, "LOG", "AUTH: Pass-through for HTTP: %s", r.Host)
	}
}
