package settings

import (
	"errors"
	"strings"
)

type Vault interface {
	Get(string) (string, error)
	Set(string, string) error
	Delete(string) error
}

var ErrNoKey = errors.New("未配置服务密钥")

func KeyName(kind string) (string, error) {
	switch kind {
	case "asr", "llm", "exa":
		return "voice_input/" + kind, nil
	}
	return "", errors.New("未知密钥类型")
}
func ValidKey(key string) error {
	if len(key) < 1 || len(key) > 2560 || strings.ContainsAny(key, "\r\n\x00") {
		return errors.New("密钥为空、过长或包含控制字符")
	}
	return nil
}
