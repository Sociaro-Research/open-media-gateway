package postgres

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

func TestConcurrentWavesRetainDatabaseConnections(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const connections = 16
	for wave := 0; wave < 2; wave++ {
		// Check out the entire wave before releasing any connection, independently
		// of scheduler speed and PostgreSQL query latency.
		var wg sync.WaitGroup
		ready := make(chan struct{}, connections)
		release := make(chan struct{})
		for i := 0; i < connections; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				connection, err := db.Conn(ctx)
				if err != nil {
					t.Error(err)
					ready <- struct{}{}
					return
				}
				ready <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
				}
				connection.Close()
			}()
		}
		for i := 0; i < connections; i++ {
			select {
			case <-ready:
			case <-ctx.Done():
				close(release)
				wg.Wait()
				t.Fatal(ctx.Err())
			}
		}
		close(release)
		wg.Wait()
		stats := db.Stats()
		if stats.MaxIdleClosed != 0 || stats.Idle != connections {
			t.Fatalf("wave %d discarded reusable connections: %+v", wave, stats)
		}
	}
}
