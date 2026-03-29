# ProxyRelay

Windows向けのプロキシ中継ツールです。

## 機能
- 上位HTTPプロキシへの中継
  - 上位プロキシへのBasic認証情報の自動付加(オプション)
  - 通信宛先ホストフィルタリング(オプション)
  - 中継通信量モニター(WebUI)
- OSのプロキシ設定バックアップ・書き換え(オプション)
- PACファイル提供のための単機能Httpd
- 中継開始/停止の前後でのプログラム実行(オプション)

## 使用方法 / Usage
1. [Releases](https://github.com/hfftjp/ProxyRelay/releases) から最新の `proxyrelay.zip` をダウンロードします。
2. `proxyrelay.exe` を実行してください。
3. 初回実行後に ./conf/config.ini が生成されます。
   - 環境に合わせて設定してください。
   - 主要な項目は管理画面(WebUI)からも変更できます。
4. 設定が完了したら、起動後タスクトレーアイコンのメニューから開始を押して中継を開始します。
   - AutoStart設定を有効にした場合は、起動直後から中継が自動的に開始されます。

・・・詳細は後ほど・・・

## ビルド方法 / How to Build
・・・後ほど・・・


## ライセンス / License
- **This Project**: [MIT License](LICENSE)
- **Third-Party Components**: See [THIRD-PARTY-NOTICES.txt](THIRD-PARTY-NOTICES.txt) for details.

## 免責事項 / Disclaimer
THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

本ソフトウェアの使用により生じた損害について、作者は一切の責任を負いません。利用者の責任において使用してください。
