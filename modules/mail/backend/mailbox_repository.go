package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/Bringbasket/running-tools/internal/platform/persistence"
	"github.com/Bringbasket/running-tools/internal/platform/persistence/ent"
	"github.com/Bringbasket/running-tools/internal/platform/persistence/ent/mailboxhiddenmessage"
	"github.com/Bringbasket/running-tools/internal/platform/persistence/ent/mailboxmessage"
	"github.com/Bringbasket/running-tools/internal/platform/persistence/ent/mailboxsyncstate"
	"github.com/Bringbasket/running-tools/internal/platform/persistence/ent/predicate"
	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

const (
	mailboxStateKey           = "default"
	mailboxAliasQueryMaximum  = 100
	mailboxRecentQueryMaximum = 500
	mailboxAliasQueryDefault  = 10
	mailboxRecentQueryDefault = 200
	mailboxMessagePreviewSize = 160
)

type mailboxRepository interface {
	Load(context.Context) (mailboxCache, bool, error)
	LoadState(context.Context) (mailboxCache, bool, error)
	Save(context.Context, mailboxCache) error
	SaveState(context.Context, mailboxCache) error
	ListAliasMessages(context.Context, string, string, int, bool) ([]MailMessage, error)
	ListRecentMessages(context.Context, string, float64, int) ([]MailMessage, error)
	GetMessage(context.Context, string, string, uint32) (MailMessage, bool, error)
	Clear(context.Context) error
}

type jsonMailboxRepository struct{ path string }

func (r *jsonMailboxRepository) Load(_ context.Context) (mailboxCache, bool, error) {
	var cache mailboxCache
	err := storage.ReadJSON(r.path, &cache)
	if errors.Is(err, os.ErrNotExist) {
		return normalizedMailboxCache(cache), false, nil
	}
	return normalizedMailboxCache(cache), err == nil, err
}

func (r *jsonMailboxRepository) LoadState(ctx context.Context) (mailboxCache, bool, error) {
	cache, exists, err := r.Load(ctx)
	cache.Messages = []MailMessage{}
	cache.Hidden = []string{}
	return cache, exists, err
}

func (r *jsonMailboxRepository) Save(_ context.Context, cache mailboxCache) error {
	return storage.WriteJSON(r.path, cache, 0o600)
}

func (r *jsonMailboxRepository) SaveState(ctx context.Context, state mailboxCache) error {
	cache, _, err := r.Load(ctx)
	if err != nil {
		return err
	}
	cache.Status = state.Status
	cache.HighestUID = state.HighestUID
	cache.AllowedAliases = append([]string(nil), state.AllowedAliases...)
	return r.Save(ctx, cache)
}

func (r *jsonMailboxRepository) ListAliasMessages(ctx context.Context, generation, alias string, limit int, detailed bool) ([]MailMessage, error) {
	cache, _, err := r.Load(ctx)
	if err != nil {
		return nil, err
	}
	if generation == "" || cache.Status.MailboxGeneration != generation {
		return []MailMessage{}, nil
	}
	return selectAliasMessages(cache, alias, limit, detailed), nil
}

func (r *jsonMailboxRepository) ListRecentMessages(ctx context.Context, generation string, cutoff float64, limit int) ([]MailMessage, error) {
	cache, _, err := r.Load(ctx)
	if err != nil {
		return nil, err
	}
	if generation == "" || cache.Status.MailboxGeneration != generation {
		return []MailMessage{}, nil
	}
	return selectRecentMessages(cache, cutoff, limit), nil
}

func (r *jsonMailboxRepository) GetMessage(ctx context.Context, generation, alias string, uid uint32) (MailMessage, bool, error) {
	cache, _, err := r.Load(ctx)
	if err != nil {
		return MailMessage{}, false, err
	}
	if generation == "" || cache.Status.MailboxGeneration != generation {
		return MailMessage{}, false, nil
	}
	message, ok := selectMessage(cache, alias, uid)
	return message, ok, nil
}

