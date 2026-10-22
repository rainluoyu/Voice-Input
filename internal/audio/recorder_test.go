package audio

import (
	"context"
	"errors"
	"testing"
	"time"

	"voice_input/internal/i18n"
)

func TestStartFailureWaitsForCleanup(t *testing.T) {
	for _, reason := range []string{"cancel", "timeout", "initialization-error"} {
		t.Run(reason, func(t *testing.T) {
			n := NewWithMute(true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			timeout := time.Hour
			if reason == "timeout" {
				timeout = 0
			}
			entered := make(chan struct{})
			canceled := make(chan struct{})
			cleanup := make(chan struct{})
			result := make(chan error, 1)
			go func() {
				result <- n.start(ctx, timeout, func(captureCtx context.Context, ready chan<- error) (Data, error) {
					close(entered)
					if reason == "initialization-error" {
						ready <- ErrUnavailable
					}
					<-captureCtx.Done()
					close(canceled)
					<-cleanup
					return Data{}, captureCtx.Err()
				})
			}()
			<-entered
			if reason == "cancel" {
				cancel()
			}
			<-canceled
			select {
			case err := <-result:
				t.Fatalf("Start returned before capture cleanup: %v", err)
			default:
			}
			close(cleanup)
			want := ErrUnavailable
			if reason == "cancel" {
				want = context.Canceled
			}
			if err := <-result; !errors.Is(err, want) {
				t.Fatalf("Start error %v, want %v", err, want)
			}
			select {
			case <-n.done:
			default:
				t.Fatal("capture is still running after failed Start")
			}
		})
	}
}

func TestStartFailureReportsRestoreError(t *testing.T) {
	for _, reason := range []string{"cancel", "timeout", "initialization-error"} {
		t.Run(reason, func(t *testing.T) {
			n := NewWithMute(true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			timeout := time.Hour
			if reason == "timeout" {
				timeout = 0
			}
			err := n.start(ctx, timeout, func(captureCtx context.Context, ready chan<- error) (Data, error) {
				if reason == "cancel" {
					cancel()
				} else if reason == "initialization-error" {
					ready <- ErrUnavailable
				}
				<-captureCtx.Done()
				return Data{}, i18n.Error("audio.restore_failed")
			})
			requireAudioError(t, err, "audio.restore_failed")
			_, err = n.Stop(context.Background())
			requireAudioError(t, err, "audio.restore_failed")
		})
	}
}

func TestStartSuccessKeepsCaptureAlive(t *testing.T) {
	n := New()
	var captureContext context.Context
	err := n.start(context.Background(), time.Hour, func(ctx context.Context, ready chan<- error) (Data, error) {
		captureContext = ctx
		ready <- nil
		select {
		case <-ctx.Done():
			return Data{}, ctx.Err()
		case <-n.stop:
			return Data{}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if captureContext.Err() != nil {
		t.Fatal("successful Start canceled capture")
	}
	if _, err := n.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
