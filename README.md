# 穿云 Windows 客户端

穿云是给同一套个人代理用的 Windows 11 桌面程序：一个大按钮连接，sing-box 负责转发，界面是简体中文。默认走全设备 TUN；也可以改成只设置系统代理。看门狗大约每 60 秒检查一次，连续失败后按 Reality 端口、再 Hysteria2 自动切换，并用 Windows 通知说明原因。

发布包是便携目录（以及在有 NSIS 时附带的安装器），不是一个 PowerShell 窗口。

## 发布包里有什么

解压 `Chuanyun-windows-amd64.zip` 后：

| 路径 | 作用 |
| --- | --- |
| `Chuanyun.exe` | 主程序 |
| `bin/sing-box.exe` | 已附带的 sing-box 1.14.4 Windows amd64 |
| `rule-set/geosite-cn.srs`、`geoip-cn.srs` | 中国域名 / IP 直连规则 |
| `示例-proxy-nodes.json` | 节点文件样例，地址是文档用的测试网段 |
| `portable.txt` | 有这个文件时，配置写在旁边的 `data\` |
| `third_party/` | sing-box 的 GPL-3.0 与字体的 OFL |

## 安装

便携：把整个 `Chuanyun` 文件夹放到任意可写目录，双击 `Chuanyun.exe`。设置在 `Chuanyun\data\`。

安装器：运行 `Chuanyun-Setup-<版本>-x64.exe`，默认装到 `%LOCALAPPDATA%\Chuanyun`（不需要管理员），在开始菜单和桌面放「穿云」快捷方式。安装版没有 `portable.txt`，设置在 `%APPDATA%\Chuanyun`，覆盖安装或升级不会丢节点。卸载在「设置 → 应用」里找「穿云」，会询问是否一并删除节点和设置。开机自启是程序里的计划任务，不随安装器自动打开。

## 托盘

关掉窗口不会断开，程序缩到右下角托盘继续运行。托盘菜单可以打开窗口、连接/断开、退出；鼠标悬停显示当前状态和节点。再次双击 `Chuanyun.exe` 或快捷方式会把已运行的窗口调出来。开机自启时只放在托盘里，不弹窗口。要彻底退出，用托盘菜单的「退出」。

需要 Windows 10 或 Windows 11 64 位。程序只依赖系统自带的 `OPENGL32.dll`、`USER32.dll` 等，不需要单独安装 WebView2 或 .NET。

## 导入节点

导入后节点按 **Reality（VLESS）在前、Hysteria2 在后** 排列，这就是自动切换的顺序。

1. **点「导入」**：不用自己找文件。窗口会自动列出程序目录、桌面、下载、文档里能用的节点文件（带自动更新的订阅文件排最前，示例文件自动排除），以及剪贴板里复制的节点链接，点一下即可导入。找不到时可以点「粘贴链接」或「其他位置」手动选。文件导入会替换当前列表。
2. **拖进窗口**：把 json 或含链接的文本文件拖到窗口上，同样按替换处理。
3. **粘贴链接**：支持 `vless://`、`hysteria2://`、`hy2://`，一行一个。粘贴纯链接是追加；粘贴整段 JSON 是替换。

节点列表按**服务器**显示，一台服务器一行，显示国家和城市（如「JP 日本 · 东京」。第一次连上后经隧道向 ip-api.com 查询一次，缓存在数据目录的 `geo.json`；未连接时从不直连查询，以免暴露服务器 IP，这时先显示节点备注名）。同一服务器的多个端口和协议合成「线路 1、线路 2…」，界面、通知和日志都不显示端口和 IP。比如一台服务器开了 4 个 Reality 端口加 1 个 Hysteria2，就是 1 台服务器、5 条线路。点服务器会切到它第一条没被判定为不通的线路；线路被封时自动换到同服务器的其他线路。

服务器上的 `/root/chuanyun-nodes.json` 和 `/root/proxy-nodes.json`（`type: vless-reality`）都能导入。示例文件里的文档测试地址（`203.0.113.x` 等）会被拒绝。已连接时导入，会立即用新列表重连。

安装器生成的文件可以是下面任意一种。

单个 Reality 加多个端口，外加 Hysteria2（和示例文件一样）：

```json
{
  "server": "203.0.113.10",
  "uuid": "b7e2c1a0-1234-4abc-8def-0123456789ab",
  "sni": "www.microsoft.com",
  "publicKey": "reality 公钥",
  "shortId": "a1b2c3d4",
  "ports": [443, 8443],
  "hysteria2": { "port": 36712, "password": "密码", "insecure": true }
}
```

