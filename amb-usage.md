# amb 使用说明

一个包装脚本，负责「选设备 → 连上 → （可选）启动 scrcpy」，并把实测调好的参数固化进去。

---

## 1. 命令速查

| 命令 | 作用 |
|---|---|
| `amb` | **只连接**，把选中的目标打印到 stdout |
| `amb -S` | 连接后启动 scrcpy（画面 + 声音都到电脑） |
| `amb -a <名或包>` | **只流转单个应用**（隐含 -S） |
| `amb --list-apps` | 列出设备上全部应用（应用名 + 包名），隐含 -S |
| `amb -U` | 强制走 USB；没有 USB 设备则报错退出 |
| `amb -W` | 强制走无线（忽略在位的 USB） |
| `amb -p <6位码>` | 无线配对（隐含 -W），配对后再连接 |
| `amb -l` | 只列出无线候选地址，不做任何连接 |
| `amb -w <秒>` | 最多等 N 秒等无线广播出现（默认 0 = 只查一次） |
| `amb tcpip [端口]` | 等价 `adb tcpip 5555`：把设备切到固定端口（默认 5555） |
| `amb -h` | 打印脚本文档头 |

---

## 2. 选路逻辑（用哪条链路）

```
-p / -l / -W  →  无线
-U            →  USB（没有则退出码 1）
默认（无参）  →  有 USB 设备就用 USB；没有才回落到无线
```

**USB 判定**：`adb devices -l` 里 state=device 且带 ` usb:` 标记的那一行（退路：非 IP、非 mDNS 别名的本地设备）。

三条链路同时在线也不会选错——`amb` 会把选中的序列号/地址用 `-s` 明确传给 scrcpy。

---

## 2.5 多设备选择

检测到**多台**可连设备时（插着两台手机，或有多个无线候选），`amb` **不会静默挑第一台**，
而是列出全部设备让你指定：

```text
$ amb
amb: 检测到 2 台可连设备，请指定用哪一台：
   1) FAKEDEV001               Pixel_7a
   2) FAKEDEV002               Mi_14
请输入序号 [1-2]，q 取消:
```

- 只有一台时直接用它，不弹选择。
- 非交互终端（管道、`$(...)`）拒绝选择并列出候选，退出码 2，不会偷偷替你决定。
- `-a` 的应用名模糊匹配遇到多个并列最高分时，用同一套交互列序号。

> Go 版（1.2.0 起）新增此行为；bash 原版是"取第一台就返回"。

---

## 3. 输出约定（可嵌脚本）

- **stdout 只有一样东西**：选中的目标（USB 序列号 或 无线 `IP:端口`）
- **stderr 全部日志与提示**（包括交互选择界面）

```bash
ADDR=$(amb -W)          # 只拿无线地址，日志不污染变量
amb -S                  # 直接投屏
```

---

## 4. 投屏参数（-S）

脚本内置默认档：

```
--video-codec=h265 --video-bit-rate=12M --max-size=1200 --max-fps=52 --video-buffer=0
```

（音频未禁用 = scrcpy 默认转发到电脑）

**覆盖方式**：环境变量 `SCRCPY_ARGS` 的优先级高于文件里的默认值，且是**整体替换**（不是追加）：

```bash
# 更清晰（代价：CPU 与温度上升）
SCRCPY_ARGS="--video-codec=h265 --video-bit-rate=32M --max-size=1600 --max-fps=60 --video-buffer=40" amb -S

# 更省 / 更安静
SCRCPY_ARGS="--video-codec=h265 --video-bit-rate=6M --max-size=1000 --max-fps=48 --video-buffer=0" amb -S

# 静音（注意：--no-audio-playback 在没有录像时会把音频整个关掉，要静音请用 --no-audio）
SCRCPY_ARGS="--video-codec=h265 --video-bit-rate=12M --max-size=1200 --max-fps=52 --video-buffer=0 --no-audio" amb -S

# 只要录像、不要窗口（CPU 极低，实测 60fps/43Mbps）
SCRCPY_ARGS="--no-playback --record=out.mkv --video-codec=h265 --video-bit-rate=48M --video-codec-options=bitrate-mode:int=2 --max-fps=60 --max-size=1200" amb -S
```

**参数含义与实测经验**

| 参数 | 作用 | 经验值 |
|---|---|---|
| `--video-codec=h265` | HEVC 硬编 | 同码率下比 H.264 帧率 +18%、丢帧 1/3 |
| `--video-bit-rate` | VBR 上限（不是目标值） | 540x1200 下 6~16M 足够；48M 纯属浪费 |
| `--max-size` | 长边像素 | 1200 是画质/CPU 的甜点；2400 以上实时会崩 |
| `--max-fps` | 帧率上限 | 52 → 实测交付 44fps；本机天花板约 45fps |
| `--video-buffer` | **唯一直接加延迟的旋钮** | 0 最低延迟；无线跳帧明显时调到 40~120 |
| `--no-audio` | 关掉音频 | 想静音用这个，别用 --no-audio-playback |

---

## 5. 单应用流转（-a）

**机制**：`--new-display`（在设备上创建一个虚拟显示）+ `--start-app=<包名>`。应用跑在虚拟显示里，**手机物理屏幕完全不受影响**，scrcpy 只镜像那个显示。

### 5.1 模糊匹配打分

先查**精确包名**（一次只读查询，最快，1 秒内）；不中才向设备取「应用名+包名」清单打分：

