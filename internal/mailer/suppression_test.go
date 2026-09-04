package mailer_test

import (
	"context"
	"testing"

	"github.com/refsdal/whenweall/internal/mailer"
	"github.com/refsdal/whenweall/internal/testdb"
)

func TestSuppressionUnknownAddressIsNotSuppressed(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	suppressed, err := mailer.IsSuppressed(ctx, d, "nobody@example.com")
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Error("an address nobody unsubscribed is suppressed")
	}
}

func TestSuppressThenResubscribe(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	if err := mailer.Suppress(ctx, d, "ada@example.com", "link"); err != nil {
		t.Fatalf("Suppress: %v", err)
	}
	suppressed, err := mailer.IsSuppressed(ctx, d, "ada@example.com")
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if !suppressed {
		t.Fatal("address is not suppressed after Suppress")
	}

	if err := mailer.Resubscribe(ctx, d, "ada@example.com"); err != nil {
		t.Fatalf("Resubscribe: %v", err)
	}
	suppressed, err = mailer.IsSuppressed(ctx, d, "ada@example.com")
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Error("address is still suppressed after Resubscribe")
	}
}

// Someone can click the same link twice, and a provider can fire a one-click POST more than
// once; neither is an error, and neither may fail the request.
func TestSuppressIsIdempotent(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	if err := mailer.Suppress(ctx, d, "ada@example.com", "link"); err != nil {
		t.Fatalf("first Suppress: %v", err)
	}
	if err := mailer.Suppress(ctx, d, "ada@example.com", "one-click"); err != nil {
		t.Fatalf("second Suppress: %v", err)
	}

	suppressed, err := mailer.IsSuppressed(ctx, d, "ada@example.com")
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if !suppressed {
		t.Error("address is not suppressed after two Suppress calls")
	}
}

// Resubscribing an address nobody suppressed is a no-op, not a 500: a stale tab, a double-click
// or a re-read link must not produce an error page.
func TestResubscribeUnknownAddressSucceeds(t *testing.T) {
	d := testdb.New(t)
	if err := mailer.Resubscribe(context.Background(), d, "nobody@example.com"); err != nil {
		t.Errorf("Resubscribe on an unsuppressed address: %v", err)
	}
}

// The send path looks addresses up exactly as they are stored on a participant row or a user
// row, which is whatever they typed. If the lookup were case-sensitive, unsubscribing as
// "Ada@Example.com" would leave mail addressed to "ada@example.com" flowing.
func TestSuppressionIgnoresCaseAndSurroundingSpace(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	if err := mailer.Suppress(ctx, d, "  Ada@Example.COM ", "link"); err != nil {
		t.Fatalf("Suppress: %v", err)
	}

	for _, probe := range []string{"ada@example.com", "ADA@EXAMPLE.COM", " Ada@Example.com "} {
		suppressed, err := mailer.IsSuppressed(ctx, d, probe)
		if err != nil {
			t.Fatalf("IsSuppressed(%q): %v", probe, err)
		}
		if !suppressed {
			t.Errorf("IsSuppressed(%q) = false, want true", probe)
		}
	}
}
