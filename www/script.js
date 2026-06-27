// DOM 元素声明
const tempDisplay = document.getElementById('temp-display');
const gaugeFill = document.getElementById('gauge-fill');
const tempSource = document.getElementById('temp-source');
const relayStatus = document.getElementById('relay-status');
const debounceBadge = document.getElementById('debounce-badge');
const connBadge = document.getElementById('conn-badge');

const btnModeAuto = document.getElementById('btn-mode-auto');
const btnModeManual = document.getElementById('btn-mode-manual');
const manualControlSection = document.getElementById('manual-control-section');

const btnFanOpen = document.getElementById('btn-fan-open');
const btnFanClose = document.getElementById('btn-fan-close');

// 新增配置弹窗相关元素
const btnOpenConfig = document.getElementById('btn-open-config');
const configModal = document.getElementById('config-modal');
const btnCloseModal = document.getElementById('btn-close-modal');
const btnCancelConfig = document.getElementById('btn-cancel-config');
const btnSaveConfig = document.getElementById('btn-save-config');
const formSystemConfig = document.getElementById('form-system-config');

const modalInputCeiling = document.getElementById('modal-input-ceiling');
const modalInputFloor = document.getElementById('modal-input-floor');
const modalInputMinRun = document.getElementById('modal-input-min-run');
const modalInputFilterWindow = document.getElementById('modal-input-filter-window');
const modalInputPollInterval = document.getElementById('modal-input-poll-interval');
const modalInputSerialPort = document.getElementById('modal-input-serial-port');
const modalInputBaudRate = document.getElementById('modal-input-baud-rate');
const modalInputTempPath = document.getElementById('modal-input-temp-path');
const modalInputHasFeedback = document.getElementById('modal-input-has-feedback');

// 继电器指令及状态绑定
const modalInputOpOpenNoFeed = document.getElementById('modal-input-op-open-nofeed');
const modalInputOpCloseNoFeed = document.getElementById('modal-input-op-close-nofeed');
const modalInputOpOpenFeed = document.getElementById('modal-input-op-open-feed');
const modalInputOpCloseFeed = document.getElementById('modal-input-op-close-feed');
const modalInputOpToggleFeed = document.getElementById('modal-input-op-toggle-feed');
const modalInputOpQuery = document.getElementById('modal-input-op-query');
const modalInputStatusOn = document.getElementById('modal-input-status-on');
const modalInputStatusOff = document.getElementById('modal-input-status-off');

const toastContainer = document.getElementById('toast-container');

let wasOffline = false; // 防止断联 toast 刷屏
let prevSerialExists = null;
let prevTempExists = null;

// 缓存配置状态
let currentConfig = {
    ceiling: null,
    floor: null,
    min_run_time: null,
    filter_window: null,
    poll_interval: null,
    serial_port: null,
    baud_rate: null,
    temp_path: null,
    has_feedback: null,
    op_close_nofeed: null,
    op_open_nofeed: null,
    op_close_feed: null,
    op_open_feed: null,
    op_toggle_feed: null,
    op_query: null,
    status_on: null,
    status_off: null
};

// ====================== Toast 消息弹窗 ======================
function showToast(message, type = 'info') {
    const toast = document.createElement('div');
    toast.className = `toast toast-${type}`;

    let prefix = '[INFO]';
    if (type === 'success') prefix = '[SUCCESS]';
    if (type === 'error') prefix = '[ERROR]';

    toast.innerHTML = `<span>${prefix}</span> <span>${message}</span>`;
    toastContainer.appendChild(toast);

    setTimeout(() => {
        toast.style.animation = 'toast-out 0.3s forwards cubic-bezier(0.18, 0.89, 0.32, 1.28)';
        toast.addEventListener('animationend', () => {
            toast.remove();
        });
    }, 3000);
}

