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
	// TV 签发时用户的 TokenVersion 快照。退出登录会使 User.TokenVersion+1，
	// 中间件比对 TV 与库内当前值，不一致即判定该 token 已被吊销（即时失效，无需等 7 天过期）。
	TV int `json:"tv"`
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

// IssueToken 签发 7 天有效期的 JWT（不绑定版本号，兼容旧调用点，tv=0）。
func IssueToken(secret string, userID uint, email string) (string, error) {
	return IssueTokenV(secret, userID, email, 0)
}

// IssueTokenV 签发 7 天有效期的 JWT，并写入调用方传入的 tokenVersion 快照。
// 退出登录后旧 token 的 tv 与库内新版本号不一致，中间件据此立即拒绝，不必等待自然过期。
func IssueTokenV(secret string, userID uint, email string, tokenVersion int) (string, error) {
	claims := Claims{
		UserID: userID,
		Email:  email,
		TV:     tokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// IssueTicket 签发 60 秒有效期的短期票据（预览/下载/SSE 专用）。
// Claims.Use = "ticket" 标记用途，jti 为随机 ID（防票据被客户端缓存后反复使用）。
func IssueTicket(ticketSecret string, userID uint, email string) (string, error) {
	return IssueTicketTTL(ticketSecret, userID, email, 60*time.Second)
}

// IssueTicketTTL 指定有效期签发票据：IssueTicket 的参数化版本。
// 测试用极短 TTL 走真实的 jwt 过期校验路径（ExpiresAt 由解析器强制验证）。
func IssueTicketTTL(ticketSecret string, userID uint, email string, ttl time.Duration) (string, error) {
	jti := fmt.Sprintf("%d-%x", userID, time.Now().UnixNano())
	claims := Claims{
		UserID: userID,
		Email:  email,
		Use:    "ticket",
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
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
