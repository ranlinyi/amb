# amb

**amb — Android Mirror Bridge（安卓镜像桥）**

把原 bash 脚本重写为 Go 程序：自动在「有 USB 走 USB，没有走无线调试」之间选路，
可一键配对、投屏、只流转单个应用。提供三种发行形态：

| 形态 | 文件 | 体积 | 说明 |
|---|---|---|---|
| Linux 内嵌单文件版 | `amb-linux-x86_64` | ~21 MB | adb/scrcpy 全部内嵌，开箱即用 |
| Linux 省空间部署版 | `amb-linux-x86_64-lite` | ~6.6 MB | 不内嵌，启动时扫描并按需下载依赖到用户缓存 |
| Windows 内嵌单文件版 | `amb-windows-x86_64.exe` | ~14.6 MB | adb/scrcpy 全部内嵌，开箱即用 |

> Windows 不提供「省空间部署版」：Windows 用户通常对安装位置和磁盘占用更敏感，也更偏好简单即用的东西，
> 一个开箱即用的单文件版就足够了。

---

## 特色功能

- **先问后连，绝不自动连接**：无参数运行会列出所有可连设备（USB + 无线合并），
  询问你连接哪一个；只有一台时也会问一句 `连接这台？[Y/n/q]`，按了才连。
- **终端设备绑定**：一旦指定，同一终端后续的 amb 命令都沿用该设备，不再重复询问；
  新开终端 = 新会话，会重新询问。换设备 `amb rd <设备>`，清除 `amb rd null`，查看 `amb rd`。
- **多设备必选，绝不静默挑第一台**：插着两台手机（或有多个无线候选）时，列出全部设备让你指定。
  非交互终端在多台时会拒绝并列出候选，不会偷偷替你决定。
- **`tcpip` 子命令**：`amb tcpip 5555` 与 `adb tcpip 5555` 完全等价，但先帮你选设备。
  这是「无线免手动重连」的前提步骤。
- **无线免手动重连**：配合 `amb tcpip 5555` 固定端口，amb 会识别手机广播的 `_adb._tcp`
  服务自动发现并连接——**手机离开 Wi-Fi 几小时再回来，也不用碰手机**。
  （Android 14+ 在断网时会自动关掉“无线调试”开关且不会恢复，而固定端口不受影响。）
- **按传输自动切画质档**：USB 用 **原生 1440×3200 + 50 Mbps**，无线用 **1600 长边 + 20 Mbps**，
  两者都是 **60fps + 零视频缓冲**（低延迟）；可分别用 `SCRCPY_ARGS_USB` / `SCRCPY_ARGS` 覆盖。
- **`-A` 纯音频模式**：把手机声音转到电脑，**手机自动静音、只有电脑出声**；
  默认 **无损 raw（未压缩 PCM，bit-perfect）** + 20ms 缓冲 + `--require-audio` 兜底。
  实测帧长：opus 20.0ms/帧、FLAC 85.3ms/帧（块=4096 采样）、raw 无编码器——
  所以“要无损又要低延迟”选 raw（带宽 1536 kbps，相对线路可忽略）。
- **`-a` 单应用虚拟显示**：只把指定应用投到电脑的新显示里，**手机主屏不受影响**；
  支持中文应用名模糊匹配（唯一命中直接用，多个并列让你选序号）。
- **零依赖便携**：单文件内嵌 adb + scrcpy，拷到 U 盘即可运行；另有 6.6 MB 的省空间版按需下载。
- **干净的输出约定**：stdout 只输出选中的目标，适合 `ADDR=$(amb -W)` 这种脚本用法。

---

## 1. 下载与使用

到本仓库的 **Releases** 页面下载对应文件：