func (r *jsonMailboxRepository) Clear(_ context.Context) error {
	return storage.WriteJSON(r.path, normalizedMailboxCache(mailboxCache{}), 0o600)
}

type postgresMailboxRepository struct {
	client    *ent.Client
	accountID string
}

func (r *postgresMailboxRepository) LoadState(ctx context.Context) (mailboxCache, bool, error) {
	state, err := r.client.MailboxSyncState.Query().Where(mailboxsyncstate.AccountIDEQ(r.accountID), mailboxsyncstate.KeyEQ(mailboxStateKey)).Only(ctx)
	if ent.IsNotFound(err) {
		return normalizedMailboxCache(mailboxCache{}), false, nil
	}
	if err != nil {
		return mailboxCache{}, false, fmt.Errorf("load mailbox sync state: %w", err)
	}
	var status MailboxStatus
	encoded, err := json.Marshal(state.Status)
	if err == nil {
		err = json.Unmarshal(encoded, &status)
	}
	if err != nil {
		return mailboxCache{}, false, fmt.Errorf("decode mailbox sync state: %w", err)
	}
	return normalizedMailboxCache(mailboxCache{
		Status:         status,
		HighestUID:     uint32(state.HighestUID),
		AllowedAliases: append([]string(nil), state.AllowedAliases...),
	}), true, nil
}

func (r *postgresMailboxRepository) Load(ctx context.Context) (mailboxCache, bool, error) {
	cache, exists, err := r.LoadState(ctx)
	if err != nil || !exists {
		return cache, exists, err
	}
	generation := cache.Status.MailboxGeneration
	if generation != "" {
		rows, err := r.client.MailboxMessage.Query().Where(mailboxmessage.AccountIDEQ(r.accountID), mailboxmessage.GenerationEQ(generation)).Order(ent.Desc(mailboxmessage.FieldMessageDate), ent.Desc(mailboxmessage.FieldUID)).All(ctx)
		if err != nil {
			return mailboxCache{}, false, fmt.Errorf("load mailbox messages: %w", err)
		}
		cache.Messages = make([]MailMessage, 0, len(rows))
		for _, row := range rows {
			cache.Messages = append(cache.Messages, MailMessage{
				UID: uint32(row.UID), Aliases: append([]string(nil), row.Aliases...), From: row.FromAddress,
				Subject: row.Subject, Date: row.MessageDate, Text: row.Text, SafeHTML: row.SafeHTML,
				Codes: append([]string(nil), row.Codes...), PartnerCodes: append([]string(nil), row.PartnerCodes...),
			})
		}
		hidden, err := r.client.MailboxHiddenMessage.Query().Where(mailboxhiddenmessage.AccountIDEQ(r.accountID), mailboxhiddenmessage.GenerationEQ(generation)).All(ctx)
		if err != nil {
			return mailboxCache{}, false, fmt.Errorf("load hidden mailbox messages: %w", err)
		}
		cache.Hidden = make([]string, 0, len(hidden))
		for _, row := range hidden {
			cache.Hidden = append(cache.Hidden, messageKey(row.Generation, row.Alias, uint32(row.UID)))
		}
		sort.Strings(cache.Hidden)
	}
	return normalizedMailboxCache(cache), true, nil
}

