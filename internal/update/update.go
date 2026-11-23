// Package update 从发布目录拉取签名过的新版本和加密的节点列表。
//
// 发布目录（Base）下有：
//
//	latest.json      版本清单
//	latest.json.sig  清单的 ed25519 签名（base64）
//	Chuanyun-<版本>.exe
//	nodes.bin        AES-256-GCM 加密的节点 JSON
//	nodes.bin.sig    nodes.bin 的签名
//
// 传输本身可以是明文 HTTP：完整性由签名保证，节点内容由加密保证。
package update

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	ManifestName = "latest.json"
	NodesName    = "nodes.bin"
	SigSuffix    = ".sig"

	maxManifest = 64 << 10
	maxSig      = 1 << 10
	maxNodes    = 1 << 20
	maxExe      = 256 << 20
)

// ErrNoKey 表示这份程序没有内置发布公钥，自动更新不可用。
var ErrNoKey = errors.New("这个版本没有内置发布公钥，无法自动更新")

// Manifest 描述一个可下载的版本。
type Manifest struct {
	Version string `json:"version"`
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Notes   string `json:"notes,omitempty"`
}

// Key 返回内置公钥。
func Key() (ed25519.PublicKey, error) {
	if PublicKey == "" {
		return nil, ErrNoKey
	}
	b, err := base64.StdEncoding.DecodeString(PublicKey)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("内置发布公钥无效")
	}
	return ed25519.PublicKey(b), nil
}

// Sign 返回 data 的 base64 签名，供发布工具使用。
func Sign(priv ed25519.PrivateKey, data []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data)))
}

// Verify 校验 base64 签名。
func Verify(pub ed25519.PublicKey, data, sig []byte) bool {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, data, raw)
}

// ParseNodesKey 解析 32 字节的 base64 节点密钥。
func ParseNodesKey(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("节点密钥无效")
	}
	return b, nil
}

// Encrypt 用 AES-256-GCM 加密，输出 nonce||密文。
func Encrypt(key, plain []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

// Decrypt 是 Encrypt 的逆过程。
func Decrypt(key, data []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, fmt.Errorf("节点文件太短")
	}
	n := gcm.NonceSize()
	plain, err := gcm.Open(nil, data[:n], data[n:], nil)
	if err != nil {
		return nil, fmt.Errorf("节点文件解密失败（密钥不对或文件损坏）")
	}
	return plain, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Newer 判断 a 是否比 b 新。只比较点分数字，非数字段按 0 处理。
func Newer(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func parts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	var out []int
	for _, s := range strings.Split(v, ".") {
		n, _ := strconv.Atoi(s)
		out = append(out, n)
	}
	return out
}

// NormalizeBase 校验发布地址并保证以 / 结尾。
func NormalizeBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("更新地址必须是 http(s)://主机/路径/")
	}
	if !strings.HasSuffix(raw, "/") {
		raw += "/"
	}
	return raw, nil
}

// Client 访问一个发布目录。Proxy 非空时经该 HTTP 代理访问。
type Client struct {
	Base  string
	Proxy string
	Key   ed25519.PublicKey
}

func (c Client) http() *http.Client {
	tr := &http.Transport{Proxy: nil, ResponseHeaderTimeout: 20 * time.Second}
	if c.Proxy != "" {
		if u, err := url.Parse(c.Proxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Transport: tr}
}

func (c Client) open(ctx context.Context, name string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+name, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s：HTTP %d", name, resp.StatusCode)
	}
	return resp, nil
}

func (c Client) get(ctx context.Context, name string, max int64) ([]byte, error) {
	resp, err := c.open(ctx, name)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s 超过大小上限", name)
	}
	return b, nil
}

func (c Client) signed(ctx context.Context, name string, max int64) ([]byte, error) {
	if c.Key == nil {
		return nil, ErrNoKey
	}
	data, err := c.get(ctx, name, max)
	if err != nil {
		return nil, err
	}
	sig, err := c.get(ctx, name+SigSuffix, maxSig)
	if err != nil {
		return nil, err
	}
	if !Verify(c.Key, data, sig) {
		return nil, fmt.Errorf("%s 签名校验失败，已丢弃", name)
	}
	return data, nil
}

// Manifest 读取并校验版本清单。
func (c Client) Manifest(ctx context.Context) (Manifest, error) {
	var m Manifest
	data, err := c.signed(ctx, ManifestName, maxManifest)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("版本清单格式错误：%w", err)
	}
	if m.Version == "" || m.File == "" || strings.ContainsAny(m.File, `/\`) || strings.Contains(m.File, "..") {
		return m, fmt.Errorf("版本清单字段无效")
	}
	if _, err := hex.DecodeString(m.SHA256); err != nil || len(m.SHA256) != 64 {
		return m, fmt.Errorf("版本清单缺少有效的 sha256")
	}
	if m.Size <= 0 || m.Size > maxExe {
		return m, fmt.Errorf("版本清单里的文件大小无效")
	}
	return m, nil
}

// Download 下载 m 描述的文件到 dst，大小和 sha256 都对上才落盘。
func (c Client) Download(ctx context.Context, m Manifest, dst string) error {
	resp, err := c.open(ctx, m.File)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	part := dst + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, m.Size+1))
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err == nil && n != m.Size {
		err = fmt.Errorf("下载不完整（%d / %d 字节）", n, m.Size)
	}
	if err == nil && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), m.SHA256) {
		err = fmt.Errorf("新版本校验值不符，已丢弃")
	}
	if err != nil {
		_ = os.Remove(part)
		return err
	}
	_ = os.Remove(dst)
	return os.Rename(part, dst)
}

// Nodes 下载、验签并解密节点 JSON。
func (c Client) Nodes(ctx context.Context, key []byte) ([]byte, error) {
	data, err := c.signed(ctx, NodesName, maxNodes)
	if err != nil {
		return nil, err
	}
	return Decrypt(key, data)
}

// FileMatches 判断本地文件是否就是 m 描述的那个。
func FileMatches(path string, m Manifest) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return err == nil && n == m.Size && strings.EqualFold(hex.EncodeToString(h.Sum(nil)), m.SHA256)
}

// Replace 用 newExe 替换正在运行的 exe：旧文件先改名为 .old，失败时改回。
// 返回的 rollback 在新进程没能启动时调用。
func Replace(exe, newExe string) (rollback func(), err error) {
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return nil, fmt.Errorf("无法替换程序文件（目录可能不可写）：%w", err)
	}
	if err := copyFile(newExe, exe); err != nil {
		_ = os.Remove(exe)
		_ = os.Rename(old, exe)
		return nil, err
	}
	return func() {
		_ = os.Remove(exe)
		_ = os.Rename(old, exe)
	}, nil
}

// CleanupOld 删除上次更新留下的 .old。旧进程可能还没退出，所以重试几次。
func CleanupOld(exe string) {
	old := exe + ".old"
	for i := 0; i < 10; i++ {
		if err := os.Remove(old); err == nil || os.IsNotExist(err) {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
