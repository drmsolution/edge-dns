package admin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/user/edge-dns/internal/sync"
)

type AdminService struct {
	rdb    *redis.Client
	chdb   clickhouse.Conn
	apiKey string
}

func New(rdb *redis.Client, chdb clickhouse.Conn, apiKey string) *AdminService {
	return &AdminService{rdb: rdb, chdb: chdb, apiKey: apiKey}
}

func (s *AdminService) authMiddleware() gin.HandlerFunc {
	if s.apiKey == "" {
		return func(c *gin.Context) {
			c.Next()
		}
	}
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing Authorization header"})
			return
		}
		var token string
		if _, err := fmt.Sscanf(header, "Bearer %s", &token); err != nil || token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid Authorization format, expected: Bearer <token>"})
			return
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.apiKey)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
			return
		}
		c.Next()
	}
}

func (s *AdminService) SetupRouter() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())

	r.GET("/", s.dashboardHandler)

	api := r.Group("/api/v1")
	api.Use(s.authMiddleware())
	{
		api.POST("/rules", s.addRule)
		api.DELETE("/rules", s.removeRule)
		api.GET("/rules", s.listRules)

		api.POST("/redirects", s.addRedirect)
		api.DELETE("/redirects", s.removeRedirect)
		api.GET("/redirects", s.listRedirects)

		api.GET("/analytics/summary", s.analyticsSummary)
		api.GET("/analytics/logs", s.analyticsLogs)
	}
	return r
}

type ruleRequest struct {
	UserID string `json:"user_id" binding:"required"`
	Domain string `json:"domain" binding:"required"`
	Action string `json:"action" binding:"required"`
}