func (r *postgresMailboxRepository) Save(ctx context.Context, cache mailboxCache) error {
	if cache.Status.MailboxGeneration == "" && len(cache.Messages) > 0 {
		cache.Status.MailboxGeneration = "legacy-import"
	}
	status, err := encodeMailboxStatus(cache.Status)
	if err != nil {
		return err
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin mailbox transaction: %w", err)
	}
	rollback := func(cause error) error {
		_ = tx.Rollback()
		return cause
	}
	if err := tx.MailboxSyncState.Create().SetAccountID(r.accountID).SetKey(mailboxStateKey).SetStatus(status).SetHighestUID(uint64(cache.HighestUID)).SetAllowedAliases(cache.AllowedAliases).OnConflictColumns(mailboxsyncstate.FieldAccountID, mailboxsyncstate.FieldKey).UpdateNewValues().Exec(ctx); err != nil {
		return rollback(fmt.Errorf("save mailbox sync state: %w", err))
	}

	generation := cache.Status.MailboxGeneration
	if generation == "" {
		if _, err := tx.MailboxMessage.Delete().Where(mailboxmessage.AccountIDEQ(r.accountID)).Exec(ctx); err != nil {
			return rollback(fmt.Errorf("clear mailbox messages: %w", err))
		}
		if _, err := tx.MailboxHiddenMessage.Delete().Where(mailboxhiddenmessage.AccountIDEQ(r.accountID)).Exec(ctx); err != nil {
			return rollback(fmt.Errorf("clear hidden mailbox messages: %w", err))
		}
	} else {
		if _, err := tx.MailboxMessage.Delete().Where(mailboxmessage.AccountIDEQ(r.accountID), mailboxmessage.GenerationNEQ(generation)).Exec(ctx); err != nil {
			return rollback(fmt.Errorf("prune old mailbox generation: %w", err))
		}
		if _, err := tx.MailboxHiddenMessage.Delete().Where(mailboxhiddenmessage.AccountIDEQ(r.accountID), mailboxhiddenmessage.GenerationNEQ(generation)).Exec(ctx); err != nil {
			return rollback(fmt.Errorf("prune old hidden generation: %w", err))
		}

		messageUIDs := make([]uint64, 0, len(cache.Messages))
		for start := 0; start < len(cache.Messages); start += 500 {
			end := min(start+500, len(cache.Messages))
			builders := make([]*ent.MailboxMessageCreate, 0, end-start)
			for _, message := range cache.Messages[start:end] {
				messageUIDs = append(messageUIDs, uint64(message.UID))
				builders = append(builders, tx.MailboxMessage.Create().SetAccountID(r.accountID).SetGeneration(generation).SetUID(uint64(message.UID)).SetAliases(message.Aliases).
					SetFromAddress(message.From).SetSubject(message.Subject).SetMessageDate(message.Date).SetText(message.Text).
					SetSafeHTML(message.SafeHTML).SetCodes(message.Codes).SetPartnerCodes(message.PartnerCodes))
			}
			if err := tx.MailboxMessage.CreateBulk(builders...).OnConflictColumns(mailboxmessage.FieldAccountID, mailboxmessage.FieldGeneration, mailboxmessage.FieldUID).UpdateNewValues().Exec(ctx); err != nil {
				return rollback(fmt.Errorf("upsert mailbox message batch: %w", err))
			}
		}
		deleteMessages := tx.MailboxMessage.Delete().Where(mailboxmessage.AccountIDEQ(r.accountID), mailboxmessage.GenerationEQ(generation))
		if len(messageUIDs) > 0 {
			deleteMessages.Where(mailboxmessage.UIDNotIn(messageUIDs...))
		}
		if len(messageUIDs) == 0 {
			if _, err := deleteMessages.Exec(ctx); err != nil {
				return rollback(fmt.Errorf("clear current mailbox messages: %w", err))
			}
		} else if _, err := deleteMessages.Exec(ctx); err != nil {
			return rollback(fmt.Errorf("prune mailbox messages: %w", err))
		}

		if _, err := tx.MailboxHiddenMessage.Delete().Where(mailboxhiddenmessage.AccountIDEQ(r.accountID), mailboxhiddenmessage.GenerationEQ(generation)).Exec(ctx); err != nil {
			return rollback(fmt.Errorf("replace hidden mailbox messages: %w", err))
		}
		hiddenBuilders := make([]*ent.MailboxHiddenMessageCreate, 0, min(500, len(cache.Hidden)))
		flushHidden := func() error {
			if len(hiddenBuilders) == 0 {
				return nil
			}
			err := tx.MailboxHiddenMessage.CreateBulk(hiddenBuilders...).OnConflictColumns(mailboxhiddenmessage.FieldAccountID, mailboxhiddenmessage.FieldGeneration, mailboxhiddenmessage.FieldAlias, mailboxhiddenmessage.FieldUID).UpdateNewValues().Exec(ctx)
			hiddenBuilders = hiddenBuilders[:0]
			return err
		}
		for _, key := range cache.Hidden {
			alias, uid, ok := parseMessageKey(generation, key)
			if ok {
				hiddenBuilders = append(hiddenBuilders, tx.MailboxHiddenMessage.Create().SetAccountID(r.accountID).SetGeneration(generation).SetAlias(alias).SetUID(uint64(uid)))
			}
			if len(hiddenBuilders) == 500 {
				if err := flushHidden(); err != nil {
					return rollback(fmt.Errorf("save hidden mailbox message batch: %w", err))
				}
			}
		}
		if err := flushHidden(); err != nil {
			return rollback(fmt.Errorf("save hidden mailbox message batch: %w", err))
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mailbox transaction: %w", err)
	}
	return nil
}

func (r *postgresMailboxRepository) SaveState(ctx context.Context, cache mailboxCache) error {
	status, err := encodeMailboxStatus(cache.Status)
	if err != nil {
		return err
	}
	if err := r.client.MailboxSyncState.Create().
		SetAccountID(r.accountID).
		SetKey(mailboxStateKey).
		SetStatus(status).
		SetHighestUID(uint64(cache.HighestUID)).
		SetAllowedAliases(cache.AllowedAliases).
		OnConflictColumns(mailboxsyncstate.FieldAccountID, mailboxsyncstate.FieldKey).
		UpdateNewValues().
		Exec(ctx); err != nil {
		return fmt.Errorf("save mailbox sync state: %w", err)
	}
	return nil
}

func encodeMailboxStatus(status MailboxStatus) (map[string]any, error) {
	statusJSON, err := json.Marshal(status)
	if err != nil {
		return nil, err
	}
	encoded := make(map[string]any)
	if err := json.Unmarshal(statusJSON, &encoded); err != nil {
		return nil, err
	}
	return encoded, nil
}

func (r *postgresMailboxRepository) ListAliasMessages(ctx context.Context, generation, alias string, limit int, detailed bool) ([]MailMessage, error) {
	alias = strings.ToLower(strings.TrimSpace(alias))
	limit = boundedMailboxQueryLimit(limit, mailboxAliasQueryDefault, mailboxAliasQueryMaximum)
	if generation == "" || alias == "" {
		return []MailMessage{}, nil
	}
	query := r.client.MailboxMessage.Query().Where(
		mailboxmessage.AccountIDEQ(r.accountID),
		mailboxmessage.GenerationEQ(generation),
		mailboxMessageHasAlias(alias),
		mailboxMessageNotHiddenForAlias(r.accountID, generation, alias),
	).Order(ent.Desc(mailboxmessage.FieldMessageDate), ent.Desc(mailboxmessage.FieldUID)).Limit(limit)
	if detailed {
		rows, err := query.All(ctx)
		if err != nil {
			return nil, fmt.Errorf("list mailbox messages for alias: %w", err)
		}
		return mailboxMessagesFromRows(rows, true), nil
	}
	rows, err := compactMailboxMessageRows(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list mailbox message previews for alias: %w", err)
	}
	return mailboxMessageSummariesFromRows(rows), nil
}

func (r *postgresMailboxRepository) ListRecentMessages(ctx context.Context, generation string, cutoff float64, limit int) ([]MailMessage, error) {
	limit = boundedMailboxQueryLimit(limit, mailboxRecentQueryDefault, mailboxRecentQueryMaximum)
	if generation == "" {
		return []MailMessage{}, nil
	}
	query := r.client.MailboxMessage.Query().Where(
		mailboxmessage.AccountIDEQ(r.accountID),
		mailboxmessage.GenerationEQ(generation),
		mailboxmessage.MessageDateGTE(cutoff),
		mailboxMessageHasVisibleAlias(),
	).Order(ent.Desc(mailboxmessage.FieldMessageDate), ent.Desc(mailboxmessage.FieldUID)).Limit(limit)
	rows, err := compactMailboxMessageRows(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list recent mailbox message previews: %w", err)
	}
	return mailboxMessageSummariesFromRows(rows), nil
}

func (r *postgresMailboxRepository) GetMessage(ctx context.Context, generation, alias string, uid uint32) (MailMessage, bool, error) {
	alias = strings.ToLower(strings.TrimSpace(alias))
	if generation == "" || alias == "" || uid == 0 {
		return MailMessage{}, false, nil
	}
	row, err := r.client.MailboxMessage.Query().Where(
		mailboxmessage.AccountIDEQ(r.accountID),
		mailboxmessage.GenerationEQ(generation),
		mailboxmessage.UIDEQ(uint64(uid)),
		mailboxMessageHasAlias(alias),
		mailboxMessageNotHiddenForAlias(r.accountID, generation, alias),
	).Only(ctx)
	if ent.IsNotFound(err) {
		return MailMessage{}, false, nil
	}
	if err != nil {
		return MailMessage{}, false, fmt.Errorf("load mailbox message: %w", err)
	}
	messages := mailboxMessagesFromRows([]*ent.MailboxMessage{row}, true)
	return messages[0], true, nil
}

func mailboxMessageHasAlias(alias string) predicate.MailboxMessage {
	return func(selector *entsql.Selector) {
		selector.Where(sqljson.ValueContains(mailboxmessage.FieldAliases, alias))
	}
}

func mailboxMessageNotHiddenForAlias(accountID, generation, alias string) predicate.MailboxMessage {
	return func(selector *entsql.Selector) {
		hidden := entsql.Table(mailboxhiddenmessage.Table).As("hidden_alias_message")
		selector.Where(entsql.NotExists(
			entsql.Select(hidden.C(mailboxhiddenmessage.FieldUID)).
				From(hidden).
				Where(entsql.And(
					entsql.EQ(hidden.C(mailboxhiddenmessage.FieldAccountID), accountID),
					entsql.EQ(hidden.C(mailboxhiddenmessage.FieldGeneration), generation),
					entsql.EQ(hidden.C(mailboxhiddenmessage.FieldAlias), alias),
					entsql.ColumnsEQ(hidden.C(mailboxhiddenmessage.FieldUID), selector.C(mailboxmessage.FieldUID)),
				)),
		))
	}
}

// A recent message remains visible while at least one of its aliases has not
// been hidden. This mirrors the in-memory behavior without loading hidden rows.
func mailboxMessageHasVisibleAlias() predicate.MailboxMessage {
	return func(selector *entsql.Selector) {
		selector.Where(entsql.ExprP(fmt.Sprintf(`EXISTS (
			SELECT 1
			FROM jsonb_array_elements_text(%s) AS visible_alias(alias)
			WHERE NOT EXISTS (
				SELECT 1 FROM %q AS hidden_recent_message
				WHERE hidden_recent_message.%q = %s
				  AND hidden_recent_message.%q = %s
				  AND hidden_recent_message.%q = %s
				  AND hidden_recent_message.%q = visible_alias.alias
			)
		)`,
			selector.C(mailboxmessage.FieldAliases),
			mailboxhiddenmessage.Table,
			mailboxhiddenmessage.FieldAccountID, selector.C(mailboxmessage.FieldAccountID),
			mailboxhiddenmessage.FieldGeneration, selector.C(mailboxmessage.FieldGeneration),
			mailboxhiddenmessage.FieldUID, selector.C(mailboxmessage.FieldUID),
			mailboxhiddenmessage.FieldAlias,
		)))
	}
}

type compactMailboxMessageRow struct {
	UID          uint64   `json:"uid"`
	Aliases      []string `json:"aliases"`
	FromAddress  string   `json:"from_address"`
	Subject      string   `json:"subject"`
	MessageDate  float64  `json:"message_date"`
	Text         string   `json:"text"`
	Codes        []string `json:"codes"`
	PartnerCodes []string `json:"partner_codes"`
}

func compactMailboxMessageRows(ctx context.Context, query *ent.MailboxMessageQuery) ([]compactMailboxMessageRow, error) {
	rows := []compactMailboxMessageRow{}
	err := query.Select(
		mailboxmessage.FieldUID,
		mailboxmessage.FieldAliases,
		mailboxmessage.FieldFromAddress,
		mailboxmessage.FieldSubject,
		mailboxmessage.FieldMessageDate,
		mailboxmessage.FieldCodes,
		mailboxmessage.FieldPartnerCodes,
	).Aggregate(ent.As(func(selector *entsql.Selector) string {
		return fmt.Sprintf("LEFT(%s, %d)", selector.C(mailboxmessage.FieldText), mailboxMessagePreviewSize)
	}, mailboxmessage.FieldText)).Scan(ctx, &rows)
	return rows, err
}

func mailboxMessageSummariesFromRows(rows []compactMailboxMessageRow) []MailMessage {
	messages := make([]MailMessage, 0, len(rows))
	for _, row := range rows {
		message := MailMessage{
			UID: uint32(row.UID), Aliases: append([]string(nil), row.Aliases...), From: row.FromAddress,
			Subject: row.Subject, Date: row.MessageDate, Text: row.Text,
			Codes: append([]string(nil), row.Codes...), PartnerCodes: append([]string(nil), row.PartnerCodes...),
		}
		normalizeMailMessageLists(&message)
		messages = append(messages, message)
	}
	return messages
}

func mailboxMessagesFromRows(rows []*ent.MailboxMessage, detailed bool) []MailMessage {
	messages := make([]MailMessage, 0, len(rows))
	for _, row := range rows {
		message := MailMessage{
			UID: uint32(row.UID), Aliases: append([]string(nil), row.Aliases...), From: row.FromAddress,
			Subject: row.Subject, Date: row.MessageDate, Text: row.Text, SafeHTML: row.SafeHTML,
			Codes: append([]string(nil), row.Codes...), PartnerCodes: append([]string(nil), row.PartnerCodes...),
		}
		if !detailed {
			message = summarizeMailMessage(message)
		}
		normalizeMailMessageLists(&message)
		messages = append(messages, message)
	}
	return messages
}

func (r *postgresMailboxRepository) Clear(ctx context.Context) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	rollback := func(cause error) error { _ = tx.Rollback(); return cause }
	if _, err := tx.MailboxHiddenMessage.Delete().Where(mailboxhiddenmessage.AccountIDEQ(r.accountID)).Exec(ctx); err != nil {
		return rollback(err)
	}
	if _, err := tx.MailboxMessage.Delete().Where(mailboxmessage.AccountIDEQ(r.accountID)).Exec(ctx); err != nil {
		return rollback(err)
	}
	if _, err := tx.MailboxSyncState.Delete().Where(mailboxsyncstate.AccountIDEQ(r.accountID)).Exec(ctx); err != nil {
		return rollback(err)
	}
	return tx.Commit()
}

