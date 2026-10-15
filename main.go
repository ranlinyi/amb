// amb — Android Mirror Bridge（安卓镜像桥）：Android 设备一键连接 / 投屏，
// 有 USB 走 USB，没有才走无线调试。
//
// 有两种构建：
//  1. 内嵌单文件版（默认）：把 adb、scrcpy、scrcpy-server 全部内嵌进可执行文件，
//     首次运行释放到用户缓存目录，开箱即用，不需要预装任何东西；
//  2. 省空间部署版（-tags lite，仅 Linux）：可执行文件本身很小，启动时扫描系统与
//     缓存里有没有 adb / scrcpy，缺哪个就从官方源下载（校验 SHA256）部署到用户缓存。
package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const appVersion = "1.3.0"

const scrcpyArgsDefault = "--video-codec=h265 --video-bit-rate=20M --max-size=1600 --max-fps=60 --video-buffer=0"

// USB 专用高画质档：USB2.0 实测 adb 吞吐约 106 Mbps，码率取 50M 约留一半余量；
// --max-size=3200 对 1440x3200 源屏等于原生分辨率。
const scrcpyArgsUsbDefault = "--video-codec=h265 --video-bit-rate=50M --max-size=3200 --max-fps=60 --video-buffer=0"

// 纯音频档（-A）：audio-source=output 会关闭设备播放，只有电脑出声。
// 用 raw（未压缩 PCM）：bit-perfect 且无编码器帧缓冲（opus 20ms/帧、FLAC 85ms/帧），延迟最低。
const scrcpyArgsAudioDefault = "--no-video --no-control --audio-source=output --audio-codec=raw --audio-buffer=20 --require-audio"

// 省空间部署版按需下载的 scrcpy 版本（与 build.sh 保持一致）
const scrcpyVersion = "4.1"

const (
	connSvc   = "_adb-tls-connect._tcp"
	legacySvc = "_adb._tcp"
	pairSvc   = "_adb-tls-pairing._tcp"
)

var ipPortRe = regexp.MustCompile("([0-9]{1,3}\\.){3}[0-9]{1,3}:[0-9]{1,5}")

var usageText = strings.Join([]string{
	"amb — Android Mirror Bridge（安卓镜像桥）",
	"",
	"  Android 设备投屏 / 控制：先列出可连设备让你指定，**不会自动连接**；",
	"  一旦指定，同一终端里后续命令都沿用该设备，直到重新指定或清除。",
	"",
	"  amb                 # 列出所有可连设备，询问连接哪个（不自动连）",
	"  amb -S              # 连上后启动 scrcpy（参数见下方 SCRCPY_ARGS）",
	"  amb -a com.miui.calculator -S   # 只流转指定应用（虚拟显示，手机主屏不受影响）；-a 隐含 -S",
	"  amb -a 计算器 -S                # 模糊匹配：唯一命中直接用；多个则按匹配度列出序号让你选",
	"  amb -a +微信 -S                 # 前缀 + = 启动前先强杀该应用；? 前缀=模糊（本程序默认就是模糊）",
	"  amb --list-apps                 # 列出设备上全部应用（应用名 + 包名）",
	"  amb -A            # 纯音频模式：只把手机声音转到电脑（手机自动静音）；隐含 -S",
	"  amb -U            # 只从 USB 设备里找（没有则报错退出）",
	"  amb -W            # 只从无线候选里找（忽略在位的 USB）",
	"  amb -p 748966     # 无线配对（748966 = 手机上显示的 6 位配对码）；隐含 -W",
	"  amb -l            # 只列出可连设备，不连接、不询问",
	"  amb -w 30         # 最多等 30 秒等 mDNS 广播出现（默认 0 = 只查一次）",
	"  amb tcpip 5555    # 等价于 adb tcpip 5555：把设备切到固定端口，之后免手动重连",
	"  amb rd            # 查看当前终端绑定的设备",
	"  amb rd <设备>     # 重新指定设备（序列号或 IP:端口）",
	"  amb rd null       # 清除当前终端的设备绑定",
	"  amb -h            # 帮助",
	"",
	"设备绑定（终端会话）:",
	"  第一次指定设备后，amb 会把它记在当前终端（按父进程 PID 区分）下；",
	"  同一终端后续的 amb 命令都直接用该设备，不再重复询问。",
	"  换设备：amb rd <新设备>；清除绑定：amb rd null；",
	"  新开一个终端 = 新的会话，会重新询问。",
	"",
	"免手动重连（断网外出场景）:",
	"  在能连上的时候执行一次  amb tcpip 5555；之后手机离开 Wi-Fi 几小时再回来，",
	"  amb 也能通过 adb 的 _adb._tcp 广播自动发现并连上固定端口 5555",
	"  （无线调试会在断网时被系统关掉，固定端口不会）。",
	"  amb --version     # 版本",
	"  amb --print-paths # 打印最终使用的 adb / scrcpy / server 路径",
	"  amb --check-deps  # 扫描依赖是否存在（省空间版；只扫描不下载，别名 --scan）",
	"",
	"这个程序会执行什么（副作用全部明列，只有这几条）:",
	"  * adb devices -l                    查询式，只读（枚举 USB 设备）",
	"  * adb mdns services                 查询式，只读（仅无线路径）",
	"  * adb pair <配对地址> <配对码>      仅当给了 -p",
	"  * adb connect <你指定的地址>        仅无线路径，且只连你指定的那一个",
	"  * adb -s <设备> tcpip <端口>        仅当给了 tcpip 子命令",
	"  * adb shell -n pm list packages     仅 -a 的精确包名快速通道，只读",
	"  * scrcpy -s <目标> --list-apps      仅当 --list-apps，或 -a 需要解析应用名时",
	"  * scrcpy -s <目标> [--new-display --start-app=<包名>] $SCRCPY_ARGS",
	"                                      仅当给了 -S / -a",
	"  不写任何文件到当前目录；不用 sudo；不扫端口；除 adb 自身的连接外不联网。",
	"  （写入仅两处：内嵌版首次运行释放依赖；终端设备绑定写到用户缓存，可用 amb rd null 清除）",
	"",
	"选路优先级:",
	"  -p / -l / -W  -> 无线",
	"  -U            -> 只找 USB（没有则退出码 1）",
	"  默认（无参数）-> USB 与无线候选一起列出，让你指定",
	"  USB 判定依据：adb devices -l 中 state=device 且带 \" usb:\" 标记的那一行",
	"",
	"多设备选择:",
	"  检测到多台可连设备时，列出全部设备让你选序号（q 取消）。",
	"  非交互终端拒绝选择：请改用 amb rd <设备> 先指定，或确保只有一台设备在线。",
	"",
	"环境变量:",
	"  ADB                    指定 adb（默认用内嵌/系统的 adb）",
	"  SCRCPY                 指定 scrcpy（默认用内嵌/系统的 scrcpy）",
	"  PAIR_IP                指定取配对地址的命令（默认自己解析 mDNS）",
	"  WIFI_ADDR              固定地址 IP:PORT（如 192.168.0.2:5555），作为额外候选",
	"  SCRCPY_ARGS            覆盖 -S 时的 scrcpy 启动参数（优先级最高）",
	"  SCRCPY_ARGS_USB        只覆盖 USB（插线）时的参数（默认见下）",
	"  SCRCPY_ARGS_AUDIO      只覆盖 -A 纯音频模式时的参数",
	"  AMB_CACHE              依赖释放 / 部署目录（默认用户缓存目录；旧名 ADBCTL_CACHE 仍兼容）",
	"  AMB_SCRCPY_URL         省空间版：scrcpy 包下载地址（可换镜像/本地文件；旧名 ADBCTL_SCRCPY_URL 仍兼容）",
	"  AMB_SCRCPY_SUMS_URL    省空间版：SHA256 清单地址（旧名 ADBCTL_SCRCPY_SUMS_URL 仍兼容）",
	"",
	"scrcpy 参数档（2026-09-27 调整）:",
	"  无线：" + scrcpyArgsDefault,
	"  USB ：" + scrcpyArgsUsbDefault + "   （插线时自动启用）",
	"  音频：" + scrcpyArgsAudioDefault + "   （-A 纯音频模式）",
	"  VBR 内容自适应；音频默认转发到电脑（不想要声音加 --no-audio）",
	"",
	"应用名模糊匹配打分:",
	"  精确包名100 / 精确应用名95 / 应用名前缀85 / 包名末段82 / 包名含点号80 / 包名子串65 / 应用名子串60",
	"  唯一最高分直接用；多个并列则按匹配度列序号让你选；非交互终端会拒绝并提示用完整包名",
	"",
}, "\n")

