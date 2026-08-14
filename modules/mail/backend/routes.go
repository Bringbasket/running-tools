package mail

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/activitylog"
	"github.com/Bringbasket/running-tools/internal/platform/httpx"
)

type routeAPI struct {
	module          *Module
	proxyTestTarget string
	session         *SessionManager
	refresh         *AutoRefresh
	creation        *CreateScheduler
	queue           *AliasQueue
	shares          *ShareLinkStore
	mailbox         *MailboxService
	logs            *activitylog.Store
	createGate      *sync.Mutex
}

func (api *routeAPI) register(mux *http.ServeMux, auth httpx.Middleware, base string, legacy bool) {
	protect := func(method, path string, handler http.HandlerFunc) {
		wrapped := http.Handler(handler)
		if definition, ok := mailLogDefinition(method, path); ok {
			wrapped = api.activityLogMiddleware(definition, method, base+path)(wrapped)
		}
		wrapped = api.accountMiddleware(wrapped)
		mux.Handle(method+" "+base+path, auth(wrapped))
	}
	if !legacy {
		mux.Handle("GET "+base+"/accounts", auth(http.HandlerFunc(api.accounts)))
		accountAction := func(method, path string, handler http.HandlerFunc) {
			wrapped := http.Handler(handler)
			if definition, ok := mailLogDefinition(method, path); ok {
				wrapped = api.activityLogMiddlewareWithStore(definition, method, base+path, api.accountUsageLogStore)(wrapped)
			}
			mux.Handle(method+" "+base+path, auth(wrapped))
		}
		accountAction(http.MethodPost, "/accounts", api.createAccount)
		accountAction(http.MethodPost, "/accounts/{id}/proxy/test", api.testAccountProxy)
		accountAction(http.MethodPut, "/accounts/{id}/proxy", api.updateAccountProxy)
		accountAction(http.MethodDelete, "/accounts/{id}", api.deleteAccount)
	}
	protect(http.MethodGet, "/aliases", api.listAliases)
	protect(http.MethodPost, "/aliases", api.createAlias)
	if legacy {
		protect(http.MethodGet, "/aliases/export.csv", api.exportAliasesLegacy)
	} else {
		protect(http.MethodGet, "/aliases/export.csv", api.exportAliasesCSV)
	}
	protect(http.MethodPost, "/aliases/{id}/enable", api.aliasAction(true, false))
	protect(http.MethodPost, "/aliases/{id}/disable", api.aliasAction(false, false))
	protect(http.MethodPost, "/aliases/{id}/delete", api.aliasAction(false, true))
	protect(http.MethodPost, "/aliases/{id}/update", api.updateAlias)
	protect(http.MethodGet, "/aliases/{id}/share-links", api.shareLinks)
	protect(http.MethodPost, "/aliases/{id}/share-links", api.createShareLink)
	protect(http.MethodPost, "/share-links/{id}/revoke", api.revokeShareLink)
	protect(http.MethodPost, "/share-links/clear-inactive", api.clearInactiveShareLinks)
	protect(http.MethodGet, "/mail/sync/status", api.mailboxStatus)
	protect(http.MethodPost, "/mail/sync/run", api.mailboxRun)
	protect(http.MethodGet, "/mail/settings", api.mailboxSettings)
	protect(http.MethodPut, "/mail/settings", api.mailboxSettingsUpdate)
	protect(http.MethodPost, "/mail/settings/test", api.mailboxSettingsTest)
	protect(http.MethodGet, "/mail/messages", api.mailMessages)
	protect(http.MethodGet, "/mail/recent", api.mailRecent)
	protect(http.MethodGet, "/mail/messages/{uid}", api.mailMessage)
	protect(http.MethodPost, "/mail/messages/{uid}/hide", api.mailHide)
	protect(http.MethodPost, "/mail/messages/hide-batch", api.mailHideBatch)
	protect(http.MethodPost, "/mail/messages/clear", api.mailClear)
	protect(http.MethodGet, "/mail/sync/wait", api.mailboxWait)
	protect(http.MethodGet, "/alias-queue", api.aliasQueueStatus)
	protect(http.MethodPost, "/alias-queue", api.aliasQueueEnqueue)
	protect(http.MethodPost, "/alias-queue/pause", api.aliasQueuePause)
	protect(http.MethodPost, "/alias-queue/resume", api.aliasQueueResume)
	protect(http.MethodPost, "/alias-queue/cancel", api.aliasQueueCancel)
	protect(http.MethodGet, "/session/status", api.sessionStatus)
	protect(http.MethodPost, "/session/refresh", api.sessionRefresh)
	protect(http.MethodPost, "/session/import", api.sessionImport)
	protect(http.MethodPost, "/session/apple-login/start", api.appleLoginStart)
	protect(http.MethodPost, "/session/apple-login/verify", api.appleLoginVerify)
	protect(http.MethodGet, "/auto-refresh", api.autoRefreshStatus)
	protect(http.MethodPost, "/auto-refresh", api.autoRefreshUpdate)
	protect(http.MethodPost, "/auto-refresh/run", api.autoRefreshRun)
	protect(http.MethodGet, "/create-schedule", api.createScheduleStatus)
	protect(http.MethodPost, "/create-schedule", api.createScheduleUpdate)
	protect(http.MethodPost, "/create-schedule/run", api.createScheduleRun)
	protect(http.MethodPost, "/create-schedule/stop", api.createScheduleStop)
	if !legacy {
		protect(http.MethodGet, "/activity-logs", api.activityLogs)
		protect(http.MethodPost, "/activity-logs/clear", api.clearActivityLogs)
	}
	if !legacy {
		mux.HandleFunc("GET /share", api.sharePage)
		mux.HandleFunc("GET /share/", api.sharePage)
		mux.HandleFunc("GET /share/share.css", api.shareCSS)
		mux.HandleFunc("GET /share/share.js", api.shareJS)
		mux.HandleFunc("POST /share/v1/session", api.shareSession)
		mux.HandleFunc("GET /share/v1/info", api.shareInfo)
		mux.HandleFunc("GET /share/v1/messages", api.shareMessages)
		mux.HandleFunc("GET /share/v1/messages/{uid}", api.shareMessage)
		mux.HandleFunc("GET /share/v1/sync/wait", api.shareWait)
	}
}