// ====================== 仪表盘环形渲染 ======================
function updateGauge(temp, ceiling, floor) {
    const maxTemp = 100;
    const perimeter = 2 * Math.PI * 50;

    updateGaugeZones(ceiling, floor, maxTemp, perimeter);

    if (temp === null || isNaN(temp)) {
        tempDisplay.textContent = '--';
        gaugeFill.style.strokeDashoffset = perimeter;
        gaugeFill.style.stroke = 'url(#grad-cool)';
        gaugeFill.style.setProperty('--gauge-glow-color', 'rgba(6, 182, 212, 0.4)');
        gaugeFill.style.setProperty('--temp-text-color', 'var(--text-primary)');
        return;
    }

    tempDisplay.textContent = temp.toFixed(1);

    const percent = Math.min(Math.max(temp / maxTemp, 0), 1);
    const offset = perimeter * (1 - percent);
    gaugeFill.style.strokeDashoffset = offset;

    if (temp < 50) {
        gaugeFill.style.stroke = 'url(#grad-cool)';
        gaugeFill.style.setProperty('--gauge-glow-color', 'rgba(6, 182, 212, 0.45)');
        gaugeFill.style.setProperty('--temp-text-color', '#0284c7');
    } else if (temp < 65) {
        gaugeFill.style.stroke = 'url(#grad-warm)';
        gaugeFill.style.setProperty('--gauge-glow-color', 'rgba(245, 158, 11, 0.45)');
        gaugeFill.style.setProperty('--temp-text-color', '#d97706');
    } else {
        gaugeFill.style.stroke = 'url(#grad-hot)';
        gaugeFill.style.setProperty('--gauge-glow-color', 'rgba(239, 68, 68, 0.45)');
        gaugeFill.style.setProperty('--temp-text-color', '#dc2626');
    }
}

function updateGaugeZones(ceiling, floor, maxTemp, perimeter) {
    const zones = [
        { id: 'zone-lower', f1: 0, f2: floor, color: 'var(--color-green)' },
        { id: 'zone-mid', f1: floor, f2: ceiling, color: 'var(--color-warm)' },
        { id: 'zone-upper', f1: ceiling, f2: maxTemp, color: 'var(--color-hot)' },
    ];
    zones.forEach(({ id, f1, f2, color }) => {
        const el = document.getElementById(id);
        if (!el) return;
        if (f2 === null || f2 === undefined || isNaN(f2) || f1 === null || f1 === undefined || isNaN(f1) || f1 >= f2) {
            el.style.display = 'none';
            return;
        }
        el.style.display = '';
        const segLen = ((f2 - f1) / maxTemp) * perimeter;
        const offset = perimeter - (f1 / maxTemp) * perimeter;
        el.style.strokeDasharray = `${segLen} ${perimeter - segLen}`;
        el.style.strokeDashoffset = String(offset);
        el.style.stroke = color;
    });
}

// ====================== 动态条件显示 ======================
function toggleFeedbackUI() {
    const hasFeedback = modalInputHasFeedback.checked;
    const feedGroup = document.getElementById('feed-group');
    const nofeedGroup = document.getElementById('nofeed-group');

    if (hasFeedback) {
        feedGroup.classList.remove('config-group-hidden');
        nofeedGroup.classList.add('config-group-hidden');
    } else {
        feedGroup.classList.add('config-group-hidden');
        nofeedGroup.classList.remove('config-group-hidden');
    }
}

