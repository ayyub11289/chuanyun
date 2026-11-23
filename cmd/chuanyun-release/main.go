// chuanyun-release 生成发布密钥、签名新版本、加密节点，产物放在 release/site，
// 原样上传到服务器的发布目录即可。
//
//	go run ./cmd/chuanyun-release init   -github 用户名/仓库名   （或 -server root@1.2.3.4 [-port 8088]）
//	go run ./cmd/chuanyun-release target -github 用户名/仓库名   （只换发布位置，密钥不变）
//	go run ./cmd/chuanyun-release app   -exe dist/Chuanyun.exe -version 1.4.1 -notes "修复…"
//	go run ./cmd/chuanyun-release nodes -in chuanyun-nodes.json
//	go run ./cmd/chuanyun-release setup
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"chuanyun/internal/nodes"
	"chuanyun/internal/update"
)

const (
	releaseDir = "release"
	pubkeyFile = "internal/update/pubkey.go"
)

// Config 保存在 release/release.json。
// GitHub 非空时发到该仓库的 Releases，否则发到 Server 上的 RemoteDir。
type Config struct {
	GitHub    string `json:"github,omitempty"`
	Server    string `json:"server,omitempty"`
	Port      int    `json:"port,omitempty"`
	Token     string `json:"token,omitempty"`
	BaseURL   string `json:"base_url"`
	RemoteDir string `json:"remote_dir,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "init":
		err = cmdInit(os.Args[2:])
	case "target":
		err = cmdTarget(os.Args[2:])
	case "app":
		err = cmdApp(os.Args[2:])
	case "nodes":
		err = cmdNodes(os.Args[2:])
	case "setup":
		err = cmdSetup()
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "用法：chuanyun-release init|target|app|nodes|setup [参数]")
	os.Exit(2)
}

func path(name string) string { return filepath.Join(releaseDir, name) }

func randomB64(n int, url bool) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	if url {
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// targetFlags 是 init 和 target 共用的发布位置参数。
type targetFlags struct {
	github, server, host *string
	port                 *int
}

func addTargetFlags(fs *flag.FlagSet) targetFlags {
	return targetFlags{
		github: fs.String("github", "", "发到 GitHub Releases，如 yourname/chuanyun-releases（仓库须公开）"),
		server: fs.String("server", "", "或者发到自己的服务器：ssh 目标，如 root@1.2.3.4"),
		host:   fs.String("host", "", "自建服务器时客户端访问的主机名，默认取 -server 里 @ 后面的部分"),
		port:   fs.Int("port", 8088, "自建服务器上提供更新文件的 HTTP 端口"),
	}
}

func (t targetFlags) apply(cfg *Config) error {
	switch {
	case *t.github != "" && *t.server != "":
		return fmt.Errorf("-github 和 -server 只能选一个")
	case *t.github != "":
		repo := strings.Trim(strings.TrimSpace(*t.github), "/")
		if strings.Count(repo, "/") != 1 {
			return fmt.Errorf("-github 要写成 用户名/仓库名")
		}
		*cfg = Config{GitHub: repo, BaseURL: "https://github.com/" + repo + "/releases/latest/download/"}
	case *t.server != "":
		h := *t.host
		if h == "" {
			h = (*t.server)[strings.LastIndex(*t.server, "@")+1:]
		}
		token := cfg.Token
		if token == "" {
			token = randomB64(12, true)
		}
		*cfg = Config{
			Server:    *t.server,
			Port:      *t.port,
			Token:     token,
			BaseURL:   fmt.Sprintf("http://%s:%d/%s/", h, *t.port, token),
			RemoteDir: "/var/www/chuanyun/" + token,
		}
	default:
		return fmt.Errorf("需要 -github 用户名/仓库名 或 -server root@IP")
	}
	return nil
}

func saveConfig(cfg Config) error {
	raw, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(path("release.json"), raw, 0o600)
}

// cmdTarget 只换发布位置，密钥不变。换完要重新跑 nodes，并让客户端重新导入订阅文件。
func cmdTarget(args []string) error {
	fs := flag.NewFlagSet("target", flag.ExitOnError)
	t := addTargetFlags(fs)
	_ = fs.Parse(args)
	cfg, _, err := loadConfig()
	if err != nil {
		return err
	}
	if err := t.apply(&cfg); err != nil {
		return err
	}
	if err := saveConfig(cfg); err != nil {
		return err
	}
	fmt.Println("发布位置已改为：", cfg.BaseURL)
	fmt.Println("接着运行 nodes 重新生成订阅文件；已导入旧订阅的客户端需要重新导入一次。")
	return nil
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	t := addTargetFlags(fs)
	_ = fs.Parse(args)
	if _, err := os.Stat(path("signing.key")); err == nil {
		return fmt.Errorf("%s 已存在。重新生成会让已发出的客户端再也收不到更新，确需重来请手动删除；只换发布位置请用 target", path("signing.key"))
	}
	var cfg Config
	if err := t.apply(&cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(path("site"), 0o700); err != nil {
		return err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path("signing.key"), []byte(base64.StdEncoding.EncodeToString(priv.Seed())), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(path("nodes.key"), []byte(randomB64(32, false)), 0o600); err != nil {
		return err
	}
	if err := saveConfig(cfg); err != nil {
		return err
	}
	src := fmt.Sprintf("package update\n\n// PublicKey 是发布签名公钥（base64）。由 `go run ./cmd/chuanyun-release init` 生成，为空时不检查更新。\nvar PublicKey = %q\n",
		base64.StdEncoding.EncodeToString(pub))
	if err := os.WriteFile(pubkeyFile, []byte(src), 0o644); err != nil {
		return err
	}
	fmt.Println("已生成 release/signing.key、release/nodes.key、release/release.json，并写入", pubkeyFile)
	fmt.Println("请备份整个 release 目录：丢了私钥，已安装的客户端就再也收不到更新。")
	fmt.Println("更新地址：", cfg.BaseURL)
	return nil
}

func loadConfig() (Config, ed25519.PrivateKey, error) {
	var cfg Config
	raw, err := os.ReadFile(path("release.json"))
	if err != nil {
		return cfg, nil, fmt.Errorf("先运行 init：%w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, nil, err
	}
	seedB64, err := os.ReadFile(path("signing.key"))
	if err != nil {
		return cfg, nil, err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(seedB64)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return cfg, nil, fmt.Errorf("signing.key 无效")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub, err := update.Key()
	if err != nil {
		return cfg, nil, fmt.Errorf("%s 里没有公钥：%w", pubkeyFile, err)
	}
	if !pub.Equal(priv.Public()) {
		return cfg, nil, fmt.Errorf("%s 里的公钥和 signing.key 不匹配", pubkeyFile)
	}
	return cfg, priv, nil
}

func writeSigned(priv ed25519.PrivateKey, name string, data []byte) error {
	if err := os.WriteFile(filepath.Join(releaseDir, "site", name), data, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(releaseDir, "site", name+update.SigSuffix), update.Sign(priv, data), 0o644)
}

func cmdApp(args []string) error {
	fs := flag.NewFlagSet("app", flag.ExitOnError)
	exe := fs.String("exe", "dist/Chuanyun.exe", "新编译的 Chuanyun.exe")
	ver := fs.String("version", "", "版本号，必须和 internal/version 一致")
	notes := fs.String("notes", "", "更新说明，显示在客户端")
	_ = fs.Parse(args)
	if *ver == "" {
		return fmt.Errorf("需要 -version")
	}
	_, priv, err := loadConfig()
	if err != nil {
		return err
	}
	site := filepath.Join(releaseDir, "site")
	if old, _ := filepath.Glob(filepath.Join(site, "Chuanyun-*.exe")); len(old) > 0 {
		for _, f := range old {
			_ = os.Remove(f)
		}
	}
	m := update.Manifest{Version: *ver, File: "Chuanyun-" + *ver + ".exe", Notes: *notes}
	in, err := os.Open(*exe)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(filepath.Join(site, m.File))
	if err != nil {
		return err
	}
	h := sha256.New()
	m.Size, err = io.Copy(io.MultiWriter(out, h), in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	m.SHA256 = hex.EncodeToString(h.Sum(nil))
	raw, _ := json.MarshalIndent(m, "", "  ")
	if err := writeSigned(priv, update.ManifestName, raw); err != nil {
		return err
	}
	fmt.Printf("已签名 v%s（%d 字节，sha256 %s）\n", m.Version, m.Size, m.SHA256)
	return nil
}

func cmdNodes(args []string) error {
	fs := flag.NewFlagSet("nodes", flag.ExitOnError)
	in := fs.String("in", "", "服务器生成的节点 JSON，如 chuanyun-nodes.json")
	_ = fs.Parse(args)
	if *in == "" {
		return fmt.Errorf("需要 -in")
	}
	cfg, priv, err := loadConfig()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	list, err := nodes.Parse(string(raw))
	if err != nil {
		return err
	}
	if nodes.AllExamples(list) {
		return fmt.Errorf("这些是示例节点，不能发布")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		obj = map[string]any{"nodes": v}
	}
	keyB64, err := os.ReadFile(path("nodes.key"))
	if err != nil {
		return err
	}
	key, err := update.ParseNodesKey(string(keyB64))
	if err != nil {
		return err
	}
	obj["update"] = cfg.BaseURL
	obj["update_key"] = strings.TrimSpace(string(keyB64))
	plain, _ := json.MarshalIndent(obj, "", "  ")
	enc, err := update.Encrypt(key, plain)
	if err != nil {
		return err
	}
	if err := writeSigned(priv, update.NodesName, enc); err != nil {
		return err
	}
	if err := os.WriteFile(path("chuanyun-nodes-subscribe.json"), plain, 0o600); err != nil {
		return err
	}
	fmt.Printf("已加密 %d 个节点到 release/site/%s\n", len(list), update.NodesName)
	fmt.Println("首次使用：把 release/chuanyun-nodes-subscribe.json 导入客户端一次，之后节点和版本都会自动更新。")
	return nil
}

func cmdSetup() error {
	cfg, _, err := loadConfig()
	if err != nil {
		return err
	}
	if cfg.GitHub != "" {
		fmt.Printf(`# 发布到 GitHub Releases：%s
# 1. 在 GitHub 上建一个公开仓库 %s（只放发布文件，不用放源码）。
#    私有仓库的 Releases 需要登录才能下载，客户端拿不到。
# 2. 安装 GitHub CLI 并登录一次：
winget install --id GitHub.cli
gh auth login
# 3. 之后用 scripts\release.ps1 发布，它会调用 gh release create / upload。
`, cfg.BaseURL, cfg.GitHub)
		return nil
	}
	fmt.Printf(`# 在服务器（%s）上以 root 执行一次：
mkdir -p %s
echo ok > /var/www/chuanyun/index.html
cat > /etc/systemd/system/chuanyun-update.service <<'EOF'
[Unit]
Description=Chuanyun update files
After=network.target

[Service]
ExecStart=/usr/bin/python3 -m http.server %d --bind 0.0.0.0 --directory /var/www/chuanyun
Restart=always
DynamicUser=yes

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now chuanyun-update
command -v ufw >/dev/null && ufw allow %d/tcp
# 云厂商安全组里也要放行 TCP %d
curl -s -o /dev/null -w '%%{http_code}\n' http://127.0.0.1:%d/
`, cfg.Server, cfg.RemoteDir, cfg.Port, cfg.Port, cfg.Port, cfg.Port)
	return nil
}