type dualMailboxRepository struct {
	json     *jsonMailboxRepository
	postgres *postgresMailboxRepository
}

func (r *dualMailboxRepository) Load(ctx context.Context) (mailboxCache, bool, error) {
	return r.json.Load(ctx)
}

func (r *dualMailboxRepository) LoadState(ctx context.Context) (mailboxCache, bool, error) {
	return r.json.LoadState(ctx)
}

func (r *dualMailboxRepository) Save(ctx context.Context, cache mailboxCache) error {
	if err := r.json.Save(ctx, cache); err != nil {
		return err
	}
	if err := r.postgres.Save(ctx, cache); err != nil {
		return fmt.Errorf("JSON saved but PostgreSQL dual-write failed: %w", err)
	}
	return nil
}

func (r *dualMailboxRepository) SaveState(ctx context.Context, cache mailboxCache) error {
	if err := r.json.SaveState(ctx, cache); err != nil {
		return err
	}
	if err := r.postgres.SaveState(ctx, cache); err != nil {
		return fmt.Errorf("JSON state saved but PostgreSQL dual-write failed: %w", err)
	}
	return nil
}

func (r *dualMailboxRepository) ListAliasMessages(ctx context.Context, generation, alias string, limit int, detailed bool) ([]MailMessage, error) {
	return r.json.ListAliasMessages(ctx, generation, alias, limit, detailed)
}