func (api *routeAPI) mailboxStatus(w http.ResponseWriter, r *http.Request) {
	httpx.WriteData(w, r, http.StatusOK, api.mailboxFor(r).Status())
}
func (api *routeAPI) mailboxSettings(w http.ResponseWriter, r *http.Request) {
	httpx.WriteData(w, r, http.StatusOK, api.mailboxFor(r).Settings())
}
func (api *routeAPI) mailboxSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	var payload MailboxSettingsInput
	if err := httpx.DecodeJSON(w, r, &payload, 64<<10); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	settings, err := api.mailboxFor(r).UpdateSettings(payload)
	if err != nil {
		if errors.Is(err, ErrInvalidMailboxSettings) {
			httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		httpx.WriteError(w, r, http.StatusInternalServerError, "STORAGE_ERROR", err.Error())
		return
	}
	httpx.WriteData(w, r, http.StatusOK, settings)
}
func (api *routeAPI) mailboxSettingsTest(w http.ResponseWriter, r *http.Request) {
	var payload MailboxSettingsInput
	if err := httpx.DecodeJSON(w, r, &payload, 64<<10); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if err := api.mailboxFor(r).TestSettings(payload); err != nil {
		status := http.StatusServiceUnavailable
		code := "MAILBOX_UNAVAILABLE"
		if errors.Is(err, ErrInvalidMailboxSettings) {
			status = http.StatusBadRequest
			code = "BAD_REQUEST"
		}
		httpx.WriteError(w, r, status, code, err.Error())
		return
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]bool{"connected": true})
}
func (api *routeAPI) mailboxRun(w http.ResponseWriter, r *http.Request) {
	api.mailboxFor(r).RequestSync()
	aliases, err := api.sessionFor(r).ListAliases(r.Context())
	if err != nil {
		api.writeMailError(w, r, err)
		return
	}
	addresses := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		if active, ok := alias["isActive"].(bool); ok && !active {
			continue
		}
		if address := strings.TrimSpace(fmt.Sprint(alias["hme"])); address != "" {
			addresses = append(addresses, address)
		}
	}
	status, err := api.mailboxFor(r).RunSync(addresses)
	if err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "MAILBOX_UNAVAILABLE", err.Error())
		return
	}
	httpx.WriteData(w, r, http.StatusOK, status)
}
func (api *routeAPI) mailMessages(w http.ResponseWriter, r *http.Request) {
	alias := strings.TrimSpace(r.URL.Query().Get("alias"))
	if alias == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", "alias is required")
		return
	}
	limit := 10
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	httpx.WriteData(w, r, http.StatusOK, api.mailboxFor(r).Messages(alias, limit))
}