// ====================== 更新状态数据 ======================
function updateUI(data) {
    // 连接恢复时重置断联标志
    wasOffline = false;

    if (connBadge) {
        connBadge.textContent = '已连接';
        connBadge.className = 'badge badge-online';
    }

    // 1. 消抖状态角标显示逻辑
    let showDebounce = false;
    if (data.mode === 'auto') {
        if (!data.relay_state && data.over_ceiling_count > 0) {
            const pct = (data.over_ceiling_count / data.filter_window) * 100;
            debounceBadge.innerHTML = `<span>超限</span><div class="mini-progress-bar"><div class="mini-progress-fill" style="width: ${pct}%"></div></div>`;
            showDebounce = true;
        } else if (data.relay_state && data.under_floor_count > 0) {
            const pct = (data.under_floor_count / data.filter_window) * 100;
            debounceBadge.innerHTML = `<span>冷却</span><div class="mini-progress-bar"><div class="mini-progress-fill" style="width: ${pct}%"></div></div>`;
            showDebounce = true;
        }
    }
    debounceBadge.style.opacity = showDebounce ? '1' : '0';
    debounceBadge.style.pointerEvents = showDebounce ? 'auto' : 'none';

    // 2. 温度仪表盘
    updateGauge(data.temp, data.ceiling, data.floor);

    // 2.5 温度监测源
    if (data.temp_path) {
        if (data.temp_path_exists === false) {
            if (prevTempExists === true) {
                showToast("温度传感器丢失", 'error');
            }
            tempSource.textContent = '传感器不存在，请检查设置';
            tempSource.className = 'info-value text-error';
        } else {
            if (prevTempExists === false) {
                showToast("温度传感器已恢复", 'success');
            }
            const hwType = data.hardware_type || '';
            const hwName = data.hardware_name || '';
            if (hwName) {
                tempSource.textContent = `${hwType} [${hwName}]`;
            } else if (hwType) {
                tempSource.textContent = hwType;
            } else {
                tempSource.textContent = data.temp_path;
            }
            tempSource.className = 'info-value';
        }
        prevTempExists = data.temp_path_exists;
    } else {
        tempSource.textContent = '未设置 (仅手动)';
        tempSource.className = 'info-value';
    }

    // 3. 继电器状态（串口不可用时显示警告）
    if (data.serial_port_exists === false) {
        if (prevSerialExists === true) {
            showToast("串口设备断开", 'error');
        }
        relayStatus.textContent = '设备不存在，请检查设置';
        relayStatus.className = 'info-value text-error';
    } else {
        if (prevSerialExists === false) {
            showToast("串口设备已恢复", 'success');
        }
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
    }
    prevSerialExists = data.serial_port_exists;

    // 4. 控制模式与按钮激活状态
    if (data.mode === 'auto') {
        btnModeAuto.className = 'btn-mode active-auto';
        btnModeManual.className = 'btn-mode';

        manualControlSection.classList.add('card-disabled-overlay');
        btnFanOpen.disabled = true;
        btnFanClose.disabled = true;
    } else if (data.mode === 'manual') {
        btnModeAuto.className = 'btn-mode';
        btnModeManual.className = 'btn-mode active-manual';

        manualControlSection.classList.remove('card-disabled-overlay');
        btnFanOpen.disabled = false;
        btnFanClose.disabled = false;
    }

    // 缓存配置状态
    currentConfig = {
        ceiling: data.ceiling,
        floor: data.floor,
        min_run_time: data.min_run_time,
        filter_window: data.filter_window,
        poll_interval: data.poll_interval,
        serial_port: data.serial_port,
        baud_rate: data.baud_rate,
        temp_path: data.temp_path,
        has_feedback: data.has_feedback,
        op_close_nofeed: data.op_close_nofeed,
        op_open_nofeed: data.op_open_nofeed,
        op_close_feed: data.op_close_feed,
        op_open_feed: data.op_open_feed,
        op_toggle_feed: data.op_toggle_feed,
        op_query: data.op_query,
        status_on: data.status_on,
        status_off: data.status_off
    };
}