func (s *AdminService) addRule(c *gin.Context) {
	var req ruleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := "user:settings:" + req.UserID + ":blocked"

	if err := s.rdb.SAdd(ctx, key, req.Domain).Err(); err != nil {
		slog.Error("redis sadd", "key", key, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save rule"})
		return
	}

	if err := s.publishClearCache(ctx, req.UserID); err != nil {
		slog.Error("redis publish", "error", err)
	}

	s.auditLog("add_rule", req.UserID, req.Domain, s.callerIP(c))
	c.JSON(http.StatusOK, gin.H{"message": "rule added"})
}

func (s *AdminService) removeRule(c *gin.Context) {
	var req ruleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := "user:settings:" + req.UserID + ":blocked"

	if err := s.rdb.SRem(ctx, key, req.Domain).Err(); err != nil {
		slog.Error("redis srem", "key", key, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove rule"})
		return
	}

	if err := s.publishClearCache(ctx, req.UserID); err != nil {
		slog.Error("redis publish", "error", err)
	}

	s.auditLog("remove_rule", req.UserID, req.Domain, s.callerIP(c))
	c.JSON(http.StatusOK, gin.H{"message": "rule removed"})
}

func (s *AdminService) listRules(c *gin.Context) {
	userID := c.Query("user_id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id query parameter is required"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := "user:settings:" + userID + ":blocked"
	domains, err := s.rdb.SMembers(ctx, key).Result()
	if err != nil {
		slog.Error("redis smembers", "key", key, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list rules"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"user_id": userID, "domains": domains})
}

type redirectRequest struct {
	UserID   string `json:"user_id" binding:"required"`
	Domain   string `json:"domain" binding:"required"`
	TargetIP string `json:"target_ip" binding:"required"`
}

func (s *AdminService) addRedirect(c *gin.Context) {
	var req redirectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := "user:settings:" + req.UserID + ":redirects"

	if err := s.rdb.HSet(ctx, key, req.Domain, req.TargetIP).Err(); err != nil {
		slog.Error("redis hset redirect", "key", key, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save redirect"})
		return
	}

	if err := s.publishClearCache(ctx, req.UserID); err != nil {
		slog.Error("redis publish", "error", err)
	}

	s.auditLog("add_redirect", req.UserID, req.Domain, s.callerIP(c))
	c.JSON(http.StatusOK, gin.H{"message": "redirect added"})
}

func (s *AdminService) removeRedirect(c *gin.Context) {
	var req redirectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := "user:settings:" + req.UserID + ":redirects"

	if err := s.rdb.HDel(ctx, key, req.Domain).Err(); err != nil {
		slog.Error("redis hdel redirect", "key", key, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove redirect"})
		return
	}

	if err := s.publishClearCache(ctx, req.UserID); err != nil {
		slog.Error("redis publish", "error", err)
	}

	s.auditLog("remove_redirect", req.UserID, req.Domain, s.callerIP(c))
	c.JSON(http.StatusOK, gin.H{"message": "redirect removed"})
}

func (s *AdminService) listRedirects(c *gin.Context) {
	userID := c.Query("user_id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id query parameter is required"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := "user:settings:" + userID + ":redirects"
	redirects, err := s.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		slog.Error("redis hgetall redirects", "key", key, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list redirects"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"user_id": userID, "redirects": redirects})
}

type summaryRow struct {
	Status string `json:"status"`
	Count  uint64 `json:"count"`
}

func (s *AdminService) analyticsSummary(c *gin.Context) {
	userID := c.Query("user_id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id query parameter is required"})
		return
	}

	if s.chdb == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "clickhouse not configured"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := s.chdb.Query(ctx,
		`SELECT status, count(*) AS cnt
		 FROM default.dns_logs
		 WHERE user_id = ?
		 GROUP BY status
		 ORDER BY cnt DESC`,
		userID,
	)
	if err != nil {
		slog.Error("clickhouse query summary", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "analytics query failed"})
		return
	}
	defer rows.Close()

	var summary []summaryRow
	for rows.Next() {
		var row summaryRow
		if err := rows.Scan(&row.Status, &row.Count); err != nil {
			slog.Error("clickhouse scan row", "error", err)
			continue
		}
		summary = append(summary, row)
	}

	c.JSON(http.StatusOK, gin.H{"user_id": userID, "summary": summary})
}

type logEntry struct {
	Timestamp    time.Time `json:"timestamp"`
	ClientIP     string    `json:"client_ip"`
	Domain       string    `json:"domain"`
	QueryType    string    `json:"query_type"`
	Status       string    `json:"status"`
	ResponseTime uint64    `json:"response_time_ns"`
}

func (s *AdminService) analyticsLogs(c *gin.Context) {
	userID := c.Query("user_id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id query parameter is required"})
		return
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 1000 {
			limit = parsed
		}
	}

	if s.chdb == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "clickhouse not configured"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := s.chdb.Query(ctx,
		`SELECT timestamp, client_ip, domain, query_type, status, response_time_ns
		 FROM default.dns_logs
		 WHERE user_id = ?
		 ORDER BY timestamp DESC
		 LIMIT ?`,
		userID, limit,
	)
	if err != nil {
		slog.Error("clickhouse query logs", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "analytics query failed"})
		return
	}
	defer rows.Close()

	var entries []logEntry
	for rows.Next() {
		var e logEntry
		if err := rows.Scan(&e.Timestamp, &e.ClientIP, &e.Domain, &e.QueryType, &e.Status, &e.ResponseTime); err != nil {
			slog.Error("clickhouse scan row", "error", err)
			continue
		}
		entries = append(entries, e)
	}

	c.JSON(http.StatusOK, gin.H{"user_id": userID, "logs": entries})
}

func (s *AdminService) auditLog(action, userID, domain, callerIP string) {
	slog.Info("admin audit",
		"action", action,
		"user_id", userID,
		"domain", domain,
		"caller_ip", callerIP,
	)
}

func (s *AdminService) callerIP(c *gin.Context) string {
	if fwd := c.GetHeader("X-Forwarded-For"); fwd != "" {
		return fwd
	}
	return c.ClientIP()
}

func (s *AdminService) publishClearCache(ctx context.Context, userID string) error {
	event := sync.ConfigEvent{
		UserID: userID,
		Action: sync.ActionClearCache,
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return s.rdb.Publish(ctx, sync.ConfigChannel, data).Err()
}