func (api *routeAPI) mailRecent(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	httpx.WriteData(w, r, http.StatusOK, api.mailboxFor(r).Recent(limit))
}
func (api *routeAPI) mailMessage(w http.ResponseWriter, r *http.Request) {
	uid64, err := strconv.ParseUint(r.PathValue("uid"), 10, 32)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", "uid 无效")
		return
	}
	message, ok := api.mailboxFor(r).Message(r.URL.Query().Get("alias"), uint32(uid64))
	if !ok {
		httpx.WriteError(w, r, http.StatusNotFound, "MAIL_MESSAGE_NOT_FOUND", "未找到该邮件")
		return
	}
	httpx.WriteData(w, r, http.StatusOK, message)
}
func (api *routeAPI) mailHide(w http.ResponseWriter, r *http.Request) {
	uid64, err := strconv.ParseUint(r.PathValue("uid"), 10, 32)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", "uid 无效")
		return
	}
	payload := struct {
		Alias             string `json:"alias"`
		UIDValidity       uint32 `json:"uidValidity"`
		MailboxGeneration string `json:"mailboxGeneration"`
	}{}
	if err := httpx.DecodeJSON(w, r, &payload, 64<<10); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	result, err := api.mailboxFor(r).Hide(payload.Alias, uint32(uid64), payload.UIDValidity, payload.MailboxGeneration)
	if err != nil {
		httpx.WriteError(w, r, http.StatusConflict, "MAILBOX_GENERATION_CHANGED", err.Error())
		return
	}
	httpx.WriteData(w, r, http.StatusOK, result)
}
func (api *routeAPI) mailHideBatch(w http.ResponseWriter, r *http.Request) {
	payload := struct {
		UIDValidity       uint32 `json:"uidValidity"`
		MailboxGeneration string `json:"mailboxGeneration"`
		Messages          []struct {
			Alias string `json:"alias"`
			UID   uint32 `json:"uid"`
		} `json:"messages"`
	}{}
	if err := httpx.DecodeJSON(w, r, &payload, 1<<20); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if len(payload.Messages) < 1 || len(payload.Messages) > 200 {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", "messages 数量必须为 1 到 200")
		return
	}
	result, err := api.mailboxFor(r).HideBatch(payload.Messages, payload.UIDValidity, payload.MailboxGeneration)
	if err != nil {
		httpx.WriteError(w, r, http.StatusConflict, "MAILBOX_GENERATION_CHANGED", err.Error())
		return
	}
	httpx.WriteData(w, r, http.StatusOK, result)
}

func (api *routeAPI) mailClear(w http.ResponseWriter, r *http.Request) {
	if err := api.mailboxFor(r).Clear(r.Context()); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "MAILBOX_STORAGE_ERROR", "清理收件箱缓存失败")
		return
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]bool{"cleared": true})
}
func (api *routeAPI) mailboxWait(w http.ResponseWriter, r *http.Request) {
	revision, _ := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
	seconds, _ := strconv.Atoi(r.URL.Query().Get("timeout"))
	if seconds < 1 {
		seconds = 25
	}
	if seconds > 30 {
		seconds = 30
	}
	httpx.WriteData(w, r, http.StatusOK, api.mailboxFor(r).WaitForRevision(revision, time.Duration(seconds)*time.Second))
}

func shareJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": status < 400, "data": payload})
}

func (api *routeAPI) sharePage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>隐藏邮箱分享</title><link rel="stylesheet" href="/share/share.css"><script src="/share/share.js" defer></script></head><body><main><header><div><h1>共享收件箱</h1><p id="alias" class="muted"></p></div><span>只读</span></header><p id="status" class="muted">正在验证分享链接...</p><section id="content" hidden><div id="messages"></div></section><dialog id="detail"><button id="close" aria-label="关闭">×</button><small id="detail-meta" class="muted"></small><h2 id="detail-title"></h2><div id="detail-codes"></div><pre id="detail-text"></pre></dialog></main></body></html>`)
}

func (api *routeAPI) shareCSS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = io.WriteString(w, `*{box-sizing:border-box}body{font-family:Inter,system-ui,"PingFang SC",sans-serif;max-width:820px;margin:36px auto;padding:0 16px;color:#172033;background:#f6f8fb}main{overflow:hidden;background:#fff;border:1px solid #dfe5ec;border-radius:8px}header{display:flex;align-items:flex-start;justify-content:space-between;gap:18px;padding:22px;border-bottom:1px solid #e8edf2}h1{margin:0;font-size:20px}header p{margin:5px 0 0}header>span{padding:4px 8px;color:#047857;background:#ecfdf5;border-radius:5px;font-size:11px;font-weight:700}.muted{color:#687386;font-size:12px}#status{padding:18px 22px}.mail{display:block;width:100%;padding:16px 22px;color:inherit;background:#fff;border:0;border-top:1px solid #e8edf2;text-align:left}.mail:hover{background:#f8fafc}.mail h3{margin:5px 0;font-size:14px}.mail p{margin:0;overflow:hidden;color:#687386;font-size:12px;text-overflow:ellipsis;white-space:nowrap}.code{display:inline-flex;margin:3px 5px 3px 0;padding:4px 8px;color:#0f766e;background:#f0fdfa;border:0;border-radius:5px;font-weight:700}dialog{position:relative;width:min(680px,calc(100% - 28px));max-height:80vh;padding:24px;border:1px solid #dfe5ec;border-radius:8px;box-shadow:0 24px 70px #0f172a33}dialog::backdrop{background:#0f172a88}dialog h2{margin:7px 34px 14px 0;font-size:18px}dialog pre{max-height:52vh;margin:16px 0 0;padding-top:16px;overflow:auto;border-top:1px solid #e8edf2;font:13px/1.65 inherit;white-space:pre-wrap;overflow-wrap:anywhere}#close{position:absolute;top:14px;right:14px;width:34px;height:34px;color:#64748b;background:#f8fafc;border:1px solid #e2e8f0;border-radius:6px;font-size:22px}@media(max-width:600px){body{margin:12px auto;padding:0 10px}header,.mail{padding:15px}dialog{padding:18px}}`)
}

