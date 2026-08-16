package mail

import (
	"context"
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/persistence/ent"
	"github.com/Bringbasket/running-tools/internal/platform/persistence/ent/mailaliasappstate"
	"github.com/Bringbasket/running-tools/internal/platform/persistence/ent/mailboxmessage"
)

const (
	aliasAppGPT             = "gpt"
	aliasAppStatusObserved  = "observed"
	aliasAppStatusConfirmed = "confirmed"
)

var gptEvidenceSubjects = []string{
	"Your temporary ChatGPT verification code",
	"Your temporary OpenAI verification code",
	"Your temporary ChatGPT login code",
	"Your temporary OpenAI login code",
	"Welcome to ChatGPT",
	"Your first chat was just the beginning",
}

type AliasApplication struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Status      string   `json:"status"`
	DetectedAt  *float64 `json:"detectedAt,omitempty"`
	ConfirmedAt *float64 `json:"confirmedAt,omitempty"`
}

type aliasApplicationStore interface {
	ObserveMessages(context.Context, []MailMessage) error
	List(context.Context) (map[string][]AliasApplication, error)
	DeleteAlias(context.Context, string) error
	Backfill(context.Context) error
}

type postgresAliasApplicationStore struct {
	client    *ent.Client
	accountID string
}

type applicationEvidenceKind uint8

const (
	applicationEvidenceRegistration applicationEvidenceKind = iota + 1
	applicationEvidenceLogin
	applicationEvidenceDefinitive
)

type applicationEvidence struct {
	kind    applicationEvidenceKind
	alias   string
	uid     uint64
	at      time.Time
	subject string
	sender  string
}

func classifyGPTMessage(message MailMessage) (applicationEvidenceKind, bool) {
	if !trustedOpenAISender(message.From) {
		return 0, false
	}
	switch normalizeEvidenceSubject(message.Subject) {
	case "your temporary chatgpt verification code", "your temporary openai verification code":
		return applicationEvidenceRegistration, true
	case "your temporary chatgpt login code", "your temporary openai login code":
		return applicationEvidenceLogin, true
	case "welcome to chatgpt", "your first chat was just the beginning":
		return applicationEvidenceDefinitive, true
	default:
		return 0, false
	}
}

func normalizeEvidenceSubject(subject string) string {
	return strings.ToLower(strings.Join(strings.Fields(subject), " "))
}