func (r *dualMailboxRepository) ListRecentMessages(ctx context.Context, generation string, cutoff float64, limit int) ([]MailMessage, error) {
	return r.json.ListRecentMessages(ctx, generation, cutoff, limit)
}

func (r *dualMailboxRepository) GetMessage(ctx context.Context, generation, alias string, uid uint32) (MailMessage, bool, error) {
	return r.json.GetMessage(ctx, generation, alias, uid)
}

func (r *dualMailboxRepository) Clear(ctx context.Context) error {
	if err := r.json.Clear(ctx); err != nil {
		return err
	}
	return r.postgres.Clear(ctx)
}

func newMailboxRepository(path string, service *persistence.Service) (mailboxRepository, error) {
	return newMailboxRepositoryForAccount(path, defaultMailAccountID, service)
}

func newMailboxRepositoryForAccount(path, accountID string, service *persistence.Service) (mailboxRepository, error) {
	jsonRepository := &jsonMailboxRepository{path: path}
	if service == nil || service.Mode() == persistence.StorageJSON {
		return jsonRepository, nil
	}
	postgresRepository := &postgresMailboxRepository{client: service.Ent(), accountID: accountID}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if service.Mode() == persistence.StorageDual {
		cache, exists, err := jsonRepository.Load(ctx)
		if err != nil {
			return nil, fmt.Errorf("load JSON mailbox cache for dual-write bootstrap: %w", err)
		}
		if exists {
			if err := postgresRepository.Save(ctx, cache); err != nil {
				return nil, fmt.Errorf("bootstrap PostgreSQL mailbox cache: %w", err)
			}
			slog.Info("已同步 JSON 邮箱缓存到 PostgreSQL", "mode", "dual", "messages", len(cache.Messages), "hidden", len(cache.Hidden))
		}
		return &dualMailboxRepository{json: jsonRepository, postgres: postgresRepository}, nil
	}
	// Probe only the sync-state row at startup. Loading every cached body here
	// defeats the bounded read path for accounts with a large mailbox.
	_, databaseExists, err := postgresRepository.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	if !databaseExists {
		cache, jsonExists, err := jsonRepository.Load(ctx)
		if err != nil {
			return nil, fmt.Errorf("load legacy mailbox cache: %w", err)
		}
		if jsonExists {
			if err := postgresRepository.Save(ctx, cache); err != nil {
				return nil, fmt.Errorf("import legacy mailbox cache: %w", err)
			}
			slog.Info("已导入旧 JSON 邮箱缓存到 PostgreSQL", "mode", "postgres", "messages", len(cache.Messages), "hidden", len(cache.Hidden))
		}
	}
	return postgresRepository, nil
}

