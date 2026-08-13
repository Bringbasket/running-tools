package systemupdate

import (
	"errors"
	"net/http"

	"github.com/Bringbasket/running-tools/internal/platform/httpx"
)

func RegisterRoutes(mux *http.ServeMux, auth httpx.Middleware, service *Service) {
	versionHandler := auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteData(w, r, http.StatusOK, service.Status())
	}))
	checkHandler := auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, err := service.Check()
		if errors.Is(err, ErrInProgress) {
			httpx.WriteError(w, r, http.StatusConflict, "UPDATE_IN_PROGRESS", "已有版本任务正在运行")
			return
		}
		if err != nil {
			httpx.WriteError(w, r, http.StatusInternalServerError, "STORAGE_ERROR", err.Error())
			return
		}
		httpx.WriteData(w, r, http.StatusAccepted, status)
	}))
	updateHandler := auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, err := service.Request()
		if errors.Is(err, ErrInProgress) {
			httpx.WriteError(w, r, http.StatusConflict, "UPDATE_IN_PROGRESS", "已有更新任务正在运行")
			return
		}
		if errors.Is(err, ErrCheckRequired) {
			httpx.WriteError(w, r, http.StatusConflict, "UPDATE_CHECK_REQUIRED", "请先检查是否有可用更新")
			return
		}
		if errors.Is(err, ErrNoUpdateAvailable) {
			httpx.WriteError(w, r, http.StatusConflict, "NO_UPDATE_AVAILABLE", "当前已经是最新版本")
			return
		}
		if err != nil {
			httpx.WriteError(w, r, http.StatusInternalServerError, "STORAGE_ERROR", err.Error())
			return
		}
		httpx.WriteData(w, r, http.StatusAccepted, status)
	}))

	for _, path := range []string{"/api/system/version", "/v1/system/version"} {
		mux.Handle("GET "+path, versionHandler)
	}
	for _, path := range []string{"/api/system/version/check", "/v1/system/version/check"} {
		mux.Handle("POST "+path, checkHandler)
	}
	for _, path := range []string{"/api/system/update", "/v1/system/update"} {
		mux.Handle("POST "+path, updateHandler)
	}
}
