package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tarm/serial"
)

// 配置
var (
	serialPort       = "/dev/ttyUSB0"
	baudRate         = 9600
	thresholdCeiling = 55.0
	thresholdFloor   = 50.0
	pollInterval     = 3
	customTempPath   = "/sys/class/thermal/thermal_zone1/temp"
	hasFeedback      = true
	minRunTime       = 30
	filterWindow     = 3

	gatewayPrefix = "/app/yuexps-usb-fan"
	socketName    = "usb_fan.sock"

	// 操作码与状态值
	opCloseNoFeed = []byte{0xA0, 0x01, 0x00, 0xA1} // 关无反馈
	opOpenNoFeed  = []byte{0xA0, 0x01, 0x01, 0xA2} // 开无反馈
	opCloseFeed   = []byte{0xA0, 0x01, 0x02, 0xA3} // 关有反馈
	opOpenFeed    = []byte{0xA0, 0x01, 0x03, 0xA4} // 开有反馈
	opToggleFeed  = []byte{0xA0, 0x01, 0x04, 0xA5} // 状态取反有反馈
	opQuery       = []byte{0xA0, 0x01, 0x05, 0xA6} // 查询状态有反馈

	statusOn  = []byte{0xA0, 0x01, 0x01, 0xA2} // 反馈开启状态
	statusOff = []byte{0xA0, 0x01, 0x00, 0xA1} // 反馈关闭状态
)

func parseHexBytes(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", " ")
	s = strings.ReplaceAll(s, ";", " ")
	tokens := strings.Fields(s)

	if len(tokens) == 1 && len(tokens[0]) > 2 && !strings.HasPrefix(strings.ToLower(tokens[0]), "0x") {
		var newTokens []string
		cleaned := tokens[0]
		for i := 0; i < len(cleaned); i += 2 {
			if i+2 <= len(cleaned) {
				newTokens = append(newTokens, cleaned[i:i+2])
			} else {
				newTokens = append(newTokens, cleaned[i:])
			}
		}
		tokens = newTokens
	}

	var res []byte
	for _, tok := range tokens {
		tok = strings.TrimPrefix(tok, "0x")
		tok = strings.TrimPrefix(tok, "0X")
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		val, err := strconv.ParseUint(tok, 16, 8)
		if err != nil {
			return nil, err
		}
		res = append(res, byte(val))
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("empty byte sequence")
	}
	return res, nil
}

func toHexBytesStr(b []byte) string {
	var parts []string
	for _, v := range b {
		parts = append(parts, fmt.Sprintf("%02X", v))
	}
	return strings.Join(parts, "")
}