| 平台 / 版本 | 文件 | 用法 |
|---|---|---|
| Linux 内嵌版 | `amb-linux-x86_64` | `chmod +x amb-linux-x86_64 && ./amb-linux-x86_64 -h` |
| Linux 省空间版 | `amb-linux-x86_64-lite` | `chmod +x amb-linux-x86_64-lite && ./amb-linux-x86_64-lite -h` |
| Windows | `amb-windows-x86_64.exe` | 放进任意目录，双击或命令行运行 `amb-windows-x86_64.exe -h` |

三个文件都是**便携版**：拷到 U 盘或任意路径即可用，不需要安装，也不需要预装 bash。

```bash
# Linux
./amb-linux-x86_64                 # 列出可连设备，询问连接哪个（不自动连）
./amb-linux-x86_64 -S              # 连上后投屏
./amb-linux-x86_64 -a 计算器 -S    # 只流转单个应用（虚拟显示，手机主屏不受影响）
./amb-linux-x86_64 -A              # 纯音频：手机静音，只在电脑出声（默认无损 raw）
./amb-linux-x86_64 -p 123456 -S    # 无线首次配对
./amb-linux-x86_64 tcpip 5555      # 等价 adb tcpip 5555：切固定端口，之后免手动重连

# 省空间版用法完全相同，只是文件名不同
./amb-linux-x86_64-lite -S
```

```powershell
:: Windows
.\amb-windows-x86_64.exe -S
.\amb-windows-x86_64.exe -a 微信 -S
```

完整选项与行为见 `amb-usage.md`，本程序额外提供：

```
amb rd                # 查看当前终端绑定的设备
amb rd <设备>         # 重新指定设备（序列号或 IP:端口）
amb rd null           # 清除当前终端的设备绑定
amb tcpip 5555        # 等价 adb tcpip 5555（先选设备）
amb --version         # 版本
amb --print-paths     # 打印最终使用的 adb / scrcpy / scrcpy-server 路径
amb --check-deps      # 只扫描依赖（别名 --scan），不下载
```

### 先问后连

无参数运行 amb **不会自动连接**，而是把所有可连设备列出来问你：

```text
$ amb
amb: 检测到 2 台可连设备，请指定用哪一台：
   1) USBDEV01                 Pixel_7a
   2) 192.168.1.51:5555
请输入序号 [1-2]，q 取消:
```

- USB 与无线候选**合并列出**，USB 设备附带 `model` 名，一眼能认出是哪台。
- 只有一台时也会问一句 `连接这台？[Y/n/q]`——按 Y 或回车才连；
  但**非交互终端**（管道、`$(...)`）在只有一台时直接使用并说明，保住 `ADDR=$(amb -W)` 这类脚本用法。
- 多台 + 非交互终端：拒绝连接并列出候选，退出码 2，不会偷偷替你决定。
- 只想看不问：`amb -l`（可配 `-U` / `-W` 过滤）。

### 终端设备绑定

指定一次后，amb 把设备记在**当前终端**下（按父进程 PID 区分，不需要任何 shell 集成）：

```text
$ amb -S                      # 第一次：列出设备，选 1
$ amb -A                      # 同一终端：直接用刚才那台，不再询问
$ amb rd                       # 查看当前绑定
$ amb rd 192.168.1.77:5555    # 换设备
$ amb rd null                  # 清除绑定
```

- **新开一个终端 = 新会话**，会重新询问，不会串到别的窗口。
- 绑定设备离线时，amb 会说明并让你重新指定，不会静默切换到另一台。
- 会话文件写在 `\$AMB_CACHE/sessions/<父进程PID>`；终端关闭后由 amb 自动清理。

### 输出约定

- **stdout 只有一样东西**：选中的目标（USB 序列号 或 无线 `IP:端口`）。
- **stderr 全部日志与提示**（包括交互选择界面）。

```bash
ADDR=$(./amb-linux-x86_64 -W)   # 变量拿到的是干净地址
```

---

## 2. 省空间部署版（Linux，`-lite`）

可执行文件本身只有约 6.6 MB，不内嵌任何依赖。每次启动按下面的顺序决定 adb / scrcpy 从哪来：

