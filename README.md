# 智能 USB 风扇温控系统 (yuexps-usb-fan)

飞牛 NAS (FNOS) 的 USB 继电器风扇温控应用。

### 自定义配置说明
应用运行后，会在持久化目录（FNOS 环境为 `/var/apps/yuexps-usb-fan/config.json`，本地运行为同级 `config.json`）生成配置文件。你可以直接编辑该 JSON 文件来自定义参数（修改保存后，重启应用服务即可生效）：

| 参数名 | 默认值 | 作用说明 |
| :--- | :--- | :--- |
| `serial_port` | `/dev/ttyUSB0` | 自定义 USB 串口硬件设备地址（如 `/dev/ttyUSB1`） |
| `baud_rate` | `9600` | 串口通信波特率，可根据实际继电器硬件波特率调整 |
| `relay_addr` | `1` | 继电器硬件设备地址/通道号（多路继电器可配置为 2、3 等） |
| `ceiling` | `55.0` | 自动温控开启上限温度 (℃)，可在网页 WebUI 直接修改并自动保存 |
| `floor` | `50.0` | 自动温控关闭下限温度 (℃)，可在网页 WebUI 直接修改并自动保存 |
| `mode` | `auto` | 默认启动模式（`auto` 为自动温控，`manual` 为手动控制） |
| `poll_interval` | `3` | 后端温度采样与温控循环周期（单位：秒，仅影响后端检测频率） |
| `temp_path` | `/sys/class/thermal/thermal_zone1/temp` | 自定义 CPU 温度文件路径 |
| `has_feedback` | `true` | 是否支持反馈（`true` 为有反馈继电器，`false` 为无反馈继电器，将使用无反馈操作码且不等待回执） |
| `op_close_nofeed` | `0` | 无反馈关闭指令操作码（对应十六进制 `0x00`） |
| `op_open_nofeed` | `1` | 无反馈开启指令操作码（对应十六进制 `0x01`） |
| `op_close_feed` | `2` | 带反馈关闭指令操作码（对应十六进制 `0x02`） |
| `op_open_feed` | `3` | 带反馈开启指令操作码（对应十六进制 `0x03`） |
| `op_toggle_feed` | `4` | 继电器取反状态操作码（对应十六进制 `0x04`） |
| `op_query` | `5` | 继电器物理状态查询操作码（对应十六进制 `0x05`） |
| `status_on` | `1` | 硬件回执所代表的开启状态判定值（对应十六进制 `0x01`） |
| `status_off` | `0` | 硬件回执所代表的关闭状态判定值（对应十六进制 `0x00`） |

## 构建与部署

### 1. 一键编译打包
在 Windows 根目录下双击运行：
**`build.cmd`**

脚本会自动进行 Go 交叉编译并调用 `fnpack` 工具，生成安装包：
**`yuexps-usb-fan.fpk`**

### 2. FNOS 部署
登录 FNOS 后台 -> 进入“应用中心” -> 手动安装导入生成的 `yuexps-usb-fan.fpk` 即可。
