package main

import (
	"bytes"
	"fmt"
	"gopkg.in/ini.v1"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
	"unsafe"
)

// 設定項目一覧
type AppConfig struct {
	// [General]
	AutoPortMin  int    `sec:"General" ini:"AutoPortMin"  default:"20000"     comment:"自動割り当てにおいて空きポートを探す範囲"`
	AutoPortMax  int    `sec:"General" ini:"AutoPortMax"  default:"29999"     comment:""`
	AutoStart    int    `sec:"General" ini:"AutoStart"    default:"0"         comment:"起動時にProxy中継を開始 1:有効, 0:無効"`
	NotifyEnable int    `sec:"General" ini:"NotifyEnable" default:"1"         comment:"処理完了時の通知 1:有効, 0:無効(エラーダイアログ), -1:無効(ダイアログなし)"`
	HttpdPort    string `sec:"General" ini:"HttpdPort"    default:""          comment:"httpdが待ち受けるポート番号 (空なら自動割り当て)"`
	HttpdAddr    string `sec:"General" ini:"HttpdAddr"    default:"127.0.0.1" comment:"httpdが待ち受けるアドレス (空なら全アドレス対象)"`

	// [Log]
	LogDir        string `sec:"Log" ini:"LogDir"        default:"./log/"         comment:""`
	LogFileName   string `sec:"Log" ini:"LogFileName"   default:"proxyrelay.log" comment:""`
	LogMaxSize    int    `sec:"Log" ini:"LogMaxSize"    default:"1"              comment:"ログローテ：1ファイルあたりの最大サイズ(MB)"`
	LogMaxBackups int    `sec:"Log" ini:"LogMaxBackups" default:"6"              comment:"ログローテ：保持するログの最大世代数"`

	// [Proxy]
	Upstream  string `sec:"Proxy" ini:"Upstream"  default:""          comment:"中継する上位プロキシサーバー(書式：http://name:port)"`
	User      string `sec:"Proxy" ini:"User"      default:""          comment:"上位プロキシのユーザー名(空なら認証の代行を行いません)"`
	Pass      string `sec:"Proxy" ini:"Pass"      default:""          comment:"上位プロキシのパスワード"`
	ProxyPort string `sec:"Proxy" ini:"ProxyPort" default:""          comment:"Proxy中継が待ち受けるポート番号 (空なら自動割り当て)"`
	ProxyAddr string `sec:"Proxy" ini:"ProxyAddr" default:"127.0.0.1" comment:"Proxy中継が待ち受けるアドレス (空なら全アドレス対象)"`
	LogLevel  int    `sec:"Proxy" ini:"LogLevel"  default:"0"         comment:"Proxy中継の追加詳細ログ 0:無効, 1:エラー/警告, 2:1に加えて接続/切断/認証/フィルタ"`

	// [Pac]
	PacDownload int    `sec:"Pac" ini:"Download"        default:"0"                 comment:"オリジナルPACの取得 1:有効, 0:無効"`
	PacModify   int    `sec:"Pac" ini:"Modify"          default:"0"                 comment:"PACを加工 1:有効, 0:無効"`
	PacUrl      string `sec:"Pac" ini:"PacUrl"          default:""                  comment:"オリジナルPACの取得先URL"`
	HtmlDir     string `sec:"Pac" ini:"HtmlDir"         default:"./html/"           comment:"PACファイルを保存するディレクトリ"`
	OrigPacName string `sec:"Pac" ini:"OriginalPacName" default:"original.pac"      comment:"オリジナルPACをダウンロード後に保存する名前"`
	RuleFile    string `sec:"Pac" ini:"RuleFile"        default:"mod_rules.pac"     comment:"PAC改変のルールが記載されたファイル名(./conf/配下)|ルール内で%PORT%は(動的割当を含む)Proxyポート番号、%ADDR%はProxyのリスナアドレス"`
	PacPrefix   string `sec:"Pac" ini:"PacPrefix"       default:"__mod_proxyrelay_" comment:"オリジナルのFindProxyForURL関数をリネームする際の接頭辞"`
	PacName     string `sec:"Pac" ini:"PacName"         default:"proxy.pac"         comment:"OS設定へ配信するPACファイル名(PAC加工が有効な場合は加工後のファイル名)"`

	// [RegAction]
	ModeOn   int    `sec:"RegAction" ini:"ModeOn"        default:"0" comment:"Proxy中継開始時のOS設定変更方法 0:変更しない, 1: PAC指定(要：PacName), 2:直接指定"`
	Override string `sec:"RegAction" ini:"ProxyOverride" default:""  comment:"2:直接指定での除外設定（<-loopback>を含めないことを推奨）"`
	ModeOff  int    `sec:"RegAction" ini:"ModeOff"       default:"0" comment:"Proxy中継終了時のOS設定変更方法 0:変更しない, 1: 開始前の状態([RegBackup]の内容)に戻す, 2:[RegForce]の内容に書き換え"`

	// [RegForce]
	RFType     int    `sec:"RegForce" ini:"Type"          default:"0" comment:"ModeOff=2の時に適用する内容|0:プロキシ無効, 1:PAC指定(AutoConfigURL), 2:直接指定(ProxyServer,ProxyOverride)"`
	RFAutoUrl  string `sec:"RegForce" ini:"AutoConfigURL" default:""  comment:""`
	RFProxy    string `sec:"RegForce" ini:"ProxyServer"   default:""  comment:""`
	RFOverride string `sec:"RegForce" ini:"ProxyOverride" default:""  comment:""`

	// [RegBackup]
	BKEnable   int    `sec:"RegBackup" ini:"ProxyEnable"   default:"0" comment:"Proxy中継開始時にここにOS設定が自動保存されます／設定リストアに使用します"`
	BKAutoUrl  string `sec:"RegBackup" ini:"AutoConfigURL" default:""  comment:""`
	BKProxy    string `sec:"RegBackup" ini:"ProxyServer"   default:""  comment:""`
	BKOverride string `sec:"RegBackup" ini:"ProxyOverride" default:""  comment:""`

	// [Hook]
	HookInterval int `sec:"Hook" ini:"HookInterval"       default:"1000" comment:"各BAT起動後の待機条件監視間隔(ms)"`

	PreStartBat      string `sec:"Hook" ini:"PreStartBat"        default:"" comment:"Proxy中継開始・停止前後に実行するBATファイル(./conf/配下)(空なら実行しない)|# PreStart -> ProxyStart -> PostStart -> PostStart2|## 開始前"`
	PreStartTimeout  string `sec:"Hook" ini:"PreStartTimeout"    default:"" comment:"省略時30秒。BATが非0で終了するか、待機条件をタイムアウトまでに満たせなければ、後続処理に進みません"`
	PreStartWaitNot  string `sec:"Hook" ini:"PreStartWaitNot"    default:"" comment:"待機条件(WaitPort/WaitProc)の反転 0: 条件成立を待つ, 1: 条件非成立を待つ"`
	PreStartWaitPort string `sec:"Hook" ini:"PreStartWaitPort"   default:"" comment:"ポート番号/プロトコル(例：8080/tcp)"`
	PreStartWaitProc string `sec:"Hook" ini:"PreStartWaitProc"   default:"" comment:"プロセス名:個数(例：myapp.exe:1)"`

	PostStartBat      string `sec:"Hook" ini:"PostStartBat"       default:"" comment:"## 開始後-1"`
	PostStartTitle    string `sec:"Hook" ini:"PostStartTitle"     default:"" comment:"UI上の表示名(これが空の場合はbatが定義されていてもUIには表示されない)(PostStart/PostStart2のみ)"`
	PostStartTimeout  string `sec:"Hook" ini:"PostStartTimeout"   default:"" comment:""`
	PostStartWaitNot  string `sec:"Hook" ini:"PostStartWaitNot"   default:"" comment:""`
	PostStartWaitPort string `sec:"Hook" ini:"PostStartWaitPort"  default:"" comment:""`
	PostStartWaitProc string `sec:"Hook" ini:"PostStartWaitProc"  default:"" comment:""`

	PostStart2Bat      string `sec:"Hook" ini:"PostStart2Bat"      default:"" comment:"## 開始後-2"`
	PostStart2Title    string `sec:"Hook" ini:"PostStart2Title"    default:"" comment:""`
	PostStart2Timeout  string `sec:"Hook" ini:"PostStart2Timeout"  default:"" comment:""`
	PostStart2WaitNot  string `sec:"Hook" ini:"PostStart2WaitNot"  default:"" comment:""`
	PostStart2WaitPort string `sec:"Hook" ini:"PostStart2WaitPort" default:"" comment:""`
	PostStart2WaitProc string `sec:"Hook" ini:"PostStart2WaitProc" default:"" comment:""`

	PreStopBat      string `sec:"Hook" ini:"PreStopBat"         default:"" comment:"# PreStop -> ProxyStop -> PostStop|## 停止前"`
	PreStopTimeout  string `sec:"Hook" ini:"PreStopTimeout"     default:"" comment:""`
	PreStopWaitNot  string `sec:"Hook" ini:"PreStopWaitNot"     default:"" comment:""`
	PreStopWaitPort string `sec:"Hook" ini:"PreStopWaitPort"    default:"" comment:""`
	PreStopWaitProc string `sec:"Hook" ini:"PreStopWaitProc"    default:"" comment:""`

	PostStopBat      string `sec:"Hook" ini:"PostStopBat"        default:"" comment:"## 停止後"`
	PostStopTimeout  string `sec:"Hook" ini:"PostStopTimeout"    default:"" comment:""`
	PostStopWaitNot  string `sec:"Hook" ini:"PostStopWaitNot"    default:"" comment:""`
	PostStopWaitPort string `sec:"Hook" ini:"PostStopWaitPort"   default:"" comment:""`
	PostStopWaitProc string `sec:"Hook" ini:"PostStopWaitProc"   default:"" comment:""`
}

