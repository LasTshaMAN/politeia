package email

import (
	"fmt"
	"testing"
	"time"

	"github.com/decred/politeia/politeiawww/user"
	"github.com/decred/politeia/politeiawww/user/mock"
	"github.com/google/go-cmp/cmp"
)

func TestLimiter_IsEnabled(t *testing.T) {
	mm := &mailerMock{
		IsEnabledFunc: func() bool {
			return true
		},
	}
	limiter := NewLimiter(mm, nil, 0)

	got := limiter.IsEnabled()
	if diff := cmp.Diff(true, got); diff != "" {
		t.Error(diff)
	}
}

func TestLimiter_SendTo(t *testing.T) {
	const subject = "some subject"
	const body = "some body"

	test := func(
		recipients []string,
		goodHistory user.EmailHistory24h,
		badHistory user.EmailHistory24h,
		existingHistories []user.EmailHistory24h,
	) func(t *testing.T) {
		return func(t *testing.T) {
			mm := &mailerMock{
				SendToFunc: func(s string, b string, rs []string) error {
					if diff := cmp.Diff(subject, s); diff != "" {
						return fmt.Errorf("unexpected s: %v", diff)
					}
					if diff := cmp.Diff(body, b); diff != "" {
						return fmt.Errorf("unexpected b: %v", diff)
					}

					if cmp.Equal([]string{"good"}, rs) {
						return nil
					}
					if cmp.Equal([]string{"bad"}, rs) {
						return nil
					}
					return fmt.Errorf("unexpected rs: %v", rs)
				},
			}
			userDB := &mock.DatabaseMock{
				FetchHistories24hFunc: func(rs []string) ([]user.EmailHistory24h, error) {
					if diff := cmp.Diff(recipients, rs); diff != "" {
						return nil, fmt.Errorf("unexpected rs: %s", diff)
					}
					return existingHistories, nil
				},
				RefreshHistories24hFunc: func(
					histories []user.EmailHistory24h, limitWarningSent bool,
				) error {
					if cmp.Equal("good", histories[0].Email) && limitWarningSent == false {
						return nil
					}
					if cmp.Equal([]user.EmailHistory24h{badHistory}, histories) && limitWarningSent == true {
						return nil
					}
					return fmt.Errorf("unexpected arguments: %v, %v", histories, limitWarningSent)
				},
			}
			limiter := NewLimiter(mm, userDB, 2)

			got := limiter.SendTo(subject, body, recipients)
			if diff := cmp.Diff(nil, got); diff != "" {
				t.Error(diff)
			}
		}
	}

	good := user.EmailHistory24h{
		Email:             "good",
		SentTimestamps24h: []time.Time{time.Now()},
		LimitWarningSent:  false,
	}
	// Exceeds limit, warning has already been sent.
	ignored := user.EmailHistory24h{
		Email:             "ignored",
		SentTimestamps24h: []time.Time{time.Now(), time.Now()},
		LimitWarningSent:  true,
	}
	// Exceeds limit, warning hasn't yet been sent.
	bad := user.EmailHistory24h{
		Email:             "bad",
		SentTimestamps24h: []time.Time{time.Now(), time.Now()},
		LimitWarningSent:  false,
	}

	t.Run(
		"good has no previous history",
		test([]string{"good", "ignored", "bad"}, good, bad, []user.EmailHistory24h{ignored, bad}),
	)
	t.Run(
		"good has previous history",
		test([]string{"good", "ignored", "bad"}, good, bad, []user.EmailHistory24h{good, ignored, bad}),
	)
}
