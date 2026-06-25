// DOM 元素声明
const tempDisplay = document.getElementById('temp-display');
const gaugeFill = document.getElementById('gauge-fill');
const relayStatus = document.getElementById('relay-status');
const modeStatus = document.getElementById('mode-status');
const connectionBadge = document.getElementById('connection-badge');

const btnModeAuto = document.getElementById('btn-mode-auto');
const btnModeManual = document.getElementById('btn-mode-manual');
const manualControlSection = document.getElementById('manual-control-section');

const btnFanOpen = document.getElementById('btn-fan-open');
const btnFanClose = document.getElementById('btn-fan-close');
const btnFanQuery = document.getElementById('btn-fan-query');
const querySpinner = document.getElementById('query-spinner');

const inputCeiling = document.getElementById('input-ceiling');
const inputFloor = document.getElementById('input-floor');

const toastContainer = document.getElementById('toast-container');

// 全局变量保存后端配置，以防与用户输入焦点冲突
let currentConfig = {
    ceiling: null,
    floor: null
};

// ====================== Toast 消息弹窗 ======================
function showToast(message, type = 'info') {
    const toast = document.createElement('div');
    toast.className = `toast toast-${type}`;
    
    // 清除 emoji 标志，改用纯文本前缀
    let prefix = '[提示]';
    if (type === 'success') prefix = '[成功]';
    if (type === 'error') prefix = '[错误]';
    
    toast.innerHTML = `<span>${prefix}</span> <span>${message}</span>`;
    toastContainer.appendChild(toast);
    
    // 3秒后淡出并移除
    setTimeout(() => {
        toast.style.animation = 'toast-out 0.3s forwards cubic-bezier(0.18, 0.89, 0.32, 1.28)';
        toast.addEventListener('animationend', () => {
            toast.remove();
        });
    }, 3000);
}

// ====================== 仪表盘环形渲染 ======================
function updateGauge(temp) {
    const maxTemp = 100; // 最大刻度 100℃
    const perimeter = 2 * Math.PI * 50; // 圆环周长: 314.15926...
    
    if (temp === null || isNaN(temp)) {
        tempDisplay.textContent = '--';
        gaugeFill.style.strokeDashoffset = perimeter;
        gaugeFill.style.stroke = 'var(--color-cool)';
        return;
    }
    
    // 显示温度值
    tempDisplay.textContent = temp.toFixed(1);
    
    // 限制温度在 0 - 100 之间做百分比显示
    const percent = Math.min(Math.max(temp / maxTemp, 0), 1);
    const offset = perimeter * (1 - percent);
    gaugeFill.style.strokeDashoffset = offset;
    
    // 根据温度范围切换仪表盘颜色
    if (temp < 50) {
        gaugeFill.style.stroke = 'var(--color-cool)';
    } else if (temp < 65) {
        gaugeFill.style.stroke = 'var(--color-warm)';
    } else {
        gaugeFill.style.stroke = 'var(--color-hot)';
    }
}

// ====================== 更新状态数据 ======================
function updateUI(data) {
    // 1. 在线状态 Badge
    connectionBadge.textContent = "连接正常";
    connectionBadge.className = "badge badge-online";
    
    // 2. 温度仪表盘
    updateGauge(data.temp);
    
    // 3. 继电器状态
    if (data.relay_state === true) {
        relayStatus.textContent = '开启';
        relayStatus.className = 'info-value text-on';
    } else if (data.relay_state === false) {
        relayStatus.textContent = '关闭';
        relayStatus.className = 'info-value text-off';
    } else {
        relayStatus.textContent = '读取失败';
        relayStatus.className = 'info-value text-unknown';
    }
    
    // 4. 控制模式与按钮激活状态
    if (data.mode === 'auto') {
        modeStatus.textContent = '自动温控';
        modeStatus.className = 'info-value text-auto';
        
        btnModeAuto.className = 'btn-mode active-auto';
        btnModeManual.className = 'btn-mode';
        
        // 自动温控下锁定手动操作
        manualControlSection.classList.add('card-disabled-overlay');
        btnFanOpen.disabled = true;
        btnFanClose.disabled = true;
        btnFanQuery.disabled = true;
    } else if (data.mode === 'manual') {
        modeStatus.textContent = '手动控制';
        modeStatus.className = 'info-value text-warm';
        
        btnModeAuto.className = 'btn-mode';
        btnModeManual.className = 'btn-mode active-manual';
        
        // 手动模式下解锁手动操作
        manualControlSection.classList.remove('card-disabled-overlay');
        btnFanOpen.disabled = false;
        btnFanClose.disabled = false;
        btnFanQuery.disabled = false;
    }
    
    // 5. 更新配置阈值输入框（未被编辑/Focus时才更新）
    currentConfig.ceiling = data.ceiling;
    currentConfig.floor = data.floor;
    
    if (document.activeElement !== inputCeiling) {
        inputCeiling.value = data.ceiling;
    }
    if (document.activeElement !== inputFloor) {
        inputFloor.value = data.floor;
    }

}

