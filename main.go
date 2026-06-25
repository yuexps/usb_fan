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
	relayAddr        = byte(0x01)
	pollInterval     = 3
	customTempPath   = "/sys/class/thermal/thermal_zone1/temp"
	hasFeedback      = true

	gatewayPrefix = "/app/yuexps-usb-fan"
	socketName    = "usb_fan.sock"

	// 操作码
	opCloseNoFeed = byte(0x00) // 0x00关无反馈
	opOpenNoFeed  = byte(0x01) // 0x01开无反馈
	opCloseFeed   = byte(0x02) // 0x02关反馈
	opOpenFeed    = byte(0x03) // 0x03开反馈
	opToggleFeed  = byte(0x04) // 0x04取反反馈
	opQuery       = byte(0x05) // 0x05查询指令

	// 状态值
	statusOn  = byte(0x01) // 开启状态
	statusOff = byte(0x00) // 关闭状态
)

func buildCmdFrame(op byte) []byte {
	mu.Lock()
	addr := relayAddr
	mu.Unlock()
	return []byte{0xA0, addr, op, (0xA0 + addr + op) & 0xFF}
}

// 全局状态
var (
	ser              *serial.Port
	fanState         bool
	controlMode      = "auto"
	cachedTempPath   string
	lastRelayHwState *bool
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

// 带反馈开关风扇
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
	mu.Unlock()

	var op byte
	var expectStatus byte
	if hasFeed {
		if state {
			op = opOpenFeed
			expectStatus = statusOn
		} else {
			op = opCloseFeed
			expectStatus = statusOff
		}
	} else {
		if state {
			op = opOpenNoFeed
		} else {
			op = opCloseNoFeed
		}
	}
	sendCmd := buildCmdFrame(op)

	serialMu.Lock()
	defer serialMu.Unlock()

	if ser == nil {
		return false
	}

	_, err := ser.Write(sendCmd)
	if err != nil {
		if hasFeed {
			writeLog(fmt.Sprintf("发送带反馈指令异常：%v", err))
		} else {
			writeLog(fmt.Sprintf("发送无反馈指令异常：%v", err))
		}
		go initSerial()
		return false
	}

	if !hasFeed {
		mu.Lock()
		fanState = state
		mu.Unlock()
		writeLog(fmt.Sprintf("无反馈指令下发成功，指令%X", sendCmd))
		return true
	}

	time.Sleep(200 * time.Millisecond)

	resp := make([]byte, 4)
	n, err := ser.Read(resp)
	if err != nil || n != 4 {
		writeLog(fmt.Sprintf("下发%X 无4字节反馈帧 (收到: %d 字节, err: %v)", sendCmd, n, err))
		return false
	}

	mu.Lock()
	addr := relayAddr
	mu.Unlock()

	if resp[0] != 0xA0 || resp[1] != addr {
		writeLog(fmt.Sprintf("反馈帧头/地址异常 resp:%X", resp))
		return false
	}

	expectedSum := (0xA0 + addr + resp[2]) & 0xFF
	if resp[3] != expectedSum {
		writeLog(fmt.Sprintf("反馈帧校验和错误: 预期 %02X, 实际 %02X", expectedSum, resp[3]))
		return false
	}

	realState := resp[2]
	if realState == expectStatus {
		mu.Lock()
		fanState = state
		mu.Unlock()
		writeLog(fmt.Sprintf("带反馈执行成功，指令%X，回执%X", sendCmd, resp))
		return true
	} else {
		writeLog(fmt.Sprintf("执行失败，预期状态%02X，硬件实际%02X", expectStatus, realState))
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

	queryCmd := buildCmdFrame(opQuery)
	_, err := ser.Write(queryCmd)
	if err != nil {
		writeLog(fmt.Sprintf("查询继电器状态出错：%v", err))
		go initSerial()
		return nil
	}

	time.Sleep(200 * time.Millisecond)

	resp := make([]byte, 4)
	n, err := ser.Read(resp)
	if err != nil || n != 4 {
		writeLog("查询状态返回无效帧")
		return nil
	}

	mu.Lock()
	addr := relayAddr
	mu.Unlock()

	if resp[0] != 0xA0 || resp[1] != addr {
		writeLog("查询状态返回无效帧头/地址")
		return nil
	}

	expectedSum := (0xA0 + addr + resp[2]) & 0xFF
	if resp[3] != expectedSum {
		writeLog(fmt.Sprintf("查询状态返回帧校验和错误: 预期 %02X, 实际 %02X", expectedSum, resp[3]))
		return nil
	}

	statByte := resp[2]
	var res bool
	if statByte == statusOn {
		res = true
		mu.Lock()
		lastRelayHwState = &res
		fanState = true
		mu.Unlock()
		return &res
	} else if statByte == statusOff {
		res = false
		mu.Lock()
		lastRelayHwState = &res
		fanState = false
		mu.Unlock()
		return &res
	} else {
		writeLog(fmt.Sprintf("查询返回状态字节异常 %02X", statByte))
		return nil
	}
}

// 定位 CPU 温度路径
func getTempPath() string {
	thermalDir := "/sys/class/thermal"
	files, err := os.ReadDir(thermalDir)
	if err != nil {
		return ""
	}
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "thermal_zone") {
			typePath := filepath.Join(thermalDir, file.Name(), "type")
			typeBytes, err := os.ReadFile(typePath)
			if err == nil && strings.TrimSpace(string(typeBytes)) == "x86_pkg_temp" {
				p := filepath.Join(thermalDir, file.Name(), "temp")
				writeLog(fmt.Sprintf("已定位 CPU 温度路径: %s", p))
				return p
			}
		}
	}
	return ""
}

