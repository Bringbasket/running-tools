package mail

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/activitylog"
	"github.com/Bringbasket/running-tools/internal/platform/httpx"
)

type mailLogEvent struct {
	Category string
	Action   string
	Summary  string
}

var mailLogEvents = map[string]mailLogEvent{
	"GET /aliases":                     {"alias", "alias.list", "读取邮箱列表"},
	"POST /aliases":                    {"alias", "alias.create", "创建隐藏邮箱"},
	"GET /aliases/export.csv":          {"alias", "alias.export", "导出邮箱列表"},
	"POST /aliases/{id}/enable":        {"alias", "alias.enable", "启用隐藏邮箱"},
	"POST /aliases/{id}/disable":       {"alias", "alias.disable", "停用隐藏邮箱"},
	"POST /aliases/{id}/delete":        {"alias", "alias.delete", "删除隐藏邮箱"},
	"POST /aliases/{id}/update":        {"alias", "alias.update", "更新邮箱信息"},
	"POST /aliases/{id}/share-links":   {"alias", "alias.share.create", "生成收件分享链接"},
	"POST /share-links/{id}/revoke":    {"alias", "alias.share.revoke", "撤销收件分享链接"},
	"POST /share-links/clear-inactive": {"alias", "alias.share.clear", "清理失效分享记录"},
	"POST /alias-queue":                {"automation", "alias.queue.create", "提交批量创建队列"},
	"POST /alias-queue/pause":          {"automation", "alias.queue.pause", "暂停批量创建队列"},
	"POST /alias-queue/resume":         {"automation", "alias.queue.resume", "继续批量创建队列"},
	"POST /alias-queue/cancel":         {"automation", "alias.queue.cancel", "取消批量创建队列"},
	"PUT /mail/settings":               {"mailbox", "mailbox.settings.update", "更新 IMAP 设置"},
	"POST /mail/settings/test":         {"mailbox", "mailbox.settings.test", "测试 IMAP 连接"},
	"POST /mail/sync/run":              {"mailbox", "mailbox.sync.manual", "手动同步收件箱"},
	"POST /mail/messages/{uid}/hide":   {"mailbox", "mailbox.message.hide", "隐藏本地邮件"},
	"POST /mail/messages/hide-batch":   {"mailbox", "mailbox.message.hide_batch", "批量隐藏本地邮件"},
	"POST /mail/messages/clear":        {"mailbox", "mailbox.message.clear", "清理收件箱缓存"},
	"POST /session/refresh":            {"session", "session.check.manual", "手动检查 Session"},
	"POST /session/import":             {"session", "session.import", "导入 iCloud Web Session"},
	"POST /session/apple-login/start":  {"session", "session.apple_login.start", "开始 Apple Account 登录"},
	"POST /session/apple-login/verify": {"session", "session.apple_login.verify", "验证 Apple Account 登录"},
	"POST /auto-refresh":               {"session", "session.auto_refresh.update", "更新 Session 自动检测设置"},
	"POST /auto-refresh/run":           {"session", "session.auto_refresh.run", "立即执行 Session 自动检测"},
	"POST /create-schedule":            {"automation", "alias.schedule.update", "更新自动创建计划"},
	"POST /create-schedule/run":        {"automation", "alias.schedule.run", "立即执行自动创建"},
	"POST /create-schedule/stop":       {"automation", "alias.schedule.stop", "暂停自动创建计划"},
}

func mailLogDefinition(method, path string) (mailLogEvent, bool) {
	event, ok := mailLogEvents[method+" "+path]
	return event, ok
}

func (api *routeAPI) activityLogMiddleware(event mailLogEvent, method, path string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			recorder := &activityStatusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(recorder, r)
			outcome, level, summary := "success", "info", event.Summary+"成功"
			detail := ""
			if recorder.status >= http.StatusBadRequest {
				outcome, level, summary = "failure", "error", event.Summary+"失败"
				detail = recorder.errorDetail
				if recorder.status == http.StatusConflict || recorder.status == http.StatusTooManyRequests {
					level = "warning"
				}
			}
			if logs := api.logsFor(r); logs != nil {
				logs.Record(r.Context(), activitylog.Input{Category: event.Category, Action: event.Action, Level: level, Outcome: outcome,
					Summary: summary, Source: "user", Method: method, Path: path, HTTPStatus: recorder.status,
					DurationMS: time.Since(started).Milliseconds(), RequestID: httpx.RequestID(r.Context()),
					Detail: detail, Metadata: map[string]any{"errorCode": recorder.errorCode}})
			}
		})
	}
}

type activityStatusRecorder struct {
	http.ResponseWriter
	status      int
	errorCode   string
	errorDetail string
}

func (recorder *activityStatusRecorder) WriteHeader(status int) {
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *activityStatusRecorder) SetErrorCode(code string) { recorder.errorCode = code }
func (recorder *activityStatusRecorder) SetErrorDetail(detail string) {
	detail = strings.TrimSpace(detail)
	if len(detail) > 2000 {
		detail = detail[:2000]
	}
	recorder.errorDetail = detail
}

func (api *routeAPI) activityLogs(w http.ResponseWriter, r *http.Request) {
	query := activitylog.Query{Page: intQuery(r, "page", 1), PageSize: intQuery(r, "pageSize", 10), Search: r.URL.Query().Get("search"),
		Level: r.URL.Query().Get("level"), Category: r.URL.Query().Get("category"), Source: r.URL.Query().Get("source")}
	var err error
	if raw := strings.TrimSpace(r.URL.Query().Get("start")); raw != "" {
		value, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", "start 必须是 RFC3339 时间")
			return
		}
		query.StartTime = &value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("end")); raw != "" {
		value, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", "end 必须是 RFC3339 时间")
			return
		}
		query.EndTime = &value
	}
	page, err := api.logsFor(r).Query(r.Context(), query)
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "LOG_STORAGE_ERROR", "读取使用日志失败")
		return
	}
	httpx.WriteData(w, r, http.StatusOK, page)
}

func (api *routeAPI) clearActivityLogs(w http.ResponseWriter, r *http.Request) {
	if err := api.logsFor(r).Clear(r.Context()); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "LOG_STORAGE_ERROR", "清理使用日志失败")
		return
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"cleared": true})
}

func intQuery(r *http.Request, name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get(name)))
	if err != nil || value < 1 {
		return fallback
	}
	return value
}