| 分数 | 规则 | 例子（查询 → 结果） |
|---|---|---|
| 100 | 包名精确相等 | `com.miui.calculator` |
| 95 | 应用名精确相等 | `计算器` |
| 85 | 应用名前缀 | `计算` → 计算器 |
| 82 | 包名末段相等 | `calculator` |
| 80 | 包名含 `.关键词` | `miui.calculator` |
| 65 | 包名字串包含 | `calc` → com.miui.calculator |
| 60 | 应用名字串包含 | `算器` |

### 5.2 唯一命中 vs 多个候选

- **唯一最高分** → 直接流转，无交互
- **多个并列最高分** → 按匹配度列序号，等你输入：

```
amb: 匹配到 9 个应用（按匹配度排序）:
   1) 计算器        com.miui.calculator
   2) 指南针        com.miui.compass
   ...
请输入序号 [1-9]，q 取消: 2
amb: 应用已解析 -> com.miui.compass
```

- **非交互终端**（管道里跑）→ 不会卡住，列出候选并要求用完整包名，退出码 2

### 5.3 前缀

| 前缀 | 含义 |
|---|---|
| `+` | 启动前先强制停止该应用（`--start-app=+包名`） |
| `?` | 模糊（本脚本默认就是模糊，写不写都一样） |

可组合：`-a +?微信`。

### 5.4 自定义虚拟显示

默认用主屏尺寸/DPI。要指定：

```bash
SCRCPY_ARGS="--new-display=1280x720 --video-codec=h265 --video-bit-rate=8M --max-fps=30" amb -a 计算器 -S
```

脚本会检测到你已经写了 `--new-display`，不会再重复添加。

### 5.5 限制

- **音频仍是设备级**：scrcpy 不支持按应用隔离音频，`-a` 模式下你听到的是整个手机的声音
- 极少数应用在虚拟显示上会异常（依赖特定屏幕特性/DRM 的）
- 模糊匹配需要取一次应用清单（102 个，约 1.2 秒）；精确包名无此开销

---

## 6. 无线相关

- **端口会变**：手机每次开关无线调试、或服务重启，端口都会换。脚本会遍历 mDNS 广播的所有候选，**逐个试、取第一个真正连上的**——所以"死端口"不会让你失败
- **配对**：只有信任关系丢失时才需要 `-p`；配对码是手机上「使用配对码配对设备」对话框里的 6 位数字，脚本不会去猜或扫
- **找不到广播时**：手机上把【无线调试】关掉再打开（配对不用重做），或 `-w 30` 等它出现

常见提示与处理：

| 提示 | 原因 | 处理 |
|---|---|---|
| `所有候选地址都连不上` | 端口全部失效 | 手机上关/开一次无线调试 |
| `没有发现 _adb-tls-connect._tcp 广播` | 无线调试没开 / 不同网段 / 手机息屏省电 | 点亮屏幕确认开关 |
| `配对失败` | 配对码错或对话框关了 | 重新打开对话框，用新码 |

---

## 7. 退出码

| 码 | 含义 |
|---|---|
| 0 | 成功（未给 -S 时已打印目标；给了 -S 时是 scrcpy 自己的退出码） |
| 1 | 没有 USB 却指定了 -U；无无线广播；配对失败；所有候选连不上；应用无匹配 |
| 2 | 参数错误；非交互终端下需要选择；序号越界/无效 |
| 3 | 你在选择界面按 q 或回车取消了 |
| 127 | 找不到 adb |

---

## 8. 环境变量

| 变量 | 默认 | 作用 |
|---|---|---|
| `ADB` | PATH 里的 adb | 指定 adb 路径 |
| `PAIR_IP` | `pair_ip` 命令，找不到则解析 mDNS | 取配对地址 |
| `SCRCPY_ARGS` | 见第 4 节 | 整体替换 scrcpy 参数 |

---

## 9. 典型场景

```bash
# 插着线，看视频（最低延迟、声音在电脑）
amb -S

# 没插线，走无线
amb -S

# 只把某个 App 投出来，自己继续用手机干别的
amb -a 微信 -S

# 先杀掉再重启这个 App
amb -a +微信 -S

# 高画质录一段
SCRCPY_ARGS="--video-codec=h265 --video-bit-rate=48M --video-codec-options=bitrate-mode:int=2 --max-size=1200 --max-fps=60 --video-buffer=40" amb -S

# 只连接不投屏，把地址交给别的工具
ADDR=$(amb -W)

# 无线首次配对
amb -p 123456 -S
```

---

## 10. 脚本的承诺与边界

**会执行的命令**（副作用全部明列）：
```
adb devices -l            只读，判断有没有 USB
adb mdns services         只读，仅无线路径
adb pair <地址> <码>      仅当 -p
adb connect <候选地址>    仅无线路径，逐个试
adb shell -n pm list packages   仅 -a 的精确包名快速通道
scrcpy --list-apps        仅 --list-apps 或 -a 需要解析时
scrcpy -s <目标> ...      仅 -S / -a / --list-apps
```

**不写任何文件、不用 sudo、不扫端口、除 adb 自身连接外不联网。**

---

## 11. 文件与回退

```
~/.local/bin/amb                        主脚本
~/.local/bin/adbwifi -> amb             兼容软链
~/.local/bin/amb.bak-20260921-233520    加音频/单应用之前的版本
~/.local/bin/adbwifi.bak-20260921-224743   最初的原版
~/.local/bin/adbwifi.bak2-20260921-225650  48M/60fps 版
~/.local/bin/adbwifi.bak3-20260921-233001  12M/52fps 版
```

回退到最初原版：
```bash
rm ~/.local/bin/amb && cp -a ~/.local/bin/adbwifi.bak-20260921-224743 ~/.local/bin/adbwifi
```