func (api *routeAPI) shareJS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = io.WriteString(w, `(async()=>{
const status=document.getElementById('status'),content=document.getElementById('content'),alias=document.getElementById('alias'),messages=document.getElementById('messages'),dialog=document.getElementById('detail');let revision=0,stopped=false;
const request=async url=>{const response=await fetch(url,{cache:'no-store'}),payload=await response.json();if(!response.ok)throw new Error(payload.data&&payload.data.error||'请求失败');return payload.data};
const codeButton=value=>{const code=document.createElement('button');code.className='code';code.textContent=value;code.title='复制';code.onclick=event=>{event.stopPropagation();navigator.clipboard.writeText(value)};return code};
const showMessage=async item=>{try{const detail=await request('/share/v1/messages/'+encodeURIComponent(item.uid));document.getElementById('detail-meta').textContent=new Date(detail.date*1000).toLocaleString('zh-CN')+' · '+(detail.from||'');document.getElementById('detail-title').textContent=detail.subject||'无主题';const codes=document.getElementById('detail-codes');codes.replaceChildren();for(const value of [...(detail.partnerCodes||[]),...(detail.codes||[])])codes.append(codeButton(value));document.getElementById('detail-text').textContent=detail.text||'';dialog.showModal()}catch(error){status.textContent=error instanceof Error?error.message:'邮件读取失败'}};
const loadMessages=async()=>{const payload=await request('/share/v1/messages?limit=50'),items=payload.messages||[];revision=payload.sync&&payload.sync.revision||revision;messages.replaceChildren();if(!items.length){const empty=document.createElement('p');empty.id='status';empty.className='muted';empty.textContent='暂无可显示的邮件';messages.append(empty);return}for(const item of items){const article=document.createElement('button');article.className='mail';article.onclick=()=>showMessage(item);const meta=document.createElement('small');meta.className='muted';meta.textContent=new Date(item.date*1000).toLocaleString('zh-CN')+' · '+(item.from||'');article.append(meta);const title=document.createElement('h3');title.textContent=item.subject||'无主题';article.append(title);const preview=document.createElement('p');preview.textContent=(item.text||'').slice(0,160);article.append(preview);messages.append(article)}};
const watch=async()=>{while(!stopped){try{const next=await request('/share/v1/sync/wait?revision='+revision+'&timeout=25');if(next.revision!==revision){revision=next.revision;await loadMessages()}}catch(error){if(!stopped)await new Promise(resolve=>setTimeout(resolve,3000))}}};
try{
  const token=location.hash.slice(1);
  if(token){const response=await fetch('/share/v1/session',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({token})});if(!response.ok)throw new Error('链接已过期或撤销');history.replaceState(null,'',location.pathname)}
  const info=await request('/share/v1/info');alias.textContent=info.alias;revision=info.sync&&info.sync.revision||0;status.textContent='';content.hidden=false;await loadMessages();watch();
}catch(error){status.textContent=error instanceof Error?error.message:'分享链接无效'}
document.getElementById('close').onclick=()=>dialog.close();window.addEventListener('beforeunload',()=>{stopped=true});
})()`)
}

func (api *routeAPI) shareSession(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&payload); err != nil {
		shareJSON(w, http.StatusBadRequest, map[string]string{"error": "令牌格式无效"})
		return
	}
	var sessionToken string
	var link ShareLink
	var ok bool
	if api.module != nil {
		for _, account := range api.module.accountList() {
			if runtime, exists := api.module.runtime(account.ID); exists {
				sessionToken, link, ok = runtime.shares.CreateSession(payload.Token, 7*24*60*60)
				if ok {
					break
				}
			}
		}
	} else {
		sessionToken, link, ok = api.shares.CreateSession(payload.Token, 7*24*60*60)
	}
	if !ok {
		shareJSON(w, http.StatusGone, map[string]string{"error": "分享链接已过期或已撤销"})
		return
	}
	maxAge := 7 * 24 * 60 * 60
	if link.ExpiresAt != nil {
		remaining := int(*link.ExpiresAt - unixNow())
		if remaining < maxAge {
			maxAge = remaining
		}
	}
	if maxAge < 1 {
		shareJSON(w, http.StatusGone, map[string]string{"error": "分享链接已过期"})
		return
	}
	secure := r.TLS != nil || strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
	http.SetCookie(w, &http.Cookie{Name: "hme_share_session", Value: sessionToken, Path: "/share", MaxAge: maxAge, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
	shareJSON(w, http.StatusOK, map[string]any{"alias": link.Alias, "expiresAt": link.ExpiresAt})
}

func (api *routeAPI) sharedAlias(r *http.Request) string {
	_, link, ok := api.sharedLink(r)
	if !ok {
		return ""
	}
	return shareAlias(link.Alias)
}

func (api *routeAPI) sharedLink(r *http.Request) (*accountRuntime, ShareLink, bool) {
	cookie, err := r.Cookie("hme_share_session")
	if err != nil {
		return nil, ShareLink{}, false
	}
	if api.module != nil {
		for _, account := range api.module.accountList() {
			if runtime, exists := api.module.runtime(account.ID); exists {
				if link, ok := runtime.shares.ResolveSession(cookie.Value); ok {
					return runtime, link, true
				}
			}
		}
		return nil, ShareLink{}, false
	}
	link, ok := api.shares.ResolveSession(cookie.Value)
	return api.runtimeFor(r), link, ok
}
func (api *routeAPI) shareInfo(w http.ResponseWriter, r *http.Request) {
	runtime, link, ok := api.sharedLink(r)
	if !ok {
		shareJSON(w, http.StatusGone, map[string]string{"error": "分享链接无效"})
		return
	}
	shareJSON(w, http.StatusOK, map[string]any{"alias": shareAlias(link.Alias), "createdAt": link.CreatedAt, "expiresAt": link.ExpiresAt, "sync": runtime.mailbox.Status()})
}
func (api *routeAPI) shareMessages(w http.ResponseWriter, r *http.Request) {
	runtime, link, ok := api.sharedLink(r)
	if !ok {
		shareJSON(w, http.StatusGone, map[string]string{"error": "分享链接无效"})
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			shareJSON(w, http.StatusBadRequest, map[string]string{"error": "limit 必须是 1 到 100 之间的整数"})
			return
		}
		limit = parsed
	}
	shareJSON(w, http.StatusOK, runtime.mailbox.Messages(shareAlias(link.Alias), limit))
}

