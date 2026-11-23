// Package core 定位 sing-box 可执行文件与规则集，并在缺失时下载。
package core

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"chuanyun/internal/version"
)

const (
	geoSiteURL = "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-cn.srs"
	geoIPURL   = "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs"
)

// SingBoxZipURL 是 Windows amd64 官方包，自动下载与说明文档都用这个地址。
func SingBoxZipURL() string {
	return "https://github.com/SagerNet/sing-box/releases/download/v" + version.SingBox + "/sing-box-" + version.SingBox + "-windows-amd64.zip"
}

// SingBoxNames 是手动放置时可以识别的相对路径，按优先级排列。
func SingBoxNames() []string {
	exe := "sing-box"
	if runtime.GOOS == "windows" {
		exe = "sing-box.exe"
	}
	return []string{
		filepath.Join("bin", exe),
		filepath.Join("sing-box", exe),
		exe,
	}
}

// Candidates 返回查找 sing-box 的绝对路径，前面是程序目录，最后是数据目录里的下载副本。
func Candidates(exeDir, dataDir string) []string {
	var out []string
	for _, rel := range SingBoxNames() {
		out = append(out, filepath.Join(exeDir, rel))
	}
	exe := "sing-box"
	if runtime.GOOS == "windows" {
		exe = "sing-box.exe"
	}
	out = append(out, filepath.Join(dataDir, "bin", exe))
	return out
}

// LocateSingBox 找到已有的 sing-box。download 为真且都没有时，下载官方 Windows 包到数据目录。
func LocateSingBox(exeDir, dataDir string, download bool) (string, error) {
	for _, p := range Candidates(exeDir, dataDir) {
		if fileExists(p) {
			return p, nil
		}
	}
	if !download || runtime.GOOS != "windows" {
		return "", fmt.Errorf("未找到 sing-box.exe。请从 %s 下载并将 sing-box.exe 放到 %s", SingBoxZipURL(), filepath.Join(exeDir, "bin"))
	}
	dest := filepath.Join(dataDir, "bin", "sing-box.exe")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if err := DownloadSingBox(dest); err != nil {
		return "", fmt.Errorf("自动下载 sing-box 失败：%w。请手动下载 %s ，把 sing-box.exe 放到 %s", err, SingBoxZipURL(), filepath.Join(exeDir, "bin"))
	}
	return dest, nil
}

// DownloadSingBox 下载官方 zip 并写出 sing-box.exe。
func DownloadSingBox(destExe string) error {
	client := &http.Client{Timeout: 3 * time.Minute}
	resp, err := client.Get(SingBoxZipURL())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 120<<20))
	if err != nil {
		return err
	}
	return InstallFromZip(body, destExe)
}

// InstallFromZip 从官方压缩包中取出 sing-box.exe。
func InstallFromZip(body []byte, destExe string) error {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return err
	}
	var exe *zip.File
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if strings.EqualFold(base, "sing-box.exe") || base == "sing-box" {
			exe = f
			break
		}
	}
	if exe == nil {
		return fmt.Errorf("压缩包里没有 sing-box.exe")
	}
	rc, err := exe.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := os.MkdirAll(filepath.Dir(destExe), 0o755); err != nil {
		return err
	}
	tmp := destExe + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, destExe)
}

// EnsureRuleSets 返回 geosite-cn.srs 与 geoip-cn.srs 的路径，必要时下载。
func EnsureRuleSets(exeDir, dataDir string, download bool) (site, ip string, err error) {
	for _, dir := range []string{filepath.Join(exeDir, "rule-set"), filepath.Join(dataDir, "rule-set")} {
		s := filepath.Join(dir, "geosite-cn.srs")
		i := filepath.Join(dir, "geoip-cn.srs")
		if fileExists(s) && fileExists(i) {
			return s, i, nil
		}
	}
	if !download {
		return "", "", fmt.Errorf("未找到 geosite-cn.srs / geoip-cn.srs")
	}
	dir := filepath.Join(dataDir, "rule-set")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	site = filepath.Join(dir, "geosite-cn.srs")
	ip = filepath.Join(dir, "geoip-cn.srs")
	if err := downloadFile(geoSiteURL, site); err != nil {
		return "", "", err
	}
	if err := downloadFile(geoIPURL, ip); err != nil {
		return "", "", err
	}
	return site, ip, nil
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("下载 %s：HTTP %s", url, resp.Status)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Size() > 0
}