var (
	cfg           *ini.File
	relPathRegex  = regexp.MustCompile(`^([-.a-zA-Z0-9_]+/)+$`)
	fileNameRegex = regexp.MustCompile(`^[-.a-zA-Z0-9_]+$`)
	upsRegex      = regexp.MustCompile(`^http://[\w.-]+(:\d+)?$`)
	pacRegex      = regexp.MustCompile(`^https?://[\w.-]+(:\d+)?(/[^?\s#]*)?\.pac$`)
	prefixRegex   = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	condPortRegex = regexp.MustCompile(`^(\d+)/(tcp|udp)`)
	condProcRegex = regexp.MustCompile(`^([^:]+):[1-9][0-9]*$`)
	addrRegex     = regexp.MustCompile(`^(?:\d{1,3}\.){3}\d{1,3}$|^$`)
)

// バリデーション
func Validate() error {
	isNum := func(sec, key string) error {
		v := cfg.Section(sec).Key(key).String()
		label := sec + " -> " + key
		if v == "" {
			return nil
		}
		if _, err := strconv.Atoi(v); err != nil {
			return fmt.Errorf("%s : must be EmptyOrNumeric", label)
		}
		return nil
	}
	inRange := func(sec, key string, min, max int) error {
		v, err := cfg.Section(sec).Key(key).Int()
		label := sec + " -> " + key
		if err != nil {
			return fmt.Errorf("%s : must be Numeric", label)
		}
		if v < min || v > max {
			return fmt.Errorf("%s : out of range", label)
		}
		return nil
	}
	match := func(sec, key string, re *regexp.Regexp, allowEmpty bool) error {
		v := cfg.Section(sec).Key(key).String()
		label := sec + " -> " + key
		if allowEmpty && v == "" {
			return nil
		}
		if !re.MatchString(v) {
			return fmt.Errorf("%s : invalid format", label)
		}
		return nil
	}
	checkNumRange := func(sec, key string, min, max int) error {
		v := cfg.Section(sec).Key(key).String()
		label := sec + " -> " + key
		if v == "" {
			return nil
		}
		_, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%s : must be EmptyOrNumeric", label)
		}
		return inRange(sec, key, min, max)
	}
	// [General]
	if err := inRange("General", "AutoPortMin", 1, 65535); err != nil {
		return err
	}
	if err := inRange("General", "AutoPortMax", 1, 65535); err != nil {
		return err
	}
	if err := inRange("General", "AutoStart", 0, 1); err != nil {
		return err
	}
	if err := inRange("General", "NotifyEnable", -1, 1); err != nil {
		return err
	}
	if err := isNum("General", "HttpdPort"); err != nil {
		return err
	}
	if err := match("General", "HttpdAddr", addrRegex, true); err != nil {
		return err
	}
	// [Log]
	if err := match("Log", "LogDir", relPathRegex, false); err != nil {
		return err
	}
	if err := match("Log", "LogFileName", fileNameRegex, false); err != nil {
		return err
	}
	if err := inRange("Log", "LogMaxSize", 1, 999999); err != nil {
		return err
	}
	if err := inRange("Log", "LogMaxBackups", 1, 999999); err != nil {
		return err
	}
	// [Proxy]
	if err := match("Proxy", "Upstream", upsRegex, true); err != nil {
		return err
	}
	if err := isNum("Proxy", "ProxyPort"); err != nil {
		return err
	}
	if err := inRange("Proxy", "LogLevel", 0, 2); err != nil {
		return err
	}
	if err := match("Proxy", "ProxyAddr", addrRegex, true); err != nil {
		return err
	}
	// [Pac]
	if err := match("Pac", "PacUrl", pacRegex, true); err != nil {
		return err
	}
	if err := match("Pac", "HtmlDir", relPathRegex, false); err != nil {
		return err
	}
	if err := match("Pac", "OriginalPacName", fileNameRegex, false); err != nil {
		return err
	}
	if err := match("Pac", "RuleFile", fileNameRegex, false); err != nil {
		return err
	}
	if err := match("Pac", "PacPrefix", prefixRegex, false); err != nil {
		return err
	}
	if err := match("Pac", "PacName", fileNameRegex, false); err != nil {
		return err
	}
	// [RegAction]
	if err := inRange("RegAction", "ModeOn", 0, 2); err != nil {
		return err
	}
	if err := inRange("RegAction", "ModeOff", 0, 2); err != nil {
		return err
	}
	// [RegForce]
	if err := inRange("RegForce", "Type", 0, 2); err != nil {
		return err
	}
	// [Hook]
	if err := inRange("Hook", "HookInterval", 100, 999999); err != nil {
		return err
	}
	hookNames := []string{"PreStart", "PostStart", "PostStart2", "PreStop", "PostStop"}
	for _, name := range hookNames {
		if err := match("Hook", name+"Bat", fileNameRegex, true); err != nil {
			return err
		}
		if err := checkNumRange("Hook", name+"Timeout", 0, 999999); err != nil {
			return err
		}
		if err := checkNumRange("Hook", name+"WaitNot", 0, 1); err != nil {
			return err
		}
		if err := match("Hook", name+"WaitPort", condPortRegex, true); err != nil {
			return err
		}
		if err := match("Hook", name+"WaitProc", condProcRegex, true); err != nil {
			return err
		}
	}
	return nil
}