var (
	payloadRoot   string
	payloadBinDir string
	adbPath       string
	scrcpyPath    string
	serverPath    string
	childEnv      []string
)

// depsReport 记录 --check-deps 扫描到的依赖来源（仅 lite 版会填充）。
type depsReport struct {
	sysAdb      string
	sysScrcpy   string
	cacheAdb    string
	cacheScrcpy string
	cacheRoot   string
}

func orNone(s string) string {
	if s == "" {
		return "（未找到）"
	}
	return s
}

func main() {
	os.Exit(run())
}

func run() int {
	route := "auto"
	doScrcpy := false
	audioOnly := false
	doList := false
	doListApps := false
	pairCode := ""
	waitSec := 0
	app := ""
	subCmd := "" // 子命令：tcpip / rd
	subArg := "" // 子命令的参数：tcpip 的端口 / rd 的设备

	args := os.Args[1:]
	// 第一个不以 "-" 开头的参数视为子命令（tcpip / rd）。
	// 这样 amb tcpip 5555、amb rd null、amb -W tcpip 5555 都能工作。
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			subCmd = args[i]
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				subArg = args[i+1]
			}
			break
		}
	}

	for i := 0; i < len(args); i++ {
		// 跳过子命令及其参数（已在上面解析）
		if subCmd != "" && args[i] == subCmd {
			if subArg != "" && i+1 < len(args) && args[i+1] == subArg {
				i++
			}
			continue
		}
		switch args[i] {
		case "-S", "--scrcpy":
			doScrcpy = true
		case "-a", "--app":
			app = takeValue(args, &i)
			doScrcpy = true
		case "--list-apps":
			doListApps = true
			doScrcpy = true
		case "-A", "--audio-only":
			audioOnly = true
			doScrcpy = true
		case "-U", "--usb":
			route = "usb"
		case "-W", "--wifi":
			route = "wifi"
		case "-l", "--list":
			// 只列设备、不连接、不询问。不再强制无线：
			// -U -l 只列 USB，-W -l 只列无线，不带则两者都列。
			doList = true
		case "-p", "--pair":
			pairCode = takeValue(args, &i)
			route = "wifi"
		case "-w", "--wait":
			w := takeValue(args, &i)
			n, err := strconv.Atoi(w)
			if err != nil || n < 0 {
				fmt.Fprintln(os.Stderr, "amb: -w 需要一个秒数")
				return 2
			}
			waitSec = n
		case "-h", "--help":
			fmt.Print(usageText)
			return 0
		case "--version":
			fmt.Printf("amb %s\n", appVersion)
			return 0
		case "--print-paths":
			if err := setup(); err != nil {
				fmt.Fprintf(os.Stderr, "amb: %v\n", err)
				return 1
			}
			fmt.Printf("adb=%s\nscrcpy=%s\nserver=%s\nroot=%s\n", adbPath, scrcpyPath, serverPath, payloadRoot)
			return 0
		case "--check-deps", "--scan":
			if len(embeddedPayload) > 0 {
				if err := setup(); err != nil {
					fmt.Fprintf(os.Stderr, "amb: %v\n", err)
					return 1
				}
				fmt.Println("模式：内嵌单文件版（依赖已内嵌，无需扫描/下载）")
				fmt.Printf("adb=%s\nscrcpy=%s\nserver=%s\n", adbPath, scrcpyPath, serverPath)
				return 0
			}
			rep := scanDeps()
			fmt.Println("模式：省空间部署版（按需下载，仅 Linux）")
			fmt.Printf("缓存目录：     %s\n", rep.cacheRoot)
			fmt.Printf("系统 adb：     %s\n", orNone(rep.sysAdb))
			fmt.Printf("系统 scrcpy：  %s\n", orNone(rep.sysScrcpy))
			fmt.Printf("已部署 adb：   %s\n", orNone(rep.cacheAdb))
			fmt.Printf("已部署 scrcpy：%s\n", orNone(rep.cacheScrcpy))
			switch {
			case rep.cacheAdb != "" && rep.cacheScrcpy != "":
				fmt.Println("结论：使用缓存里已部署的依赖，无需下载。")
			case rep.sysAdb != "" && rep.sysScrcpy != "":
				fmt.Println("结论：系统已有 adb 与 scrcpy，直接使用，无需下载。")
			default:
				fmt.Printf("结论：缺少依赖，运行时会自动下载官方 scrcpy v%s（约 18MB）到缓存。\n", scrcpyVersion)
			}
			return 0
		default:
			fmt.Fprintf(os.Stderr, "amb: 未知参数 '%s'（-h 看用法）\n", args[i])
			return 2
		}
	}

	if err := setup(); err != nil {
		fmt.Fprintf(os.Stderr, "amb: %v\n", err)
		return 127
	}
	if err := checkExecutable(adbPath); err != nil {
		fmt.Fprintln(os.Stderr, "amb: 找不到 adb（可用 ADB=/path/to/adb 指定）")
		return 127
	}

	// ---------- rd 子命令 ----------
	// amb rd / amb rd <设备> / amb rd null —— 查看 / 指定 / 清除当前终端的设备绑定。
	if subCmd == "rd" {
		return cmdRd(subArg)
	}

	// ---------- tcpip 子命令 ----------
	// 等价于 adb tcpip <端口>，但先帮你选设备（多台时不静默挑第一台）。
	// 放在设备枚举之前：tcpip 自己会按 route 决定查 USB 还是查无线，
	// 且打错端口号时不该先白跑一次设备枚举。
	if subCmd == "tcpip" {
		return cmdTcpip(subArg, route, waitSec)
	}

	// ---------- 设备枚举 ----------
	// 默认（无 -U/-W）把 USB 与无线候选合并后一起列出，让用户指定；
	// 已绑定的设备排在第一位并标注"当前"，不重复询问。
	var devs []deviceInfo
	if route != "wifi" {
		devs = append(devs, usbDevices()...)
	}
	if route != "usb" {
		deadline := time.Now().Add(time.Duration(waitSec) * time.Second)
		var cands []string
		for {
			cands = connectCandidates()
			if len(cands) > 0 {
				break
			}
			if time.Now().Before(deadline) {
				time.Sleep(time.Second)
				continue
			}
			break
		}
		for _, c := range cands {
			devs = append(devs, deviceInfo{serial: c})
		}
	}

	// -U 明确要求只走 USB：一台都没有就直接报错，不回落到无线
	if route == "usb" && len(devs) == 0 {
		fmt.Fprintln(os.Stderr, "amb: -U 指定只走 USB，但当前没有在位的 USB 设备。")
		fmt.Fprintln(os.Stderr, "         检查：1) 数据线已插好  2) 手机的『USB 调试』已授权本机")
		return 1
	}

	if doList {
		// -l：只列设备，不连接、不询问、不写会话
		if len(devs) == 0 {
			fmt.Fprintf(os.Stderr, "amb: 没有发现任何设备（%s / %s）。检查：\n", connSvc, legacySvc)
			fmt.Fprintln(os.Stderr, "         1. 手机【无线调试】已开启（Android 11+）")
			fmt.Fprintln(os.Stderr, "         2. 与电脑同一网段，路由器没开 AP 隔离 / 访客网络隔离")
			fmt.Fprintln(os.Stderr, "         3. 电脑上别跑 avahi-daemon（会和 adb 抢 5353）")
			return 1
		}
		for _, d := range devs {
			fmt.Println(d.serial)
		}
		return 0
	}

	// ---------- 配对（仅无线） ----------
	if pairCode != "" {
		paddr := pairingAddress()
		if paddr == "" {
			fmt.Fprintln(os.Stderr, "amb: 没找到配对服务。请在手机上打开『使用配对码配对设备』对话框后重试（-w N 可等待）。")
			return 1
		}
		fmt.Fprintf(os.Stderr, "amb: 配对 %s …\n", paddr)
		out, err := adbCaptureAll("pair", paddr, pairCode)
		fmt.Fprint(os.Stderr, out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "amb: 配对失败：核对 6 位配对码，并确认配对对话框仍开着。")
			return 1
		}
		time.Sleep(time.Second)
		// 配对后重新枚举，把新出现的连接服务加进来
		cands := connectCandidates()
		known := make(map[string]bool, len(devs))
		for _, d := range devs {
			known[d.serial] = true
		}
		for _, c := range cands {
			if !known[c] {
				devs = append(devs, deviceInfo{serial: c})
			}
		}
	}

	// ---------- 选设备 ----------
	// 会话里已绑定的设备优先：同一终端后续命令不再重复询问。
	target := loadSession()
	if target != "" {
		if !containsDevice(devs, target) {
			// 绑定设备当前离线：提示但不静默切换，交给用户决定
			fmt.Fprintf(os.Stderr, "amb: 当前终端绑定的设备 %s 现在不在线。\n", target)
			fmt.Fprintln(os.Stderr, "        （amb rd null 清除绑定；或重新指定 amb rd <设备>）")
			target = ""
		} else {
			fmt.Fprintf(os.Stderr, "amb: 使用当前终端已指定的设备：%s\n", target)
		}
	}

	if target == "" {
		if len(devs) == 0 {
			fmt.Fprintf(os.Stderr, "amb: 没有发现任何可连设备（%s / %s）。检查：\n", connSvc, legacySvc)
			fmt.Fprintln(os.Stderr, "         4. 免手动重连：先在能连上时执行一次 amb tcpip 5555")
			fmt.Fprintln(os.Stderr, "         1. 手机【无线调试】已开启（Android 11+）")
			fmt.Fprintln(os.Stderr, "         2. 与电脑同一网段，路由器没开 AP 隔离 / 访客网络隔离")
			fmt.Fprintln(os.Stderr, "         3. 电脑上别跑 avahi-daemon（会和 adb 抢 5353）")
			return 1
		}
		if len(devs) == 1 {
			// 只有一台：交互终端里仍然问一句（用户要求"不自动连接"），
			// 但非交互终端直接用它并说明，保住 ADDR=$(amb -W) 这类脚本用法。
			target = devs[0].serial
			if stdinIsTerminal() {
				fmt.Fprintf(os.Stderr, "amb: 只发现一台设备：%s\n", deviceLabel(devs[0]))
				fmt.Fprint(os.Stderr, "amb: 连接这台？[Y/n/q(取消)] ")
				ans := readLine()
				switch strings.ToLower(strings.TrimSpace(ans)) {
				case "", "y", "yes":
					// 继续
				case "q", "n", "no":
					fmt.Fprintln(os.Stderr, "amb: 已取消，未连接。")
					return 3
				}
			} else {
				fmt.Fprintf(os.Stderr, "amb: 非交互终端，只发现一台设备 %s，直接使用（多台时会拒绝并请你先 amb rd <设备>）。\n", target)
			}
		} else {
			serials := make([]string, len(devs))
			labels := make([]string, len(devs))
			for i, d := range devs {
				serials[i] = d.serial
				labels[i] = d.label
			}
			var code int
			target, code = chooseOne(serials, labels)
			if code != 0 {
				return code
			}
		}
		// 记下本次选择，同一终端后续命令沿用
		if err := saveSession(target); err != nil {
			// 绑定失败不影响本次使用，只是不再有"记住设备"的便利
			fmt.Fprintf(os.Stderr, "amb: 提示：无法记住设备选择（%v），本次仍继续。\n", err)
		} else {
			sweepSessions()
		}
	}

	// ---------- 连接 ----------
	transport := "usb"
	if isWirelessTarget(target) {
		transport = "wifi"
		out, _ := adbCaptureAll("connect", target)
		if !strings.Contains(out, "connected to") {
			first := out
			if idx := strings.IndexByte(out, '\n'); idx >= 0 {
				first = out[:idx]
			}
			fmt.Fprintf(os.Stderr, "amb: %s 不可用（%s）\n", target, strings.TrimSpace(first))
			fmt.Fprintln(os.Stderr, "         如果刚开关过无线调试，等几秒再试；必要时在手机上重新配对。")
			return 1
		}
	}
	fmt.Fprintf(os.Stderr, "amb: 已连接 %s\n", target)
	fmt.Println(target)
	return launchOrExit(target, transport, audioOnly, doScrcpy, doListApps, app)
}