`ports` 也可写成 `reality_ports`。Hysteria2 也可以摊平为 `hysteria2_port`、`hysteria2_password`。

节点数组，或带 `nodes` / `links` / sing-box `outbounds` 的对象都可以。字段名接受 `server`/`address`、`port`/`server_port`、`uuid`/`id`、`public_key`/`publicKey`/`pbk`、`short_id`/`sid`。`type: direct` 会被忽略。

链接示例：

```text
vless://UUID@203.0.113.10:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.microsoft.com&fp=chrome&pbk=公钥&sid=a1b2c3d4&type=tcp#Reality-443
hysteria2://密码@203.0.113.10:36712?sni=www.microsoft.com&insecure=1#Hysteria2
```

## 连接

顶部的圆形大按钮是 **连接** / **断开**，下面是代理模式和节点列表；网络测速、日志和设置折叠在最下面。

- **TUN 全局**：给整台电脑建网卡，国内站点直连，其余走代理。需要管理员。未提升时点连接会弹出 UAC，同意后新窗口自动开始连接。
- **系统代理**：只监听 `127.0.0.1:2080` 的混合端口，并写入当前用户的 Internet 设置。不用管理员。断开时会关掉系统代理。

**自动选择**：有两台以上服务器时，节点列表顶部有「自动选择」（默认开启）。每次连接前对每台服务器握手一次（只是 TCP 握手，不发数据），选最快的；手动点某台服务器即关闭自动，点「自动选择」恢复。只有一台服务器时不显示，连接前只确认当前线路能握手，不通就直接换同服务器的下一条线路，不用等看门狗一分钟。

**防泄漏**（只在 TUN 模式下显示，默认关闭）：勾选后，TUN 连接期间用 Windows 筛选平台（WFP）阻断所有不经过隧道的出站连接，只放行 sing-box、穿云自身、本机回环和局域网（打印机、NAS、DHCP 不受影响）。换线路、核心自动重启的间隙也不会漏。如果隧道断开且无法自动恢复，会继续阻断并在主界面显示「解除阻断」；点「连接」恢复，或点「解除阻断」临时直连。点「断开」、切到系统代理或取消勾选时立即解除；规则挂在 WFP 动态会话上，穿云退出（包括崩溃）时系统会自动删除，不会把电脑锁在断网状态。第一次连接就失败时不阻断。

不管哪种模式，混合端口都会开着，看门狗用它访问 `generate_204`。当前节点、端口、状态和最近日志在主界面上。日志文件是数据目录里的 `chuanyun.log`，实际下发给核心的配置是 `sing-box.json`。

## 直连程序

点「直连程序」里的「添加程序」，会按图标列出正在运行和已安装的程序（运行中的排前面，可搜索），勾选后点「完成」。列表里没有的可以在下面手动输入程序名（如 `Steam.exe`，可省略 `.exe`）。已选的程序带图标显示，点右边的减号移除。这些程序不走代理，用本机网络和国内 IP 出去，适合银行、游戏、风控会查海外或机房 IP 的软件。已连接时修改会立即重连生效。按文件名匹配，不区分大小写。

注意：这只解决「出口 IP」，不隐藏 TUN 虚拟网卡。会扫描网卡的软件在 TUN 模式下仍能看到它，这类软件建议改用「系统代理」模式。走系统 DNS 的程序，域名仍按正常分流规则解析。

## 延迟、实时吞吐和下载测速

延迟是**当前已连接节点**经本地代理访问 `generate_204` 的往返时间，按钮为「测量当前节点延迟」。没测过的节点显示「未测量」，超时和失败分开显示，不会把到服务器端口的 TCP 握手时间当成延迟。未连接或核心不支持时显示「不可用」。

实时上下行来自本机 sing-box 的 clash API，只累计出站标签为 `proxy` 的连接。直连流量和本机其他网卡不计算在内。单位同时写出：`Mbps` 是兆比特/秒，`MB/s` 是十进制兆字节/秒（10^6）。第一拍还没算出速率时显示「等待采样」，接口没有数据时显示「不可用」，不会填 0 冒充。

下载测速必须自己点「开始下载测速」，可以「取消测速」。它经当前代理下载，最长约 12 秒或 8 MB，先到为准，并写明已接收的大约字节数。不会在连接、启动或看门狗里自动跑。测速和延迟测量期间暂停自动切换，避免把测速拥塞误判成端口被封。没有真实节点时，这些数字不会出现；离线测试只验证计算、上限和取消。

