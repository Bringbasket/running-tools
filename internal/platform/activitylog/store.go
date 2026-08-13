package activitylog

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/persistence"
	"github.com/Bringbasket/running-tools/internal/platform/persistence/ent"
	entactivitylog "github.com/Bringbasket/running-tools/internal/platform/persistence/ent/activitylog"
	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

const (
	DefaultRetentionDays  = 30
	DefaultMaximumEntries = 10_000
)

var sensitiveDetailPattern = regexp.MustCompile(`(?i)(authorization|cookie|set-cookie|password|secret|api[_-]?key|token|x-apple-[a-z0-9_-]+)(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;}\]]+)`)

type Entry struct {
	ID         string         `json:"id"`
	Module     string         `json:"module"`
	Category   string         `json:"category"`
	Action     string         `json:"action"`
	Level      string         `json:"level"`
	Outcome    string         `json:"outcome"`
	Summary    string         `json:"summary"`
	Source     string         `json:"source"`
	Method     string         `json:"method,omitempty"`
	Path       string         `json:"path,omitempty"`
	HTTPStatus int            `json:"httpStatus,omitempty"`
	DurationMS int64          `json:"durationMs"`
	RequestID  string         `json:"requestId,omitempty"`
	Detail     string         `json:"detail,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  time.Time      `json:"createdAt"`
}

type Input struct {
	Category   string
	Action     string
	Level      string
	Outcome    string
	Summary    string
	Source     string
	Method     string
	Path       string
	HTTPStatus int
	DurationMS int64
	RequestID  string
	Detail     string
	Metadata   map[string]any
}

type Query struct {
	Page      int
	PageSize  int
	Search    string
	Level     string
	Category  string
	Source    string
	StartTime *time.Time
	EndTime   *time.Time
}

type Stats struct {
	Today      int `json:"today"`
	Failures24 int `json:"failures24h"`
	Background int `json:"background24h"`
}

type Page struct {
	Items    []Entry `json:"items"`
	Total    int     `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"pageSize"`
	Stats    Stats   `json:"stats"`
}

type Store struct {
	mu          sync.Mutex
	module      string
	path        string
	mode        persistence.StorageMode
	client      *ent.Client
	db          *sql.DB
	accountID   string
	lastCleanup time.Time
}

func New(module, directory string, service *persistence.Service) *Store {
	return NewForAccount(module, "default", directory, service)
}

func NewForAccount(module, accountID, directory string, service *persistence.Service) *Store {
	mode := persistence.StorageJSON
	var client *ent.Client
	if service != nil {
		mode, client = service.Mode(), service.Ent()
	}
	var db *sql.DB
	if service != nil {
		db = service.DB()
	}
	store := &Store{module: strings.TrimSpace(module), path: filepath.Join(directory, strings.TrimSpace(module)+".json"), mode: mode, client: client, db: db, accountID: strings.TrimSpace(accountID)}
	if mode == persistence.StoragePostgres {
		store.importLegacyJSON()
	}
	return store
}

func (s *Store) importLegacyJSON() {
	if s.client == nil || s.accountID != "default" {
		return
	}
	marker := "activity-log-imported:" + s.module + ":" + filepath.Clean(s.path)
	var imported bool
	if s.db == nil {
		return
	}
	if err := s.db.QueryRowContext(context.Background(), "SELECT EXISTS (SELECT 1 FROM running_state WHERE state_key = $1)", marker).Scan(&imported); err != nil || imported {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		slog.Warn("旧版使用日志解析失败，已保留原文件", "module", s.module, "error", safeDiagnostic(err))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, entry := range entries {
		entry.Module = s.module
		entry.Detail = safeDetail(entry.Detail)
		if _, err := s.client.ActivityLog.Create().SetModule(s.module).SetAccountID(s.accountID).SetCategory(entry.Category).SetAction(entry.Action).
			SetLevel(entry.Level).SetOutcome(entry.Outcome).SetSummary(entry.Summary).SetSource(entry.Source).SetMethod(entry.Method).
			SetPath(entry.Path).SetHTTPStatus(entry.HTTPStatus).SetDurationMs(entry.DurationMS).SetRequestID(entry.RequestID).
			SetDetail(entry.Detail).SetMetadata(safeMetadata(entry.Metadata)).SetCreatedAt(entry.CreatedAt).Save(ctx); err != nil {
			slog.Warn("旧版使用日志导入中断，已保留原文件", "module", s.module, "error", safeDiagnostic(err))
			return
		}
	}
	_, _ = s.db.ExecContext(context.Background(), `INSERT INTO running_state (state_key, value, updated_at) VALUES ($1, 'true'::jsonb, NOW()) ON CONFLICT (state_key) DO NOTHING`, marker)
	slog.Info("旧版使用日志已导入 PostgreSQL", "module", s.module, "entries", len(entries))
}

func (s *Store) Module() string { return s.module }

func (s *Store) Record(ctx context.Context, input Input) {
	entry := normalizedEntry(s.module, input)
	if err := s.record(ctx, entry); err != nil {
		slog.Error("写入模块使用日志失败", "module", s.module, "action", entry.Action, "error", safeDiagnostic(err))
	}
}

func (s *Store) record(ctx context.Context, entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mode != persistence.StoragePostgres {
		if err := s.appendJSONLocked(entry); err != nil {
			return err
		}
	}
	if s.mode != persistence.StorageJSON {
		if err := s.insertPostgres(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Query(ctx context.Context, query Query) (Page, error) {
	query = normalizedQuery(query)
	if s.mode == persistence.StoragePostgres {
		return s.queryPostgres(ctx, query)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.readJSONLocked()
	if err != nil {
		return Page{}, err
	}
	return queryEntries(entries, query), nil
}

// Clear removes all records for this module from the active store. In
// PostgreSQL mode this is a real DELETE and is not merely a UI filter.
func (s *Store) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mode != persistence.StoragePostgres {
		if err := storage.WriteJSON(s.path, []Entry{}, 0o600); err != nil {
			return err
		}
	}
	if s.mode != persistence.StorageJSON {
		if s.client == nil {
			return errors.New("PostgreSQL is unavailable")
		}
		if _, err := s.client.ActivityLog.Delete().Where(entactivitylog.ModuleEQ(s.module), entactivitylog.AccountIDEQ(s.accountID)).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) appendJSONLocked(entry Entry) error {
	entries, err := s.readJSONLocked()
	if err != nil {
		return err
	}
	entries = append(entries, entry)
	cutoff := time.Now().AddDate(0, 0, -DefaultRetentionDays)
	kept := make([]Entry, 0, min(len(entries), DefaultMaximumEntries))
	for index := len(entries) - 1; index >= 0 && len(kept) < DefaultMaximumEntries; index-- {
		if entries[index].CreatedAt.Before(cutoff) {
			continue
		}
		kept = append(kept, entries[index])
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	return storage.WriteJSON(s.path, kept, 0o600)
}

func (s *Store) readJSONLocked() ([]Entry, error) {
	entries := []Entry{}
	if err := storage.ReadJSON(s.path, &entries); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return entries, nil
}

func (s *Store) insertPostgres(ctx context.Context, entry Entry) error {
	if s.client == nil {
		return errors.New("PostgreSQL is unavailable")
	}
	if _, err := s.client.ActivityLog.Create().SetModule(entry.Module).SetAccountID(s.accountID).SetCategory(entry.Category).SetAction(entry.Action).
		SetLevel(entry.Level).SetOutcome(entry.Outcome).SetSummary(entry.Summary).SetSource(entry.Source).SetMethod(entry.Method).
		SetPath(entry.Path).SetHTTPStatus(entry.HTTPStatus).SetDurationMs(entry.DurationMS).SetRequestID(entry.RequestID).
		SetDetail(entry.Detail).SetMetadata(entry.Metadata).SetCreatedAt(entry.CreatedAt).Save(ctx); err != nil {
		return err
	}
	if time.Since(s.lastCleanup) >= time.Hour {
		s.lastCleanup = time.Now()
		_, _ = s.client.ActivityLog.Delete().Where(entactivitylog.ModuleEQ(s.module), entactivitylog.AccountIDEQ(s.accountID), entactivitylog.CreatedAtLT(time.Now().AddDate(0, 0, -DefaultRetentionDays))).Exec(ctx)
		if ids, cleanupErr := s.client.ActivityLog.Query().Where(entactivitylog.ModuleEQ(s.module), entactivitylog.AccountIDEQ(s.accountID)).
			Order(ent.Desc(entactivitylog.FieldCreatedAt), ent.Desc(entactivitylog.FieldID)).Offset(DefaultMaximumEntries).IDs(ctx); cleanupErr == nil && len(ids) > 0 {
			_, _ = s.client.ActivityLog.Delete().Where(entactivitylog.IDIn(ids...)).Exec(ctx)
		}
	}
	return nil
}

func (s *Store) queryPostgres(ctx context.Context, query Query) (Page, error) {
	if s.client == nil {
		return Page{}, errors.New("PostgreSQL is unavailable")
	}
	builder := s.client.ActivityLog.Query().Where(entactivitylog.ModuleEQ(s.module), entactivitylog.AccountIDEQ(s.accountID))
	if query.Search != "" {
		builder.Where(entactivitylog.Or(entactivitylog.SummaryContainsFold(query.Search), entactivitylog.ActionContainsFold(query.Search), entactivitylog.RequestIDContainsFold(query.Search)))
	}
	if query.Level != "" {
		builder.Where(entactivitylog.LevelEQ(query.Level))
	}
	if query.Category != "" {
		builder.Where(entactivitylog.CategoryEQ(query.Category))
	}
	if query.Source != "" {
		builder.Where(entactivitylog.SourceEQ(query.Source))
	}
	if query.StartTime != nil {
		builder.Where(entactivitylog.CreatedAtGTE(*query.StartTime))
	}
	if query.EndTime != nil {
		builder.Where(entactivitylog.CreatedAtLTE(*query.EndTime))
	}
	total, err := builder.Clone().Count(ctx)
	if err != nil {
		return Page{}, err
	}
	rows, err := builder.Order(ent.Desc(entactivitylog.FieldCreatedAt), ent.Desc(entactivitylog.FieldID)).
		Limit(query.PageSize).Offset((query.Page - 1) * query.PageSize).All(ctx)
	if err != nil {
		return Page{}, err
	}
	items := make([]Entry, 0, len(rows))
	for _, row := range rows {
		items = append(items, Entry{ID: strconv.Itoa(row.ID), Module: row.Module, Category: row.Category, Action: row.Action,
			Level: row.Level, Outcome: row.Outcome, Summary: row.Summary, Source: row.Source, Method: row.Method, Path: row.Path,
			HTTPStatus: row.HTTPStatus, DurationMS: row.DurationMs, RequestID: row.RequestID, Detail: row.Detail,
			Metadata: row.Metadata, CreatedAt: row.CreatedAt})
	}
	stats, err := s.postgresStats(ctx)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: items, Total: total, Page: query.Page, PageSize: query.PageSize, Stats: stats}, nil
}

func (s *Store) postgresStats(ctx context.Context) (Stats, error) {
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	today, err := s.client.ActivityLog.Query().Where(entactivitylog.ModuleEQ(s.module), entactivitylog.AccountIDEQ(s.accountID), entactivitylog.CreatedAtGTE(dayStart)).Count(ctx)
	if err != nil {
		return Stats{}, err
	}
	failures, err := s.client.ActivityLog.Query().Where(entactivitylog.ModuleEQ(s.module), entactivitylog.AccountIDEQ(s.accountID), entactivitylog.OutcomeEQ("failure"), entactivitylog.CreatedAtGTE(now.Add(-24*time.Hour))).Count(ctx)
	if err != nil {
		return Stats{}, err
	}
	background, err := s.client.ActivityLog.Query().Where(entactivitylog.ModuleEQ(s.module), entactivitylog.AccountIDEQ(s.accountID), entactivitylog.SourceEQ("background"), entactivitylog.CreatedAtGTE(now.Add(-24*time.Hour))).Count(ctx)
	return Stats{Today: today, Failures24: failures, Background: background}, err
}

func queryEntries(entries []Entry, query Query) Page {
	filtered := make([]Entry, 0, len(entries))
	search := strings.ToLower(query.Search)
	for _, entry := range entries {
		if search != "" && !strings.Contains(strings.ToLower(entry.Summary+" "+entry.Action+" "+entry.RequestID), search) {
			continue
		}
		if query.Level != "" && entry.Level != query.Level || query.Category != "" && entry.Category != query.Category || query.Source != "" && entry.Source != query.Source {
			continue
		}
		if query.StartTime != nil && entry.CreatedAt.Before(*query.StartTime) || query.EndTime != nil && entry.CreatedAt.After(*query.EndTime) {
			continue
		}
		filtered = append(filtered, entry)
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].CreatedAt.After(filtered[j].CreatedAt) })
	start := min((query.Page-1)*query.PageSize, len(filtered))
	end := min(start+query.PageSize, len(filtered))
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	last24 := now.Add(-24 * time.Hour)
	stats := Stats{}
	for _, entry := range entries {
		if !entry.CreatedAt.Before(dayStart) {
			stats.Today++
		}
		if !entry.CreatedAt.Before(last24) && entry.Outcome == "failure" {
			stats.Failures24++
		}
		if !entry.CreatedAt.Before(last24) && entry.Source == "background" {
			stats.Background++
		}
	}
	return Page{Items: append([]Entry(nil), filtered[start:end]...), Total: len(filtered), Page: query.Page, PageSize: query.PageSize, Stats: stats}
}

func normalizedEntry(module string, input Input) Entry {
	level := input.Level
	if level != "warning" && level != "error" {
		level = "info"
	}
	outcome := input.Outcome
	if outcome != "failure" {
		outcome = "success"
	}
	source := input.Source
	if source != "background" && source != "system" {
		source = "user"
	}
	return Entry{ID: randomID(), Module: limit(module, 64), Category: limit(input.Category, 64), Action: limit(input.Action, 128),
		Level: level, Outcome: outcome, Summary: limit(input.Summary, 500), Source: source, Method: limit(input.Method, 16),
		Path: limit(input.Path, 500), HTTPStatus: input.HTTPStatus, DurationMS: max(0, input.DurationMS), RequestID: limit(input.RequestID, 128),
		Detail: safeDetail(input.Detail), Metadata: safeMetadata(input.Metadata), CreatedAt: time.Now().UTC()}
}

func safeDetail(value string) string {
	value = sensitiveDetailPattern.ReplaceAllString(value, `$1$2<redacted>`)
	return limit(value, 2000)
}

func normalizedQuery(query Query) Query {
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize != 10 && query.PageSize != 20 && query.PageSize != 50 && query.PageSize != 100 {
		query.PageSize = 10
	}
	query.Search, query.Level, query.Category, query.Source = limit(strings.TrimSpace(query.Search), 200), strings.TrimSpace(query.Level), strings.TrimSpace(query.Category), strings.TrimSpace(query.Source)
	return query
}

func safeMetadata(input map[string]any) map[string]any {
	result := map[string]any{}
	for key, value := range input {
		lower := strings.ToLower(key)
		if lower == "errorcode" || lower == "error_code" {
			if typed, ok := value.(string); ok && typed != "" {
				result[limit(key, 64)] = limit(typed, 128)
			}
			continue
		}
		if strings.Contains(lower, "token") || strings.Contains(lower, "cookie") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "body") || strings.Contains(lower, "html") || strings.Contains(lower, "code") || strings.Contains(lower, "key") {
			continue
		}
		switch typed := value.(type) {
		case string:
			result[limit(key, 64)] = limit(typed, 300)
		case bool, int, int32, int64, uint, uint32, uint64, float32, float64:
			result[limit(key, 64)] = typed
		}
	}
	return result
}

func randomID() string {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err == nil {
		return hex.EncodeToString(value)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func limit(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}

func safeDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	return limit(err.Error(), 300)
}
