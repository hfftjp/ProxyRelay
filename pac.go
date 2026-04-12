package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// 改変元のPACファイルダウンロード
func downloadOriginalPac() bool {
	if !ReloadCfg() {
		return false
	}
	if GetIntSafe(cfg.Section("Pac").Key("Download"), 0) == 0 {
		return true
	}
	hDir := GetStringSafe(cfg.Section("Pac").Key("HtmlDir"), "./html/")
	oName := GetStringSafe(cfg.Section("Pac").Key("OriginalPacName"))
	uStr := GetStringSafe(cfg.Section("Pac").Key("PacUrl"))
	if uStr == "" || oName == "" {
		notify("ERROR", "PAC", "PacUrl / OriginalPacName is empty.")
		return false
	}
	os.MkdirAll(hDir, os.ModePerm)
	resp, err := http.Get(uStr)
	if err != nil {
		notify("ERROR", "PAC", "Download failed.")
		return false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	os.WriteFile(filepath.Join(hDir, oName), body, 0644)
	logOutput("LOG", "PAC", "Download completed.")
	return true
}

// PACファイルの改変
func modifySavedPac() bool {
	if !ReloadCfg() {
		return false
	}
	if GetIntSafe(cfg.Section("Pac").Key("Modify"), 0) == 0 {
		return true
	}
	hDir := GetStringSafe(cfg.Section("Pac").Key("HtmlDir"), "./html/")
	oName := GetStringSafe(cfg.Section("Pac").Key("OriginalPacName"))
	pName := GetStringSafe(cfg.Section("Pac").Key("PacName"))
	if oName == "" || pName == "" {
		notify("ERROR", "PAC", "PacName / OriginalPacName is empty.")
		return false
	}
	content, err := os.ReadFile(filepath.Join(hDir, oName))
	if err != nil {
		notify("ERROR", "PAC", "OriginalPac file not found.")
		return false
	}
	rFile := GetStringSafe(cfg.Section("Pac").Key("RuleFile"), "mod_rules.pac")
	rulesRaw, rerr := os.ReadFile(filepath.Join(CONF_DIR, rFile))
	if rerr != nil {
		notify("ERROR", "PAC", "RuleFile not found.")
		return false
	}
	useCRLF := bytes.Contains(content, []byte("\r\n"))
	if getCurrentProxyPort() == 0 {
		return false
	}
	rules := strings.NewReplacer(
		"%PORT%", strconv.Itoa(CurrentProxyPort),
		"%ADDR%", getToaddr(CurrentProxyAddr),
	).Replace(string(rulesRaw))
	prefix := GetStringSafe(cfg.Section("Pac").Key("PacPrefix"), "__mod_proxyrelay_")
	re := regexp.MustCompile(`(?m)^([ \t]*)function([ \t]+)FindProxyForURL([ \t]*\()`)
	modified := re.ReplaceAllString(string(content), "${1}function${2}"+prefix+"FindProxyForURL${3}")
	wrapper := fmt.Sprintf("function FindProxyForURL(url, host) {\n%s\n    return %sFindProxyForURL(url, host);\n}\n\n", rules, prefix)
	finalContent := []byte(wrapper + modified)
	finalContent = bytes.ReplaceAll(finalContent, []byte("\r\n"), []byte("\n"))
	if useCRLF {
		finalContent = bytes.ReplaceAll(finalContent, []byte("\n"), []byte("\r\n"))
	}
	err = os.WriteFile(filepath.Join(hDir, pName), finalContent, 0644)
	if err != nil {
		logOutput("ERROR", "PAC", "Modification failed. %v", err)
		return false
	}
	logOutput("LOG", "PAC", "Modification completed. ("+CurrentProxyAddr+":"+strconv.Itoa(CurrentProxyPort)+")")
	return true
}
