# pcat2-display-feed

Photonicat2（RK3576）板载 GC9307 SPI 小屏的**用户态**显示与控制插件。

- 纯用户态实现：不改内核、不改 DTS、不改 U-Boot；
- 单文件静态 Go 二进制 `pcat2d`；
- 直接驱动 `/dev/spidev1.0`（GC9307，172×320，50 MHz）+ GPIO（RST=122 / DC=121）+ PWM 背光（`/sys/class/backlight`）；
- 读取 RK805 电源键（`/dev/input/event0`，KEY_POWER）与移动传感器（`/sys/kernel/photonicat-pm/movement_trigger`）实现亮屏/熄屏/翻页；
- 通过 MCU（`/dev/pcat-pm-ctl`）控制风扇、充电上限、电源 LED、蜂鸣器；
- 提供 LuCI 设置页（服务 → Photonicat2 控制页），经 `ubus pcat2d`（rpcd 桥）调用 daemon 的 Unix socket，全程 OpenWrt 原生，不监听 HTTP 端口。

测试环境：ImmortalWrt SNAPSHOT r42943（kernel 6.18.55，rockchip/armv8，ariaboard_photonicat2）。

## 功能

屏幕（小屏，172×320）：

- 点亮 / 熄屏（亮度 0–255 正确映射，不受官方 0–100 直写 bug 影响）
- 亮度调节 0–100%
- 空闲熄屏超时（电池 / 充电分别可配，0 = 禁用）
- 移动唤醒（movement sensor）
- 电源键：亮屏时翻页；暗屏时第一次按键仅唤醒
- 翻页帘幕动画（0.5 s，8 行条带部分刷新，无撕裂）
- 3 个内置页面：SYSTEM（电池/充电/温度/运行时间）、CPU（8 核占用/内存/磁盘/网络）、DEVICE（板温/风扇/按键/系统信息）

设备控制（经 MCU，失败自动降级并显示 `mcu_err`）：

- B1 风扇：自动（smart_cool / smart_quiet，按板温 PID）或手动 1–9 档
- B2 充电上限：50–100%（设置后读回校验；超过上限自动停止充电）
- B4 电源 LED：关 / 常亮 / 定时（起止时间）
- B5 蜂鸣器：关 / 常亮 / 定时（起止时间）

LuCI 控制页（服务 → Photonicat2 控制页）：

- 设备状态（风扇/板温/电池/充电器/屏幕/pcat2d 服务，5 s 轮询）
- 屏幕设置、风扇控制、充电上限、电源 LED、蜂鸣器
- 「保存并应用」即时下发，配置持久化在 `/etc/config/pcat2d`

## 安装

### 方式一：OpenWrt 编译进固件（feed）

```sh
# 在 OpenWrt/ImmortalWrt 源码树中
echo 'src-link pcat2 /path/to/pcat2-display-feed' >> feeds.conf
./scripts/feeds update pcat2
./scripts/feeds install pcat2d
make menuconfig   # Utilities -> pcat2d
make package/pcat2d/compile V=s
```

注意：`package/pcat2d/Makefile` 默认从 GitHub Release 下载预编译的 aarch64 二进制。发布仓库为 `zhou1407/pcat2-display-feed`，`PCAT2D_GH_USER` 已设为 `zhou1407`，或把 release 的 `pcat2d-<version>-aarch64.tar.gz` 放到 `dl/` 并把 `PKG_HASH` 设为对应 sha256（本地测试可用 `skip`）。

### 方式二：在设备上直接安装

```sh
# 把 pcat2d-<version>-r1.apk（或 .ipk）传到板子上
apk add --allow-untrusted /tmp/pcat2d-0.1.0-r1.apk
# 或（opkg 系固件）
opkg install /tmp/pcat2d-0.1.0-r1.ipk
```

安装后自动启用并启动服务，刷新 LuCI 即可在「服务」菜单看到入口。

### 依赖

- 固件需自带 `kmod-photonicat-pm`（官方 imm/ImmortalWrt 快照默认包含）
- 运行时：`luci-base`、`rpcd`（安装包会声明依赖）
- 内核需 `CONFIG_SPI_SPIDEV=y`、`SPI_ROCKCHIP`、`PWM_BACKLIGHT`、`INPUT_RK805_PWRKEY`（官方快照默认满足）

## 配置

`/etc/config/pcat2d`：

| 选项 | 默认 | 说明 |
|---|---|---|
| `fan_mode` | `smart_cool` | `smart_cool` / `smart_quiet` / `manual` |
| `fan_level` | `4` | 手动档位 1–9 |
| `charge_limit` | `100` | 充电上限 50–100% |
| `led_mode` | `on` | `off` / `on` / `timed` |
| `led_start` / `led_end` | `08:00` / `22:00` | timed 模式时段 |
| `beeper_mode` | `on` | `off` / `on` / `timed` |
| `beeper_start` / `beeper_end` | `08:00` / `22:00` | timed 模式时段 |
| `brightness` | `50` | 屏幕亮度 0–100% |
| `dim_battery_sec` | `60` | 电池供电时息屏超时（0 = 禁用） |
| `dim_charger_sec` | `300` | 充电时息屏超时（0 = 禁用） |
| `movement` | `1` | 移动唤醒开关 |

也可以直接通过 ubus / CLI 操作：

```sh
ubus call pcat2d status
ubus call pcat2d set '{"data":{"brightness":80,"fan_mode":"manual","fan_level":6}}'

pcat2d ctl -s /var/run/pcat2d.sock status
pcat2d ctl -s /var/run/pcat2d.sock set '{"charge_limit":85}'
pcat2d probe        # 只读探测 MCU 版本 / 充电上限
```

## 构建

```sh
cd src
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o pcat2d .
```

GitHub Actions（`.github/workflows/build.yml`）在打 tag（`v*`）时自动交叉编译并发布 Release 资产 `pcat2d-<version>-aarch64.tar.gz`；当前 `PKG_HASH` 已固定为 v0.1.0 release tarball 的 sha256，feed 用户可直接构建。

## 目录结构

```
src/                       Go 源码（单 package main）
package/pcat2d/            OpenWrt 包
  Makefile
  files/pcat2d.init        procd 服务脚本
  files/pcat2d.config      uci 默认配置
  files/rpcd-pcat2d        rpcd ubus 桥
  files/luci/...           menu / acl / 控制页 JS
.github/workflows/build.yml
```

## 已知限制

- 仅适配 Photonicat2（RK3576 + GC9307 + RK806 + photonicat-pm MCU）；其它板子需要改 GPIO/SPI 参数，当前版本未做 profile 抽象。
- MCU 控制命令（风扇/充电上限/LED/蜂鸣器）由树内官方脚本与实测反推，未获得官方协议文档；daemon 对失败做降级处理（`mcu_err` 上报，屏幕与页面继续工作）。
- 屏幕无 TE 引脚，全屏刷新会撕裂，因此翻页采用帘幕动画 + 条带局部刷新；静态 1 Hz 刷新仅重写变化区域。
- 屏幕 3 页为测试页面；正式 UI（状态栏 / 电池 / 5G / CPU 页）属于后续目标，另行实现。

## License

GPL-3.0-only，见 [LICENSE](LICENSE)。