1. **缓存**：之前已经部署过就直接用，可离线（`\$XDG_CACHE_HOME/amb/<版本>/bin`，通常是 `~/.cache/amb/<版本>/bin`）；
2. **系统**：`PATH` 里**同时**有 `adb` 和 `scrcpy` 时直接用系统的，
   **不下载、不占额外空间**；
3. **按需下载**：缺哪个就下载官方 **scrcpy v4.1** 预编译包（自带 adb + scrcpy + scrcpy-server，
   约 18 MB），**校验官方 SHA256 通过后才解压**到用户缓存目录，之后一直复用。

先用它看看当前环境会怎么处理（**只扫描、不下载**）：

```bash
./amb-linux-x86_64-lite --check-deps
```

```text
模式：省空间部署版（按需下载，仅 Linux）
缓存目录：     /home/you/.cache/amb/<版本>
系统 adb：     /home/you/Android/Sdk/platform-tools/adb
系统 scrcpy：  /usr/bin/scrcpy
已部署 adb：   （未找到）
已部署 scrcpy：（未找到）
结论：系统已有 adb 与 scrcpy，直接使用，无需下载。
```

- 部署位置用 `ADBCTL_CACHE` 可以改（默认 `~/.cache/amb/<版本>`）。
- 删掉该目录，下次运行会重新下载部署。
- 下载默认走官方 GitHub；可用下面的变量换成镜像或本地文件，也遵循标准代理变量
  `HTTPS_PROXY` / `HTTP_PROXY`：
  | 变量 | 作用 |
  |---|---|
  | `ADBCTL_SCRCPY_URL` | scrcpy 预编译包下载地址 |
  | `ADBCTL_SCRCPY_SUMS_URL` | 官方 SHA256SUMS.txt 地址 |
- **注意**：官方 scrcpy 预编译包要求 glibc ≥ 2.35（Ubuntu 22.04+）。如果系统较老但自带 scrcpy/adb，
  会走「系统」这条路，不受影响。

---

## 3. 内嵌版依赖释放位置

内嵌版（`amb-linux-x86_64`、`amb-windows-x86_64.exe`）首次运行时把内嵌的
adb / scrcpy / scrcpy-server 释放到系统缓存目录：

| 平台 | 默认目录 |
|---|---|
| Linux | `\$XDG_CACHE_HOME/amb/<版本>`（通常是 `~/.cache/amb/<版本>`） |
| Windows | `%LOCALAPPDATA%\amb\<版本>` |

可用 `AMB_CACHE` 覆盖（旧名 `ADBCTL_CACHE` 仍兼容）。删除该目录后下次运行会重新释放。

---

## 4. 环境变量

| 变量 | 默认 | 作用 |
|---|---|---|
| `ADB` | 内嵌 / 系统 adb | 指定外部 adb 可执行文件 |
| `SCRCPY` | 内嵌 / 系统 scrcpy | 指定外部 scrcpy 可执行文件 |
| `PAIR_IP` | 自己解析 mDNS | 指定取配对地址的命令 |
| `WIFI_ADDR` | 无 | 固定地址 `IP:端口`（如 `192.168.0.2:5555`），作为额外候选并优先尝试 |
| `SCRCPY_ARGS` | 见下 | 整体替换 `-S` 时的 scrcpy 参数（**最高优先级**） |
| `SCRCPY_ARGS_USB` | 见下 | 只覆盖 USB（插线）时的 scrcpy 参数 |
| `SCRCPY_ARGS_AUDIO` | 见下 | 只覆盖 `-A` 纯音频模式时的 scrcpy 参数 |
| `AMB_CACHE` | 系统缓存目录 | 依赖释放 / 部署目录（旧名 `ADBCTL_CACHE` 仍兼容） |
| `SCRCPY_SERVER_PATH` | 内嵌 / 下载的 server | 指定 scrcpy-server 路径 |
| `AMB_SCRCPY_URL` | 官方地址 | 省空间版：scrcpy 包下载地址（旧名 `ADBCTL_SCRCPY_URL` 仍兼容） |
| `AMB_SCRCPY_SUMS_URL` | 官方地址 | 省空间版：SHA256 清单地址（旧名 `ADBCTL_SCRCPY_SUMS_URL` 仍兼容） |
| `HTTPS_PROXY` / `HTTP_PROXY` | 无 | 下载依赖时使用的代理 |