// containsDevice 判断 target 是否在设备列表里。
func containsDevice(devs []deviceInfo, target string) bool {
	for _, d := range devs {
		if d.serial == target {
			return true
		}
	}
	return false
}

// deviceLabel 返回设备的展示名：有 model 就带上，没有就只给序列号。
func deviceLabel(d deviceInfo) string {
	if d.label != "" {
		return d.serial + " [" + d.label + "]"
	}
	return d.serial
}

// isWirelessTarget 判断目标是无线地址（IP:端口）还是 USB 序列号。
func isWirelessTarget(target string) bool {
	return strings.Contains(target, ":")
}

// takeValue 取出当前选项的值；没有下一个参数时返回空串（与原脚本行为一致）。
func takeValue(args []string, i *int) string {
	if *i+1 < len(args) {
		*i++
		return args[*i]
	}
	return ""
}

// ---------------- 内嵌依赖释放 ----------------

func setup() error {
	if len(embeddedPayload) > 0 {
		root, err := ensurePayload()
		if err != nil {
			return err
		}
		payloadRoot = root
		payloadBinDir = filepath.Join(root, "bin")
		adbPath = filepath.Join(payloadBinDir, exeName("adb"))
		scrcpyPath = filepath.Join(payloadBinDir, exeName("scrcpy"))
		serverPath = filepath.Join(payloadBinDir, "scrcpy-server")
	} else {
		if err := resolveExternal(); err != nil {
			return err
		}
	}
	if v := os.Getenv("ADB"); v != "" {
		adbPath = v
	}
	if v := os.Getenv("SCRCPY"); v != "" {
		scrcpyPath = v
	}
	childEnv = buildEnv(payloadBinDir)
	return nil
}

