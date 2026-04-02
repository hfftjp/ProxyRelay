// タブ切替
let actTab = "log";
function switchTab(viewId) {
  actTab = viewId;
  document.querySelectorAll('.view-content, .tab-btn').forEach(el => el.classList.remove('active'));
  document.getElementById(viewId).classList.add('active');
  document.getElementById('tab-' + viewId).classList.add('active');
  if (viewId === 'settings') loadSettings();
  if (viewId === 'stats') updateTrafficHistory();
}

// ヘッダステータス更新
async function updateStatus() {
  try {
    const res = await fetch('/api/status');
    const s = await res.json();
    document.getElementById('dot-httpd').className = s.httpd ? 'dot on' : 'dot off';
    document.getElementById('dot-proxy').className = s.proxy ? 'dot on' : 'dot off';
  } catch (e) {}
}

// 汎用アクション受付
async function controlSrv(action,msg) {
  if(!confirm( "Do you want to '" + msg + "' ?")) return;
  switch (action){
    case 'httpd-reboot':
      showToast('Httpd is rebooting...', 'success');
      break;
    case 'start-all':
      showToast('Proxy is starting...', 'success');
      break;
    case 'stop-all':
      showToast('Proxy is stopping...', 'error');
      break;
    case 'quit':
      showToast('Terminaing...', 'error');
      break;
  }
  try {
    const response = await fetch('/api/control?action=' + action, { method: 'POST' });
    if (action === 'quit' && response.ok) {
      setTimeout(() => {
        window.open('about:blank', '_self').close();
        document.body.innerHTML = '<h1 style="text-align:center; margin-top:20%;">Server Terminated.<br>You can close this tab.</h1>';
      }, 500);
      return;
    }
  } catch (e) {
    if (action !== 'quit') {
      showToast('Failed to communicate with server', 'error');
    }
  }
  updateStatus();
}

// 通知表示
function showToast(msg, type = 'success') {
  const container = document.getElementById('toast-container');
  const toast = document.createElement('div');
  toast.className = `toast ${type}`;
  toast.innerText = msg;
  container.appendChild(toast);
  setTimeout(() => toast.classList.add('show'), 10);
  setTimeout(() => {
    toast.classList.remove('show');
    setTimeout(() => container.removeChild(toast), 300);
  }, 4000);
}

// アプリ側通知履歴読取
let lastNotifyID = 0;
let isFirstNotifyPoll = true;
async function updateNotifications() {
  try {
    const res = await fetch(`/api/notifications?last_id=${lastNotifyID}`);
    if (!res.ok) return;
    const data = await res.json();
    if (!data || data.length === 0) {
      isFirstNotifyPoll = false;
      return;
    }
    const latest = data[data.length - 1];
    if (isFirstNotifyPoll) {
      lastNotifyID = latest.id;
      isFirstNotifyPoll = false;
      return;
    }
    data.forEach(n => {
      if (n.id !== lastNotifyID) {
        if (typeof showToast === 'function') {
          showToast(n.message, n.type);
        }
        lastNotifyID = n.id;
      }
    });
  } catch (e) {
    console.error("Notify poll error:", e);
  }
}

// 通信速度情報更新
async function updateTraffic() {
  try {
    const res = await fetch('/api/traffic');
    const data = await res.json();
    formatSpeedFull(data.sent, 'speed-sent');
    formatSpeedFull(data.recv, 'speed-recv');
  } catch (e) {}
}

function formatSpeedFull(bytes, elementId) {
  const n = Number(bytes) || 0;
  const kb = n / 1024;
  const mb = kb / 1024;
  let valStr, unitStr, isHighSpeed;
  if (mb >= 1) {
    valStr = mb.toFixed(1);
    unitStr = "MB/s";
    isHighSpeed = true;
  } else {
    valStr = kb.toFixed(1);
    unitStr = "KB/s";
    isHighSpeed = false;
  }
  const el = document.getElementById(elementId);
  if (el) {
    el.style.color = isHighSpeed ? "var(--danger-color)" : "inherit";
    el.style.fontWeight = isHighSpeed ? "bold" : "normal";
    el.innerText = `${valStr.padStart(5, ' ')} ${unitStr}`;
  }
}