func (api *routeAPI) shareMessage(w http.ResponseWriter, r *http.Request) {
	runtime, link, ok := api.sharedLink(r)
	if !ok {
		shareJSON(w, http.StatusGone, map[string]string{"error": "分享链接无效"})
		return
	}
	uid, err := strconv.ParseUint(r.PathValue("uid"), 10, 32)
	if err != nil || uid == 0 {
		shareJSON(w, http.StatusBadRequest, map[string]string{"error": "uid 无效"})
		return
	}
	message, ok := runtime.mailbox.Message(shareAlias(link.Alias), uint32(uid))
	if !ok {
		shareJSON(w, http.StatusNotFound, map[string]string{"error": "未找到该邮件"})
		return
	}
	shareJSON(w, http.StatusOK, message)
}

func (api *routeAPI) shareWait(w http.ResponseWriter, r *http.Request) {
	runtime, _, ok := api.sharedLink(r)
	if !ok {
		shareJSON(w, http.StatusGone, map[string]string{"error": "分享链接无效"})
		return
	}
	revision, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
	if err != nil || revision < 0 {
		shareJSON(w, http.StatusBadRequest, map[string]string{"error": "revision 必须是非负整数"})
		return
	}
	timeout := 25
	if raw := r.URL.Query().Get("timeout"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 1 || parsed > 30 {
			shareJSON(w, http.StatusBadRequest, map[string]string{"error": "timeout 必须是 1 到 30 之间的整数"})
			return
		}
		timeout = parsed
	}
	shareJSON(w, http.StatusOK, runtime.mailbox.WaitForRevision(revision, time.Duration(timeout)*time.Second))
}

func (api *routeAPI) shareLinks(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	aliases, err := api.sessionFor(r).ListAliases(r.Context())
	if err != nil {
		api.writeMailError(w, r, err)
		return
	}
	for _, a := range aliases {
		if fmt.Sprint(a["anonymousId"]) == id {
			httpx.WriteData(w, r, http.StatusOK, map[string]any{"alias": a["hme"], "links": api.sharesFor(r).List(fmt.Sprint(a["hme"]))})
			return
		}
	}
	httpx.WriteError(w, r, http.StatusNotFound, "ALIAS_NOT_FOUND", "未找到该隐藏邮箱")
}
func (api *routeAPI) createShareLink(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	aliases, err := api.sessionFor(r).ListAliases(r.Context())
	if err != nil {
		api.writeMailError(w, r, err)
		return
	}
	alias := ""
	for _, a := range aliases {
		if fmt.Sprint(a["anonymousId"]) == id {
			if active, ok := a["isActive"].(bool); ok && !active {
				httpx.WriteError(w, r, http.StatusConflict, "ALIAS_INACTIVE", "停用的隐藏邮箱不能生成分享链接")
				return
			}
			alias = fmt.Sprint(a["hme"])
			break
		}
	}
	if alias == "" {
		httpx.WriteError(w, r, http.StatusNotFound, "ALIAS_NOT_FOUND", "未找到该隐藏邮箱")
		return
	}
	payload := struct {
		ExpiresInSeconds *int `json:"expiresInSeconds"`
	}{ExpiresInSeconds: func() *int { value := 7 * 24 * 60 * 60; return &value }()}
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(w, r, &payload, 64<<10); err != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
	}
	result, err := api.sharesFor(r).Create(alias, payload.ExpiresInSeconds)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	httpx.WriteData(w, r, http.StatusOK, result)
}
func (api *routeAPI) revokeShareLink(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if !api.sharesFor(r).Revoke(id) {
		httpx.WriteError(w, r, http.StatusNotFound, "SHARE_LINK_NOT_FOUND", "未找到可撤销的分享链接")
		return
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"id": id, "revoked": true})
}

