package broadcast

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

func drain[T any](ch <-chan T) []T {
	var out []T
	for v := range ch {
		out = append(out, v)
	}
	return out
}

func TestBroadcaster_FanoutAllSubscribersReceiveInOrder(t *testing.T) {
	t.Parallel()
	b := New[int](16)
	subs := make([]<-chan int, 3)
	for i := range subs {
		_, ch, _ := b.Subscribe()
		subs[i] = ch
	}

	for i := 0; i < 8; i++ {
		b.Publish(i)
	}
	b.Close()

	for i, ch := range subs {
		got := drain(ch)
		if len(got) != 8 {
			t.Fatalf("sub %d: got %d values, want 8 (%v)", i, len(got), got)
		}
		for j, v := range got {
			if v != j {
				t.Fatalf("sub %d position %d: got %d, want %d", i, j, v, j)
			}
		}
	}
}

func TestBroadcaster_DropOldestOnFullBuffer(t *testing.T) {
	t.Parallel()
	b := New[int](2)
	_, ch, drops := b.Subscribe()

	for i := 0; i < 10; i++ {
		b.Publish(i)
	}
	b.Close()

	got := drain(ch)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2 buffered values (%v)", len(got), got)
	}
	if got[0] != 8 || got[1] != 9 {
		t.Fatalf("expected newest two values [8,9], got %v", got)
	}
	if drops.Load() != 8 {
		t.Fatalf("drops: got %d, want 8", drops.Load())
	}
}

func TestBroadcaster_SlowSubDoesNotBlockPublisher(t *testing.T) {
	t.Parallel()
	b := New[int](4)
	_, slow, slowDrops := b.Subscribe() // never read
	_, fast, _ := b.Subscribe()

	// Fast drainer so Close() below completes promptly; its receive behavior
	// is not under test.
	done := make(chan struct{})
	go func() {
		for range fast {
		}
		close(done)
	}()

	const N = 2000
	start := time.Now()
	for i := 0; i < N; i++ {
		b.Publish(i)
	}
	// Publisher must complete far below a wall-clock budget even though slow
	// never reads: that is the "doesn't block the producer" invariant.
	if d := time.Since(start); d > time.Second {
		t.Fatalf("publisher took %v for %d publishes (blocked by slow sub)", d, N)
	}

	b.Close()
	<-done

	// Slow sub must have dropped nearly everything (buffer=4, published 2000).
	if got := slowDrops.Load(); got < int64(N-8) {
		t.Fatalf("slow drops: got %d, want >= %d", got, N-8)
	}
	// Draining slow must not deadlock and must yield at most buffer-sized
	// suffix.
	got := drain(slow)
	if len(got) > 4 {
		t.Fatalf("slow buffer contents: got %d values, want <= 4", len(got))
	}
}

func TestBroadcaster_UnsubscribeClosesOneChannelOnly(t *testing.T) {
	t.Parallel()
	b := New[int](4)
	id1, ch1, _ := b.Subscribe()
	_, ch2, _ := b.Subscribe()

	b.Publish(1)
	b.Unsubscribe(id1)
	// After Unsubscribe, publishes no longer reach ch1 but ch2 keeps going.
	b.Publish(2)
	b.Close()

	got1 := drain(ch1)
	if len(got1) != 1 || got1[0] != 1 {
		t.Fatalf("sub 1 got %v, want [1] (buffered pre-unsubscribe)", got1)
	}
	got2 := drain(ch2)
	if len(got2) != 2 || got2[0] != 1 || got2[1] != 2 {
		t.Fatalf("sub 2 got %v, want [1 2]", got2)
	}

	// Second Unsubscribe on same id is a no-op.
	b.Unsubscribe(id1)
}

func TestBroadcaster_PublishAfterCloseIsNoop(t *testing.T) {
	t.Parallel()
	b := New[int](4)
	_, ch, _ := b.Subscribe()
	b.Close()
	// Must not panic.
	b.Publish(42)

	if _, ok := <-ch; ok {
		t.Fatalf("expected ch closed, got value")
	}

	// Subscribe after close returns a closed channel with sentinel id -1.
	id, ch2, _ := b.Subscribe()
	if id != -1 {
		t.Fatalf("post-close Subscribe id: got %d, want -1", id)
	}
	if _, ok := <-ch2; ok {
		t.Fatalf("post-close Subscribe channel should be closed")
	}

	// Second Close is idempotent.
	b.Close()
}

func TestBroadcaster_NoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	for cycle := 0; cycle < 20; cycle++ {
		b := New[int](4)
		var wg sync.WaitGroup
		for i := 0; i < 3; i++ {
			_, ch, _ := b.Subscribe()
			wg.Add(1)
			go func(c <-chan int) {
				defer wg.Done()
				for range c {
				}
			}(ch)
		}
		for i := 0; i < 10; i++ {
			b.Publish(i)
		}
		b.Close()
		wg.Wait()
	}

	// Allow any runtime goroutines to settle.
	time.Sleep(50 * time.Millisecond)

	after := runtime.NumGoroutine()
	if after > before+2 {
		t.Fatalf("possible goroutine leak: before=%d after=%d", before, after)
	}
}
