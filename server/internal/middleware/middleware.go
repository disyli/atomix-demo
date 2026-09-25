package middleware

import (
	"net/http"
	"strings"

	"atomix-demo/server/internal/auth"
	"github.com/gin-gonic/gin"
)

// UserIdentity 通过 Authorization 头或短期票据（ticket= query 参数）解析当前用户。
// ticket= 用于 EventSource 等无法设置请求头的场景（60s 有效，不写入日志危险区）；
// 旧的 ?t=<长期token> 已废弃，不再接受，防止长期 token 出现在 nginx 访问日志。
func UserIdentity(jwtSecret, ticketSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 优先检查短期票据（EventSource / preview 场景）
		if t := c.Query("ticket"); t != "" {
			claims, err := auth.ParseToken(ticketSecret, t)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "票据无效或已过期"})
				return
			}
			c.Set("uid", claims.UserID)
			c.Set("email", claims.Email)
			c.Next()
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