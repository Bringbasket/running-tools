package mail

import (
	"testing"
	"time"
)

func TestClassifyGPTMessageRequiresTrustedSenderAndKnownSubject(t *testing.T) {
	tests := []struct {
		name    string
		message MailMessage
		kind    applicationEvidenceKind
		ok      bool
	}{
		{
			name:    "direct registration sender",
			message: MailMessage{From: "ChatGPT <otp@tm1.openai.com>", Subject: "Your temporary ChatGPT verification code"},
			kind:    applicationEvidenceRegistration, ok: true,
		},
		{
			name:    "icloud rewritten registration sender",
			message: MailMessage{From: "ChatGPT <otp_at_tm1_openai_com_random@icloud.com>", Subject: "Your temporary ChatGPT verification code"},
			kind:    applicationEvidenceRegistration, ok: true,
		},
		{
			name:    "login sender",
			message: MailMessage{From: "OpenAI <noreply@tm.openai.com>", Subject: "Your temporary OpenAI login code"},
			kind:    applicationEvidenceLogin, ok: true,
		},
		{
			name:    "definitive welcome message",
			message: MailMessage{From: "ChatGPT <noreply_at_tm_openai_com_random@icloud.com>", Subject: "Welcome to ChatGPT"},
			kind:    applicationEvidenceDefinitive, ok: true,
		},
		{
			name:    "spoofed sender",
			message: MailMessage{From: "ChatGPT <attacker@example.com>", Subject: "Your temporary ChatGPT verification code"},
			ok:      false,
		},
		{
			name:    "unrelated official message",
			message: MailMessage{From: "OpenAI <noreply@tm.openai.com>", Subject: "You have been invited to a workspace"},
			ok:      false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kind, ok := classifyGPTMessage(test.message)
			if ok != test.ok || kind != test.kind {
				t.Fatalf("classifyGPTMessage() = (%v, %v), want (%v, %v)", kind, ok, test.kind, test.ok)
			}
		})
	}
}

func TestNextApplicationStatusRequiresLaterLoginEvidence(t *testing.T) {
	detected := time.Unix(100, 0).UTC()
	tests := []struct {
		name       string
		current    string
		detectedAt *time.Time
		detectedID uint64
		evidence   applicationEvidence
		want       string
		changed    bool
	}{
		{name: "registration creates yellow", evidence: applicationEvidence{kind: applicationEvidenceRegistration}, want: aliasAppStatusObserved, changed: true},
		{name: "login alone does not confirm", evidence: applicationEvidence{kind: applicationEvidenceLogin, at: detected.Add(time.Minute)}, want: "", changed: false},
		{name: "older login does not confirm", current: aliasAppStatusObserved, detectedAt: &detected, detectedID: 10, evidence: applicationEvidence{kind: applicationEvidenceLogin, at: detected.Add(-time.Second), uid: 11}, want: aliasAppStatusObserved, changed: false},
		{name: "later login confirms", current: aliasAppStatusObserved, detectedAt: &detected, detectedID: 10, evidence: applicationEvidence{kind: applicationEvidenceLogin, at: detected.Add(time.Second), uid: 11}, want: aliasAppStatusConfirmed, changed: true},
		{name: "same timestamp needs later uid", current: aliasAppStatusObserved, detectedAt: &detected, detectedID: 10, evidence: applicationEvidence{kind: applicationEvidenceLogin, at: detected, uid: 11}, want: aliasAppStatusConfirmed, changed: true},
		{name: "welcome confirms directly", evidence: applicationEvidence{kind: applicationEvidenceDefinitive}, want: aliasAppStatusConfirmed, changed: true},
		{name: "confirmed never downgrades", current: aliasAppStatusConfirmed, detectedAt: &detected, evidence: applicationEvidence{kind: applicationEvidenceRegistration}, want: aliasAppStatusConfirmed, changed: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, changed := nextApplicationStatus(test.current, test.detectedAt, test.detectedID, test.evidence)
			if got != test.want || changed != test.changed {
				t.Fatalf("nextApplicationStatus() = (%q, %v), want (%q, %v)", got, changed, test.want, test.changed)
			}
		})
	}
}
