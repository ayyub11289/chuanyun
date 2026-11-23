package platform

import (
	"os"
	"path/filepath"
)

// DataDir 在便携目录（exe 旁有 portable.txt）使用 data，否则使用 %APPDATA%\Chuanyun。
func DataDir(exeDir string) string {
	if _, err := os.Stat(filepath.Join(exeDir, "portable.txt")); err == nil {
		d := filepath.Join(exeDir, "data")
		_ = os.MkdirAll(d, 0o755)
		return d
	}
	base := os.Getenv("APPDATA")
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			base = exeDir
		}
	}
	d := filepath.Join(base, "Chuanyun")
	_ = os.MkdirAll(d, 0o755)
	return d
}