func trustedOpenAISender(raw string) bool {
	address, err := mail.ParseAddress(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	local, domain, ok := strings.Cut(strings.ToLower(address.Address), "@")
	if !ok {
		return false
	}
	if domain == "openai.com" || strings.HasSuffix(domain, ".openai.com") {
		return true
	}
	if domain != "icloud.com" && domain != "me.com" && domain != "mac.com" {
		return false
	}
	return strings.HasPrefix(local, "otp_at_tm1_openai_com") ||
		strings.HasPrefix(local, "noreply_at_tm_openai_com") ||
		strings.HasPrefix(local, "noreply_at_openai_com")
}

func evidenceTime(value float64) time.Time {
	if value <= 0 {
		return time.Now().UTC()
	}
	if value > 1e12 {
		value /= 1000
	}
	seconds := int64(value)
	nanoseconds := int64((value - float64(seconds)) * float64(time.Second))
	return time.Unix(seconds, nanoseconds).UTC()
}

func evidenceFollows(detectedAt *time.Time, detectedUID uint64, evidence applicationEvidence) bool {
	if detectedAt == nil || evidence.at.After(*detectedAt) {
		return detectedAt != nil
	}
	return evidence.at.Equal(*detectedAt) && evidence.uid > detectedUID
}

func nextApplicationStatus(current string, detectedAt *time.Time, detectedUID uint64, evidence applicationEvidence) (string, bool) {
	if current == aliasAppStatusConfirmed {
		return current, false
	}
	switch evidence.kind {
	case applicationEvidenceRegistration:
		if current == "" {
			return aliasAppStatusObserved, true
		}
	case applicationEvidenceLogin:
		if current == aliasAppStatusObserved && evidenceFollows(detectedAt, detectedUID, evidence) {
			return aliasAppStatusConfirmed, true
		}
	case applicationEvidenceDefinitive:
		return aliasAppStatusConfirmed, true
	}
	return current, false
}

func (store *postgresAliasApplicationStore) ObserveMessages(ctx context.Context, messages []MailMessage) error {
	sorted := append([]MailMessage(nil), messages...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Date == sorted[j].Date {
			return sorted[i].UID < sorted[j].UID
		}
		return sorted[i].Date < sorted[j].Date
	})
	for _, message := range sorted {
		kind, ok := classifyGPTMessage(message)
		if !ok {
			continue
		}
		for _, alias := range normalizeAliases(message.Aliases) {
			evidence := applicationEvidence{
				kind: kind, alias: alias, uid: uint64(message.UID), at: evidenceTime(message.Date),
				subject: truncateText(strings.TrimSpace(message.Subject), 500),
				sender:  truncateText(strings.TrimSpace(message.From), 1000),
			}
			if err := store.observeEvidence(ctx, evidence, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func (store *postgresAliasApplicationStore) observeEvidence(ctx context.Context, evidence applicationEvidence, retryConstraint bool) error {
	state, err := store.client.MailAliasAppState.Query().Where(
		mailaliasappstate.AccountIDEQ(store.accountID),
		mailaliasappstate.AliasEQ(evidence.alias),
		mailaliasappstate.AppKeyEQ(aliasAppGPT),
	).Only(ctx)
	if ent.IsNotFound(err) {
		nextStatus, changed := nextApplicationStatus("", nil, 0, evidence)
		if !changed {
			return nil
		}
		create := store.client.MailAliasAppState.Create().
			SetAccountID(store.accountID).
			SetAlias(evidence.alias).
			SetAppKey(aliasAppGPT).
			SetStatus(nextStatus)
		if evidence.kind == applicationEvidenceRegistration {
			create.SetDetectedAt(evidence.at).
				SetDetectedUID(evidence.uid).
				SetDetectedSubject(evidence.subject).
				SetDetectedSender(evidence.sender)
		} else {
			create.SetConfirmedAt(evidence.at).
				SetConfirmedUID(evidence.uid).
				SetConfirmedSubject(evidence.subject).
				SetConfirmedSender(evidence.sender)
		}
		if _, err := create.Save(ctx); err != nil {
			if retryConstraint && ent.IsConstraintError(err) {
				return store.observeEvidence(ctx, evidence, false)
			}
			return fmt.Errorf("create alias application state: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("load alias application state: %w", err)
	}

	update := state.Update()
	switch evidence.kind {
	case applicationEvidenceRegistration:
		if state.Status == aliasAppStatusConfirmed {
			return nil
		}
		if state.DetectedAt != nil && !evidence.at.Before(*state.DetectedAt) {
			return nil
		}
		update.SetDetectedAt(evidence.at).
			SetDetectedUID(evidence.uid).
			SetDetectedSubject(evidence.subject).
			SetDetectedSender(evidence.sender)
	case applicationEvidenceLogin:
		nextStatus, changed := nextApplicationStatus(state.Status, state.DetectedAt, state.DetectedUID, evidence)
		if !changed {
			return nil
		}
		update.SetStatus(nextStatus).
			SetConfirmedAt(evidence.at).
			SetConfirmedUID(evidence.uid).
			SetConfirmedSubject(evidence.subject).
			SetConfirmedSender(evidence.sender)
	case applicationEvidenceDefinitive:
		nextStatus, changed := nextApplicationStatus(state.Status, state.DetectedAt, state.DetectedUID, evidence)
		if !changed {
			return nil
		}
		update.SetStatus(nextStatus).
			SetConfirmedAt(evidence.at).
			SetConfirmedUID(evidence.uid).
			SetConfirmedSubject(evidence.subject).
			SetConfirmedSender(evidence.sender)
	default:
		return nil
	}
	if _, err := update.Save(ctx); err != nil {
		return fmt.Errorf("update alias application state: %w", err)
	}
	return nil
}

func (store *postgresAliasApplicationStore) List(ctx context.Context) (map[string][]AliasApplication, error) {
	rows, err := store.client.MailAliasAppState.Query().
		Where(mailaliasappstate.AccountIDEQ(store.accountID)).
		Order(ent.Asc(mailaliasappstate.FieldAlias), ent.Asc(mailaliasappstate.FieldAppKey)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list alias application states: %w", err)
	}
	result := make(map[string][]AliasApplication, len(rows))
	for _, row := range rows {
		item := AliasApplication{Key: row.AppKey, Label: "GPT", Status: row.Status}
		if row.DetectedAt != nil {
			value := float64(row.DetectedAt.UnixNano()) / float64(time.Second)
			item.DetectedAt = &value
		}
		if row.ConfirmedAt != nil {
			value := float64(row.ConfirmedAt.UnixNano()) / float64(time.Second)
			item.ConfirmedAt = &value
		}
		alias := strings.ToLower(strings.TrimSpace(row.Alias))
		result[alias] = append(result[alias], item)
	}
	return result, nil
}

func (store *postgresAliasApplicationStore) DeleteAlias(ctx context.Context, alias string) error {
	_, err := store.client.MailAliasAppState.Delete().Where(
		mailaliasappstate.AccountIDEQ(store.accountID),
		mailaliasappstate.AliasEQ(strings.ToLower(strings.TrimSpace(alias))),
	).Exec(ctx)
	return err
}

func (store *postgresAliasApplicationStore) Backfill(ctx context.Context) error {
	rows, err := store.client.MailboxMessage.Query().Where(
		mailboxmessage.AccountIDEQ(store.accountID),
		mailboxmessage.Or(
			mailboxmessage.SubjectEqualFold(gptEvidenceSubjects[0]),
			mailboxmessage.SubjectEqualFold(gptEvidenceSubjects[1]),
			mailboxmessage.SubjectEqualFold(gptEvidenceSubjects[2]),
			mailboxmessage.SubjectEqualFold(gptEvidenceSubjects[3]),
			mailboxmessage.SubjectEqualFold(gptEvidenceSubjects[4]),
			mailboxmessage.SubjectEqualFold(gptEvidenceSubjects[5]),
		),
	).Select(
		mailboxmessage.FieldUID,
		mailboxmessage.FieldAliases,
		mailboxmessage.FieldFromAddress,
		mailboxmessage.FieldSubject,
		mailboxmessage.FieldMessageDate,
	).Order(ent.Asc(mailboxmessage.FieldMessageDate), ent.Asc(mailboxmessage.FieldUID)).All(ctx)
	if err != nil {
		return fmt.Errorf("load application evidence messages: %w", err)
	}
	messages := make([]MailMessage, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, MailMessage{
			UID: uint32(row.UID), Aliases: append([]string(nil), row.Aliases...),
			From: row.FromAddress, Subject: row.Subject, Date: row.MessageDate,
		})
	}
	return store.ObserveMessages(ctx, messages)
}