func (api *routeAPI) clearInactiveShareLinks(w http.ResponseWriter, r *http.Request) {
	deleted, err := api.sharesFor(r).ClearInactive()
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "SHARE_STORAGE_ERROR", "清理失效分享记录失败")
		return
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"cleared": true, "deleted": deleted})
}

func (api *routeAPI) updateAlias(w http.ResponseWriter, r *http.Request) {
	id, err := url.PathUnescape(strings.TrimSpace(r.PathValue("id")))
	if err != nil || id == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", "anonymous id is required")
		return
	}
	payload := struct {
		Label string `json:"label"`
		Note  string `json:"note"`
	}{}
	if err := httpx.DecodeJSON(w, r, &payload, 64<<10); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	payload.Label = strings.TrimSpace(payload.Label)
	if payload.Label == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", "label is required")
		return
	}
	result, err := api.sessionFor(r).UpdateAlias(r.Context(), id, payload.Label, payload.Note)
	if err != nil {
		api.writeMailError(w, r, err)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, result)
}

func (api *routeAPI) aliasQueueStatus(w http.ResponseWriter, r *http.Request) {
	httpx.WriteData(w, r, http.StatusOK, api.queueFor(r).Status())
}

func (api *routeAPI) aliasQueueEnqueue(w http.ResponseWriter, r *http.Request) {
	payload := struct {
		BaseLabel string `json:"baseLabel"`
		Count     int    `json:"count"`
		Note      string `json:"note"`
		RequestID string `json:"requestId"`
	}{}
	if err := httpx.DecodeJSON(w, r, &payload, 64<<10); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	result, err := api.queueFor(r).Enqueue(r.Context(), payload.BaseLabel, payload.Count, payload.Note, payload.RequestID)
	if err != nil {
		api.writeQueueError(w, r, err)
		return
	}
	httpx.WriteData(w, r, http.StatusAccepted, result)
}

func (api *routeAPI) aliasQueuePause(w http.ResponseWriter, r *http.Request) {
	api.queueControl(w, r, "pause")
}
func (api *routeAPI) aliasQueueResume(w http.ResponseWriter, r *http.Request) {
	api.queueControl(w, r, "resume")
}
func (api *routeAPI) aliasQueueCancel(w http.ResponseWriter, r *http.Request) {
	api.queueControl(w, r, "cancel")
}
func (api *routeAPI) queueControl(w http.ResponseWriter, r *http.Request, action string) {
	payload := struct {
		JobID            string `json:"jobId"`
		ConfirmUncertain bool   `json:"confirmUncertain"`
	}{}
	if err := httpx.DecodeJSON(w, r, &payload, 64<<10); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	var result AliasQueueStatus
	var err error
	switch action {
	case "pause":
		result, err = api.queueFor(r).Pause(payload.JobID)
	case "resume":
		result, err = api.queueFor(r).Resume(payload.JobID, payload.ConfirmUncertain)
	case "cancel":
		result, err = api.queueFor(r).Cancel(payload.JobID)
	}
	if err != nil {
		api.writeQueueError(w, r, err)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, result)
}

func (api *routeAPI) writeQueueError(w http.ResponseWriter, r *http.Request, err error) {
	var conflict *AliasQueueError
	if errors.As(err, &conflict) {
		status := http.StatusBadRequest
		if conflict.Conflict {
			status = http.StatusConflict
		}
		httpx.WriteError(w, r, status, conflict.Code, conflict.Error())
		return
	}
	httpx.WriteError(w, r, http.StatusInternalServerError, "QUEUE_ERROR", err.Error())
}

func (api *routeAPI) listAliases(w http.ResponseWriter, r *http.Request) {
	aliases, source, err := api.sessionFor(r).listAliases(r.Context())
	if err != nil {
		api.writeMailError(w, r, err)
		return
	}
	w.Header().Set("X-Running-Mail-Source", source)
	httpx.WriteData(w, r, http.StatusOK, aliases)
}

func (api *routeAPI) createAlias(w http.ResponseWriter, r *http.Request) {
	if api.queueFor(r).Active() || api.creationFor(r).Running() {
		httpx.WriteError(w, r, http.StatusConflict, "CREATE_IN_PROGRESS", "后台创建任务正在执行，请稍后再试")
		return
	}
	payload := struct {
		Label string `json:"label"`
		Note  string `json:"note"`
	}{}
	if err := httpx.DecodeJSON(w, r, &payload, 64<<10); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	payload.Label = strings.TrimSpace(payload.Label)
	if payload.Label == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", "label is required")
		return
	}
	api.createGateFor(r).Lock()
	alias, err := api.sessionFor(r).CreateAlias(r.Context(), payload.Label, payload.Note)
	api.createGateFor(r).Unlock()
	if err != nil {
		api.writeMailError(w, r, err)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, alias)
}

