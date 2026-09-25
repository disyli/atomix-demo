package middleware

import (
	"net/http"
	"strings"

	"atomix-demo/server/internal/auth"
	"github.com/gin-gonic/gin"
)

// UserIdentity 通过 Authorization Bearer 头解析当前用户（长期登录 token）。
// 短期票据（ticket= query 参数）仅被 ticketOrBearer 中间件接受，
// 用于 preview/source/generate 等 URL 传参场景；其他接口拒绝 ticket，
// 防止票据被用于写操作（创建项目、迭代等）。
func UserIdentity(jwtSecret, _ string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 短期票据严禁在写接口组使用：返回 401 而不是静默降级
		if c.Query("ticket") != "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "此接口不接受票据，请使用 Bearer token"})
			return
		}
		// 标准 Bearer token
		token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		claims, err := auth.ParseToken(jwtSecret, token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		// 拒绝票据伪造成普通 token 的情形（Use != "" && Use != "token"）
		if claims.Use == "ticket" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "ticket 不可用于此接口"})
			return
		}
		c.Set("uid", claims.UserID)
		c.Set("email", claims.Email)
		c.Next()
	}
}

// UID 从上下文取出当前用户 ID。
func UID(c *gin.Context) uint {
	if v, ok := c.Get("uid"); ok {
		if id, ok := v.(uint); ok {
			return id
		}
	}
	return 0
}