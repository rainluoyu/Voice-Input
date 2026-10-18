//go:build windows && amd64

package main

import "errors"

var errCopy = errors.New("复制失败；剪贴板可能被其他程序占用，请重试")