func bytesContains(a, b []byte) bool {
	if len(b) == 0 {
		return false
	}
	if len(a) < len(b) {
		return false
	}
	for i := 0; i <= len(a)-len(b); i++ {
		match := true
		for j := 0; j < len(b); j++ {
			if a[i+j] != b[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// 全局状态
var (
	ser              *serial.Port
	fanState         bool
	controlMode      = "auto"
	lastRelayHwState *bool
	lastTurnOnTime   time.Time
	tempHistory      []float64
	mu               sync.Mutex
	serialMu         sync.Mutex
)

// 内嵌静态网页资源
//go:embed www/*
var webFS embed.FS

// 全局路径
var (
	configPath = "config.json"
	sockPath   = socketName
)

func initPaths() {
	if pkgVar := os.Getenv("TRIM_PKGVAR"); pkgVar != "" {
		configPath = filepath.Join(pkgVar, "config.json")
	}
	if appDest := os.Getenv("TRIM_APPDEST"); appDest != "" {
		sockPath = filepath.Join(appDest, socketName)
	}
}

// 写日志
func writeLog(msg string) {
	t := time.Now().Format("2006-01-02 15:04:05")
	fmt.Printf("[%s] %s\n", t, msg)
}

// 串口初始化
func initSerial() bool {
	serialMu.Lock()
	defer serialMu.Unlock()

	if ser != nil {
		_ = ser.Close()
		ser = nil
	}

	c := &serial.Config{
		Name:        serialPort,
		Baud:        baudRate,
		ReadTimeout: 300 * time.Millisecond,
	}
	s, err := serial.OpenPort(c)
	if err != nil {
		writeLog(fmt.Sprintf("串口打开失败：%v", err))
		return false
	}
	ser = s
	writeLog(fmt.Sprintf("串口 %s 初始化成功", serialPort))
	return true
}

// 带反馈开关继电器
func setFan(state bool) bool {
	var s *serial.Port
	serialMu.Lock()
	s = ser
	serialMu.Unlock()

	if s == nil {
		if !initSerial() {
			return false
		}
	}

	mu.Lock()
	hasFeed := hasFeedback
	var cmd []byte
	var expectStatus []byte
	if hasFeed {
		if state {
			cmd = opOpenFeed
			expectStatus = statusOn
		} else {
			cmd = opCloseFeed
			expectStatus = statusOff
		}
	} else {
		if state {
			cmd = opOpenNoFeed
		} else {
			cmd = opCloseNoFeed
		}
	}
	mu.Unlock()

	sendCmd := make([]byte, len(cmd))
	copy(sendCmd, cmd)

	serialMu.Lock()
	defer serialMu.Unlock()

	if ser == nil {
		return false
	}

	_, err := ser.Write(sendCmd)
	if err != nil {
		writeLog(fmt.Sprintf("发送指令异常：%v", err))
		go initSerial()
		return false
	}

	if !hasFeed {
		mu.Lock()
		if fanState != state {
			fanState = state
			tempHistory = nil
		}
		mu.Unlock()
		writeLog(fmt.Sprintf("无反馈指令下发成功，指令：%X", sendCmd))
		return true
	}

	time.Sleep(200 * time.Millisecond)

	resp := make([]byte, 32)
	n, err := ser.Read(resp)
	if err != nil || n == 0 {
		writeLog(fmt.Sprintf("下发 %X 无反馈帧 (收到: %d 字节, err: %v)", sendCmd, n, err))
		return false
	}

	actualResp := resp[:n]
	if bytesContains(actualResp, expectStatus) {
		mu.Lock()
		if fanState != state {
			fanState = state
			tempHistory = nil
		}
		mu.Unlock()
		writeLog(fmt.Sprintf("带反馈执行成功，指令：%X，回执：%X", sendCmd, actualResp))
		return true
	} else {
		writeLog(fmt.Sprintf("执行失败，预期回执中包含：%X，实际收到：%X", expectStatus, actualResp))
		return false
	}
}

// 查询硬件状态
func getHardwareRelayState() *bool {
	mu.Lock()
	hasFeed := hasFeedback
	cachedState := fanState
	mu.Unlock()

	if !hasFeed {
		return &cachedState
	}

	var s *serial.Port
	serialMu.Lock()
	s = ser
	serialMu.Unlock()

	if s == nil {
		if !initSerial() {
			return nil
		}
	}

	serialMu.Lock()
	defer serialMu.Unlock()

	if ser == nil {
		return nil
	}

	mu.Lock()
	cmd := opQuery
	stOn := statusOn
	stOff := statusOff
	mu.Unlock()

	queryCmd := make([]byte, len(cmd))
	copy(queryCmd, cmd)

	_, err := ser.Write(queryCmd)
	if err != nil {
		writeLog(fmt.Sprintf("查询状态发送指令出错：%v", err))
		go initSerial()
		return nil
	}

	time.Sleep(200 * time.Millisecond)

	resp := make([]byte, 32)
	n, err := ser.Read(resp)
	if err != nil || n == 0 {
		writeLog("查询状态返回无效数据或超时")
		return nil
	}

	actualResp := resp[:n]
	if bytesContains(actualResp, stOn) {
		res := true
		mu.Lock()
		lastRelayHwState = &res
		fanState = true
		mu.Unlock()
		return &res
	} else if bytesContains(actualResp, stOff) {
		res := false
		mu.Lock()
		lastRelayHwState = &res
		fanState = false
		mu.Unlock()
		return &res
	} else {
		writeLog(fmt.Sprintf("查询返回状态未匹配已知状态，收到：%X", actualResp))
		return nil
	}
}

// 获取温度值
func getTemp() *float64 {
	mu.Lock()
	path := customTempPath
	mu.Unlock()

	if path == "" || !fileExists(path) {
		return nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		writeLog(fmt.Sprintf("读取温度失败：%v", err))
		return nil
	}

	raw, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil {
		writeLog(fmt.Sprintf("解析温度失败：%v", err))
		return nil
	}

	temp := float64(raw) / 1000.0
	return &temp
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

// 配置文件管理
type AppConfig struct {
	Ceiling       float64 `json:"ceiling"`
	Floor         float64 `json:"floor"`
	Mode          string  `json:"mode"`
	SerialPort    string  `json:"serial_port"`
	BaudRate      int     `json:"baud_rate"`
	PollInterval  int     `json:"poll_interval"`
	TempPath      string  `json:"temp_path"`
	HasFeedback   bool    `json:"has_feedback"`
	MinRunTime    int     `json:"min_run_time"`
	FilterWindow  int     `json:"filter_window"`
	OpCloseNoFeed string  `json:"op_close_nofeed"`
	OpOpenNoFeed  string  `json:"op_open_nofeed"`
	OpCloseFeed   string  `json:"op_close_feed"`
	OpOpenFeed    string  `json:"op_open_feed"`
	OpToggleFeed  string  `json:"op_toggle_feed"`
	OpQuery       string  `json:"op_query"`
	StatusOn      string  `json:"status_on"`
	StatusOff     string  `json:"status_off"`
}

func loadConfig() {
	if !fileExists(configPath) {
		saveConfig(AppConfig{
			Ceiling:       55.0,
			Floor:         50.0,
			Mode:          "auto",
			SerialPort:    serialPort,
			BaudRate:      baudRate,
			PollInterval:  pollInterval,
			TempPath:      customTempPath,
			HasFeedback:   hasFeedback,
			MinRunTime:    30,
			FilterWindow:  3,
			OpCloseNoFeed: toHexBytesStr(opCloseNoFeed),
			OpOpenNoFeed:  toHexBytesStr(opOpenNoFeed),
			OpCloseFeed:   toHexBytesStr(opCloseFeed),
			OpOpenFeed:    toHexBytesStr(opOpenFeed),
			OpToggleFeed:  toHexBytesStr(opToggleFeed),
			OpQuery:       toHexBytesStr(opQuery),
			StatusOn:      toHexBytesStr(statusOn),
			StatusOff:     toHexBytesStr(statusOff),
		})
		return
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		writeLog(fmt.Sprintf("读取配置文件失败：%v", err))
		return
	}

	var cfg AppConfig
	err = json.Unmarshal(data, &cfg)
	if err != nil {
		writeLog(fmt.Sprintf("解析配置文件失败：%v", err))
		return
	}

	mu.Lock()
	thresholdCeiling = cfg.Ceiling
	thresholdFloor = cfg.Floor
	serialPort = cfg.SerialPort
	baudRate = cfg.BaudRate
	pollInterval = cfg.PollInterval
	customTempPath = cfg.TempPath
	if customTempPath == "" {
		controlMode = "manual"
	} else {
		controlMode = cfg.Mode
	}
	hasFeedback = cfg.HasFeedback
	minRunTime = cfg.MinRunTime
	filterWindow = cfg.FilterWindow
	opCloseNoFeed, _ = parseHexBytes(cfg.OpCloseNoFeed)
	opOpenNoFeed, _ = parseHexBytes(cfg.OpOpenNoFeed)
	opCloseFeed, _ = parseHexBytes(cfg.OpCloseFeed)
	opOpenFeed, _ = parseHexBytes(cfg.OpOpenFeed)
	opToggleFeed, _ = parseHexBytes(cfg.OpToggleFeed)
	opQuery, _ = parseHexBytes(cfg.OpQuery)
	statusOn, _ = parseHexBytes(cfg.StatusOn)
	statusOff, _ = parseHexBytes(cfg.StatusOff)
	mu.Unlock()
	writeLog("配置文件加载成功")
}

func saveConfig(cfg AppConfig) {
	parentDir := filepath.Dir(configPath)
	if parentDir != "." && parentDir != "/" {
		_ = os.MkdirAll(parentDir, 0755)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		writeLog(fmt.Sprintf("序列化配置失败：%v", err))
		return
	}
	err = os.WriteFile(configPath, data, 0644)
	if err != nil {
		writeLog(fmt.Sprintf("写入配置文件失败：%v", err))
	}
}

func persistCurrentConfig() {
	mu.Lock()
	cfg := AppConfig{
		Ceiling:       thresholdCeiling,
		Floor:         thresholdFloor,
		Mode:          controlMode,
		SerialPort:    serialPort,
		BaudRate:      baudRate,
		PollInterval:  pollInterval,
		TempPath:      customTempPath,
		HasFeedback:   hasFeedback,
		MinRunTime:    minRunTime,
		FilterWindow:  filterWindow,
		OpCloseNoFeed: toHexBytesStr(opCloseNoFeed),
		OpOpenNoFeed:  toHexBytesStr(opOpenNoFeed),
		OpCloseFeed:   toHexBytesStr(opCloseFeed),
		OpOpenFeed:    toHexBytesStr(opOpenFeed),
		OpToggleFeed:  toHexBytesStr(opToggleFeed),
		OpQuery:       toHexBytesStr(opQuery),
		StatusOn:      toHexBytesStr(statusOn),
		StatusOff:     toHexBytesStr(statusOff),
	}
	mu.Unlock()
	saveConfig(cfg)
}

// 温控后台循环
func autoTempLoop() {
	_ = initSerial()
	_ = setFan(false)
	writeLog("自动温控后台启动")
	for {
		mu.Lock()
		mode := controlMode
		ceiling := thresholdCeiling
		floor := thresholdFloor
		state := fanState
		onTime := lastTurnOnTime
		minRun := minRunTime
		win := filterWindow
		mu.Unlock()

		if mode == "auto" {
			tVal := getTemp()
			if tVal != nil {
				t := *tVal

				mu.Lock()
				tempHistory = append(tempHistory, t)
				if len(tempHistory) > win {
					tempHistory = tempHistory[len(tempHistory)-win:]
				}
				sum := 0.0
				for _, v := range tempHistory {
					sum += v
				}
				tAvg := sum / float64(len(tempHistory))
				mu.Unlock()

				if (tAvg >= ceiling || t >= ceiling+5.0) && !state {
					reason := fmt.Sprintf("平均温度 %.2f℃ 超过上限 %.2f℃", tAvg, ceiling)
					if t >= ceiling+5.0 && tAvg < ceiling {
						reason = fmt.Sprintf("瞬时温度 %.2f℃ 触发紧急上限 (%.2f℃+5℃)", t, ceiling)
					}
					writeLog(reason + "，自动开启继电器")
					_ = setFan(true)
				} else if tAvg <= floor && state {
					elapsed := time.Since(onTime)
					if elapsed >= time.Duration(minRun)*time.Second {
						writeLog(fmt.Sprintf("平均温度 %.2f℃ 低于下限 %.2f℃，且已运行 %.1f 秒，自动关闭继电器", tAvg, floor, elapsed.Seconds()))
						_ = setFan(false)
					} else {
						writeLog(fmt.Sprintf("平均温度 %.2f℃ 已低于下限 %.2f℃，因未达到最小运行时间（%d 秒，已运行 %.1f 秒）保持开启", tAvg, floor, minRun, elapsed.Seconds()))
					}
				}
			}
		}
		mu.Lock()
		sleepSec := pollInterval
		mu.Unlock()
		if sleepSec <= 0 {
			sleepSec = 3
		}
		time.Sleep(time.Duration(sleepSec) * time.Second)
	}
}

// Web 处理器
type StatusResponse struct {
	Temp          *float64 `json:"temp"`
	RelayState    *bool    `json:"relay_state"`
	Mode          string   `json:"mode"`
	Ceiling       float64  `json:"ceiling"`
	Floor         float64  `json:"floor"`
	MinRunTime    int      `json:"min_run_time"`
	FilterWindow  int      `json:"filter_window"`
	SerialPort    string   `json:"serial_port"`
	BaudRate      int      `json:"baud_rate"`
	PollInterval  int      `json:"poll_interval"`
	TempPath      string   `json:"temp_path"`
	HasFeedback   bool     `json:"has_feedback"`
	OpCloseNoFeed string   `json:"op_close_nofeed"`
	OpOpenNoFeed  string   `json:"op_open_nofeed"`
	OpCloseFeed   string   `json:"op_close_feed"`
	OpOpenFeed    string   `json:"op_open_feed"`
	OpToggleFeed  string   `json:"op_toggle_feed"`
	OpQuery       string   `json:"op_query"`
	StatusOn      string   `json:"status_on"`
	StatusOff     string   `json:"status_off"`
}

type BasicResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type QueryResponse struct {
	Success    bool   `json:"success"`
	RelayState *bool  `json:"relay_state"`
	Message    string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, BasicResponse{Success: false, Message: "Method not allowed"})
		return
	}
	t := getTemp()
	relaySt := getHardwareRelayState()

	mu.Lock()
	res := StatusResponse{
		Temp:          t,
		RelayState:    relaySt,
		Mode:          controlMode,
		Ceiling:       thresholdCeiling,
		Floor:         thresholdFloor,
		MinRunTime:    minRunTime,
		FilterWindow:  filterWindow,
		SerialPort:    serialPort,
		BaudRate:      baudRate,
		PollInterval:  pollInterval,
		TempPath:      customTempPath,
		HasFeedback:   hasFeedback,
		OpCloseNoFeed: toHexBytesStr(opCloseNoFeed),
		OpOpenNoFeed:  toHexBytesStr(opOpenNoFeed),
		OpCloseFeed:   toHexBytesStr(opCloseFeed),
		OpOpenFeed:    toHexBytesStr(opOpenFeed),
		OpToggleFeed:  toHexBytesStr(opToggleFeed),
		OpQuery:       toHexBytesStr(opQuery),
		StatusOn:      toHexBytesStr(statusOn),
		StatusOff:     toHexBytesStr(statusOff),
	}
	mu.Unlock()
	writeJSON(w, http.StatusOK, res)
}

func handleOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, BasicResponse{Success: false, Message: "Method not allowed"})
		return
	}
	res := setFan(true)
	writeLog(fmt.Sprintf("API手动开启继电器，执行结果：%t", res))
	msg := "手动开启继电器成功"
	if !res {
		msg = "手动开启继电器失败"
	}
	writeJSON(w, http.StatusOK, BasicResponse{Success: res, Message: msg})
}

func handleClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, BasicResponse{Success: false, Message: "Method not allowed"})
		return
	}
	res := setFan(false)
	writeLog(fmt.Sprintf("API手动关闭继电器，执行结果：%t", res))
	msg := "手动关闭继电器成功"
	if !res {
		msg = "手动关闭继电器失败"
	}
	writeJSON(w, http.StatusOK, BasicResponse{Success: res, Message: msg})
}

func handleSetMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, BasicResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, BasicResponse{Success: false, Message: "Invalid JSON request"})
		return
	}
	if req.Mode != "auto" && req.Mode != "manual" {
		writeJSON(w, http.StatusBadRequest, BasicResponse{Success: false, Message: "无效的控制模式"})
		return
	}

	mu.Lock()
	if req.Mode == "auto" && customTempPath == "" {
		mu.Unlock()
		writeJSON(w, http.StatusOK, BasicResponse{Success: false, Message: "未配置自定义温度文件路径，无法启用自动温控"})
		return
	}
	controlMode = req.Mode
	mu.Unlock()

	persistCurrentConfig()

	writeLog(fmt.Sprintf("API切换控制模式为：%s", req.Mode))
	modeName := "手动控制"
	if req.Mode == "auto" {
		modeName = "自动温控"
	}
	writeJSON(w, http.StatusOK, BasicResponse{Success: true, Message: fmt.Sprintf("成功切换控制模式为：%s", modeName)})
}

func handleSetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, BasicResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var req struct {
		Ceiling       float64 `json:"ceiling"`
		Floor         float64 `json:"floor"`
		MinRunTime    int     `json:"min_run_time"`
		FilterWindow  int     `json:"filter_window"`
		SerialPort    string  `json:"serial_port"`
		BaudRate      int     `json:"baud_rate"`
		PollInterval  int     `json:"poll_interval"`
		TempPath      string  `json:"temp_path"`
		HasFeedback   bool    `json:"has_feedback"`
		OpCloseNoFeed string  `json:"op_close_nofeed"`
		OpOpenNoFeed  string  `json:"op_open_nofeed"`
		OpCloseFeed   string  `json:"op_close_feed"`
		OpOpenFeed    string  `json:"op_open_feed"`
		OpToggleFeed  string  `json:"op_toggle_feed"`
		OpQuery       string  `json:"op_query"`
		StatusOn      string  `json:"status_on"`
		StatusOff     string  `json:"status_off"`
	}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, BasicResponse{Success: false, Message: "Invalid JSON request"})
		return
	}

	if req.Ceiling <= req.Floor {
		writeLog("API更新配置失败：温度上限必须大于下限")
		writeJSON(w, http.StatusOK, BasicResponse{Success: false, Message: "温度上限必须大于下限"})
		return
	}
	if req.MinRunTime < 5 {
		writeJSON(w, http.StatusOK, BasicResponse{Success: false, Message: "最少运行时间必须大于等于5秒"})
		return
	}
	if req.FilterWindow < 1 || req.FilterWindow > 10 {
		writeJSON(w, http.StatusOK, BasicResponse{Success: false, Message: "平滑窗口大小必须在1至10之间"})
		return
	}

	var parsedCloseNoFeed, parsedOpenNoFeed, parsedCloseFeed, parsedOpenFeed []byte
	var parsedToggleFeed, parsedQuery, parsedStatusOn, parsedStatusOff []byte

	if parsedCloseNoFeed, err = parseHexBytes(req.OpCloseNoFeed); err != nil {
		writeJSON(w, http.StatusOK, BasicResponse{Success: false, Message: "无效的关无反馈指令码"})
		return
	}
	if parsedOpenNoFeed, err = parseHexBytes(req.OpOpenNoFeed); err != nil {
		writeJSON(w, http.StatusOK, BasicResponse{Success: false, Message: "无效的开无反馈指令码"})
		return
	}
	if parsedCloseFeed, err = parseHexBytes(req.OpCloseFeed); err != nil {
		writeJSON(w, http.StatusOK, BasicResponse{Success: false, Message: "无效的关反馈指令码"})
		return
	}
	if parsedOpenFeed, err = parseHexBytes(req.OpOpenFeed); err != nil {
		writeJSON(w, http.StatusOK, BasicResponse{Success: false, Message: "无效的开反馈指令码"})
		return
	}
	if parsedToggleFeed, err = parseHexBytes(req.OpToggleFeed); err != nil {
		writeJSON(w, http.StatusOK, BasicResponse{Success: false, Message: "无效的取反反馈指令码"})
		return
	}
	if parsedQuery, err = parseHexBytes(req.OpQuery); err != nil {
		writeJSON(w, http.StatusOK, BasicResponse{Success: false, Message: "无效的查询指令码"})
		return
	}
	if parsedStatusOn, err = parseHexBytes(req.StatusOn); err != nil {
		writeJSON(w, http.StatusOK, BasicResponse{Success: false, Message: "无效的开启状态码"})
		return
	}
	if parsedStatusOff, err = parseHexBytes(req.StatusOff); err != nil {
		writeJSON(w, http.StatusOK, BasicResponse{Success: false, Message: "无效的关闭状态码"})
		return
	}

	mu.Lock()
	oldPort := serialPort
	oldBaud := baudRate

	thresholdCeiling = req.Ceiling
	thresholdFloor = req.Floor
	minRunTime = req.MinRunTime
	filterWindow = req.FilterWindow
	serialPort = req.SerialPort
	baudRate = req.BaudRate
	pollInterval = req.PollInterval
	customTempPath = req.TempPath
	if customTempPath == "" {
		controlMode = "manual"
	}
	hasFeedback = req.HasFeedback
	
	opCloseNoFeed = parsedCloseNoFeed
	opOpenNoFeed = parsedOpenNoFeed
	opCloseFeed = parsedCloseFeed
	opOpenFeed = parsedOpenFeed
	opToggleFeed = parsedToggleFeed
	opQuery = parsedQuery
	statusOn = parsedStatusOn
	statusOff = parsedStatusOff
	
	tempHistory = nil
	mu.Unlock()

	persistCurrentConfig()

	writeLog(fmt.Sprintf("API更新配置成功：上限 %.2f℃，下限 %.2f℃，防抖最少时间 %d秒，平滑窗口 %d次，串口 %s", 
		req.Ceiling, req.Floor, req.MinRunTime, req.FilterWindow, req.SerialPort))

	if oldPort != req.SerialPort || oldBaud != req.BaudRate {
		writeLog("检测到串口或波特率变更，重新初始化串口")
		go initSerial()
	}

	writeJSON(w, http.StatusOK, BasicResponse{Success: true, Message: "配置保存并应用成功"})
}

func handleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, BasicResponse{Success: false, Message: "Method not allowed"})
		return
	}
	writeLog("API触发手动查询继电器状态")
	relaySt := getHardwareRelayState()
	success := (relaySt != nil)
	msg := "查询成功"
	if !success {
		msg = "查询失败"
	}
	writeJSON(w, http.StatusOK, QueryResponse{Success: success, RelayState: relaySt, Message: msg})
}

// 启动 Web 服务
func startWebServer() {
	mux := http.NewServeMux()
	prefix := gatewayPrefix

	// 批量挂载 API
	apiRoutes := map[string]http.HandlerFunc{
		"/api/status":     handleStatus,
		"/api/open":       handleOpen,
		"/api/close":      handleClose,
		"/api/set_mode":   handleSetMode,
		"/api/set_config": handleSetConfig,
		"/api/query":      handleQuery,
	}
	for path, handler := range apiRoutes {
		mux.HandleFunc(path, handler)
		mux.HandleFunc(prefix+path, handler)
	}

	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
	})

	// 剥离子目录前缀挂载内嵌文件系统
	subFS, err := fs.Sub(webFS, "www")
	if err != nil {
		log.Fatalf("内嵌网页系统挂载失败: %v", err)
	}
	fileServer := http.FileServer(http.FS(subFS))

	mux.Handle("/", fileServer)
	mux.Handle(prefix+"/", http.StripPrefix(prefix+"/", fileServer))

	writeLog(fmt.Sprintf("Web服务准备监听 UNIX Domain Socket: %s", sockPath))

	// 捕获信号以清理 Socket
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	_ = os.Remove(sockPath)

	parentDir := filepath.Dir(sockPath)
	if parentDir != "." && parentDir != "/" {
		_ = os.MkdirAll(parentDir, 0755)
	}

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		log.Fatalf("监听 Unix Socket 失败: %v", err)
	}

	// 设置 Socket 权限，确保反代可通信
	_ = os.Chmod(sockPath, 0666)

	go func() {
		sig := <-sigChan
		writeLog(fmt.Sprintf("接收到系统信号: %v，准备退出程序并清理 Socket 文件...", sig))
		_ = listener.Close()
		_ = os.Remove(sockPath)
		os.Exit(0)
	}()

	if err := http.Serve(listener, mux); err != nil {
		writeLog(fmt.Sprintf("Web服务已关闭: %v", err))
	}
}

// 主入口
func main() {
	initPaths()
	loadConfig()
	go autoTempLoop()
	startWebServer()
}