// 回填配置表单
function fillConfigForm() {
    modalInputCeiling.value = currentConfig.ceiling ?? '';
    modalInputFloor.value = currentConfig.floor ?? '';
    modalInputMinRun.value = currentConfig.min_run_time ?? '';
    modalInputFilterWindow.value = currentConfig.filter_window ?? '';
    modalInputPollInterval.value = currentConfig.poll_interval ?? '';

    // 回填串口
    if (currentConfig.serial_port) {
        // 清理失效选项
        Array.from(modalInputSerialPort.options).forEach(opt => {
            if (opt.textContent.includes('当前不可用')) {
                opt.remove();
            }
        });
        const exists = Array.from(modalInputSerialPort.options).some(opt => opt.value === currentConfig.serial_port);
        if (!exists) {
            const opt = document.createElement('option');
            opt.value = currentConfig.serial_port;
            opt.textContent = `${currentConfig.serial_port} (当前不可用)`;
            modalInputSerialPort.appendChild(opt);
        }
    }
    modalInputSerialPort.value = currentConfig.serial_port ?? '';

    modalInputBaudRate.value = currentConfig.baud_rate ?? '';

    // 回填温度传感器
    if (currentConfig.temp_path) {
        // 清理失效选项
        Array.from(modalInputTempPath.options).forEach(opt => {
            if (opt.textContent.includes('当前不可用')) {
                opt.remove();
            }
        });
        const exists = Array.from(modalInputTempPath.options).some(opt => opt.value === currentConfig.temp_path);
        if (!exists) {
            const opt = document.createElement('option');
            opt.value = currentConfig.temp_path;
            opt.textContent = `${currentConfig.temp_path} (当前不可用)`;
            modalInputTempPath.appendChild(opt);
        }
        modalInputTempPath.value = currentConfig.temp_path ?? '';
    }

    modalInputHasFeedback.checked = !!currentConfig.has_feedback;

    // 回填操作码
    modalInputOpOpenNoFeed.value = currentConfig.op_open_nofeed ?? '';
    modalInputOpCloseNoFeed.value = currentConfig.op_close_nofeed ?? '';
    modalInputOpOpenFeed.value = currentConfig.op_open_feed ?? '';
    modalInputOpCloseFeed.value = currentConfig.op_close_feed ?? '';
    modalInputOpToggleFeed.value = currentConfig.op_toggle_feed ?? '';
    modalInputOpQuery.value = currentConfig.op_query ?? '';
    modalInputStatusOn.value = currentConfig.status_on ?? '';
    modalInputStatusOff.value = currentConfig.status_off ?? '';
}

// 离线 UI 渲染
function handleOffline(error) {
    console.error("Fetch status failed:", error);
    if (!wasOffline) {
        wasOffline = true;
        showToast("与后端服务连接断开", 'error');
    }
    tempDisplay.textContent = '--';
    gaugeFill.style.strokeDashoffset = 2 * Math.PI * 50;
    ['zone-lower', 'zone-mid', 'zone-upper'].forEach(id => {
        const el = document.getElementById(id);
        if (el) el.style.display = 'none';
    });
    tempSource.textContent = '加载失败';
    tempSource.className = 'info-value text-unknown';
    relayStatus.textContent = '读取失败';
    relayStatus.className = 'info-value text-unknown';
    debounceBadge.style.opacity = '0';
    debounceBadge.style.pointerEvents = 'none';
    if (connBadge) {
        connBadge.textContent = '已断开';
        connBadge.className = 'badge badge-offline';
    }
}

// ====================== API 交互请求 ======================

let ws = null;
let reconnectDelay = 1000;

function connectWebSocket() {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const host = window.location.host;
    const path = window.location.pathname.replace(/\/$/, '');
    const wsUrl = `${protocol}//${host}${path}/api/ws`;

    ws = new WebSocket(wsUrl);

    ws.onopen = () => {
        console.log("WebSocket connected to:", wsUrl);
        reconnectDelay = 1000;
        wasOffline = false;
        if (connBadge) {
            connBadge.textContent = '连接中';
            connBadge.className = 'badge badge-debounce';
        }
    };

    ws.onmessage = (event) => {
        try {
            const data = JSON.parse(event.data);
            updateUI(data);
        } catch (err) {
            console.error("解析 WebSocket 数据失败:", err);
        }
    };

    ws.onclose = (e) => {
        console.log("WebSocket connection closed, reconnecting...", e);
        handleOffline(e);
        setTimeout(() => {
            connectWebSocket();
        }, reconnectDelay);
        reconnectDelay = Math.min(reconnectDelay * 2, 10000);
    };

    ws.onerror = (err) => {
        console.error("WebSocket error:", err);
        ws.close();
    };
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
    const actionName = state ? '开启继电器' : '关闭继电器';
    try {
        const response = await fetch(url, { method: 'POST' });
        const res = await response.json();
        if (res.success) {
            showToast(res.message, 'success');
        } else {
            showToast(res.message, 'error');
        }
    } catch (err) {
        showToast(`${actionName}失败，请重试`, 'error');
    }
}