func exeName(base string) string {
	if isWindows() {
		return base + ".exe"
	}
	return base
}

func cacheHome() string {
	// AMB_CACHE 优先；旧名 ADBCTL_CACHE 仍兼容（老用户/老脚本不用改）
	if v := os.Getenv("AMB_CACHE"); v != "" {
		return v
	}
	if v := os.Getenv("ADBCTL_CACHE"); v != "" {
		return v
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "amb", appVersion)
}

func ensurePayload() (string, error) {
	dir := cacheHome()
	marker := filepath.Join(dir, ".extracted-"+appVersion)
	if st, err := os.Stat(marker); err == nil && !st.IsDir() {
		return dir, nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", fmt.Errorf("无法创建缓存目录: %w", err)
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), "amb-extract-")
	if err != nil {
		return "", fmt.Errorf("无法创建临时目录: %w", err)
	}
	if err := extractZip(embeddedPayload, tmp); err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("释放内嵌依赖失败: %w", err)
	}
	_ = os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		os.RemoveAll(tmp)
		if _, e2 := os.Stat(marker); e2 != nil {
			return "", fmt.Errorf("无法放置内嵌依赖: %w", err)
		}
		return dir, nil
	}
	_ = os.WriteFile(marker, []byte("ok\n"), 0o644)
	return dir, nil
}

