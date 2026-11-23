//go:build windows

package platform

import (
	"bytes"
	"image/png"
	"os"
	"testing"
)

func TestListAppsFindsSelfWithIcon(t *testing.T) {
	apps := ListApps()
	t.Logf("found %d apps", len(apps))
	withIcon := 0
	for _, a := range apps {
		if a.Exe == "" || a.Name == "" {
			t.Fatalf("incomplete entry %+v", a)
		}
		if len(a.Icon) > 0 {
			if _, err := png.Decode(bytes.NewReader(a.Icon)); err != nil {
				t.Fatalf("%s icon: %v", a.Exe, err)
			}
			withIcon++
		}
	}
	if len(apps) > 0 && withIcon == 0 {
		t.Fatal("no icons extracted")
	}
}

func TestExeIconOfSystemProgram(t *testing.T) {
	p := os.Getenv("SystemRoot") + `\explorer.exe`
	b := exeIcon(p)
	if len(b) == 0 {
		t.Fatal("no icon for explorer.exe")
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() < 16 {
		t.Fatalf("size=%v", img.Bounds())
	}
	if d := fileDescription(p); d == "" {
		t.Fatal("no file description for explorer.exe")
	}
}
