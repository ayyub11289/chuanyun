package core

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCandidatesPreferBundledBin(t *testing.T) {
	got := Candidates(`C:\Chuanyun`, `C:\Users\a\AppData\Roaming\Chuanyun`)
	if len(got) < 2 {
		t.Fatal(got)
	}
	if !strings.Contains(got[0], filepath.Join("bin", "sing-box")) {
		t.Fatalf("first=%s", got[0])
	}
	if !strings.HasSuffix(got[len(got)-1], filepath.Join("bin", "sing-box.exe")) && !strings.HasSuffix(got[len(got)-1], filepath.Join("bin", "sing-box")) {
		t.Fatalf("last=%s", got[len(got)-1])
	}
}

func TestInstallFromZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("sing-box-1.14.4-windows-amd64/sing-box.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("MZ-fake")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "bin", "sing-box.exe")
	if err := InstallFromZip(buf.Bytes(), dest); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "MZ-fake" {
		t.Fatalf("body=%q", b)
	}
}

func TestLocateUsesExistingFile(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "bin")
	if err := os.MkdirAll(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "sing-box"
	if strings.Contains(strings.ToLower(os.Getenv("GOOS")), "windows") {
		name = "sing-box.exe"
	}
	// Candidates 按当前 GOOS 取文件名。
	path := Candidates(dir, filepath.Join(dir, "data"))[0]
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := LocateSingBox(dir, filepath.Join(dir, "data"), false)
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("got %s want %s (%s)", got, path, name)
	}
}