func extractZip(data []byte, dest string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		name := filepath.FromSlash(f.Name)
		if name == "" || strings.Contains(name, "..") || filepath.IsAbs(name) {
			continue
		}
		target := filepath.Join(dest, name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := f.Mode()
		if mode == 0 {
			mode = 0o644
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			return err
		}
		out.Close()
		rc.Close()
		if !isWindows() && strings.HasPrefix(filepath.ToSlash(name), "bin/") {
			_ = os.Chmod(target, 0o755)
		}
	}
	return nil
}

func buildEnv(binDir string) []string {
	env := os.Environ()
	if binDir != "" {
		env = prependPath(env, "PATH", binDir)
	}
	if payloadRoot != "" {
		libDir := filepath.Join(payloadRoot, "lib")
		if st, err := os.Stat(libDir); err == nil && st.IsDir() {
			env = prependPath(env, "LD_LIBRARY_PATH", libDir)
		}
	}
	// 只有确实存在 scrcpy-server 时才设置，避免污染系统 scrcpy 的查找
	if serverPath != "" && os.Getenv("SCRCPY_SERVER_PATH") == "" {
		if _, err := os.Stat(serverPath); err == nil {
			env = setEnv(env, "SCRCPY_SERVER_PATH", serverPath)
		}
	}
	return env
}

func prependPath(env []string, key, value string) []string {
	for i, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if strings.EqualFold(k, key) {
			if v == "" {
				env[i] = k + "=" + value
			} else {
				env[i] = k + "=" + value + string(os.PathListSeparator) + v
			}
			return env
		}
	}
	return append(env, key+"="+value)
}

func setEnv(env []string, key, value string) []string {
	for i, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(k, key) {
			env[i] = key + "=" + value
			return env
		}
	}
	return append(env, key+"="+value)
}

func checkExecutable(path string) error {
	if strings.ContainsAny(path, "/\\") {
		st, err := os.Stat(path)
		if err != nil {
			return err
		}
		if st.IsDir() {
			return fmt.Errorf("%s 是目录", path)
		}
		return nil
	}
	_, err := exec.LookPath(path)
	return err
}

// ---------------- 子进程 ----------------

func adbCaptureAll(args ...string) (string, error) {
	cmd := exec.Command(adbPath, args...)
	cmd.Env = childEnv
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func adbCaptureOut(args ...string) (string, error) {
	cmd := exec.Command(adbPath, args...)
	cmd.Env = childEnv
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = io.Discard
	err := cmd.Run()
	return buf.String(), err
}

func scrcpyCaptureOut(args ...string) (string, error) {
	cmd := exec.Command(scrcpyPath, args...)
	cmd.Env = childEnv
	cmd.Stdin = nil
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = io.Discard
	err := cmd.Run()
	return buf.String(), err
}

// runAttached 前台运行子进程并把终端交给它，返回其退出码。
func runAttached(path string, args []string) int {
	cmd := exec.Command(path, args...)
	cmd.Env = childEnv
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "amb: 无法启动 %s: %v\n", path, err)
		return 1
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigCh:
				_ = cmd.Process.Signal(s)
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	close(done)
	signal.Stop(sigCh)
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "amb: %s 运行失败: %v\n", path, err)
		return 1
	}
	return 0
}

// ---------------- 设备发现 ----------------

func mdnsRaw() string {
	out, _ := adbCaptureAll("mdns", "services")
	return strings.ReplaceAll(out, "\r", "")
}

func extractAddrs(raw, service string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if !strings.Contains(line, service) {
			continue
		}
		out = append(out, ipPortRe.FindAllString(line, -1)...)
	}
	return out
}

// 候选顺序：已连上的 > WIFI_ADDR > 无线调试(TLS) > 固定端口(_adb._tcp)。
// 已连上的排最前，避免对残留的陈旧 mDNS 广播做无谓 connect。
func connectCandidates() []string {
	raw := mdnsRaw()
	var cands []string
	for _, a := range connectedAddrs() {
		if strings.Contains(raw, a) {
			cands = append(cands, a)
		}
	}
	if a := os.Getenv("WIFI_ADDR"); a != "" {
		cands = append(cands, a)
	}
	cands = append(cands, extractAddrs(raw, connSvc)...)
	cands = append(cands, extractAddrs(raw, legacySvc)...)
	return dedupe(cands)
}

// connectedAddrs 返回 adb devices 中已处于 device 状态的 IP:端口 地址。
func connectedAddrs() []string {
	out, _ := adbCaptureOut("devices")
	var addrs []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(strings.TrimRight(line, "\r"))
		if len(f) >= 2 && f[1] == "device" && ipPortRe.MatchString(f[0]) {
			addrs = append(addrs, f[0])
		}
	}
	return addrs
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func pairingAddress() string {
	if cmd := os.Getenv("PAIR_IP"); cmd != "" {
		if p, err := exec.LookPath(cmd); err == nil {
			c := exec.Command(p)
			c.Env = childEnv
			out, err := c.Output()
			if err == nil {
				line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
				if line != "" {
					return line
				}
			}
		}
	}
	addrs := extractAddrs(mdnsRaw(), pairSvc)
	if len(addrs) > 0 {
		return addrs[0]
	}
	return ""
}