// ====================== 保存配置 ======================
async function saveConfig() {
    const ceiling = parseFloat(modalInputCeiling.value);
    const floor = parseFloat(modalInputFloor.value);
    const min_run_time = parseInt(modalInputMinRun.value);
    const filter_window = parseInt(modalInputFilterWindow.value);
    const poll_interval = parseInt(modalInputPollInterval.value);
    const serial_port = modalInputSerialPort.value.trim();
    const baud_rate = parseInt(modalInputBaudRate.value);
    const temp_path = modalInputTempPath.value.trim();
    const has_feedback = modalInputHasFeedback.checked;

    // 基础校验
    if (isNaN(ceiling) || isNaN(floor) || isNaN(min_run_time) || isNaN(filter_window) ||
        isNaN(poll_interval) || isNaN(baud_rate) || !serial_port) {
        showToast("请填写完整的参数配置", 'error');
        return;
    }

    if (ceiling <= floor) {
        showToast("温度上限必须大于温度下限", 'error');
        return;
    }

    if (min_run_time < 5) {
        showToast("最少运行时间必须大于等于 5 秒", 'error');
        return;
    }

    if (filter_window < 1 || filter_window > 10) {
        showToast("消抖判定次数必须在 1 至 10 之间", 'error');
        return;
    }

    if (poll_interval < 1 || poll_interval > 60) {
        showToast("采集间隔必须在 1 至 60 秒之间", 'error');
        return;
    }

    // 提取协议参数，未启用项使用缓存数据填充
    let op_open_nofeed = "";
    let op_close_nofeed = "";
    let op_open_feed = "";
    let op_close_feed = "";
    let op_toggle_feed = "";
    let op_query = "";
    let status_on = "";
    let status_off = "";

    if (has_feedback) {
        op_open_feed = modalInputOpOpenFeed.value.trim();
        op_close_feed = modalInputOpCloseFeed.value.trim();
        op_toggle_feed = modalInputOpToggleFeed.value.trim();
        op_query = modalInputOpQuery.value.trim();
        status_on = modalInputStatusOn.value.trim();
        status_off = modalInputStatusOff.value.trim();

        // 填充无反馈默认值
        op_open_nofeed = currentConfig.op_open_nofeed ?? "";
        op_close_nofeed = currentConfig.op_close_nofeed ?? "";

        if (!op_open_feed || !op_close_feed || !op_toggle_feed || !op_query || !status_on || !status_off) {
            showToast("请填写完整的有反馈继电器控制指令", 'error');
            return;
        }
    } else {
        op_open_nofeed = modalInputOpOpenNoFeed.value.trim();
        op_close_nofeed = modalInputOpCloseNoFeed.value.trim();

        // 填充有反馈默认值
        op_open_feed = currentConfig.op_open_feed ?? "";
        op_close_feed = currentConfig.op_close_feed ?? "";
        op_toggle_feed = currentConfig.op_toggle_feed ?? "";
        op_query = currentConfig.op_query ?? "";
        status_on = currentConfig.status_on ?? "";
        status_off = currentConfig.status_off ?? "";

        if (!op_open_nofeed || !op_close_nofeed) {
            showToast("请填写完整的无反馈继电器控制指令", 'error');
            return;
        }
    }

    try {
        const response = await fetch('api/set_config', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                ceiling,
                floor,
                min_run_time,
                filter_window,
                poll_interval,
                serial_port,
                baud_rate,
                temp_path,
                has_feedback,
                op_open_nofeed,
                op_close_nofeed,
                op_open_feed,
                op_close_feed,
                op_toggle_feed,
                op_query,
                status_on,
                status_off
            })
        });
        const res = await response.json();
        if (res.success) {
            showToast("系统配置保存并应用成功", 'success');
            closeModal();
        } else {
            showToast(res.message, 'error');
        }
    } catch (err) {
        showToast("保存配置请求失败，请检查网络", 'error');
    }
}

async function openModal() {
    await loadHardwareOptions();
    fillConfigForm();
    toggleFeedbackUI();
    configModal.classList.add('active');
}

function closeModal() {
    configModal.classList.remove('active');
}