## 通知与自动切换

连接成功约 15 秒后做第一次探测，正常时每 60 秒一次，失败后改为每 15 秒复查。每一轮经本地混合端口请求 `https://www.gstatic.com/generate_204`（失败再试 http），这是「现在能不能用」的依据，成功时的耗时也作为当前节点的延迟显示。

连续 3 次失败后：

1. 先查国内网络：访问百度，或 TCP 连接 `223.5.5.5:53`。两个都不通，就记「本地网络不可用，暂不切换节点」，**不**把失败算到服务器上。
2. 国内是通的，就**同时**直连服务器上的所有 Reality 端口（TCP），按结果判断原因：
   - 当前端口不通、其他端口通：`线路 1 疑似被封，已自动切换到 线路 3`。已知不通的端口直接跳过。
   - 端口能连但代理不通：`线路 1 能连通但代理不通（疑似被干扰），已切换到 线路 2`。
   - 同一服务器的所有 TCP 端口都不通：`服务器 IP 疑似被墙，请更换 IP`，并尝试 Hysteria2（UDP）。同一服务器 30 分钟内只通知一次，主界面会一直显示提示条，直到恢复正常。
   - Hysteria2 失败时回到 TCP 可达的 Reality 端口。

节点列表里每个节点都会显示最近一次的结论（正常 / 端口可达 / 端口被封 / IP 不通 / 被干扰 / UDP）。点一下节点就切换过去；「检测」按钮可以随时手动跑一遍上面的端口诊断，不连接也能用。

sing-box 意外退出时会自动重启一次，一分钟内再次退出就停止连接并写明原因。穿云被强制结束时，sing-box 会随之结束；下次启动会清理残留的 sing-box 进程和指向 2080 的系统代理。系统代理模式会先备份你原来的代理设置，断开时还原。

通知是 Windows 通知中心的 toast，标题为「穿云」。程序会在当前用户下登记 AppUserModelId `Chuanyun.Desktop`。开机自启用计划任务、登录时以**最高权限**、且只在用户已登录时交互运行（`schtasks /SC ONLOGON /RL HIGHEST /IT`），这样提权之后通知仍然出现在当前会话，而不是消失在会话 0。任务名是 `Chuanyun`。勾选「启动后自动连接」后，任务拉起的程序会自己连接。

创建或删除这个任务需要管理员。没提权就勾选会失败并自动取消勾选；先用 TUN 连接完成一次 UAC，再勾选即可。

## 分流

有规则集文件时，`geosite-cn` 与 `geoip-cn` 走直连，其余走当前节点。DNS 上中国域名用 `223.5.5.5`，其他用经由代理的 `1.1.1.1` DoH。私有地址直连。本程序自己的进程也直连，避免看门狗的国内探测和端口探测被 TUN 卷进代理。

规则集缺失且下载失败时，退回内置规则：`.cn` 后缀以及一批常见中文站点关键字直连，并在日志里说明。

## 没有附带 sing-box 时

正式包已经带了 `bin\sing-box.exe`。如果这份文件被删掉，程序会按下面的顺序找，都没有再尝试下载：

1. 程序目录 `bin\sing-box.exe`
2. 程序目录 `sing-box\sing-box.exe`（手动放一个文件夹也行）
3. 与 `Chuanyun.exe` 同一层的 `sing-box.exe`
4. 数据目录 `bin\sing-box.exe`（自动下载的位置）

自动下载地址（1.14.4，Windows amd64）：

https://github.com/SagerNet/sing-box/releases/download/v1.14.4/sing-box-1.14.4-windows-amd64.zip