// usbDevices 返回所有在位的 USB 设备（序列号 + model 说明）。
// 与旧的 usbSerial() 的区别：不再"取第一台就返回"，而是枚举全部，
// 让上层在多台时弹选择——避免插着两台手机却默默投错那一台。
func usbDevices() []deviceInfo {
	var devs []deviceInfo
	out, _ := adbCaptureOut("devices", "-l")
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "device" && strings.Contains(line, " usb:") {
			devs = append(devs, deviceInfo{serial: fields[0], label: modelFromLine(fields)})
		}
	}
	if len(devs) > 0 {
		return devs
	}
	// 退路（极老版本 adb 没有 " usb:" 标记）：非 IP、非 mDNS 别名的本地设备
	out, _ = adbCaptureOut("devices")
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "device" &&
			!strings.Contains(fields[0], ":") && !strings.Contains(fields[0], "_adb-tls-") {
			devs = append(devs, deviceInfo{serial: fields[0]})
		}
	}
	return devs
}

// modelFromLine 从 adb devices -l 的一行里取 model:xxx / product:xxx 作为说明。
func modelFromLine(fields []string) string {
	model, product := "", ""
	for _, f := range fields[2:] {
		switch {
		case strings.HasPrefix(f, "model:"):
			model = strings.TrimPrefix(f, "model:")
		case strings.HasPrefix(f, "product:") && product == "":
			product = strings.TrimPrefix(f, "product:")
		}
	}
	if model != "" {
		return model
	}
	return product
}

// deviceInfo 一台设备：serial 是 adb -s 的目标，label 是给人看的说明。
type deviceInfo struct {
	serial string
	label  string
}

// ---------------- 应用解析 ----------------

type appEntry struct {
	pkg   string
	label string
	score int
}

func parseAppSpec(spec string) (string, bool) {
	stop := false
	for {
		switch {
		case strings.HasPrefix(spec, "+"):
			stop = true
			spec = spec[1:]
		case strings.HasPrefix(spec, "?"):
			spec = spec[1:]
		default:
			return spec, stop
		}
	}
}

func appsWithLabels(target string) []appEntry {
	out, _ := scrcpyCaptureOut("-s", target, "--list-apps")
	var apps []appEntry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, " - ") {
			continue
		}
		line = line[3:]
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "" {
			continue
		}
		var pkg, label string
		if i := strings.LastIndexAny(trimmed, " \t"); i >= 0 {
			pkg = trimmed[i+1:]
			label = strings.TrimRight(trimmed[:i], " \t")
		} else {
			pkg = trimmed
			label = trimmed
		}
		if pkg == "" {
			continue
		}
		apps = append(apps, appEntry{pkg: pkg, label: label})
	}
	return apps
}

