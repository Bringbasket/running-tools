package mail

import (
	"context"
	"errors"
	"testing"

	"github.com/Bringbasket/running-tools/internal/platform/activitylog"
)

func TestMailboxBackgroundLogDeduplicatesFailuresAndRecordsRecovery(t *testing.T) {
	root := t.TempDir()
	logs := activitylog.New("mail", root, nil)
	mailbox := NewMailboxService(root, nil)
	mailbox.SetActivityLog(logs)
	failure := errors.New("temporary IMAP failure")

	mailbox.recordBackgroundSync(MailboxStatus{}, MailboxStatus{}, failure)
	mailbox.recordBackgroundSync(MailboxStatus{}, MailboxStatus{}, failure)
	page, err := logs.Query(context.Background(), activitylog.Query{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].Outcome != "failure" {
		t.Fatalf("repeated failure was not deduplicated: %#v", page)
	}

	mailbox.recordBackgroundSync(MailboxStatus{LastError: failure.Error()}, MailboxStatus{}, nil)
	page, err = logs.Query(context.Background(), activitylog.Query{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || page.Items[0].Outcome != "success" || page.Items[0].Summary != "后台收件箱同步已恢复" {
		t.Fatalf("recovery was not recorded: %#v", page)
	}
}

func TestAutoRefreshBackgroundLogOnlyRecordsStateChanges(t *testing.T) {
	root := t.TempDir()
	logs := activitylog.New("mail", root, nil)
	refresh := NewAutoRefresh(root, NewSessionManager(root+"/missing.json", root))
	refresh.SetActivityLog(logs)

	_, _ = refresh.run(context.Background(), "background")
	_, _ = refresh.run(context.Background(), "background")
	page, err := logs.Query(context.Background(), activitylog.Query{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].Action != "session.auto_refresh.check" {
		t.Fatalf("repeated refresh state was not deduplicated: %#v", page)
	}
}