function translateSerialName(name, path) {
    const lower = name.toLowerCase();
    let label = name;
    if (lower.includes('1a86') || lower.includes('ch34')) {
        label = 'USB继电器(CH340)';
    } else if (lower.includes('cp210')) {
        label = 'USB串口(CP210X)';
    } else if (lower.includes('ftdi') || lower.includes('ft232')) {
        label = 'USB串口(FTDI)';
    } else if (lower.includes('pl2303')) {
        label = 'USB串口(PL2303)';
    } else if (lower.includes('uart') || lower.includes('ttys')) {
        label = '板载串口(UART)';
    }

    if (path.includes('by-id')) {
        return `${label} [固定]`;
    }
    return `${label} [动态]`;
}

function formatTempSensorDisplay(s) {
    const temp = s.current_temp.toFixed(1);
    const hwType = s.hardware_type || '';
    const hwName = s.hardware_name || '';
    if (hwName) {
        return `${hwType} [${hwName}] (${temp}℃)`;
    }
    return `${hwType} (${temp}℃)`;
}

// ====================== 硬件检测加载逻辑 ======================
async function loadHardwareOptions() {
    const loadPorts = async () => {
        try {
            const serialRes = await fetch('api/list_serial_ports');
            if (serialRes.ok) {
                const ports = await serialRes.json();
                if (modalInputSerialPort) {
                    modalInputSerialPort.innerHTML = '';
                    ports.forEach(p => {
                        const opt = document.createElement('option');
                        const friendlyName = translateSerialName(p.name, p.path);
                        opt.value = p.path;
                        opt.textContent = friendlyName;
                        modalInputSerialPort.appendChild(opt);
                    });
                }
            }
        } catch (err) {
            console.error("加载串口列表失败:", err);
        }
    };

    const loadSensors = async () => {
        try {
            const tempRes = await fetch('api/list_temp_sensors');
            if (tempRes.ok) {
                const sensors = await tempRes.json();
                // 自定义优雅排序：CPU > 硬盘 > 其他中文首字母
                sensors.sort((a, b) => {
                    const typeA = a.hardware_type || '';
                    const typeB = b.hardware_type || '';
                    const isCpuA = typeA === 'CPU';
                    const isCpuB = typeB === 'CPU';
                    if (isCpuA && !isCpuB) return -1;
                    if (!isCpuA && isCpuB) return 1;

                    const isHddA = typeA === '硬盘';
                    const isHddB = typeB === '硬盘';
                    if (isHddA && !isHddB) return -1;
                    if (!isHddA && isHddB) return 1;

                    return typeA.localeCompare(typeB, 'zh');
                });

                if (modalInputTempPath) {
                    modalInputTempPath.innerHTML = '';
                    sensors.forEach(s => {
                        const opt = document.createElement('option');
                        const labelText = formatTempSensorDisplay(s);
                        opt.value = s.type;
                        opt.textContent = labelText;
                        modalInputTempPath.appendChild(opt);
                    });
                }
            }
        } catch (err) {
            console.error("加载温度传感器列表失败:", err);
        }
    };

    await Promise.all([loadPorts(), loadSensors()]);
}

// ====================== 事件绑定与生命周期 ======================
document.addEventListener('DOMContentLoaded', () => {
    // 建立 WebSocket 订阅
    connectWebSocket();

    // 按钮动作监听
    btnModeAuto.addEventListener('click', () => setMode('auto'));
    btnModeManual.addEventListener('click', () => setMode('manual'));

    btnFanOpen.addEventListener('click', () => controlFan(true));
    btnFanClose.addEventListener('click', () => controlFan(false));

    // 弹窗相关事件监听
    btnOpenConfig.addEventListener('click', openModal);
    btnCloseModal.addEventListener('click', closeModal);
    btnCancelConfig.addEventListener('click', closeModal);
    btnSaveConfig.addEventListener('click', saveConfig);

    // 滑块变化联动事件
    modalInputHasFeedback.addEventListener('change', toggleFeedbackUI);

    // 点击遮罩外部关闭弹窗
    configModal.addEventListener('click', (e) => {
        if (e.target === configModal) closeModal();
    });
});