默认画质档（按传输方式自动选择，2026-09-27 修订）：

```text
无线：--video-codec=h265 --video-bit-rate=20M  --max-size=1600 --max-fps=60 --video-buffer=0
USB ：--video-codec=h265 --video-bit-rate=50M  --max-size=3200 --max-fps=60 --video-buffer=0   （插线自动启用）
音频：--no-video --no-control --audio-source=output --audio-codec=raw --audio-buffer=20 --require-audio   （-A）
```

---

## 5. 从源码构建

### 依赖

- Go 1.22+（本仓库无需任何第三方 Go 模块）
- `curl`、`tar`、`unzip`、`sha256sum`

### 一键构建

```bash
./build.sh
```

脚本会：

1. 下载官方产物并校验 scrcpy 的 SHA256；
2. 组装每个平台的运行目录（`bin/` + `lib/`）；
3. 用仓库内的小工具 `tools/mkzip` 打成 `payload/*.zip` 供 `go:embed` 内嵌；
4. 交叉编译出三个文件：
   - `dist/amb-linux-x86_64`：Linux 内嵌单文件版（默认构建，内嵌 payload）
   - `dist/amb-windows-x86_64.exe`：Windows 内嵌单文件版（默认构建，内嵌 payload）
   - `dist/amb-linux-x86_64-lite`：Linux 省空间部署版（`-tags lite`，不内嵌）

### 手动构建

```bash
# 先按 build.sh 组装好 .build/linux 与 .build/windows，再：
go run ./tools/mkzip .build/linux   payload/amb-payload-linux-amd64.zip
go run ./tools/mkzip .build/windows payload/amb-payload-windows-amd64.zip

# 内嵌单文件版
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/amb-linux-x86_64 .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/amb-windows-x86_64.exe .

# 省空间部署版（Linux，按需下载）
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -tags lite -trimpath -ldflags "-s -w" -o dist/amb-linux-x86_64-lite .
```

---

## 6. 依赖版本与来源

| 组件 | 版本 | 来源 |
|---|---|---|
| adb | 37.0.0（随 scrcpy 官方预编译包附带） | https://github.com/Genymobile/scrcpy/releases/tag/v4.1 |
| scrcpy（Linux x86_64 预编译包） | v4.1 | 同上 |
| scrcpy（Windows win64 预编译包） | v4.1 | 同上 |

内嵌版在**构建时**、省空间版在**运行时下载时**都会用 scrcpy 官方 `SHA256SUMS.txt` 校验
（SHA256 不匹配直接中止）。

各依赖的许可证归其各自作者所有（adb/platform-tools 为 Apache-2.0，scrcpy 为 Apache-2.0），
本仓库仅分发打包后的运行时。

---

## 7. 源码结构

