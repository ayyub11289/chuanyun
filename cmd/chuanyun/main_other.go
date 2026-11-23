//go:build !windows

package main

import "fmt"

func main() {
	fmt.Println("穿云客户端是 Windows 程序。请在 Windows x64 上运行 Chuanyun.exe，或在本机执行 scripts/build-windows.sh 交叉编译。")
}
