package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// 网关密钥的落库加密。
//
// 用 AES-256-GCM：既要保密，也要能发现密文被改动（配置被悄悄换成别人的 SMTP，
// 等于把验证码发到攻击者手里）。密钥由 StateSecret 派生，不额外引入一份必须
// 单独保管的密钥文件。
type gatewayCipher struct {
	aead cipher.AEAD
}

var errGatewayCipherUnavailable = errors.New("缺少加密密钥（BEEFTV_AUTH_STATE_SECRET），无法保存网关密钥")

func newGatewayCipher(secret []byte) (*gatewayCipher, error) {
	if len(secret) == 0 {
		return nil, errGatewayCipherUnavailable
	}
	sum := sha256.Sum256(secret)
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("auth: 初始化网关加密失败: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("auth: 初始化网关加密失败: %w", err)
	}
	return &gatewayCipher{aead: aead}, nil
}

func (c *gatewayCipher) Encrypt(plaintext []byte) ([]byte, error) {
	if c == nil {
		return nil, errGatewayCipherUnavailable
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("auth: 生成随机数失败: %w", err)
	}
	sealed := c.aead.Seal(nil, nonce, plaintext, nil)
	return []byte(base64.StdEncoding.EncodeToString(append(nonce, sealed...))), nil
}

func (c *gatewayCipher) Decrypt(payload []byte) ([]byte, error) {
	if c == nil {
		return nil, errGatewayCipherUnavailable
	}
	raw, err := base64.StdEncoding.DecodeString(string(payload))
	if err != nil {
		return nil, fmt.Errorf("auth: 网关配置密文格式错误: %w", err)
	}
	size := c.aead.NonceSize()
	if len(raw) < size {
		return nil, errors.New("auth: 网关配置密文长度不足")
	}
	plaintext, err := c.aead.Open(nil, raw[:size], raw[size:], nil)
	if err != nil {
		return nil, fmt.Errorf("auth: 网关配置解密失败（密钥是否已更换？）: %w", err)
	}
	return plaintext, nil
}
