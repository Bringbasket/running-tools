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

	"github.com/Bringbasket/running-tools/internal/platform/persistence"
	"github.com/Bringbasket/running-tools/internal/platform/persistence/ent"
	"github.com/Bringbasket/running-tools/internal/platform/persistence/ent/mailboxhiddenmessage"
	"github.com/Bringbasket/running-tools/internal/platform/persistence/ent/mailboxmessage"
	"github.com/Bringbasket/running-tools/internal/platform/persistence/ent/mailboxsyncstate"
	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

const mailboxStateKey = "default"

type mailboxRepository interface {
	Load(context.Context) (mailboxCache, bool, error)
	Save(context.Context, mailboxCache) error
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

func (r *jsonMailboxRepository) Save(_ context.Context, cache mailboxCache) error {
	return storage.WriteJSON(r.path, cache, 0o600)
}

type postgresMailboxRepository struct{ client *ent.Client }

func (r *postgresMailboxRepository) Load(ctx context.Context) (mailboxCache, bool, error) {
	state, err := r.client.MailboxSyncState.Query().Where(mailboxsyncstate.KeyEQ(mailboxStateKey)).Only(ctx)
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

	cache := mailboxCache{Status: status, HighestUID: uint32(state.HighestUID), AllowedAliases: append([]string(nil), state.AllowedAliases...)}
	generation := status.MailboxGeneration
	if generation != "" {
		rows, err := r.client.MailboxMessage.Query().Where(mailboxmessage.GenerationEQ(generation)).Order(ent.Desc(mailboxmessage.FieldMessageDate), ent.Desc(mailboxmessage.FieldUID)).All(ctx)
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
		hidden, err := r.client.MailboxHiddenMessage.Query().Where(mailboxhiddenmessage.GenerationEQ(generation)).All(ctx)
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
	statusJSON, err := json.Marshal(cache.Status)
	if err != nil {
		return err
	}
	var status map[string]any
	if err := json.Unmarshal(statusJSON, &status); err != nil {
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
	if err := tx.MailboxSyncState.Create().SetKey(mailboxStateKey).SetStatus(status).SetHighestUID(uint64(cache.HighestUID)).SetAllowedAliases(cache.AllowedAliases).OnConflictColumns(mailboxsyncstate.FieldKey).UpdateNewValues().Exec(ctx); err != nil {
		return rollback(fmt.Errorf("save mailbox sync state: %w", err))
	}

	generation := cache.Status.MailboxGeneration
	if generation == "" {
		if _, err := tx.MailboxMessage.Delete().Exec(ctx); err != nil {
			return rollback(fmt.Errorf("clear mailbox messages: %w", err))
		}
		if _, err := tx.MailboxHiddenMessage.Delete().Exec(ctx); err != nil {
			return rollback(fmt.Errorf("clear hidden mailbox messages: %w", err))
		}
	} else {
		if _, err := tx.MailboxMessage.Delete().Where(mailboxmessage.GenerationNEQ(generation)).Exec(ctx); err != nil {
			return rollback(fmt.Errorf("prune old mailbox generation: %w", err))
		}
		if _, err := tx.MailboxHiddenMessage.Delete().Where(mailboxhiddenmessage.GenerationNEQ(generation)).Exec(ctx); err != nil {
			return rollback(fmt.Errorf("prune old hidden generation: %w", err))
		}

		messageUIDs := make([]uint64, 0, len(cache.Messages))
		for start := 0; start < len(cache.Messages); start += 500 {
			end := min(start+500, len(cache.Messages))
			builders := make([]*ent.MailboxMessageCreate, 0, end-start)
			for _, message := range cache.Messages[start:end] {
				messageUIDs = append(messageUIDs, uint64(message.UID))
				builders = append(builders, tx.MailboxMessage.Create().SetGeneration(generation).SetUID(uint64(message.UID)).SetAliases(message.Aliases).
					SetFromAddress(message.From).SetSubject(message.Subject).SetMessageDate(message.Date).SetText(message.Text).
					SetSafeHTML(message.SafeHTML).SetCodes(message.Codes).SetPartnerCodes(message.PartnerCodes))
			}
			if err := tx.MailboxMessage.CreateBulk(builders...).OnConflictColumns(mailboxmessage.FieldGeneration, mailboxmessage.FieldUID).UpdateNewValues().Exec(ctx); err != nil {
				return rollback(fmt.Errorf("upsert mailbox message batch: %w", err))
			}
		}
		deleteMessages := tx.MailboxMessage.Delete().Where(mailboxmessage.GenerationEQ(generation))
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

		if _, err := tx.MailboxHiddenMessage.Delete().Where(mailboxhiddenmessage.GenerationEQ(generation)).Exec(ctx); err != nil {
			return rollback(fmt.Errorf("replace hidden mailbox messages: %w", err))
		}
		hiddenBuilders := make([]*ent.MailboxHiddenMessageCreate, 0, min(500, len(cache.Hidden)))
		flushHidden := func() error {
			if len(hiddenBuilders) == 0 {
				return nil
			}
			err := tx.MailboxHiddenMessage.CreateBulk(hiddenBuilders...).OnConflictColumns(mailboxhiddenmessage.FieldGeneration, mailboxhiddenmessage.FieldAlias, mailboxhiddenmessage.FieldUID).UpdateNewValues().Exec(ctx)
			hiddenBuilders = hiddenBuilders[:0]
			return err
		}
		for _, key := range cache.Hidden {
			alias, uid, ok := parseMessageKey(generation, key)
			if ok {
				hiddenBuilders = append(hiddenBuilders, tx.MailboxHiddenMessage.Create().SetGeneration(generation).SetAlias(alias).SetUID(uint64(uid)))
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

type dualMailboxRepository struct {
	json     *jsonMailboxRepository
	postgres *postgresMailboxRepository
}

func (r *dualMailboxRepository) Load(ctx context.Context) (mailboxCache, bool, error) {
	return r.json.Load(ctx)
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

func newMailboxRepository(path string, service *persistence.Service) (mailboxRepository, error) {
	jsonRepository := &jsonMailboxRepository{path: path}
	if service == nil || service.Mode() == persistence.StorageJSON {
		return jsonRepository, nil
	}
	postgresRepository := &postgresMailboxRepository{client: service.Ent()}
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
	_, databaseExists, err := postgresRepository.Load(ctx)
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