```
.
├── main.go                 # amb 全部逻辑（选路、mDNS、配对、应用模糊匹配、启动 scrcpy）
├── lite.go                 # //go:build lite —— 省空间版：扫描依赖 + 下载 + SHA256 校验 + 解压
├── lite_stub.go            # //go:build !lite —— 内嵌版的空实现
├── isatty_linux.go         # Linux：真正的 isatty（ioctl TCGETS）
├── isatty_windows.go       # Windows：GetConsoleMode
├── payload_linux.go        # //go:build linux && !lite —— 内嵌 Linux payload
├── payload_lite_linux.go   # //go:build linux && lite —— 省空间版（payload 为空）
├── payload_windows.go      # //go:build windows —— 内嵌 Windows payload
├── tools/mkzip/main.go     # 把目录打成保留可执行位的 zip
├── build.sh                # 一键下载 + 组装 + 交叉编译（生成三个文件）
├── publish.sh              # 一条命令推源码到 GitHub 并发 Release（需 PAT）
├── RELEASING.md            # 推送到 GitHub / 发 Release 的步骤（含 PAT 获取）
├── amb-usage.md            # 完整用法说明
├── legacy/                 # 原 bash 脚本与 pair_ip（仅供对照）
├── dist/                   # 构建输出（gitignore，Release 资源从这里上传）
└── payload/                # 下载的官方产物与生成的 payload zip（gitignore）
```

---

## 8. 与原脚本的差异

- 运行时不依赖 bash / awk / sed / grep 等外部命令，逻辑全部在 Go 内实现。
- `-a` 模糊匹配的分数规则、退出码、stdout/stderr 约定与原脚本保持一致。
- 多个候选地址现在会**去重**后逐个尝试（原脚本可能重复试同一地址）。
- 候选顺序优化：**已连上的地址与 `WIFI_ADDR` 排在最前**，避免对手机残留的陈旧 mDNS 广播做无谓连接（安卓 mdnsd 有时不撤回旧服务，会残留一个已失效的端口）。
- 交互判定用真正的 `isatty`（Linux `ioctl(TCGETS)` / Windows `GetConsoleMode`），
  管道、`/dev/null`、CI 中不会卡住，与原脚本 `[ -t 0 ]` 一致。
- 新增 `--version`、`--print-paths`、`--check-deps`、`SCRCPY` 环境变量。
- 新增 Linux 省空间部署版：启动时扫描系统/缓存，缺依赖自动下载并校验后部署。

### 1.2.0 相对 1.1.6 的变化

- **改名**：`adbctl` → `amb`（Android Mirror Bridge，安卓镜像桥）。命令、产物名、
  环境变量（`AMB_*`）全部跟随；旧环境变量名仍兼容读取。
- **新增 `tcpip` 子命令**：`amb tcpip 5555` 与 `adb tcpip 5555` 等价，但先帮你选设备。
- **多设备不再静默选第一台**：原版插着两台手机时直接投第一台（`usbSerial()` 取到就返回），
  无线路径也是"连上第一个就停"。现在两台以上会列出全部设备让你指定；
  只有一台时直接用它；非交互终端拒绝选择并列出候选（退出码 2）。
- 设备列表现在带 `model` 说明（从 `adb devices -l` 的 `model:` 字段取），选择时更易辨认。

### 1.3.0 相对 1.2.0 的变化

- **不再自动连接**：无参数运行改为「列出全部可连设备 → 询问连接哪个」。
  USB 与无线候选合并枚举；只有一台时也问一句 `连接这台？[Y/n/q]`，按了才连。
- **终端设备绑定**：指定一次后，同一终端后续命令沿用该设备，不再重复询问。
  按父进程 PID 区分会话——新开终端会重新询问，不需要任何 shell 集成（无 `eval`/`export`）。
  - `amb rd` 查看当前绑定
  - `amb rd <设备>` 重新指定
  - `amb rd null` 清除绑定
  - 绑定设备离线时会说明并让你重新指定，不静默切换
- **`-l` 不再强制无线**：`-U -l` 只列 USB、`-W -l` 只列无线、不带则两者都列。
- **`-U` 无 USB 设备时直接报错**（退出码 1），不再静默回落到无线。
- **会话目录自动清理**：写入新绑定时顺带删除已关闭终端的残留会话文件。
- 副作用新增一处并已在 `-h` 明列：终端设备绑定写入用户缓存目录，可用 `amb rd null` 清除。

---

## 9. 许可证

包装程序源码：MIT（见 `LICENSE`）。内嵌/下载的二进制遵循各自许可证。