func scoreApps(q string, apps []appEntry) []appEntry {
	var out []appEntry
	for _, a := range apps {
		lp := strings.ToLower(a.pkg)
		ll := strings.ToLower(a.label)
		s := 0
		switch {
		case lp == q:
			s = 100
		case ll == q:
			s = 95
		case strings.HasPrefix(ll, q):
			s = 85
		default:
			parts := strings.Split(lp, ".")
			switch {
			case parts[len(parts)-1] == q:
				s = 82
			case strings.Contains(lp, "."+q):
				s = 80
			case strings.Contains(lp, q):
				s = 65
			case strings.Contains(ll, q):
				s = 60
			}
		}
		if s > 0 {
			a.score = s
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out
}

// resolveApp 返回选中的包名与退出码：0 成功，1 无匹配，2 需选择但不可选/序号无效，3 用户取消。
func resolveApp(target, q string) (string, int) {
	qLC := strings.ToLower(q)

	if out, err := adbCaptureOut("-s", target, "shell", "-n", "pm", "list", "packages"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimRight(line, "\r")
			if !strings.HasPrefix(line, "package:") {
				continue
			}
			pkg := strings.TrimPrefix(line, "package:")
			if strings.EqualFold(pkg, q) {
				return pkg, 0
			}
		}
	}

	fmt.Fprintln(os.Stderr, "amb: 正在获取设备应用清单（约 1~2 秒）…")
	all := scoreApps(qLC, appsWithLabels(target))
	if len(all) == 0 {
		return "", 1
	}

	top := all[0].score
	cnt := 0
	for _, a := range all {
		if a.score == top {
			cnt++
		}
	}
	if cnt == 1 {
		return all[0].pkg, 0
	}

	total := len(all)
	shown := total
	if shown > 30 {
		shown = 30
	}
	if !stdinIsTerminal() {
		fmt.Fprintf(os.Stderr, "amb: 匹配到 %d 个应用，但当前不是交互终端，无法选择。请改用完整包名：\n", total)
		for _, a := range all[:shown] {
			fmt.Fprintf(os.Stderr, "         %-36s %s\n", a.label, a.pkg)
		}
		return "", 2
	}
	fmt.Fprintf(os.Stderr, "amb: 匹配到 %d 个应用（按匹配度排序）:\n", total)
	for i, a := range all[:shown] {
		fmt.Fprintf(os.Stderr, "  %2d) %-36s %s\n", i+1, a.label, a.pkg)
	}
	if total > 30 {
		fmt.Fprintf(os.Stderr, "        …共 %d 个，上面只显示前 30 个；也可直接输入后续序号，或换更精确的关键词\n", total)
	}
	fmt.Fprintf(os.Stderr, "请输入序号 [1-%d]，q 取消: ", shown)
	ans := readLine()
	switch {
	case ans == "" || ans == "q" || ans == "Q":
		fmt.Fprintln(os.Stderr, "amb: 已取消。")
		return "", 3
	}
	n, err := strconv.Atoi(ans)
	if err != nil {
		fmt.Fprintln(os.Stderr, "amb: 序号无效。")
		return "", 2
	}
	if n < 1 || n > total {
		fmt.Fprintf(os.Stderr, "amb: 序号超出范围（1-%d）。\n", total)
		return "", 2
	}
	return all[n-1].pkg, 0
}

func stdinIsTerminal() bool {
	return isTerminal(os.Stdin.Fd())
}

// ---------------- tcpip 子命令 ----------------

// cmdTcpip 等价于 adb tcpip <端口>，但先选设备。
//
//	amb tcpip 5555   把设备切到固定端口 5555（默认就是 5555）
//
// 多台设备时列序号让你选；只有一台直接用。
// 这一步是"免手动重连"的前提：切过一次固定端口后，手机离开 Wi-Fi 再回来，
// amb 也能通过 adb 的 _adb._tcp 广播自动发现并连上（无线调试开关断网即失效）。
func cmdTcpip(portArg, route string, waitSec int) int {
	port := portArg
	if port == "" {
		port = "5555"
	}
	if _, err := strconv.Atoi(port); err != nil {
		fmt.Fprintf(os.Stderr, "amb: tcpip 需要一个端口号，如 amb tcpip 5555（收到 '%s'）\n", portArg)
		return 2
	}

	// 设备来源：默认先看 USB（插线时最典型），-W 则只看无线候选
	var serials []string
	var labels []string
	if route != "wifi" {
		for _, d := range usbDevices() {
			serials = append(serials, d.serial)
			labels = append(labels, d.label)
		}
	}
	if len(serials) == 0 {
		// 没有 USB 设备（或 -W）：从无线候选里挑
		deadline := time.Now().Add(time.Duration(waitSec) * time.Second)
		var cands []string
		for {
			cands = connectCandidates()
			if len(cands) > 0 {
				break
			}
			if time.Now().Before(deadline) {
				time.Sleep(time.Second)
				continue
			}
			break
		}
		if len(cands) == 0 {
			fmt.Fprintln(os.Stderr, "amb: tcpip 需要一台设备，但没有找到 USB 设备，也没发现无线候选。")
			fmt.Fprintln(os.Stderr, "         请用 USB 连接手机并开启『USB 调试』，或用 -w N 等无线广播出现。")
			return 1
		}
		serials = cands
		labels = nil
	}

	target, code := chooseOne(serials, labels)
	if code != 0 {
		return code
	}

	fmt.Fprintf(os.Stderr, "amb: 对 %s 执行 adb tcpip %s …\n", target, port)
	out, err := adbCaptureAll("-s", target, "tcpip", port)
	if strings.TrimSpace(out) != "" {
		fmt.Fprint(os.Stderr, out)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "amb: 切换失败：%v\n", err)
		fmt.Fprintln(os.Stderr, "         检查：1) 设备已连接且已授权  2) 端口在 1-65535 之间")
		return 1
	}
	fmt.Fprintf(os.Stderr, "amb: 已切到固定端口 %s。\n", port)
	fmt.Fprintln(os.Stderr, "       现在可以拔线；之后手机离开 Wi-Fi 再回来，amb 也能自动发现并连上。")
	// stdout 只输出选中的设备，保持"stdout 只放选中目标"的约定
	fmt.Println(target)
	return 0
}

// ---------------- 终端设备绑定（会话） ----------------

// 会话按「父进程 PID」区分：每个终端的 shell 有独立 PID，
// 于是「同一终端后续命令沿用该设备、新开终端重新询问」不需要任何 shell 集成。
// 例：bash(PID 100) 里先后跑 amb -S、amb -A，两次的 os.Getppid() 都是 100。

// sessionPath 返回当前终端会话文件的路径。
func sessionPath() string {
	return filepath.Join(cacheHome(), "sessions", fmt.Sprintf("%d", os.Getppid()))
}