func selectAliasMessages(cache mailboxCache, alias string, limit int, detailed bool) []MailMessage {
	alias = strings.ToLower(strings.TrimSpace(alias))
	limit = boundedMailboxQueryLimit(limit, mailboxAliasQueryDefault, mailboxAliasQueryMaximum)
	if alias == "" {
		return []MailMessage{}
	}
	hidden := hiddenSet(cache.Hidden)
	messages := orderedMailboxMessages(cache.Messages)
	selected := make([]MailMessage, 0, min(limit, len(messages)))
	for _, message := range messages {
		if hidden[messageKey(cache.Status.MailboxGeneration, alias, message.UID)] || !mailMessageHasAlias(message, alias) {
			continue
		}
		if !detailed {
			message = summarizeMailMessage(message)
		}
		normalizeMailMessageLists(&message)
		selected = append(selected, message)
		if len(selected) == limit {
			break
		}
	}
	return selected
}

func selectRecentMessages(cache mailboxCache, cutoff float64, limit int) []MailMessage {
	limit = boundedMailboxQueryLimit(limit, mailboxRecentQueryDefault, mailboxRecentQueryMaximum)
	hidden := hiddenSet(cache.Hidden)
	messages := orderedMailboxMessages(cache.Messages)
	selected := make([]MailMessage, 0, min(limit, len(messages)))
	for _, message := range messages {
		if message.Date < cutoff || !mailMessageHasVisibleAlias(message, cache.Status.MailboxGeneration, hidden) {
			continue
		}
		message = summarizeMailMessage(message)
		normalizeMailMessageLists(&message)
		selected = append(selected, message)
		if len(selected) == limit {
			break
		}
	}
	return selected
}