// 設定ファイル内容生成
func generateDefault(v interface{}) *ini.File {
	f := ini.Empty()
	val := reflect.ValueOf(v).Elem()
	typ := val.Type()
	for i := 0; i < typ.NumField(); i++ {
		fld := typ.Field(i)
		secN := fld.Tag.Get("sec")
		iniK := fld.Tag.Get("ini")
		defV := fld.Tag.Get("default")
		comm := fld.Tag.Get("comment")
		if secN == "" || iniK == "" {
			continue
		}
		s, _ := f.GetSection(secN)
		if s == nil {
			s, _ = f.NewSection(secN)
		}
		key, _ := s.NewKey(iniK, defV)
		if comm != "" {
			lines := strings.Split(comm, "|")
			var formatted string
			for idx, line := range lines {
				if idx == 0 {
					formatted = " " + line
				} else {
					formatted += "\r\n; " + line
				}
			}
			key.Comment = formatted
		}
	}
	return f
}

// 設定値読み込み関数 ( fallback 省略時は 0(int) / ""空文字列(String) )
func GetIntSafe(key *ini.Key, fallback ...int) int {
	val, err := key.Int()
	if err != nil && len(fallback) > 0 {
		return fallback[0]
	}
	return val
}

func GetInt64Safe(key *ini.Key, fallback ...int64) int64 {
	val, err := key.Int64()
	if err != nil && len(fallback) > 0 {
		return fallback[0]
	}
	return val
}