// 通信速度詳細データ(statsタブ時のみ)
let trafficChart = null;
async function updateTrafficHistory() {
  if (actTab !== 'stats') return;
  try {
    const res = await fetch('/api/traffic-history');
    const data = await res.json();
    if (!trafficChart) initChart();
    trafficChart.data.datasets[0].data = data.sent_history.map(b => (b / 1024).toFixed(1));
    trafficChart.data.datasets[1].data = data.recv_history.map(b => (b / 1024).toFixed(1));
    trafficChart.update();
  } catch (e) {
    console.error("Traffic history error:", e);
  }
}

function initChart() {
  const ctx = document.getElementById('trafficChart').getContext('2d');
  const maxLinePlugin = {
    id: 'maxLinePlugin',
    afterDraw: (chart) => {
      const {ctx, chartArea: {left, right}, scales: {y}} = chart;
      ctx.save();
      chart.data.datasets.forEach((dataset) => {
        if (!dataset.data || dataset.data.length === 0) return;
        const numericData = dataset.data.map(Number);
        const maxVal = Math.max(...numericData);
        if (maxVal <= 0) return;
        const yPos = y.getPixelForValue(maxVal);
        ctx.strokeStyle = dataset.borderColor;
        ctx.setLineDash([5, 5]);
        ctx.lineWidth = 1;
        ctx.beginPath();
        ctx.moveTo(left, yPos);
        ctx.lineTo(right, yPos);
        ctx.stroke();
        let displayValue;
        if (maxVal >= 1024 / 8) {
          displayValue = (maxVal / 1024 * 8).toFixed(2) + ' Mbps';
        } else {
          displayValue = (maxVal * 8).toFixed(1) + ' Kbps';
        }
        ctx.fillStyle = dataset.borderColor;
        ctx.setLineDash([]);
        ctx.font = 'bold 10px sans-serif';
        ctx.textAlign = 'left';
        ctx.fillText(`Max: ${displayValue}`, left + 5, yPos - 5);
      });
      ctx.restore();
    }
  };
  trafficChart = new Chart(ctx, {
    type: 'line',
    plugins: [maxLinePlugin],
    data: {
      labels: Array.from({length: 120}, (_, i) => 119 - i),
      datasets: [
        { 
          label: 'Sent', 
          borderColor: '#2980b9', 
          backgroundColor: 'rgba(41, 128, 185, 0.1)',
          borderWidth: 2, 
          pointRadius: 0,
          fill: true, 
          data: [],
          tension: 0.2
        },
        { 
          label: 'Recv', 
          borderColor: '#27ae60', 
          backgroundColor: 'rgba(39, 174, 96, 0.1)',
          borderWidth: 2, 
          pointRadius: 0,
          fill: true, 
          data: [],
          tension: 0.2
        }
      ]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      interaction: { mode: 'none', intersect: false },
      scales: {
        x: { 
          display: true,
          ticks: {
            callback: function(value, index) {
              const label = this.getLabelForValue(value);
              return (label % 10 === 0) ? label : '';
            },
            autoSkip: false,
            maxRotation: 0
          },
          grid: {
            color: (context) => {
              if (context.tick && context.tick.label !== "") {
                const label = parseInt(context.tick.label);
                return (label % 10 === 0) ? '#eeeeee' : 'transparent';
              }
              return 'transparent';
            }
          }
        },
        y: { 
          beginAtZero: true,
          title: { display: true, text: 'Speed (KB/s)', font: { size: 11 } }
        }
      },
      plugins: {
        legend: { position: 'top' },
        tooltip: {
          enabled: false
        }
      }
    }
  });
}

// セクションの開閉切り替え(settingsタブ)
function toggleSection(titleEl) {
  titleEl.classList.toggle('collapsed');
  const content = titleEl.nextElementSibling;
  if (content) {
    content.classList.toggle('hidden');
  }
}