// 离线 UI 渲染
function handleOffline(error) {
    console.error("Fetch status failed:", error);
    connectionBadge.textContent = "离线/断开";
    connectionBadge.className = "badge badge-offline";
    tempDisplay.textContent = '--';
    gaugeFill.style.strokeDashoffset = 2 * Math.PI * 50;
    relayStatus.textContent = '读取失败';
    relayStatus.className = 'info-value text-unknown';
}

// ====================== API 交互请求 ======================

// 获取所有数据状态
async function fetchStatus() {
    try {
        const response = await fetch('api/status');
        if (!response.ok) throw new Error("Server response error");
        const data = await response.json();
        updateUI(data);
    } catch (err) {
        handleOffline(err);
    }
}

// 设置运行模式
async function setMode(mode) {
    try {
        const response = await fetch('api/set_mode', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ mode })
        });
        const res = await response.json();
        if (res.success) {
            showToast(res.message, 'success');
            fetchStatus();
        } else {
            showToast(res.message, 'error');
        }
    } catch (err) {
        showToast("切换模式失败，请检查网络", 'error');
    }
}

// 手动开关继电器
async function controlFan(state) {
    const url = state ? 'api/open' : 'api/close';
    const actionName = state ? '开启风扇' : '关闭风扇';
    try {
        const response = await fetch(url, { method: 'POST' });
        const res = await response.json();
        if (res.success) {
            showToast(res.message, 'success');
            fetchStatus();
        } else {
            showToast(res.message, 'error');
        }
    } catch (err) {
        showToast(`${actionName}失败，请重试`, 'error');
    }
}

// 手动查询状态
async function queryHardwareState() {
    btnFanQuery.classList.add('loading');
    btnFanQuery.disabled = true;
    try {
        const response = await fetch('api/query', { method: 'POST' });
        const res = await response.json();
        if (res.success) {
            const stateStr = res.relay_state === true ? "开启" : (res.relay_state === false ? "关闭" : "未知");
            showToast(`状态同步成功，当前硬件状态为：【${stateStr}】`, 'success');
            fetchStatus();
        } else {
            showToast(res.message, 'error');
        }
    } catch (err) {
        showToast("查询继电器状态失败，硬件无响应或连接异常", 'error');
    } finally {
        btnFanQuery.classList.remove('loading');
        btnFanQuery.disabled = false;
    }
}

// 自动保存温控配置
async function autoSaveConfig() {
    const ceiling = parseFloat(inputCeiling.value);
    const floor = parseFloat(inputFloor.value);
    
    if (isNaN(ceiling) || isNaN(floor)) {
        showToast("请输入合法的温度数值", 'error');
        inputCeiling.value = currentConfig.ceiling;
        inputFloor.value = currentConfig.floor;
        return;
    }
    
    if (ceiling <= floor) {
        showToast("温度上限必须大于温度下限", 'error');
        inputCeiling.value = currentConfig.ceiling;
        inputFloor.value = currentConfig.floor;
        return;
    }
    
    // 如果值没有改变，避免多余请求
    if (ceiling === currentConfig.ceiling && floor === currentConfig.floor) {
        return;
    }
    
    try {
        const response = await fetch('api/set_config', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ ceiling, floor })
        });
        const res = await response.json();
        if (res.success) {
            showToast("配置已自动保存并应用", 'success');
            fetchStatus();
        } else {
            showToast(res.message, 'error');
            inputCeiling.value = currentConfig.ceiling;
            inputFloor.value = currentConfig.floor;
        }
    } catch (err) {
        showToast("自动保存配置失败，请重试", 'error');
        inputCeiling.value = currentConfig.ceiling;
        inputFloor.value = currentConfig.floor;
    }
}

// ====================== 事件绑定与生命周期 ======================
document.addEventListener('DOMContentLoaded', () => {
    // 首次拉取数据
    fetchStatus();
    
    // 设置 1 秒的定时轮询
    setInterval(fetchStatus, 1000);
    
    // 按钮动作监听
    btnModeAuto.addEventListener('click', () => setMode('auto'));
    btnModeManual.addEventListener('click', () => setMode('manual'));
    
    btnFanOpen.addEventListener('click', () => controlFan(true));
    btnFanClose.addEventListener('click', () => controlFan(false));
    btnFanQuery.addEventListener('click', queryHardwareState);
    
    inputCeiling.addEventListener('change', autoSaveConfig);
    inputFloor.addEventListener('change', autoSaveConfig);
});