func GetStringSafe(key *ini.Key, fallback ...string) string {
	val := key.String()
	if len(val) == 0 && len(fallback) > 0 {
		return fallback[0]
	}
	return val
}

// 設定ファイル読み込み(初回)
func LoadCfg() bool {
	if _, err := os.Stat(CONF_PATH); os.IsNotExist(err) {
		if err := os.MkdirAll(CONF_DIR, 0755); err != nil {
			showMessage("ERROR", "Conf", "conf dir not found.\n%v", err)
			return false
		}
		cfg = generateDefault(&AppConfig{})
		if err := saveAsSJIS(cfg, CONF_PATH); err != nil {
			showMessage("ERROR", "Conf", "ini create failed.\n%v", err)
			return false
		}
	} else {
		cfg, err = ini.Load(CONF_PATH)
		if err != nil {
			showMessage("ERROR", "Conf", "ini load failed.\n%v", err)
			return false
		}
	}
	err := Validate()
	if err != nil {
		showMessage("ERROR", "Conf", "ini validate failed.\n%v", err)
		return false
	}
	return true
}

// 設定ファイルリロード
func ReloadCfg() bool {
	if err := cfg.Reload(); err != nil {
		logOutput("ERROR", "Conf", "Settings reload failed. %v", err)
		return false
	}
	err := Validate()
	if err != nil {
		logOutput("ERROR", "Conf", "Settings validate failed. %v", err)
		return false
	}
	return true
}

// SJISで保存
func saveAsSJIS(f *ini.File, path string) error {
	var buf strings.Builder
	if _, err := f.WriteTo(&buf); err != nil {
		return err
	}
	return os.WriteFile(path, encodeToSJIS(buf.String()), 0644)
}

// SJISへ変換
func encodeToSJIS(s string) []byte {
	if s == "" {
		return []byte{}
	}
	if !utf8.ValidString(s) {
		return []byte(s)
	}
	pUtf16, err := syscall.UTF16FromString(s)
	if err != nil {
		return []byte(s)
	}
	resSize, _, _ := procWideCharToMultiByte.Call(
		uintptr(CP_932), 0, uintptr(unsafe.Pointer(&pUtf16[0])),
		uintptr(len(pUtf16)), 0, 0, 0, 0)
	if resSize == 0 {
		return []byte(s)
	}
	res := make([]byte, resSize)
	procWideCharToMultiByte.Call(
		uintptr(CP_932), 0, uintptr(unsafe.Pointer(&pUtf16[0])), uintptr(len(pUtf16)),
		uintptr(unsafe.Pointer(&res[0])), resSize, 0, 0)
	return bytes.TrimRight(res, "\x00")
}
