package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// Claims JWT 载荷。
type Claims struct {
	UserID uint   `json:"uid"`
	Email  string `json:"email"`
	// Use 标记令牌用途："" 或 "token" 表示长期登录令牌，"ticket" 表示短期预览/下载票据。
	// IssueTicket 会写入 "ticket"，issueToken 不写（保持空）。
	Use string `json:"use,omitempty"`
	jwt.RegisteredClaims
}

// HashPassword 生成 bcrypt 哈希。
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword 校验密码。
func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// IssueToken 签发 7 天有效期的 JWT。
func IssueToken(secret string, userID uint, email string) (string, error) {
	claims := Claims{
		UserID: userID,
		Email:  email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// IssueTicket 签发 60 秒有效期的短期票据（预览/下载专用）。
// Claims.Use = "ticket" 标记用途，jti 为随机 ID（防票据被客户端缓存后反复使用）。
func IssueTicket(ticketSecret string, userID uint, email string) (string, error) {
	jti := fmt.Sprintf("%d-%x", userID, time.Now().UnixNano())
	claims := Claims{
		UserID: userID,
		Email:  email,
		Use:    "ticket",
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(60 * time.Second)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(ticketSecret))
}

// ParseToken 解析并校验 JWT。
func ParseToken(secret, tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}