// 設定読み込み(settingsタブ)
async function loadSettings(force = false) {
  try {
    const res = await fetch('/api/read-config');
    const s = await res.json();
    const fields = {
      'cfg-autostart': s.autoStart,
      'cfg-upstream': s.upstream,
      'cfg-user': s.user,
      'cfg-proxy-port': s.proxyPort,
      'cfg-pac-url': s.pacUrl,
      'cfg-pac-prefix': s.pacPrefix,
      'pac-content': s.pacContent
    };
    for(let id in fields) {
      const el = document.getElementById(id);
      if(el && (force || !el.dataset.loaded)) {
        if (el.type === 'checkbox') {
          el.checked = (fields[id] === 1);
        } else {
          el.value = fields[id] || "";
        }
        el.dataset.loaded = "true";
      }
    }
    if(force) showToast('Settings reloaded from server.');
  } catch (e) {
    showToast('Failed to load settings: ' + e, 'error');
  }
}

// 設定保存(settingsタブ)
async function saveSettings() {
  const btn = document.getElementById('save-btn');
  btn.disabled = true;
  const portEl = document.getElementById('cfg-proxy-port');
  const portVal = portEl.value.trim();
  if (portVal !== "") {
    const portNum = Number(portVal);
    if (!Number.isInteger(portNum) || portNum < 1 || portNum > 65535) {
      showToast('For ProxyPort, please enter a number between 1 and 65535.', 'error');
      portEl.focus();
      btn.disabled = false;
      return;
    }
  }
  const data = {
    autoStart: document.getElementById('cfg-autostart').checked ? 1 : 0,
    upstream: document.getElementById('cfg-upstream').value,
    user: document.getElementById('cfg-user').value,
    pass: document.getElementById('cfg-pass').value ? btoa(document.getElementById('cfg-pass').value) : "",
    proxyPort: portVal,
    pacUrl: document.getElementById('cfg-pac-url').value,
    pacPrefix: document.getElementById('cfg-pac-prefix').value,
    pacContent: document.getElementById('pac-content').value
  };
  try {
    const res = await fetch('/api/save-config', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(data)
    });
    
    if(res.ok) {
      showToast('Settings have been saved.');
      document.getElementById('cfg-pass').value = '';
    } else {
      const errText = await res.text();
      showToast('Save failed : ' + errText, 'error');
    }
  } catch (e) {
    showToast('A communication error has occurred.', 'error');
  } finally {
    btn.disabled = false;
  }
}

// ログ自動読み込みトグル(logタブ)
let isPaused = false;
function togglePause() {
  isPaused = !isPaused;
  const btn = document.getElementById('pause-btn');
  btn.innerText = isPaused ? "Resume Log" : "Pause Log";
  btn.classList.toggle('active', isPaused);
}

// ログ読み込み(logタブ時のみ)
let currentLogOffset = -1;
let isLogUpdating = false;
async function updateLog() {
  if (actTab !== 'log' || isPaused || isLogUpdating) return;
  isLogUpdating = true;
  const el = document.getElementById('log-container');
  try {
    const res = await fetch(`/api/log?offset=${currentLogOffset}`);
    if (!res.ok) {
      throw new Error(`HTTP error! status: ${res.status}`);
    }
    const newOffsetStr = res.headers.get('X-Log-Size');
    if (!newOffsetStr) {
      if (currentLogOffset === -1) el.innerText = "Error: X-Log-Size header missing.";
      return;
    }
    const newOffset = parseInt(newOffsetStr);
    const t = await res.text();
    if (currentLogOffset === -1) {
      el.innerText = ""; 
    }
    if (currentLogOffset !== -1 && newOffset < currentLogOffset) {
      el.appendChild(document.createTextNode("\n--- Log Rotated ---\n"));
    }
    if (t.length > 0) {
      const isAtBottom = el.scrollHeight - el.scrollTop <= el.clientHeight + 50;
      const frag = document.createDocumentFragment();
      const lines = t.split('\n');
      lines.forEach((line, index) => {
        if (line === "" && index === lines.length - 1) return;
        const span = document.createElement('span');
        let content = line;
        if (line.includes('[ERROR]')) {
          span.className = 'log-err';
        } else if (line.includes('[WARNING]') || line.includes('BLOCK:') ) {
          span.className = 'log-warn';
        } else if (line.includes('[INFO]')) {
          span.className = 'log-info';
        }
        span.innerText = content + (index === lines.length - 1 ? "" : "\n");
        frag.appendChild(span);
      });
      
      el.appendChild(frag);
      if (isAtBottom) el.scrollTop = el.scrollHeight;
    }
    currentLogOffset = newOffset;
    document.getElementById('last-update').innerText = 'Last update: ' + new Date().toLocaleTimeString();
  } catch (e) {
    if (currentLogOffset === -1) {
      el.innerText = `Connection Error: ${e.message}`;
    }
  } finally {
    isLogUpdating = false;
  }
}

