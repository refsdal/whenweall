package mailer_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/jobs"
	"github.com/refsdal/whenweall/internal/mailer"
	"github.com/refsdal/whenweall/internal/testdb"
)

func TestParseFromAddress(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantName  string
		wantEmail string
	}{
		{
			name:      "name and address pair",
			in:        "whenweall <no-reply@whenweall.com>",
			wantName:  "whenweall",
			wantEmail: "no-reply@whenweall.com",
		},
		{
			name:      "quoted name with surrounding whitespace",
			in:        `  "whenweall"  <no-reply@whenweall.com>  `,
			wantName:  "whenweall",
			wantEmail: "no-reply@whenweall.com",
		},
		{
			name:      "bare address unchanged",
			in:        "no-reply@whenweall.com",
			wantName:  "",
			wantEmail: "no-reply@whenweall.com",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotEmail := mailer.ParseFromAddress(tc.in)
			if gotName != tc.wantName || gotEmail != tc.wantEmail {
				t.Errorf("ParseFromAddress(%q) = (%q, %q), want (%q, %q)",
					tc.in, gotName, gotEmail, tc.wantName, tc.wantEmail)
			}
		})
	}
}

// TestEnqueueClaimRoundTrip verifies Enqueue schedules kind "mail:send" with the Message as its
// payload, and that a claimed job's payload unmarshals back to an equal Message — the job queue
// is only useful as a transport if what comes out is what went in.
func TestEnqueueClaimRoundTrip(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	msg := mailer.Message{
		To:       "ada@example.com",
		ToName:   "Ada",
		Template: "verify_email",
		Data: map[string]any{
			"Name":  "Ada",
			"Token": "abc123",
		},
	}

	if err := mailer.Enqueue(ctx, d, msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	claimed, err := jobs.ClaimDue(ctx, d, "w1", 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}

	job := claimed[0]
	if job.Kind != "mail:send" {
		t.Errorf("Kind = %q, want %q", job.Kind, "mail:send")
	}
	if job.MaxAttempts != 10 {
		t.Errorf("MaxAttempts = %d, want 10", job.MaxAttempts)
	}
	if job.RunAt.After(time.Now().Add(time.Second)) {
		t.Errorf("RunAt = %v, want approximately now", job.RunAt)
	}

	var got mailer.Message
	if err := json.Unmarshal(job.Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got.To != msg.To || got.ToName != msg.ToName || got.Template != msg.Template {
		t.Errorf("got = %+v, want %+v", got, msg)
	}
	if got.Data["Name"] != "Ada" || got.Data["Token"] != "abc123" {
		t.Errorf("Data = %+v, want %+v", got.Data, msg.Data)
	}
}