// loadSession 读取当前终端绑定的设备；没有绑定返回空串。
func loadSession() string {
	b, err := os.ReadFile(sessionPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// saveSession 把设备绑定写到当前终端会话文件（覆盖写）。
func saveSession(target string) error {
	p := sessionPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(target+"\n"), 0o644)
}

// clearSession 删除当前终端的设备绑定。
func clearSession() error {
	err := os.Remove(sessionPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// sweepSessions 清理已失效终端的会话文件（父进程已退出时 PID 不存在）。
// 只在写入新会话时顺带做一次，避免 sessions 目录无限堆积。
func sweepSessions() {
	dir := filepath.Join(cacheHome(), "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		pid, err := strconv.Atoi(name)
		if err != nil {
			continue
		}
		// PID 不存在 => 那个终端已经关了
		if _, err := os.FindProcess(pid); err != nil {
			continue
		}
		if !processAlive(pid) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// processAlive 判断 PID 是否存在（Unix: kill(pid, 0)；无权限也算存在）。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// cmdRd 处理 amb rd：查看 / 重新指定 / 清除当前终端的设备绑定。
//
//	amb rd            查看当前绑定
//	amb rd <设备>     重新指定
//	amb rd null       清除绑定
func cmdRd(arg string) int {
	cur := loadSession()
	switch {
	case arg == "":
		if cur == "" {
			fmt.Fprintln(os.Stderr, "amb: 当前终端还没有指定设备。")
			fmt.Fprintln(os.Stderr, "        （无参数运行 amb 会列出可连设备并询问；或 amb rd <设备> 直接指定）")
			return 0
		}
		fmt.Printf("当前终端绑定的设备：%s\n", cur)
		fmt.Fprintln(os.Stderr, "        （amb rd null 清除；amb rd <设备> 重新指定）")
		return 0
	case arg == "null":
		if cur == "" {
			fmt.Fprintln(os.Stderr, "amb: 当前终端本来就没有设备绑定。")
			return 0
		}
		if err := clearSession(); err != nil {
			fmt.Fprintf(os.Stderr, "amb: 清除失败：%v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "amb: 已清除当前终端的设备绑定（原为 %s）。\n", cur)
		return 0
	default:
		if err := saveSession(arg); err != nil {
			fmt.Fprintf(os.Stderr, "amb: 保存设备绑定失败：%v\n", err)
			return 1
		}
		sweepSessions()
		fmt.Fprintf(os.Stderr, "amb: 当前终端已指定设备：%s\n", arg)
		fmt.Fprintln(os.Stderr, "        同一终端后续的 amb 命令都会用它；amb rd null 可清除。")
		return 0
	}
}

// ---------------- 设备枚举 ----------------

// chooseOne 从多个候选里让用户选一个。
//
//	items  候选（序列号或 IP:PORT）
//	labels 与 items 一一对应的说明（可为 nil）
//
// 返回选中的那个；退出码：0 成功，2 需选择但不可选，3 用户取消。
// 只有一项时直接返回它，不弹选择。
func chooseOne(items []string, labels []string) (string, int) {
	if len(items) == 1 {
		return items[0], 0
	}
	if len(items) == 0 {
		return "", 2
	}
	if !stdinIsTerminal() {
		fmt.Fprintf(os.Stderr, "amb: 检测到 %d 台可连设备，但当前不是交互终端，无法选择：\n", len(items))
		for i, it := range items {
			if labels != nil && i < len(labels) && labels[i] != "" {
				fmt.Fprintf(os.Stderr, "         %-24s %s\n", it, labels[i])
			} else {
				fmt.Fprintf(os.Stderr, "         %s\n", it)
			}
		}
		fmt.Fprintln(os.Stderr, "        （无第二合法出口：请在交互终端里重试）")
		return "", 2
	}
	fmt.Fprintf(os.Stderr, "amb: 检测到 %d 台可连设备，请指定用哪一台：\n", len(items))
	for i, it := range items {
		if labels != nil && i < len(labels) && labels[i] != "" {
			fmt.Fprintf(os.Stderr, "  %2d) %-24s %s\n", i+1, it, labels[i])
		} else {
			fmt.Fprintf(os.Stderr, "  %2d) %s\n", i+1, it)
		}
	}
	fmt.Fprintf(os.Stderr, "请输入序号 [1-%d]，q 取消: ", len(items))
	ans := readLine()
	switch {
	case ans == "" || ans == "q" || ans == "Q":
		fmt.Fprintln(os.Stderr, "amb: 已取消。")
		return "", 3
	}
	n, err := strconv.Atoi(ans)
	if err != nil {
		fmt.Fprintln(os.Stderr, "amb: 序号无效。")
		return "", 2
	}
	if n < 1 || n > len(items) {
		fmt.Fprintf(os.Stderr, "amb: 序号超出范围（1-%d）。\n", len(items))
		return "", 2
	}
	return items[n-1], 0
}

func readLine() string {
	var buf [1]byte
	var sb strings.Builder
	for {
		n, err := os.Stdin.Read(buf[:])
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			if buf[0] != '\r' {
				sb.WriteByte(buf[0])
			}
		}
		if err != nil {
			break
		}
	}
	return strings.TrimSpace(sb.String())
}

// ---------------- 启动 ----------------

func launchOrExit(target, transport string, audioOnly, doScrcpy, doListApps bool, app string) int {
	if doListApps {
		return runAttached(scrcpyPath, []string{"-s", target, "--list-apps"})
	}
	var appargs []string
	if app != "" {
		spec, stop := parseAppSpec(app)
		if spec == "" {
			fmt.Fprintln(os.Stderr, "amb: -a 需要应用名或包名（-h 看用法）")
			return 2
		}
		pkg, rc := resolveApp(target, spec)
		if rc != 0 {
			if rc == 1 {
				fmt.Fprintf(os.Stderr, "amb: 没有匹配到应用「%s」。用 amb --list-apps 查看全部应用。\n", spec)
			}
			return rc
		}
		fmt.Fprintf(os.Stderr, "amb: 应用已解析 -> %s\n", pkg)
		if !strings.Contains(" "+scrcpyArgsValue(transport, audioOnly)+" ", " --new-display") {
			appargs = append(appargs, "--new-display")
		}
		if stop {
			appargs = append(appargs, "--start-app=+"+pkg)
		} else {
			appargs = append(appargs, "--start-app="+pkg)
		}
		fmt.Fprintf(os.Stderr, "amb: 单应用模式：虚拟显示 + 启动 %s（手机主屏不受影响）\n", pkg)
	}
	if doScrcpy {
		args := append([]string{"-s", target}, appargs...)
		args = append(args, strings.Fields(scrcpyArgsValue(transport, audioOnly))...)
		fmt.Fprintf(os.Stderr, "amb: 启动 scrcpy -s %s %s\n", target, strings.Join(args[2:], " "))
		return runAttached(scrcpyPath, args)
	}
	return 0
}

func scrcpyArgsValue(transport string, audioOnly bool) string {
	if v, ok := os.LookupEnv("SCRCPY_ARGS"); ok && v != "" {
		return v
	}
	if audioOnly {
		if v, ok := os.LookupEnv("SCRCPY_ARGS_AUDIO"); ok && v != "" {
			return v
		}
		return scrcpyArgsAudioDefault
	}
	if transport == "usb" {
		if v, ok := os.LookupEnv("SCRCPY_ARGS_USB"); ok && v != "" {
			return v
		}
		return scrcpyArgsUsbDefault
	}
	return scrcpyArgsDefault
}