func (api *routeAPI) exportAliasesLegacy(w http.ResponseWriter, r *http.Request) {
	csvText, err := api.aliasesCSV(r)
	if err != nil {
		api.writeMailError(w, r, err)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, csvText)
}

func (api *routeAPI) exportAliasesCSV(w http.ResponseWriter, r *http.Request) {
	csvText, err := api.aliasesCSV(r)
	if err != nil {
		api.writeMailError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="hide-my-email.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(csvText))
}

func (api *routeAPI) aliasesCSV(r *http.Request) (string, error) {
	aliases, err := api.sessionFor(r).ListAliases(r.Context())
	if err != nil {
		return "", err
	}
	return AliasesCSV(aliases)
}

func (api *routeAPI) aliasAction(active, deleteAlias bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := url.PathUnescape(strings.TrimSpace(r.PathValue("id")))
		if err != nil || id == "" {
			httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", "anonymous id is required")
			return
		}
		// Capture the address before a delete; iCloud no longer returns the
		// deleted record in a subsequent list response.
		var aliasAddress string
		if deleteAlias || !active {
			if aliases, listErr := api.sessionFor(r).ListAliases(r.Context()); listErr == nil {
				for _, alias := range aliases {
					if fmt.Sprint(alias["anonymousId"]) == id {
						aliasAddress = fmt.Sprint(alias["hme"])
						break
					}
				}
			}
		}
		var result map[string]any
		if deleteAlias {
			result, err = api.sessionFor(r).DeleteAlias(r.Context(), id)
		} else {
			result, err = api.sessionFor(r).SetAliasActive(r.Context(), id, active)
		}
		if err != nil {
			api.writeMailError(w, r, err)
			return
		}
		if aliasAddress != "" && (deleteAlias || !active) {
			_ = api.sharesFor(r).RevokeForAlias(aliasAddress)
		}
		httpx.WriteData(w, r, http.StatusOK, result)
	}
}

func (api *routeAPI) sessionStatus(w http.ResponseWriter, r *http.Request) {
	httpx.WriteData(w, r, http.StatusOK, api.sessionFor(r).Status())
}

func (api *routeAPI) sessionRefresh(w http.ResponseWriter, r *http.Request) {
	httpx.WriteData(w, r, http.StatusOK, api.sessionFor(r).Check(r.Context()))
}

func (api *routeAPI) sessionImport(w http.ResponseWriter, r *http.Request) {
	payload := struct {
		CurlText string `json:"curl_text"`
		Region   string `json:"icloud_region"`
	}{}
	if err := httpx.DecodeJSON(w, r, &payload, 16<<20); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if api.queueFor(r) != nil && api.queueFor(r).Active() {
		incoming, parseErr := ParseImportText(payload.CurlText, payload.Region)
		if parseErr != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", parseErr.Error())
			return
		}
		if expected := api.queueFor(r).AccountDSID(); expected != "" && incoming.DSID != expected {
			httpx.WriteError(w, r, http.StatusConflict, "QUEUE_ACCOUNT_MISMATCH", "当前批量队列绑定了另一个 iCloud 账号，请完成或取消队列后再切换 Session")
			return
		}
	}
	result, err := api.sessionFor(r).Import(payload.CurlText, payload.Region)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if config, loadErr := api.sessionFor(r).Client(); loadErr == nil && api.module != nil {
		api.module.updateAccountIdentity(api.runtimeFor(r).account.ID, &config.config, "")
	}
	httpx.WriteData(w, r, http.StatusOK, result)
}

func (api *routeAPI) appleLoginStart(w http.ResponseWriter, r *http.Request) {
	var payload AppleLoginStartInput
	if err := httpx.DecodeJSON(w, r, &payload, 64<<10); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	expectedDSID := ""
	if api.queueFor(r).Active() {
		expectedDSID = api.queueFor(r).AccountDSID()
	}
	result, err := api.sessionFor(r).StartAppleLogin(r.Context(), payload, expectedDSID)
	if err != nil {
		api.writeAppleLoginError(w, r, err)
		return
	}
	if !result.Needs2FA && api.module != nil {
		api.module.updateAccountIdentity(api.runtimeFor(r).account.ID, result.webConfig, result.AppleID)
	}
	httpx.WriteData(w, r, http.StatusOK, result)
}

