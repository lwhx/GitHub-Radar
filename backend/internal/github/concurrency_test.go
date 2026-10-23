package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientLimitsConcurrentRequestsByDefault(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 3)
	release := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := active.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"octocat","id":1,"type":"User"}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	var waitGroup sync.WaitGroup
	for range 3 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, _, _ = client.GetAuthenticatedUser(context.Background())
		}()
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("two requests did not start")
		}
	}
	select {
	case <-started:
		close(release)
		waitGroup.Wait()
		t.Fatal("third request started before a slot was released")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	waitGroup.Wait()
	if got := maximum.Load(); got != 2 {
		t.Errorf("maximum concurrent requests = %d", got)
	}
}