// リストの開閉(logタブ)
function toggleLogLevelList(e) {
  e.stopPropagation();
  document.getElementById('log-level-options').classList.toggle('show');
}

// ログレベル変更(logタブ)
async function changeLogLevel(val, text) {
  try {
    const res = await fetch(`/api/set-log-level?level=${val}`);
    if (res.ok) {
      document.getElementById('log-level-display').innerText = text;
      document.getElementById('log-level-options').classList.remove('show');
      showToast(`Log level changed to ${val}`);
    }
  } catch (e) {
    console.error("LogLevel change error:", e);
  }
}

// 現在のログレベルをサーバーから取得してUIに反映(logタブ)
async function loadCurrentLogLevel() {
  try {
    const res = await fetch('/api/get-log-level');
    const data = await res.json();
    const levels = {
      0: '0: None',
      1: '1: Error/Warning',
      2: '2: Connect/Auth'
    };
    if (levels[data.level] !== undefined) {
      document.getElementById('log-level-display').innerText = levels[data.level];
    }
  } catch (e) {}
}

// 枠外をクリックしたら閉じる設定(logタブ)
window.addEventListener('click', () => {
  const options = document.getElementById('log-level-options');
  if (options) options.classList.remove('show');
});

// 定期実行登録・初期実行
async function poll(fn) {
  await fn().catch(() => {});
  setTimeout(() => poll(fn), 2000);
}
poll(updateStatus);
poll(updateNotifications);
poll(updateTraffic);
poll(updateTrafficHistory);
loadCurrentLogLevel();
poll(updateLog);
loadSettings(false);

// 全ての入力欄の補完・スペルチェックを強制オフにする
document.querySelectorAll('input, textarea').forEach(el => {
  el.setAttribute('autocomplete', 'off');
  el.setAttribute('autocorrect', 'off');
  el.setAttribute('autocapitalize', 'off');
  el.setAttribute('spellcheck', false);
});

// 右クリックメニューを禁止
document.addEventListener('contextmenu', e => e.preventDefault());

// Ctrl + マウスホイールによるズームを禁止
document.addEventListener('wheel', e => {
  if (e.ctrlKey) {
    e.preventDefault();
  }
}, { passive: false });

// キーボードショートカット (Ctrl + +, -, 0) によるズームを禁止
document.addEventListener('keydown', e => {
  if (e.ctrlKey) {
    const isZoomKeys = (
      e.key === '+' || e.key === '-' || e.key === '=' || e.key === '0' || 
      e.key === 'Add' || e.key === 'Subtract' ||
      e.keyCode === 107 || e.keyCode === 109 || e.keyCode === 187 || e.keyCode === 189 || e.keyCode === 48
    );
    
    if (isZoomKeys) {
      e.preventDefault();
    }
  }
}, { capture: true });

// 反映タイミングの説明文マスター
const DELAY_MESSAGES = {
  "1": "Reflected on next ProxyRelay (re)start",
  "2": "Requires App restart",
};
function initDelayTooltips() {
  document.querySelectorAll('.delay-info').forEach(el => {
    const id = el.dataset.delay;
    if (DELAY_MESSAGES[id]) {
      el.title = DELAY_MESSAGES[id];
    }
  });
}
initDelayTooltips();