func (api *routeAPI) appleLoginVerify(w http.ResponseWriter, r *http.Request) {
	payload := struct {
		PendingID string `json:"pendingId"`
		Code      string `json:"code"`
	}{}
	if err := httpx.DecodeJSON(w, r, &payload, 64<<10); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	expectedDSID := ""
	if api.queueFor(r) != nil && api.queueFor(r).Active() && api.sessionFor(r).PendingAppleLoginChannel(payload.PendingID) == AppleChannelICloudWeb {
		expectedDSID = api.queueFor(r).AccountDSID()
	}
	result, err := api.sessionFor(r).VerifyAppleLogin(r.Context(), payload.PendingID, payload.Code, expectedDSID)
	if err != nil {
		api.writeAppleLoginError(w, r, err)
		return
	}
	if api.module != nil {
		api.module.updateAccountIdentity(api.runtimeFor(r).account.ID, result.webConfig, result.AppleID)
	}
	httpx.WriteData(w, r, http.StatusOK, result)
}

func (api *routeAPI) writeAppleLoginError(w http.ResponseWriter, r *http.Request, err error) {
	var protocol *AppleProtocolError
	if errors.As(err, &protocol) {
		status := http.StatusBadGateway
		if !protocol.Retryable {
			status = http.StatusBadRequest
		}
		httpx.WriteError(w, r, status, protocol.Code, protocol.Message)
		return
	}
	httpx.WriteError(w, r, http.StatusBadGateway, "APPLE_LOGIN_FAILED", safeErrorText(err))
}

func (api *routeAPI) autoRefreshStatus(w http.ResponseWriter, r *http.Request) {
	httpx.WriteData(w, r, http.StatusOK, api.refreshFor(r).Status())
}

func (api *routeAPI) autoRefreshUpdate(w http.ResponseWriter, r *http.Request) {
	payload := struct {
		Enabled         *bool `json:"enabled"`
		IntervalSeconds *int  `json:"intervalSeconds"`
	}{}
	if err := httpx.DecodeJSON(w, r, &payload, 64<<10); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	result, err := api.refreshFor(r).Update(payload.Enabled, payload.IntervalSeconds)
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "STORAGE_ERROR", err.Error())
		return
	}
	httpx.WriteData(w, r, http.StatusOK, result)
}

func (api *routeAPI) autoRefreshRun(w http.ResponseWriter, r *http.Request) {
	result, err := api.refreshFor(r).Run(r.Context())
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "STORAGE_ERROR", err.Error())
		return
	}
	httpx.WriteData(w, r, http.StatusOK, result)
}

func (api *routeAPI) createScheduleStatus(w http.ResponseWriter, r *http.Request) {
	httpx.WriteData(w, r, http.StatusOK, api.creationFor(r).Status())
}

func (api *routeAPI) createScheduleUpdate(w http.ResponseWriter, r *http.Request) {
	payload := struct {
		Enabled              *bool   `json:"enabled"`
		BatchSize            *int    `json:"batchSize"`
		AliasIntervalSeconds *int    `json:"aliasIntervalSeconds"`
		IntervalSeconds      *int    `json:"intervalSeconds"`
		Label                *string `json:"label"`
		Note                 *string `json:"note"`
	}{}
	if err := httpx.DecodeJSON(w, r, &payload, 64<<10); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	result, err := api.creationFor(r).Update(payload.Enabled, payload.BatchSize, payload.AliasIntervalSeconds, payload.IntervalSeconds, payload.Label, payload.Note)
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "STORAGE_ERROR", err.Error())
		return
	}
	httpx.WriteData(w, r, http.StatusOK, result)
}

func (api *routeAPI) createScheduleRun(w http.ResponseWriter, r *http.Request) {
	if err := api.creationFor(r).RunNow(); err != nil {
		if errors.Is(err, ErrCreateInProgress) {
			httpx.WriteError(w, r, http.StatusConflict, "CREATE_IN_PROGRESS", err.Error())
			return
		}
		httpx.WriteError(w, r, http.StatusInternalServerError, "CREATE_FAILED", err.Error())
		return
	}
	httpx.WriteData(w, r, http.StatusAccepted, api.creationFor(r).Status())
}

func (api *routeAPI) createScheduleStop(w http.ResponseWriter, r *http.Request) {
	result, err := api.creationFor(r).Stop()
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "STORAGE_ERROR", err.Error())
		return
	}
	httpx.WriteData(w, r, http.StatusOK, result)
}

func (api *routeAPI) writeMailError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrSessionMissing) {
		httpx.WriteError(w, r, http.StatusConflict, "SESSION_MISSING", err.Error())
		return
	}
	if isSessionExpired(err) {
		httpx.WriteError(w, r, http.StatusConflict, "SESSION_EXPIRED", err.Error())
		return
	}
	var upstream *UpstreamError
	var apple *AppleError
	var protocol *AppleProtocolError
	if errors.As(err, &protocol) {
		httpx.WriteError(w, r, http.StatusBadGateway, protocol.Code, protocol.Message)
		return
	}
	if errors.As(err, &upstream) || errors.As(err, &apple) {
		httpx.WriteError(w, r, http.StatusBadGateway, "ICLOUD_ERROR", err.Error())
		return
	}
	httpx.WriteError(w, r, http.StatusBadGateway, "ICLOUD_ERROR", fmt.Sprint(err))
}
