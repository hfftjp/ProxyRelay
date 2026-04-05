package main

import (
	"fmt"
	"golang.org/x/sys/windows/registry"
	"strconv"
	"syscall"
)

var (
	modwininet            = syscall.NewLazyDLL("wininet.dll")
	procInternetSetOption = modwininet.NewProc("InternetSetOptionW")
)

const (
	INTERNET_OPTION_SETTINGS_CHANGED = 39
	INTERNET_OPTION_REFRESH          = 37
	REG_PATH                         = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
)

type RegState struct {
	Enable   uint32
	PAC      string
	Server   string
	Override string
}

// 共通：レジストリ読み取り部
func getRegistryState() (RegState, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, REG_PATH, registry.QUERY_VALUE)
	if err != nil {
		return RegState{}, err
	}
	defer k.Close()
	e, _, _ := k.GetIntegerValue("ProxyEnable")
	p, _, _ := k.GetStringValue("AutoConfigURL")
	s, _, _ := k.GetStringValue("ProxyServer")
	o, _, _ := k.GetStringValue("ProxyOverride")
	return RegState{Enable: uint32(e), PAC: p, Server: s, Override: o}, nil
}

// 共通：レジストリバックアップ部
func backupRegistry() bool {
	s, err := getRegistryState()
	if err != nil {
		notify("ERROR", "REG", "getRegistry: %v", err)
		return false
	}
	if !ReloadCfg() {
		return false
	}
	sec := cfg.Section("RegBackup")
	sec.Key("ProxyEnable").SetValue(fmt.Sprint(s.Enable))
	sec.Key("AutoConfigURL").SetValue(s.PAC)
	sec.Key("ProxyServer").SetValue(s.Server)
	sec.Key("ProxyOverride").SetValue(s.Override)
	if err := saveAsSJIS(cfg, CONF_PATH); err != nil {
		notify("ERROR", "REG", "Settings save failed. %v", err)
		return false
	}
	logOutput("INFO", "REG", "Backup saved to ini.")
	return true
}

// 共通：レジストリ書き換え部
func applyRegistry(s RegState) {
	k, _ := registry.OpenKey(registry.CURRENT_USER, REG_PATH, registry.SET_VALUE)
	defer k.Close()
	k.SetDWordValue("ProxyEnable", s.Enable)
	if s.PAC != "" {
		k.SetStringValue("AutoConfigURL", s.PAC)
	} else {
		k.DeleteValue("AutoConfigURL")
	}
	if s.Server != "" {
		k.SetStringValue("ProxyServer", s.Server)
	} else {
		k.DeleteValue("ProxyServer")
	}
	if s.Override != "" {
		k.SetStringValue("ProxyOverride", s.Override)
	} else {
		k.DeleteValue("ProxyOverride")
	}
	procInternetSetOption.Call(0, INTERNET_OPTION_SETTINGS_CHANGED, 0, 0)
	procInternetSetOption.Call(0, INTERNET_OPTION_REFRESH, 0, 0)
}

// システムプロキシ設定書き換え
func setRegistryOn() bool {
	if !ReloadCfg() {
		return false
	}
	mode := GetIntSafe(cfg.Section("RegAction").Key("ModeOn"), 0)
	if mode == 0 {
		return true
	}
	if mode == 1 {
		pac := GetStringSafe(cfg.Section("Pac").Key("PacName"))
		if pac == "" {
			notify("ERROR", "REG", "Mode 1: PacName is empty.")
			return false
		}
	}
	if !backupRegistry() {
		return false
	}
	sBefore, _ := getRegistryState()
	logOutput("LOG", "REG", "Before: E:%d, P:%s, S:%s, O:%s", sBefore.Enable, sBefore.PAC, sBefore.Server, sBefore.Override)
	var s RegState
	switch mode {
	case 1:
		pac := GetStringSafe(cfg.Section("Pac").Key("PacName"), "proxy.pac")
		s = RegState{Enable: 0, PAC: fmt.Sprintf("http://127.0.0.1:%s/%s", strconv.Itoa(getCurrentHttpdPort()), pac)}
	case 2:
		override := GetStringSafe(cfg.Section("RegAction").Key("ProxyOverride"))
		s = RegState{Enable: 1, Server: "127.0.0.1:" + strconv.Itoa(getCurrentProxyPort()), Override: override}
	}
	applyRegistry(s)
	notify("INFO", "REG", "Proxy settings have been changed.")
	logOutput("LOG", "REG", "After : E:%d, P:%s, S:%s, O:%s", s.Enable, s.PAC, s.Server, s.Override)
	return true
}

// システムプロキシ設定戻し
func setRegistryOff() bool {
	if !ReloadCfg() {
		return false
	}
	if GetIntSafe(cfg.Section("RegAction").Key("ModeOn"), 0) == 0 {
		return true
	}
	mode := GetIntSafe(cfg.Section("RegAction").Key("ModeOff"), 0)
	switch mode {
	case 1:
		sec := cfg.Section("RegBackup")
		applyRegistry(RegState{
			Enable:   uint32(GetIntSafe(sec.Key("ProxyEnable"), 0)),
			PAC:      GetStringSafe(sec.Key("AutoConfigURL")),
			Server:   GetStringSafe(sec.Key("ProxyServer")),
			Override: GetStringSafe(sec.Key("ProxyOverride")),
		})
	case 2:
		sec := cfg.Section("RegForce")
		t := GetIntSafe(sec.Key("Type"), 0)
		var s RegState
		switch t {
		case 0:
			s = RegState{
				Enable: 0,
			}
		case 1:
			s = RegState{
				Enable: 0,
				PAC:    GetStringSafe(sec.Key("AutoConfigURL")),
			}
		case 2:
			s = RegState{
				Enable:   1,
				Server:   GetStringSafe(sec.Key("ProxyServer")),
				Override: GetStringSafe(sec.Key("ProxyOverride")),
			}
		}
		applyRegistry(s)
	}
	notify("INFO", "REG", "Proxy settings have been restored.")
	sAfter, _ := getRegistryState()
	logOutput("LOG", "REG", "After : E:%d, P:%s, S:%s, O:%s", sAfter.Enable, sAfter.PAC, sAfter.Server, sAfter.Override)
	return true
}