func selectMessage(cache mailboxCache, alias string, uid uint32) (MailMessage, bool) {
	alias = strings.ToLower(strings.TrimSpace(alias))
	if alias == "" || uid == 0 || hiddenSet(cache.Hidden)[messageKey(cache.Status.MailboxGeneration, alias, uid)] {
		return MailMessage{}, false
	}
	for _, message := range cache.Messages {
		if message.UID == uid && mailMessageHasAlias(message, alias) {
			normalizeMailMessageLists(&message)
			return message, true
		}
	}
	return MailMessage{}, false
}

func orderedMailboxMessages(messages []MailMessage) []MailMessage {
	ordered := append([]MailMessage(nil), messages...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Date == ordered[j].Date {
			return ordered[i].UID > ordered[j].UID
		}
		return ordered[i].Date > ordered[j].Date
	})
	return ordered
}

func mailMessageHasAlias(message MailMessage, alias string) bool {
	for _, candidate := range message.Aliases {
		if strings.EqualFold(strings.TrimSpace(candidate), alias) {
			return true
		}
	}
	return false
}

func mailMessageHasVisibleAlias(message MailMessage, generation string, hidden map[string]bool) bool {
	for _, alias := range message.Aliases {
		if !hidden[messageKey(generation, alias, message.UID)] {
			return true
		}
	}
	return false
}

func boundedMailboxQueryLimit(limit, fallback, maximum int) int {
	if limit < 1 {
		return fallback
	}
	return min(limit, maximum)
}

func normalizedMailboxCache(cache mailboxCache) mailboxCache {
	if cache.Messages == nil {
		cache.Messages = []MailMessage{}
	}
	if cache.Hidden == nil {
		cache.Hidden = []string{}
	}
	return cache
}

func parseMessageKey(generation, key string) (string, uint32, bool) {
	prefix := generation + ":"
	if !strings.HasPrefix(key, prefix) {
		return "", 0, false
	}
	remainder := strings.TrimPrefix(key, prefix)
	index := strings.LastIndexByte(remainder, ':')
	if index < 1 {
		return "", 0, false
	}
	uid, err := strconv.ParseUint(remainder[index+1:], 10, 32)
	if err != nil {
		return "", 0, false
	}
	alias := strings.ToLower(strings.TrimSpace(remainder[:index]))
	return alias, uint32(uid), alias != ""
}