下载失败时，打开上面的页面，解压出 `sing-box.exe`，放到 `bin\` 再点连接。规则集同理，放到程序目录的 `rule-set\`：

- https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-cn.srs
- https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs

sing-box 是独立的 GPL-3.0 程序，本客户端只负责启动它，不把它链进自己的二进制。源码与许可证见发布包里的 `third_party/sing-box/`，上游仓库是 https://github.com/SagerNet/sing-box/tree/v1.14.4 。界面字体是 Noto Sans SC，许可证在 `third_party/noto/OFL.txt`。

## 自动更新与节点同步

客户端每 3 小时（启动 30 秒后先查一次）读取服务器上的发布目录：

- **新版本**：签名正确、校验值对上才会下载，下好后窗口顶部出现「重启更新」。点了会短暂断开，换成新 exe 后自动重新打开并恢复连接。替换失败会退回原版本。
- **节点**：服务器上的节点列表变了，就自动替换本地列表并重连，并弹通知。IP 被墙时也会马上查一次，所以服务器换 IP 后只要发布目录还能访问，客户端就能自己拿到新地址。

更新文件优先经代理通道下载，代理不通时才直连。程序只认内置公钥签过名的文件；节点在服务器上是加密存放的。

### 一次性准备

1. 生成发布密钥（只做一次，之后编出的客户端都内置这把公钥）：
   `go run ./cmd/chuanyun-release init -github 用户名/仓库名`
   私钥在 `release\`（已被 .gitignore 排除）。**务必备份这个目录**：私钥丢了，已装的客户端就再也收不到更新。
   已经 init 过、只想换发布位置时用 `target -github 用户名/仓库名`，密钥不变、不用重新编译。
2. 在 GitHub 建一个**公开**仓库专门放发布文件（私有仓库的 Releases 要登录才能下载）。装 GitHub CLI 并登录：`winget install --id GitHub.cli`，然后 `gh auth login`。exe 本身不含任何密钥，节点文件 `nodes.bin` 是加密的，公开不会泄露节点。
   客户端读取 `https://github.com/用户名/仓库名/releases/latest/download/`，国内直连 GitHub 慢，所以默认经代理通道下载。
   也可以改用自建服务器：`init`/`target` 用 `-server root@IP`，再用 `setup` 打印服务器上要执行的命令。
3. 用户把 `release\chuanyun-nodes-subscribe.json` 导入客户端一次。这份文件比普通节点文件多了 `update`（发布地址）和 `update_key`（节点解密密钥），导入后自动更新就开启了。**这份文件含密钥，不要公开。**

### 之后每次发布

```powershell
.\scripts\release.ps1 -Version 1.4.1 -Notes "修复某某问题"
.\scripts\release.ps1 -Version 1.4.1 -Notes "…" -Nodes D:\Chuanyun\chuanyun-nodes.json   # 顺带推节点
.\scripts\release.ps1 -NodesOnly -Nodes D:\Chuanyun\chuanyun-nodes.json                   # 只推节点
```

脚本会改版本号、跑测试、编译、签名，再用 `gh release create` 发一个新 Release（自建服务器时用 scp）。`-NodesOnly` 会覆盖最新 Release 里的 `nodes.bin`。加 `-NoUpload` 只生成 `release\site`，加 `-Installer` 同时打安装包给新用户。想立刻生效，可在客户端「设置 → 检查更新」。

## 从源码构建

在 Linux 上交叉编译，不需要 Windows 构建机：

```bash
sudo apt install gcc-mingw-w64-x86-64 golang-go curl unzip zip
# 可选，用来打安装器
sudo apt install nsis
./scripts/build-windows.sh
```

脚本会：

1. 下载 sing-box 1.14.4 的 Linux 与 Windows 包，以及两份 `.srs`（已有缓存则跳过）。
2. 跑 `go test ./...`。其中包含故障切换决策的单元测试，并用 Linux 版 `sing-box check` 校验生成的 Reality TUN 配置和 Hysteria2 系统代理配置。
3. 用 mingw 和 Fyne 编出无控制台窗口的 `Chuanyun.exe`（`CGO_ENABLED=1 GOOS=windows GOARCH=amd64`）。
4. 打出 `Chuanyun-windows-amd64.zip`。装了 `makensis` 时再打 `Chuanyun-Setup-x64.exe`。

产物默认写到 `/opt/cursor/artifacts`，也可以 `ARTIFACT_DIR=... ./scripts/build-windows.sh`。

只跑测试：

```bash
export SINGBOX_BIN=/path/to/sing-box
export CHUANYUN_RULESET_DIR=/path/to/dir   # 内有 geosite-cn.srs 与 geoip-cn.srs
go test ./...
```

## 限制

这个包是在 Linux 上交叉编译并用 `sing-box check` 校验配置的，**没有在真实的 Windows 桌面上点过界面**。TUN 网卡、UAC、系统代理注册表、toast 和计划任务的代码按 Windows API 写了，但此环境执行不了这些调用。第一次在 Windows 上使用时，请先用系统代理模式确认节点，再试 TUN。

界面是 Fyne 画出来的原生窗口，不是浏览器壳。OpenGL 不可用的机器上窗口可能无法创建，这种环境很少见于 Windows 11 实体机。