// 获取温度值
func getTemp() *float64 {
	mu.Lock()
	path := customTempPath
	if path == "" {
		path = cachedTempPath
	}
	mu.Unlock()

	if path == "" || !fileExists(path) {
		if customTempPath == "" {
			path = getTempPath()
			mu.Lock()
			cachedTempPath = path
			mu.Unlock()
		}
	}

	if path == "" {
		return nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		writeLog(fmt.Sprintf("读取温度失败：%v", err))
		mu.Lock()
		cachedTempPath = ""
		mu.Unlock()
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
	RelayAddr     int     `json:"relay_addr"`
	PollInterval  int     `json:"poll_interval"`
	TempPath      string  `json:"temp_path"`
	HasFeedback   bool    `json:"has_feedback"`
	OpCloseNoFeed int     `json:"op_close_nofeed"`
	OpOpenNoFeed  int     `json:"op_open_nofeed"`
	OpCloseFeed   int     `json:"op_close_feed"`
	OpOpenFeed    int     `json:"op_open_feed"`
	OpToggleFeed  int     `json:"op_toggle_feed"`
	OpQuery       int     `json:"op_query"`
	StatusOn      int     `json:"status_on"`
	StatusOff     int     `json:"status_off"`
}

func loadConfig() {
	if !fileExists(configPath) {
		saveConfig(AppConfig{
			Ceiling:       55.0,
			Floor:         50.0,
			Mode:          "auto",
			SerialPort:    serialPort,
			BaudRate:      baudRate,
			RelayAddr:     int(relayAddr),
			PollInterval:  pollInterval,
			TempPath:      customTempPath,
			HasFeedback:   hasFeedback,
			OpCloseNoFeed: int(opCloseNoFeed),
			OpOpenNoFeed:  int(opOpenNoFeed),
			OpCloseFeed:   int(opCloseFeed),
			OpOpenFeed:    int(opOpenFeed),
			OpToggleFeed:  int(opToggleFeed),
			OpQuery:       int(opQuery),
			StatusOn:      int(statusOn),
			StatusOff:     int(statusOff),
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
	controlMode = cfg.Mode
	serialPort = cfg.SerialPort
	baudRate = cfg.BaudRate
	relayAddr = byte(cfg.RelayAddr)
	pollInterval = cfg.PollInterval
	customTempPath = cfg.TempPath
	hasFeedback = cfg.HasFeedback

	opCloseNoFeed = byte(cfg.OpCloseNoFeed)
	opOpenNoFeed = byte(cfg.OpOpenNoFeed)
	opCloseFeed = byte(cfg.OpCloseFeed)
	opOpenFeed = byte(cfg.OpOpenFeed)
	opToggleFeed = byte(cfg.OpToggleFeed)
	opQuery = byte(cfg.OpQuery)
	statusOn = byte(cfg.StatusOn)
	statusOff = byte(cfg.StatusOff)
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
		RelayAddr:     int(relayAddr),
		PollInterval:  pollInterval,
		TempPath:      customTempPath,
		HasFeedback:   hasFeedback,
		OpCloseNoFeed: int(opCloseNoFeed),
		OpOpenNoFeed:  int(opOpenNoFeed),
		OpCloseFeed:   int(opCloseFeed),
		OpOpenFeed:    int(opOpenFeed),
		OpToggleFeed:  int(opToggleFeed),
		OpQuery:       int(opQuery),
		StatusOn:      int(statusOn),
		StatusOff:     int(statusOff),
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
		mu.Unlock()

		if mode == "auto" {
			tVal := getTemp()
			if tVal != nil {
				t := *tVal
				if t >= ceiling && !state {
					writeLog(fmt.Sprintf("温度%.2f℃ 超过上限%.2f，自动开继电器", t, ceiling))
					_ = setFan(true)
				} else if t <= floor && state {
					writeLog(fmt.Sprintf("温度%.2f℃ 低于下限%.2f，自动关继电器", t, floor))
					_ = setFan(false)
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
	Temp       *float64 `json:"temp"`
	RelayState *bool    `json:"relay_state"`
	Mode       string   `json:"mode"`
	Ceiling    float64  `json:"ceiling"`
	Floor      float64  `json:"floor"`
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
		Temp:       t,
		RelayState: relaySt,
		Mode:       controlMode,
		Ceiling:    thresholdCeiling,
		Floor:      thresholdFloor,
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
		Ceiling float64 `json:"ceiling"`
		Floor   float64 `json:"floor"`
	}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, BasicResponse{Success: false, Message: "Invalid JSON request"})
		return
	}

	if req.Ceiling > req.Floor {
		mu.Lock()
		thresholdCeiling = req.Ceiling
		thresholdFloor = req.Floor
		mu.Unlock()

		persistCurrentConfig()

		writeLog(fmt.Sprintf("API更新温控配置成功：上限 %.2f℃，下限 %.2f℃", req.Ceiling, req.Floor))
		writeJSON(w, http.StatusOK, BasicResponse{Success: true, Message: "更新配置成功"})
	} else {
		writeLog("API更新温控配置失败：温度上限必须大于下限")
		writeJSON(w, http.StatusOK, BasicResponse{Success: false, Message: "温度上限必须大于下限"})
	}
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
