//go:build !windows

package main

import (
	"fmt"
	"voice_input/internal/branding"
)

func main() {
	fmt.Println(branding.Name + " 仅支持 Windows 10/11 x64；WSL 可运行 go test ./... 和 Windows 交叉编译。")
}
